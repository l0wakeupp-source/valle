package headless

import (
    "bytes"
    "context"
    "encoding/json"
    "strings"
    "testing"

    "rick/internal/config"
    "rick/internal/permission"
    "rick/internal/plugin"
    "rick/internal/provider"
    "rick/internal/session"
    "rick/internal/tools"
)

// mockProvider is a minimal provider that returns a fixed text response.
type mockProvider struct {
    response string
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Models() []provider.ModelInfo {
    return []provider.ModelInfo{{ID: "mock-model", Name: "Mock", ContextWindow: 8192}}
}

func (m *mockProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
    defer close(ch)
    ch <- provider.Event{Kind: provider.EventText, Text: m.response}
    ch <- provider.Event{Kind: provider.EventUsage, Usage: &provider.Usage{
        InputTokens:  10,
        OutputTokens: 20,
    }}
    ch <- provider.Event{Kind: provider.EventDone, StopReason: "end_turn"}
}

func newTestDeps(resp string) Deps {
    reg := tools.NewRegistry()
    perm := permission.New(&config.Permission{Default: config.PermAllow}, "")
    return Deps{
        Provider: &mockProvider{response: resp},
        ModelID:  "mock-model",
        Config:   config.Config{MaxTokens: 1024},
        Tools:    reg,
        Perms:    perm,
        Plugins:  plugin.NewRegistry(),
        Store:    nil,
    }
}

func TestRunTextFormat(t *testing.T) {
    var stdout, stderr bytes.Buffer
    deps := newTestDeps("Hello from mock")
    opts := Options{
        Prompt:      "say hello",
        Model:       "mock/mock-model",
        Format:      FormatText,
        Cwd:         t.TempDir(),
        ProjectRoot: t.TempDir(),
        MaxTurns:    5,
    }

    err := Run(context.Background(), opts, deps, &stdout, &stderr)
    if err != nil {
        t.Fatalf("Run returned error: %v", err)
    }
    out := stdout.String()
    if !strings.Contains(out, "Hello from mock") {
        t.Errorf("expected stdout to contain %q, got %q", "Hello from mock", out)
    }
}

func TestRunJSONFormat(t *testing.T) {
    var stdout, stderr bytes.Buffer
    deps := newTestDeps("JSON response")
    opts := Options{
        Prompt:      "test json",
        Model:       "mock/mock-model",
        Format:      FormatJSON,
        Cwd:         t.TempDir(),
        ProjectRoot: t.TempDir(),
        MaxTurns:    5,
    }

    err := Run(context.Background(), opts, deps, &stdout, &stderr)
    if err != nil {
        t.Fatalf("Run returned error: %v", err)
    }

    var result Result
    if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
        t.Fatalf("failed to parse JSON output: %v\nraw: %s", err, stdout.String())
    }
    if result.Response != "JSON response" {
        t.Errorf("expected response %q, got %q", "JSON response", result.Response)
    }
    if result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 20 {
        t.Errorf("unexpected usage: %+v", result.Usage)
    }
    if result.SessionID == "" {
        t.Error("expected non-empty session_id")
    }
}

func TestRunStreamJSONFormat(t *testing.T) {
    var stdout, stderr bytes.Buffer
    deps := newTestDeps("stream test")
    opts := Options{
        Prompt:      "test stream",
        Model:       "mock/mock-model",
        Format:      FormatStreamJSON,
        Cwd:         t.TempDir(),
        ProjectRoot: t.TempDir(),
        MaxTurns:    5,
    }

    err := Run(context.Background(), opts, deps, &stdout, &stderr)
    if err != nil {
        t.Fatalf("Run returned error: %v", err)
    }

    lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
    if len(lines) < 2 {
        t.Fatalf("expected at least 2 NDJSON lines, got %d: %q", len(lines), stdout.String())
    }

    // First line should be a text event.
    var ev streamEvent
    if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
        t.Fatalf("failed to parse first NDJSON line: %v", err)
    }
    if ev.Type != "text" || ev.Text != "stream test" {
        t.Errorf("expected text event with %q, got type=%q text=%q", "stream test", ev.Type, ev.Text)
    }

    // Last line should be done.
    var last streamEvent
    if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
        t.Fatalf("failed to parse last NDJSON line: %v", err)
    }
    if last.Type != "done" {
        t.Errorf("expected last event type %q, got %q", "done", last.Type)
    }
}

func TestRunSavesSession(t *testing.T) {
    dir := t.TempDir()
    store, err := session.NewStore(dir)
    if err != nil {
        t.Fatalf("failed to create session store: %v", err)
    }

    var stdout, stderr bytes.Buffer
    deps := newTestDeps("session test")
    deps.Store = store
    opts := Options{
        Prompt:      "save me",
        Model:       "mock/mock-model",
        Format:      FormatText,
        Cwd:         t.TempDir(),
        ProjectRoot: t.TempDir(),
        MaxTurns:    5,
    }

    if err := Run(context.Background(), opts, deps, &stdout, &stderr); err != nil {
        t.Fatalf("Run returned error: %v", err)
    }

    metas, err := store.List("")
    if err != nil {
        t.Fatalf("failed to list sessions: %v", err)
    }
    if len(metas) != 1 {
        t.Fatalf("expected 1 saved session, got %d", len(metas))
    }
    if metas[0].Messages < 2 {
        t.Errorf("expected at least 2 messages (user + assistant), got %d", metas[0].Messages)
    }
}

func TestRunYoloMode(t *testing.T) {
    var stdout, stderr bytes.Buffer
    deps := newTestDeps("yolo works")
    opts := Options{
        Prompt:      "test yolo",
        Model:       "mock/mock-model",
        Yolo:        true,
        Format:      FormatText,
        Cwd:         t.TempDir(),
        ProjectRoot: t.TempDir(),
        MaxTurns:    5,
    }

    err := Run(context.Background(), opts, deps, &stdout, &stderr)
    if err != nil {
        t.Fatalf("Run returned error: %v", err)
    }
    if !strings.Contains(stdout.String(), "yolo works") {
        t.Errorf("expected yolo response in output, got %q", stdout.String())
    }
}
