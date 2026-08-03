package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"rick/internal/agent"
	"rick/internal/config"
	"rick/internal/goal"
	"rick/internal/mcp"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/session"
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
		runCancel:   map[string]context.CancelFunc{},
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
