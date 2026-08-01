// Package mcp implements a minimal Model Context Protocol client supporting
// local (stdio subprocess) and remote (HTTP) servers.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const protocolVersion = "2024-11-05"
const maxMCPFrameBytes = 16 << 20

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type stdioReply struct {
	resp rpcResponse
	err  error
}

// ToolDef is a tool advertised by an MCP server.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// CallResult is the outcome of tools/call.
type CallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// Text flattens the content blocks.
func (r CallResult) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Client is a connection to one MCP server.
type Client struct {
	Name string

	mu      sync.Mutex
	writeMu sync.Mutex
	nextID  atomic.Int64
	tools   []ToolDef
	closed  bool

	// stdio transport
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	reader     *bufio.Reader
	pending    map[int64]chan stdioReply
	readerErr  error
	readerDone chan struct{}

	// http transport
	url     string
	headers map[string]string
	httpc   *http.Client
}

// DialLocal spawns a stdio MCP server and performs the handshake.
func DialLocal(ctx context.Context, name string, command []string, env map[string]string) (*Client, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("mcp %s: empty command", name)
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: %w", name, err)
	}

	c := &Client{
		Name: name, cmd: cmd, stdin: stdin, stdout: stdout,
		reader:  bufio.NewReaderSize(stdout, 1<<20),
		pending: make(map[int64]chan stdioReply), readerDone: make(chan struct{}),
	}
	go c.readStdio()
	if err := c.handshake(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// DialRemote connects to an HTTP MCP server. Only static header auth is
// supported (no OAuth).
func DialRemote(ctx context.Context, name, url string, headers map[string]string) (*Client, error) {
	c := &Client{
		Name: name, url: strings.TrimRight(url, "/"), headers: headers,
		httpc: &http.Client{Timeout: 60 * time.Second},
	}
	if err := c.handshake(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) handshake(ctx context.Context) error {
	initParams := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"clientInfo":      map[string]any{"name": "rick", "version": "0.1.0"},
	}
	if _, err := c.call(ctx, "initialize", initParams); err != nil {
		return fmt.Errorf("mcp %s: initialize: %w", c.Name, err)
	}
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("mcp %s: initialized: %w", c.Name, err)
	}
	return c.refreshTools(ctx)
}

func (c *Client) refreshTools(ctx context.Context) error {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return fmt.Errorf("mcp %s: tools/list: %w", c.Name, err)
	}
	var out struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("mcp %s: tools/list decode: %w", c.Name, err)
	}
	c.mu.Lock()
	for i := range out.Tools {
		out.Tools[i].InputSchema = withoutSchemaDialect(out.Tools[i].InputSchema)
	}
	c.tools = out.Tools
	c.mu.Unlock()
	return nil
}

func withoutSchemaDialect(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return schema
	}
	return normalizeSchemaValue(schema).(map[string]any)
}

func normalizeSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		compact := make(map[string]any, len(typed))
		for key, nested := range typed {
			if key != "$schema" {
				compact[key] = normalizeSchemaValue(nested)
			}
		}
		return compact
	case []any:
		compact := make([]any, len(typed))
		for i, nested := range typed {
			compact[i] = normalizeSchemaValue(nested)
		}
		return compact
	default:
		return value
	}
}

// Tools returns the advertised tool list.
func (c *Client) Tools() []ToolDef {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ToolDef(nil), c.tools...)
}

// Call invokes a tool.
func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (CallResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return CallResult{}, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, err
	}
	return res, nil
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}

	if c.url != "" {
		return c.httpCall(ctx, req)
	}
	return c.stdioCall(ctx, req)
}

func (c *Client) notify(method string, params any) error {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	if c.url != "" {
		return nil // HTTP transports treat notifications as optional
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.writeStdio(append(data, '\n'))
}

func (c *Client) stdioCall(ctx context.Context, req rpcRequest) (json.RawMessage, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	done := make(chan stdioReply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %s: closed", c.Name)
	}
	if c.readerErr != nil {
		err := c.readerErr
		c.mu.Unlock()
		return nil, err
	}
	c.pending[req.ID] = done
	c.mu.Unlock()

	if err := c.writeStdio(append(data, '\n')); err != nil {
		c.removePending(req.ID)
		return nil, err
	}

	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		if r.resp.Error != nil {
			return nil, r.resp.Error
		}
		return r.resp.Result, nil
	case <-ctx.Done():
		c.removePending(req.ID)
		return nil, ctx.Err()
	case <-timer.C:
		c.removePending(req.ID)
		return nil, fmt.Errorf("mcp %s: timeout waiting for %s", c.Name, req.Method)
	}
}

func (c *Client) writeStdio(data []byte) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("mcp %s: closed", c.Name)
	}
	stdin := c.stdin
	c.mu.Unlock()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := stdin.Write(data)
	return err
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) readStdio() {
	defer close(c.readerDone)
	for {
		line, err := readMCPFrame(c.reader)
		if err != nil {
			c.failPending(err)
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var envelope struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if json.Unmarshal(line, &envelope) != nil {
			continue
		}
		if envelope.Method != "" {
			// A server request is not a response to one of our pending calls.
			// We do not expose callbacks yet, but answer it so the peer cannot
			// wait forever.
			if len(envelope.ID) > 0 && string(envelope.ID) != "null" {
				response := map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(envelope.ID),
					"error": map[string]any{
						"code":    -32601,
						"message": "method not supported",
					},
				}
				if data, err := json.Marshal(response); err == nil {
					_ = c.writeStdio(append(data, '\n'))
				}
			}
			continue
		}
		var resp rpcResponse
		if json.Unmarshal(line, &resp) != nil || resp.ID == 0 {
			continue
		}

		c.mu.Lock()
		done := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if done != nil {
			done <- stdioReply{resp: resp}
		}
	}
}

func readMCPFrame(reader *bufio.Reader) ([]byte, error) {
	var frame []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(frame)+len(chunk) > maxMCPFrameBytes {
			return nil, fmt.Errorf("mcp response exceeds %d bytes", maxMCPFrameBytes)
		}
		frame = append(frame, chunk...)
		if err == nil {
			return frame, nil
		}
		if err != bufio.ErrBufferFull {
			return nil, err
		}
	}
}

func (c *Client) failPending(err error) {
	c.mu.Lock()
	if c.readerErr == nil {
		c.readerErr = err
	}
	pending := c.pending
	c.pending = make(map[int64]chan stdioReply)
	c.mu.Unlock()
	for _, done := range pending {
		done <- stdioReply{err: err}
	}
}

func (c *Client) httpCall(ctx context.Context, req rpcRequest) (json.RawMessage, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("mcp %s: http %d: %s", c.Name, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	// Servers may reply as SSE. Inspect every data event and ignore
	// notifications or server requests until the response for this request
	// arrives; accepting the first event can return the wrong result.
	var candidates [][]byte
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Contains(body, []byte("\ndata:")) {
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(line[len("data:"):])
				if data != "" && data != "[DONE]" {
					candidates = append(candidates, []byte(data))
				}
			}
		}
	} else {
		candidates = append(candidates, trimmed)
	}
	for _, candidate := range candidates {
		var envelope struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(candidate, &envelope); err != nil {
			continue
		}
		if envelope.Method != "" || envelope.ID == nil || *envelope.ID != req.ID {
			continue
		}
		var rpcResp rpcResponse
		if err := json.Unmarshal(candidate, &rpcResp); err != nil {
			return nil, fmt.Errorf("mcp %s: bad response: %w", c.Name, err)
		}
		if rpcResp.Error != nil {
			return nil, rpcResp.Error
		}
		return rpcResp.Result, nil
	}
	return nil, fmt.Errorf("mcp %s: response id mismatch for %s", c.Name, req.Method)
}

// Close shuts the connection down.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	stdin := c.stdin
	stdout := c.stdout
	cmd := c.cmd
	c.mu.Unlock()

	c.failPending(fmt.Errorf("mcp %s: closed", c.Name))
	c.writeMu.Lock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	c.writeMu.Unlock()

	if cmd != nil && cmd.Process != nil {
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	return nil
}
