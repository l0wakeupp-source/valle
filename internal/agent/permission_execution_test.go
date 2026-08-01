package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"rick/internal/config"
	"rick/internal/permission"
	"rick/internal/provider"
	"rick/internal/tools"
)

type approvalExecutionTool struct{ runs atomic.Int32 }

func (t *approvalExecutionTool) Name() string        { return "bash" }
func (t *approvalExecutionTool) Description() string { return "test command" }
func (t *approvalExecutionTool) Schema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *approvalExecutionTool) ReadOnly() bool { return false }
func (t *approvalExecutionTool) Run(context.Context, tools.Context, json.RawMessage) (tools.Result, error) {
	t.runs.Add(1)
	return tools.Result{Output: "executed"}, nil
}

func TestAcceptedPermissionExecutesTool(t *testing.T) {
	tool := &approvalExecutionTool{}
	registry := tools.NewRegistry()
	registry.Register(tool)
	perms := permission.New(&config.Permission{Default: config.PermAsk}, t.TempDir())
	asked := 0
	runner := New(Config{
		Tools: registry,
		Perms: perms,
		Ask: func(context.Context, permission.Request) PermissionDecision {
			asked++
			return DecideAccept
		},
	})

	result, event := runner.execOne(context.Background(), provider.ToolCall{
		ID:    "approval-call",
		Name:  "bash",
		Input: json.RawMessage(`{"command":"touch approved"}`),
	})
	if result.IsError {
		t.Fatalf("accepted tool returned an error: %s", result.Text)
	}
	if event == nil || event.IsError {
		t.Fatalf("accepted tool event = %#v, want successful event", event)
	}
	if asked != 1 {
		t.Fatalf("approval callback count = %d, want 1", asked)
	}
	if runs := tool.runs.Load(); runs != 1 {
		t.Fatalf("tool run count = %d, want 1 after approval", runs)
	}
}
