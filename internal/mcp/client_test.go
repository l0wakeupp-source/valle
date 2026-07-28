package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

func TestReadMCPFrameRejectsOversizedFrame(t *testing.T) {
	input := bytes.NewReader(append(bytes.Repeat([]byte{'x'}, maxMCPFrameBytes+1), '\n'))
	_, err := readMCPFrame(bufio.NewReaderSize(input, 1024))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("readMCPFrame error = %v, want frame-size error", err)
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
