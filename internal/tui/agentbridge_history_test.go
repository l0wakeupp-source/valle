package tui

import (
	"encoding/json"
	"testing"

	"rick/internal/agent"
	"rick/internal/goal"
	"rick/internal/plugin"
	"rick/internal/provider"
)

type testSnapshotter struct{}

func (testSnapshotter) Snapshot(string) (string, error) { return "", nil }

func TestRebuildHistoryGroupsParallelToolCallsInOneAssistantTurn(t *testing.T) {
	model := &Model{msgs: []ChatMsg{
		{Kind: MsgAssistant, Text: "checking"},
		{Kind: MsgTool, CallID: "call-1", ToolName: "read", ToolInput: json.RawMessage(`{"path":"a"}`), ToolOutput: "A"},
		{Kind: MsgTool, CallID: "call-2", ToolName: "read", ToolInput: json.RawMessage(`{"path":"b"}`), ToolOutput: "B"},
		{Kind: MsgAssistant, Text: "done"},
	}}

	model.rebuildHistory()

	if len(model.history) != 3 {
		t.Fatalf("history has %d messages, want assistant tools + user results + assistant text", len(model.history))
	}
	first := model.history[0]
	if first.Role != provider.RoleAssistant || len(first.Content) != 3 {
		t.Fatalf("first message = role %q with %d blocks, want one assistant turn with text and two tool calls", first.Role, len(first.Content))
	}
	if first.Content[1].Type != "tool_use" || first.Content[2].Type != "tool_use" {
		t.Fatalf("assistant tool blocks were not grouped: %#v", first.Content)
	}
	results := model.history[1]
	if results.Role != provider.RoleUser || len(results.Content) != 2 {
		t.Fatalf("results message = role %q with %d blocks, want one user turn with two results", results.Role, len(results.Content))
	}
	if results.Content[0].Type != "tool_result" || results.Content[1].Type != "tool_result" {
		t.Fatalf("tool results were not grouped: %#v", results.Content)
	}
	if model.history[2].Role != provider.RoleAssistant || model.history[2].Text() != "done" {
		t.Fatalf("terminal assistant message = %#v", model.history[2])
	}
}

func TestSwarmWorkerConfigInheritsPolicyAndAccounting(t *testing.T) {
	plugins := plugin.NewRegistry()
	goals := &goal.Store{}
	snapshots := testSnapshotter{}
	cfg := inheritSwarmWorkerRuntime(agent.Config{}, snapshots, plugins, goals)

	if cfg.Snapshotter != snapshots {
		t.Fatal("worker did not inherit snapshotter")
	}
	if cfg.Plugins != plugins {
		t.Fatal("worker did not inherit plugin registry")
	}
	if cfg.Goals != goals {
		t.Fatal("worker did not inherit goal store")
	}
}
