package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"rick/internal/provider"
	"rick/internal/tools"
)

func TestCapModelToolOutputPreservesCanonicalEventOutput(t *testing.T) {
	fullOutput := "\x1b[2K\rprogress 10%\nFAIL: important diagnostic\n" + strings.Repeat("details ", maxModelToolResultBytes)
	registry := tools.NewRegistry()
	registry.Register(canonicalOutputTool{output: fullOutput})
	runner := New(Config{Tools: registry})

	block, event := runner.execOne(context.Background(), provider.ToolCall{ID: "call-1", Name: "canonical_output", Input: json.RawMessage(`{}`)})
	if event == nil || event.Output != fullOutput {
		t.Fatal("canonical tool event output was compressed or changed")
	}
	if len(block.Content) >= len(fullOutput) {
		t.Fatalf("provider-facing result was not reduced: %d >= %d", len(block.Content), len(fullOutput))
	}
	if strings.Contains(block.Content, "progress 10%") {
		t.Fatal("provider-facing result retained progress noise")
	}
	if !strings.Contains(block.Content, "tool output truncated") {
		t.Fatal("provider-facing result lacks truncation marker")
	}
	if event.Optimization == nil || event.Optimization.SavedTokens <= 0 || !event.Optimization.Truncated {
		t.Fatalf("missing compression metrics: %#v", event.Optimization)
	}
}

func TestBuildRequestTrimsOldGroupsWithoutOrphaningToolResults(t *testing.T) {
	runner := New(Config{
		ContextWindow:      200,
		MaxTokens:          10,
		SafetyMarginTokens: 10,
	})
	messages := []provider.Message{
		provider.UserText(strings.Repeat("old context ", 100)),
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "tool_use", ID: "tool-1", Name: "read", Input: json.RawMessage(`{"path":"x"}`)}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock("tool-1", "result", false)}},
		provider.UserText("latest request"),
	}

	request := runner.buildRequest(messages, nil)
	if len(request.Messages) >= len(messages) {
		t.Fatal("buildRequest did not trim the over-budget history")
	}
	for index, message := range request.Messages {
		if containsBlock(message, "tool_use") {
			if index+1 >= len(request.Messages) || !containsBlock(request.Messages[index+1], "tool_result") {
				t.Fatal("trimmed request left an orphaned tool call")
			}
		}
	}
}

type canonicalOutputTool struct {
	output string
}

func (canonicalOutputTool) Name() string           { return "canonical_output" }
func (canonicalOutputTool) Description() string    { return "test output tool" }
func (canonicalOutputTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (canonicalOutputTool) ReadOnly() bool         { return true }
func (tool canonicalOutputTool) Run(context.Context, tools.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Output: tool.output}, nil
}
