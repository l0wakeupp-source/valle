package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

type captureWriteCloser struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *captureWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *captureWriteCloser) Close() error { return nil }

func (w *captureWriteCloser) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func TestReadMCPFrameRejectsOversizedFrame(t *testing.T) {
	input := bytes.NewReader(append(bytes.Repeat([]byte{'x'}, maxMCPFrameBytes+1), '\n'))
	_, err := readMCPFrame(bufio.NewReaderSize(input, 1024))
	if err == nil {
		t.Fatal("expected oversized frame error")
	}
}

func TestWithoutSchemaDialectCopiesSchemaWithoutDialectKey(t *testing.T) {
	input := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"query": map[string]any{"$schema": "nested", "type": "string"},
		},
	}
	output := withoutSchemaDialect(input)
	if _, ok := output["$schema"]; ok {
		t.Fatalf("schema dialect key survived: %#v", output)
	}
	if input["$schema"] == nil {
		t.Fatal("input schema was mutated")
	}
	if output["type"] != "object" {
		t.Fatalf("schema type = %#v", output["type"])
	}
	properties := output["properties"].(map[string]any)
	query := properties["query"].(map[string]any)
	if _, ok := query["$schema"]; ok {
		t.Fatal("nested schema dialect key survived")
	}
}

func TestWithoutSchemaDialectPreservesSchemaAndCopiesMap(t *testing.T) {
	input := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
	}
	got := withoutSchemaDialect(input)
	if _, ok := got["$schema"]; ok {
		t.Fatal("schema dialect marker was not removed")
	}
	if got["type"] != "object" {
		t.Fatalf("type = %#v, want object", got["type"])
	}
	got["type"] = "array"
	if input["type"] != "object" {
		t.Fatal("schema normalization mutated the server-owned map")
	}
}

func TestStdioDispatcherRoutesResponse(t *testing.T) {
	response, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "result": map[string]any{"ok": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout := io.NopCloser(bytes.NewReader(append(response, '\n')))
	client := &Client{
		Name: "test", stdin: discardWriteCloser{}, stdout: stdout,
		reader: bufio.NewReader(stdout), pending: make(map[int64]chan stdioReply),
		readerDone: make(chan struct{}),
	}
	go client.readStdio()

	result, err := client.stdioCall(context.Background(), rpcRequest{ID: 1, Method: "test"})
	if err != nil {
		t.Fatalf("stdioCall returned error: %v", err)
	}
	var decoded map[string]bool
	if err := json.Unmarshal(result, &decoded); err != nil || !decoded["ok"] {
		t.Fatalf("result = %s, want an ok response", result)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestStdioCallRemovesCancelledRequest(t *testing.T) {
	reader, writer := io.Pipe()
	stdout := struct {
		io.Reader
		io.Closer
	}{Reader: reader, Closer: writer}
	client := &Client{
		Name: "test", stdin: discardWriteCloser{}, stdout: stdout,
		reader: bufio.NewReader(reader), pending: make(map[int64]chan stdioReply),
		readerDone: make(chan struct{}),
	}
	go client.readStdio()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := client.stdioCall(ctx, rpcRequest{ID: 1, Method: "test"})
	if err == nil {
		t.Fatal("stdioCall unexpectedly succeeded")
	}
	client.mu.Lock()
	pending := len(client.pending)
	client.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending requests = %d, want 0", pending)
	}
	_ = client.Close()
	select {
	case <-client.readerDone:
	case <-time.After(time.Second):
		t.Fatal("stdio reader did not stop after Close")
	}
}

func TestStdioDispatcherAnswersUnsupportedServerRequest(t *testing.T) {
	request := []byte(`{"jsonrpc":"2.0","id":7,"method":"sampling/createMessage"}` + "\n")
	stdout := io.NopCloser(bytes.NewReader(request))
	stdin := &captureWriteCloser{}
	client := &Client{
		Name: "test", stdin: stdin, stdout: stdout,
		reader: bufio.NewReader(stdout), pending: make(map[int64]chan stdioReply),
		readerDone: make(chan struct{}),
	}
	go client.readStdio()
	select {
	case <-client.readerDone:
	case <-time.After(time.Second):
		t.Fatal("stdio reader did not stop")
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdin.String())), &response); err != nil {
		t.Fatalf("server-request response is invalid JSON: %v", err)
	}
	if response["error"] == nil {
		t.Fatalf("response = %v, want JSON-RPC error", response)
	}
}

func TestHTTPCallSkipsWrongSSEResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w,
			"data: {\"jsonrpc\":\"2.0\",\"id\":99,\"result\":{\"wrong\":true}}\n\n"+
				"data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n")
	}))
	defer server.Close()
	client := &Client{Name: "test", url: server.URL, httpc: server.Client()}
	result, err := client.httpCall(context.Background(), rpcRequest{ID: 1, Method: "test"})
	if err != nil {
		t.Fatalf("httpCall returned error: %v", err)
	}
	var decoded map[string]bool
	if err := json.Unmarshal(result, &decoded); err != nil || !decoded["ok"] {
		t.Fatalf("result = %s, want the matching response", result)
	}
}
