package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rick/internal/agent"
	"rick/internal/config"
	"rick/internal/goal"
	"rick/internal/mcp"
	"rick/internal/permission"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/sandbox"
	"rick/internal/session"
	"rick/internal/swarm"
	"rick/internal/tools"
)

// blockingProvider emits one text event per stream then blocks until the
// context ends, simulating a long-running model response.
type blockingProvider struct {
	mu     sync.Mutex
	calls  int
	start  chan struct{}
	closed chan struct{}
}

func (p *blockingProvider) Name() string { return "fake" }

func (p *blockingProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "model", Name: "model"}}
}

func (p *blockingProvider) Stream(ctx context.Context, req provider.Request, ch chan<- provider.Event) {
	defer close(ch)
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if first && p.start != nil {
		select {
		case p.start <- struct{}{}:
		default:
		}
	}
	if first {
		select {
		case ch <- provider.Event{Kind: provider.EventText, Text: "working"}:
		case <-ctx.Done():
			return
		}
	}
	<-ctx.Done()
	if p.closed != nil {
		close(p.closed)
	}
}

type usageProvider struct{}

func (usageProvider) Name() string { return "fake" }
func (usageProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "model", Name: "model", ContextWindow: 128000}}
}
func (usageProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
	defer close(ch)
	ch <- provider.Event{Kind: provider.EventText, Text: "finished"}
	ch <- provider.Event{Kind: provider.EventUsage, Usage: &provider.Usage{
		InputTokens: 40, OutputTokens: 10, CacheReadTokens: 50, CacheWriteTokens: 5,
	}}
	ch <- provider.Event{Kind: provider.EventDone, StopReason: "end_turn"}
}

type continuationProvider struct {
	calls int
}

func (p *continuationProvider) Name() string { return "fake" }
func (p *continuationProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "model", Name: "model"}}
}
func (p *continuationProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
	defer close(ch)
	p.calls++
	if p.calls == 1 {
		ch <- provider.Event{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
			ID: "call-1", Name: "continuation_test", Input: json.RawMessage(`{}`),
		}}
		ch <- provider.Event{Kind: provider.EventDone, StopReason: "tool_use"}
		return
	}
	ch <- provider.Event{Kind: provider.EventText, Text: "final response"}
	ch <- provider.Event{Kind: provider.EventDone, StopReason: "end_turn"}
}

type continuationTool struct{}

func (continuationTool) Name() string           { return "continuation_test" }
func (continuationTool) Description() string    { return "returns a test result" }
func (continuationTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (continuationTool) ReadOnly() bool         { return true }
func (continuationTool) Run(context.Context, tools.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Output: "tool output"}, nil
}

// newTestServer wires a server with a fake provider and isolated storage.
func newTestServer(t *testing.T, prov provider.Provider) (*server, *config.Loaded) {
	t.Helper()
	t.Setenv("RICK_DATA", t.TempDir())
	root := t.TempDir()
	cfg, tui := config.Defaults()
	loaded := &config.Loaded{
		Config:      cfg,
		TUI:         tui,
		ProjectRoot: root,
		SandboxRoot: root,
	}
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goals, err := goal.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &server{
		loaded:      loaded,
		cwd:         root,
		provs:       map[string]provider.Provider{"fake": prov},
		tools:       tools.NewRegistry(),
		plugins:     plugin.NewRegistry(),
		store:       store,
		mcp:         mcp.NewManager(),
		goals:       goals,
		snaps:       map[string]*session.Snapshotter{},
		agents:      map[string]*agent.Registry{},
		permPending: map[string]*pendingPerm{},
		runCancel:   map[string]activeRun{},
	}, loaded
}

// sendLine writes one ndjson request into the connection.
func sendLine(t *testing.T, w io.Writer, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
}

// nextResponse reads one response line with a timeout.
func nextResponse(t *testing.T, sc *bufio.Scanner, timeout time.Duration) Response {
	t.Helper()
	type result struct {
		res Response
		err error
	}
	ch := make(chan result, 1)
	go func() {
		if !sc.Scan() {
			ch <- result{err: sc.Err()}
			return
		}
		var res Response
		if err := json.Unmarshal(sc.Bytes(), &res); err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{res: res}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read response: %v", r.err)
		}
		return r.res
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for response")
		return Response{}
	}
}

type requestCaptureProvider struct {
	mu       sync.Mutex
	model    provider.ModelInfo
	requests []provider.Request
}

func (p *requestCaptureProvider) Name() string { return "fake" }
func (p *requestCaptureProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{p.model}
}
func (p *requestCaptureProvider) Stream(_ context.Context, req provider.Request, ch chan<- provider.Event) {
	defer close(ch)
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	ch <- provider.Event{Kind: provider.EventText, Text: "done"}
	ch <- provider.Event{Kind: provider.EventDone, StopReason: "end_turn"}
}
func (p *requestCaptureProvider) lastRequest(t *testing.T) provider.Request {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		t.Fatal("provider received no request")
	}
	return p.requests[len(p.requests)-1]
}

func TestRunRequestDecodesDesktopExecutionOptions(t *testing.T) {
	var req Request
	if err := json.Unmarshal([]byte(`{"type":"run","run_id":"run-1","request_id":"request-1","permission_profile":"readonly","sandbox":"read-only","thinking":"high","yolo":true}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.RunID != "run-1" || req.RequestID != "request-1" || req.PermissionProfile != "readonly" || req.Sandbox != "read-only" || req.Thinking != "high" || !req.Yolo {
		t.Fatalf("desktop run options were dropped: %+v", req)
	}
}

func TestRunWriterAddsCorrelationToEveryResponse(t *testing.T) {
	var output bytes.Buffer
	correlated := &runWriter{parent: newWriter(&output), requestID: "request-1", runID: "run-1"}
	correlated.emit(Response{Type: "event", SessionID: "session-1", Event: "Content"})
	correlated.emit(Response{Type: "done", SessionID: "session-1"})

	scanner := bufio.NewScanner(&output)
	count := 0
	for scanner.Scan() {
		count++
		var response Response
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.RequestID != "request-1" || response.RunID != "run-1" {
			t.Fatalf("response lost correlation: %+v", response)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("response count = %d, want 2", count)
	}
}

func TestInterruptRejectsStaleRunGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := &server{runCancel: map[string]activeRun{"session-1": {runID: "run-new", cancel: cancel}}}

	srv.handleInterrupt(Request{SessionID: "session-1", RunID: "run-old"})
	if ctx.Err() != nil {
		t.Fatal("stale interrupt cancelled the active run")
	}
	srv.handleInterrupt(Request{SessionID: "session-1", RunID: "run-new"})
	if ctx.Err() != context.Canceled {
		t.Fatal("matching interrupt did not cancel the active run")
	}
}

func TestResolveRunSecurityAppliesProfileSandboxAndYoloPerRun(t *testing.T) {
	root := t.TempDir()
	cfg, tuiCfg := config.Defaults()
	cfg.Sandbox = &config.SandboxConfig{DenyPaths: []string{"**/.secret/**"}}
	loaded := &config.Loaded{Config: cfg, TUI: tuiCfg, ProjectRoot: root, SandboxRoot: root}

	perms, holder, err := resolveRunSecurity(loaded, "readonly", "trusted", true)
	if err != nil {
		t.Fatal(err)
	}
	if perms.Profile() != "readonly" || !perms.Yolo() {
		t.Fatalf("permission engine = profile %q yolo %v", perms.Profile(), perms.Yolo())
	}
	policy := holder.Policy()
	if policy.Mode != sandbox.ModeOff {
		t.Fatalf("sandbox mode = %q, want off in YOLO mode", policy.Mode)
	}
	if len(policy.DenyPaths) != 1 || policy.DenyPaths[0] != "**/.secret/**" {
		t.Fatalf("sandbox lost global protected paths: %#v", policy.DenyPaths)
	}
	if decision := perms.Resolve(permission.Request{Tool: "bash", Command: "anything"}); decision.Level != permission.Allow || decision.Source != "yolo" {
		t.Fatalf("yolo decision = %+v, want allow from yolo", decision)
	}

	// A second run resolves fresh state: YOLO must not leak, and the selected
	// readonly profile must affect actual tool decisions rather than metadata.
	readonly, readonlySandbox, err := resolveRunSecurity(loaded, "readonly", "read-only", false)
	if err != nil {
		t.Fatal(err)
	}
	if readonly.Yolo() {
		t.Fatal("yolo leaked into a later run")
	}
	if got := readonly.Resolve(permission.Request{Tool: "bash", Command: "go test ./..."}); got.Level != permission.Deny {
		t.Fatalf("readonly profile bash decision = %+v, want deny", got)
	}
	if readonlySandbox.Policy().Mode != sandbox.ModeReadOnly || holder.Policy().Mode != sandbox.ModeOff {
		t.Fatalf("run-local sandboxes leaked: first=%q second=%q", holder.Policy().Mode, readonlySandbox.Policy().Mode)
	}
}

func TestHandleRunPassesDesktopThinkingToProvider(t *testing.T) {
	for _, tc := range []struct {
		name     string
		thinking string
		want     provider.ReasoningEffort
	}{
		{name: "auto uses model default", thinking: "auto", want: provider.ReasoningMedium},
		{name: "explicit high", thinking: "high", want: provider.ReasoningHigh},
		{name: "explicit off", thinking: "off", want: provider.ReasoningOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := &requestCaptureProvider{model: provider.ModelInfo{ID: "gpt-5.2", Name: "gpt-5.2"}}
			srv, _ := newTestServer(t, prov)
			var output bytes.Buffer
			srv.handleRun(context.Background(), Request{
				Type: "run", SessionID: "thinking-session", Prompt: "answer", Model: "fake/gpt-5.2", Thinking: tc.thinking,
			}, newWriter(&output))
			if got := prov.lastRequest(t).Reasoning; got != tc.want {
				t.Fatalf("provider reasoning = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveRunSecurityUsesRequestedWorkspaceRoot(t *testing.T) {
	startupRoot := t.TempDir()
	requestedRoot := t.TempDir()
	cfg, tuiCfg := config.Defaults()
	loaded := &config.Loaded{Config: cfg, TUI: tuiCfg, ProjectRoot: startupRoot, SandboxRoot: startupRoot}

	_, holder, err := resolveRunSecurityAtRoot(loaded, requestedRoot, "standard", "workspace-write", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Clean(holder.Policy().Workspace); got != filepath.Clean(requestedRoot) {
		t.Fatalf("workspace = %q, want requested cwd %q", got, requestedRoot)
	}
}

func TestResolveRunReasoningRejectsUnsupportedModelEffort(t *testing.T) {
	models := []provider.ModelInfo{{
		ID: "strict-model", ReasoningKnown: true, ReasoningMandatory: true, ReasoningEffortsKnown: true,
		ReasoningEfforts: []provider.ReasoningEffort{provider.ReasoningLow, provider.ReasoningHigh},
	}}
	if _, err := resolveRunReasoning("fake", "strict-model", "off", models); err == nil {
		t.Fatal("unsupported model-specific reasoning effort was accepted")
	}
}

func TestEmitAgentEventPreservesToolCallIDAndFullOutput(t *testing.T) {
	var output bytes.Buffer
	longOutput := strings.Repeat("result", 1000)
	emitAgentEvent(newWriter(&output), "session", agent.Event{Kind: agent.EvToolEnd, Tool: &agent.ToolEvent{
		CallID: "call-42", Name: "read", Output: longOutput,
	}})
	var response Response
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	var data struct {
		CallID string `json:"call_id"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.CallID != "call-42" || data.Output != longOutput {
		t.Fatalf("tool result lost identity or output: call=%q bytes=%d", data.CallID, len(data.Output))
	}
}

func TestHandleRunPersistsCurrentSentTranscript(t *testing.T) {
	srv, _ := newTestServer(t, usageProvider{})
	srv.handleRun(context.Background(), Request{Type: "run", SessionID: "persisted", Prompt: "answer", Model: "fake/model"}, newWriter(io.Discard))
	saved, err := srv.store.Load("persisted")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.SentTranscript) == 0 || len(saved.SentTranscript) != len(saved.Messages) {
		t.Fatalf("sent transcript=%d canonical messages=%d", len(saved.SentTranscript), len(saved.Messages))
	}
}

func TestSessionToolsUsesRunLocalSandboxHolder(t *testing.T) {
	srv, loaded := newTestServer(t, &requestCaptureProvider{model: provider.ModelInfo{ID: "model"}})
	_, holder, err := resolveRunSecurity(loaded, "standard", "read-only", false)
	if err != nil {
		t.Fatal(err)
	}
	registry := srv.sessionTools("session", srv.cwd, "fake/model", "", nil, permission.New(nil, loaded.ProjectRoot), holder, nil, newWriter(io.Discard))
	tool, ok := registry.Get("bash")
	if !ok {
		t.Fatal("run-local registry has no bash tool")
	}
	bashTool, ok := tool.(tools.BashTool)
	if !ok || bashTool.Sandbox != holder {
		t.Fatalf("bash tool sandbox = %#v, want run-local holder %#v", tool, holder)
	}
}

// TestInterruptStopsRun verifies the full stop path: a running request is
// cancelled by an interrupt and the daemon reports cancelled + done.
func TestInterruptStopsRun(t *testing.T) {
	prov := &blockingProvider{start: make(chan struct{}, 1), closed: make(chan struct{})}
	srv, _ := newTestServer(t, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go srv.serveConn(ctx, inR, newWriter(outW))

	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)

	// Start a run against the blocking provider.
	sendLine(t, inW, map[string]any{
		"type":       "run",
		"session_id": "sess-1",
		"prompt":     "do work",
		"model":      "fake/model",
	})

	// The run is live once the provider streams its first event.
	select {
	case <-prov.start:
	case <-time.After(5 * time.Second):
		t.Fatal("run never reached the provider")
	}

	// Interrupt it.
	sendLine(t, inW, map[string]any{"type": "interrupt", "session_id": "sess-1"})

	// Expect a cancelled response then a done response.
	seenCancelled := false
	seenDone := false
	deadline := time.After(10 * time.Second)
	for !seenDone {
		select {
		case <-deadline:
			t.Fatalf("run did not stop: cancelled=%v done=%v", seenCancelled, seenDone)
		default:
		}
		res := nextResponse(t, sc, 5*time.Second)
		switch res.Type {
		case "event":
			// Fine — trailing stream events.
		case "cancelled":
			seenCancelled = true
			if res.SessionID != "sess-1" {
				t.Fatalf("cancelled for wrong session %q", res.SessionID)
			}
		case "done":
			seenDone = true
		case "error":
			t.Fatalf("unexpected error response: %s", res.Error)
		}
	}
	if !seenCancelled {
		t.Fatal("expected a cancelled response, got none")
	}
}

// TestQueryWhileRunActive verifies concurrent dispatch: a ping is answered
// while a run is still blocking.
func TestQueryWhileRunActive(t *testing.T) {
	prov := &blockingProvider{start: make(chan struct{}, 1), closed: make(chan struct{})}
	srv, _ := newTestServer(t, prov)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go srv.serveConn(ctx, inR, newWriter(outW))

	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)

	sendLine(t, inW, map[string]any{
		"type":       "run",
		"session_id": "sess-2",
		"prompt":     "do work",
		"model":      "fake/model",
	})
	select {
	case <-prov.start:
	case <-time.After(5 * time.Second):
		t.Fatal("run never reached the provider")
	}

	sendLine(t, inW, map[string]any{"type": "ping"})

	deadline := time.After(5 * time.Second)
	for {
		res := nextResponse(t, sc, 5*time.Second)
		if res.Type == "pong" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("ping was not answered while run was active")
		default:
		}
	}

	// Clean up.
	sendLine(t, inW, map[string]any{"type": "interrupt", "session_id": "sess-2"})
	select {
	case <-prov.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not stop after interrupt")
	}
}

func TestResumeAccumulatesUsageAndPersistsCurrentContext(t *testing.T) {
	srv, _ := newTestServer(t, usageProvider{})
	prior := &session.Session{
		ID: "usage-session", Cwd: srv.cwd, Model: "fake/model",
		Messages:    []provider.Message{provider.UserText("old"), provider.AssistantText("answer")},
		Usage:       session.Usage{Input: 100, Output: 20, CacheRead: 30, CacheWrite: 2},
		ContextUsed: 132,
	}
	if err := srv.store.Save(prior); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	srv.handleRun(context.Background(), Request{
		Type: "run", SessionID: prior.ID, Prompt: "continue", Model: "fake/model", Resume: true,
	}, newWriter(&output))

	updated, err := srv.store.Load(prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := updated.Usage, (session.Usage{Input: 140, Output: 30, CacheRead: 80, CacheWrite: 7}); got != want {
		t.Fatalf("cumulative usage = %+v, want %+v", got, want)
	}
	if got, want := updated.ContextUsed, 95; got != want {
		t.Fatalf("context used = %d, want current prompt tokens %d", got, want)
	}

	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	foundUsage := false
	for scanner.Scan() {
		var response Response
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Type != "event" || response.Event != "Usage" {
			continue
		}
		var payload map[string]int
		if err := json.Unmarshal(response.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["context_tokens"] != 95 || payload["context_limit"] != 128000 {
			t.Fatalf("usage payload = %+v, want current context and model limit", payload)
		}
		foundUsage = true
	}
	if !foundUsage {
		t.Fatal("run emitted no usage event")
	}
}

func TestRunContinuesFromToolResultToFinalContent(t *testing.T) {
	prov := &continuationProvider{}
	srv, _ := newTestServer(t, prov)
	srv.tools.Register(continuationTool{})

	var output bytes.Buffer
	srv.handleRun(context.Background(), Request{
		Type: "run", SessionID: "continuation-session", Prompt: "use the tool", Model: "fake/model",
	}, newWriter(&output))

	var sequence []string
	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	for scanner.Scan() {
		var response Response
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Type == "event" {
			sequence = append(sequence, response.Event)
		} else if response.Type == "done" || response.Type == "error" {
			sequence = append(sequence, response.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	indexOf := func(value string) int {
		for index, item := range sequence {
			if item == value {
				return index
			}
		}
		return -1
	}
	toolUse := indexOf("ToolUse")
	toolResult := indexOf("ToolResult")
	content := indexOf("Content")
	done := indexOf("done")
	if toolUse < 0 || toolResult <= toolUse || content <= toolResult || done <= content {
		t.Fatalf("unexpected continuation sequence: %v", sequence)
	}
	if indexOf("error") >= 0 {
		t.Fatalf("tool continuation failed: %v", sequence)
	}
}

// TestAuthLifecycle exercises the auth protocol end-to-end against an
// isolated RICK_HOME: save, add_keys, remove_key, update, and remove must all
// round-trip through auth.json without ever exposing a plaintext key.
func TestAuthLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RICK_HOME", home)
	srv, _ := newTestServer(t, &blockingProvider{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go srv.serveConn(ctx, inR, newWriter(outW))
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)

	authRowsOf := func(res Response) []authRow {
		t.Helper()
		var rows []authRow
		if err := json.Unmarshal(res.Data, &rows); err != nil {
			t.Fatalf("decode auth rows: %v", err)
		}
		return rows
	}

	// Save a key for openrouter.
	sendLine(t, inW, map[string]any{
		"type": "auth", "action": "save", "provider": "openrouter", "api_key": "sk-abcdefgh12345678",
	})
	rows := authRowsOf(nextResponse(t, sc, 5*time.Second))
	row := findAuthRow(t, rows, "openrouter")
	if !row.Connected {
		t.Fatalf("openrouter not connected: %+v", row)
	}
	if row.MaskedKey == "" || row.MaskedKey == "sk-abcdefgh12345678" {
		t.Fatalf("key must be masked, got %q", row.MaskedKey)
	}
	if row.KeyCount != 1 {
		t.Fatalf("expected 1 key, got %d", row.KeyCount)
	}

	// Add a second key; key count becomes 2.
	sendLine(t, inW, map[string]any{
		"type": "auth", "action": "add_keys", "provider": "openrouter",
		"api_keys": []string{"sk-second-key-1234"},
	})
	rows = authRowsOf(nextResponse(t, sc, 5*time.Second))
	row = findAuthRow(t, rows, "openrouter")
	if row.KeyCount != 2 {
		t.Fatalf("expected 2 keys, got %d", row.KeyCount)
	}

	// Remove the first key; one remains.
	sendLine(t, inW, map[string]any{"type": "auth", "action": "remove_key", "provider": "openrouter", "key_index": 1})
	rows = authRowsOf(nextResponse(t, sc, 5*time.Second))
	row = findAuthRow(t, rows, "openrouter")
	if row.KeyCount != 1 {
		t.Fatalf("expected 1 key after removal, got %d", row.KeyCount)
	}

	// Toggle only_free and key mode.
	onlyFree := true
	sendLine(t, inW, map[string]any{
		"type": "auth", "action": "update", "provider": "openrouter",
		"only_free": onlyFree, "key_mode": "round-robin",
	})
	rows = authRowsOf(nextResponse(t, sc, 5*time.Second))
	row = findAuthRow(t, rows, "openrouter")
	if !row.OnlyFree || row.KeyMode != "round-robin" {
		t.Fatalf("update did not apply: %+v", row)
	}

	// Remove the provider entirely; it falls back to the catalog entry.
	sendLine(t, inW, map[string]any{"type": "auth", "action": "remove", "provider": "openrouter"})
	rows = authRowsOf(nextResponse(t, sc, 5*time.Second))
	row = findAuthRow(t, rows, "openrouter")
	if row.Connected || row.KeyCount != 0 {
		t.Fatalf("openrouter should be disconnected after removal: %+v", row)
	}
}

func TestSwarmRuntimeResponseCollapsesWorkerActivityIntoAgentUpdates(t *testing.T) {
	toolEvent := swarm.RuntimeEvent{
		Name: "business",
		Kind: swarm.EventAgentTool,
		Value: agent.Event{Kind: agent.EvToolStart, Tool: &agent.ToolEvent{
			Name: "websearch", Title: "Elon Musk companies",
		}},
	}
	response, ok := swarmRuntimeResponse("session", "swarm-1", "research", toolEvent)
	if !ok || response.Event != "agent.updated" || response.SessionID != "session" {
		t.Fatalf("tool activity response = %#v, %v", response, ok)
	}
	var payload struct {
		SwarmID string `json:"swarm_id"`
		Agents  []struct {
			Name        string `json:"name"`
			Status      string `json:"status"`
			CurrentTool string `json:"current_tool"`
			Action      string `json:"action"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(response.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SwarmID != "swarm-1" || len(payload.Agents) != 1 || payload.Agents[0].Name != "business" || payload.Agents[0].CurrentTool != "websearch" || payload.Agents[0].Status != "working" {
		t.Fatalf("agent update payload = %#v", payload)
	}

	if _, ok := swarmRuntimeResponse("session", "swarm-1", "research", swarm.RuntimeEvent{
		Name: "business", Kind: swarm.EventAgentTool, Value: agent.Event{Kind: agent.EvText, Text: "streamed child prose"},
	}); ok {
		t.Fatal("child text should not become a parent timeline event")
	}

	response, ok = swarmRuntimeResponse("session", "swarm-1", "research", swarm.RuntimeEvent{Name: "business", Kind: swarm.EventAgentDone, Value: "finished"})
	if !ok || response.Event != "agent.updated" {
		t.Fatalf("completion response = %#v, %v", response, ok)
	}
	var completed struct {
		Agents []struct {
			Status string `json:"status"`
			Result string `json:"result"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(response.Data, &completed); err != nil {
		t.Fatal(err)
	}
	if len(completed.Agents) != 1 || completed.Agents[0].Status != "completed" || completed.Agents[0].Result != "finished" {
		t.Fatalf("completion payload = %#v", completed)
	}
}

func findAuthRow(t *testing.T, rows []authRow, id string) authRow {
	t.Helper()
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("row %q not found", id)
	return authRow{}
}
