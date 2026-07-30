package agent

import (
	"context"
	"encoding/json"
	"testing"

	"rick/internal/provider"
	"rick/internal/tools"
)

type repeatedCallProvider struct{}

func (repeatedCallProvider) Name() string                 { return "loop-provider" }
func (repeatedCallProvider) Models() []provider.ModelInfo { return nil }
func (repeatedCallProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
	defer close(ch)
	input := json.RawMessage(`{"command":"same"}`)
	ch <- provider.Event{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{ID: "new-call", Name: "shell", Input: input}}
	ch <- provider.Event{Kind: provider.EventDone, StopReason: "tool_use"}
}

type repeatedCallTool struct{}

func (repeatedCallTool) Name() string           { return "shell" }
func (repeatedCallTool) Description() string    { return "runs a command" }
func (repeatedCallTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (repeatedCallTool) ReadOnly() bool         { return true }
func (repeatedCallTool) Run(context.Context, tools.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Output: "same output"}, nil
}

func TestRunnerStopsRepeatedToolLoop(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(repeatedCallTool{})
	runner := New(Config{
		Provider: repeatedCallProvider{},
		Model:    "loop-model",
		Tools:    registry,
		MaxTurns: 10,
	})
	events := make(chan Event, 64)
	_, err := runner.Run(context.Background(), []provider.Message{provider.UserText("repeat")}, events)
	if err == nil || err.Error() != "agent: repeated tool call limit reached for shell" {
		t.Fatalf("Run error = %v, want repeated-call guard error", err)
	}
	sawError := false
	for event := range events {
		if event.Kind == EvError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("repeated-call guard did not emit an error event")
	}
}

type silentProvider struct{}

func (silentProvider) Name() string                 { return "silent-provider" }
func (silentProvider) Models() []provider.ModelInfo { return nil }
func (silentProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
	close(ch)
}

func TestRunnerReportsProviderStreamWithoutDone(t *testing.T) {
	registry := tools.NewRegistry()
	runner := New(Config{Provider: silentProvider{}, Model: "silent-model", Tools: registry, MaxTurns: 1})
	events := make(chan Event, 16)
	_, err := runner.Run(context.Background(), nil, events)
	if err == nil || err.Error() != "agent: provider stream ended without a completion event" {
		t.Fatalf("Run error = %v, want missing-completion error", err)
	}
}
