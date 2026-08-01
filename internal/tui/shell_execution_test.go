package tui

import (
	"context"
	"encoding/json"
	"testing"

	"rick/internal/config"
	"rick/internal/permission"
	"rick/internal/tools"
)

type directShellTestTool struct{ called bool }

func (t *directShellTestTool) Name() string        { return "bash" }
func (t *directShellTestTool) Description() string { return "test shell" }
func (t *directShellTestTool) Schema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *directShellTestTool) ReadOnly() bool { return false }
func (t *directShellTestTool) Run(_ context.Context, _ tools.Context, input json.RawMessage) (tools.Result, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tools.Errf("invalid test input: %v", err), nil
	}
	t.called = args.Command == "printf approved"
	return tools.Result{Output: "approved output"}, nil
}

func TestDirectShellEscapeExecutesTheRegisteredBashTool(t *testing.T) {
	m := newModelChoiceTestModel()
	tool := &directShellTestTool{}
	registry := tools.NewRegistry()
	registry.Register(tool)
	m.deps.Registry = registry
	m.deps.Cwd = t.TempDir()
	m.deps.Perms = permission.New(&config.Permission{Default: config.PermAllow}, m.deps.Cwd)

	_, cmd := m.runShell("printf approved")
	msg := cmd()
	done, ok := msg.(shellDoneMsg)
	if !ok {
		t.Fatalf("shell command returned %T, want shellDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("approved shell command failed: %v", done.err)
	}
	if done.output != "approved output" || !tool.called {
		t.Fatalf("shell result = %#v, tool called = %v", done.output, tool.called)
	}
}
