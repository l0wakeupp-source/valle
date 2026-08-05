package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

type doneWithoutCloseProvider struct{}

func (doneWithoutCloseProvider) Name() string                 { return "done-without-close" }
func (doneWithoutCloseProvider) Models() []provider.ModelInfo { return nil }
func (doneWithoutCloseProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
	ch <- provider.Event{Kind: provider.EventText, Text: "answer"}
	ch <- provider.Event{Kind: provider.EventDone, StopReason: "end_turn"}
}

func TestRunnerStopsAtDoneWithoutWaitingForProviderClose(t *testing.T) {
	runner := New(Config{Provider: doneWithoutCloseProvider{}, Model: "done-model", Tools: tools.NewRegistry()})
	events := make(chan Event, 16)
	_, err := runner.Run(context.Background(), []provider.Message{provider.UserText("hello")}, events)
	if err != nil {
		t.Fatalf("Run error = %v, want success", err)
	}
	seenDone := false
	for event := range events {
		if event.Kind == EvDone {
			seenDone = true
		}
	}
	if !seenDone {
		t.Fatal("runner did not emit completion after provider EventDone")
	}
}

type errorWithoutCloseProvider struct{}

func (errorWithoutCloseProvider) Name() string                 { return "error-without-close" }
func (errorWithoutCloseProvider) Models() []provider.ModelInfo { return nil }
func (errorWithoutCloseProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
	ch <- provider.Event{Kind: provider.EventError, Err: errors.New("stream failed")}
}

func TestRunnerStopsAtErrorWithoutWaitingForProviderClose(t *testing.T) {
	runner := New(Config{Provider: errorWithoutCloseProvider{}, Model: "error-model", Tools: tools.NewRegistry()})
	events := make(chan Event, 16)
	_, err := runner.Run(context.Background(), []provider.Message{provider.UserText("hello")}, events)
	if err == nil || err.Error() != "stream failed" {
		t.Fatalf("Run error = %v, want provider error", err)
	}
}

// workProvider emits tool calls for limit turns (input varies each turn so the
// repeated-call guard does not fire), then finishes with a plain answer.
type workProvider struct {
	limit int
	calls *int
}

func (workProvider) Name() string                 { return "work-provider" }
func (workProvider) Models() []provider.ModelInfo { return nil }
func (p workProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
	defer close(ch)
	*p.calls++
	if *p.calls <= p.limit {
		input, _ := json.Marshal(map[string]any{"command": fmt.Sprintf("work-%d", *p.calls)})
		ch <- provider.Event{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
			ID: fmt.Sprintf("c%d", *p.calls), Name: "shell", Input: input,
		}}
		ch <- provider.Event{Kind: provider.EventDone, StopReason: "tool_use"}
		return
	}
	ch <- provider.Event{Kind: provider.EventText, Text: "finished"}
	ch <- provider.Event{Kind: provider.EventDone, StopReason: "end_turn"}
}

// TestRunnerUnlimitedTurnsByDefault pins that a runner without an explicit
// MaxTurns cap runs past the old 50-turn default to a normal completion.
func TestRunnerUnlimitedTurnsByDefault(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(repeatedCallTool{})
	calls := 0
	runner := New(Config{
		Provider: workProvider{limit: 60, calls: &calls},
		Model:    "work-model",
		Tools:    registry,
	})
	events := make(chan Event, 256)
	_, err := runner.Run(context.Background(), []provider.Message{provider.UserText("do the work")}, events)
	if err != nil {
		t.Fatalf("unlimited run should complete, got error: %v", err)
	}
	if calls != 61 {
		t.Fatalf("provider should have run 60 tool turns plus a final answer (61 calls), got %d", calls)
	}
	sawDone := false
	for event := range events {
		if event.Kind == EvDone {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("unlimited run did not emit EvDone")
	}
}

// TestRunnerRespectsExplicitTurnCap pins that a positive MaxTurns still stops
// the loop with the actionable error.
func TestRunnerRespectsExplicitTurnCap(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(repeatedCallTool{})
	calls := 0
	runner := New(Config{
		Provider: workProvider{limit: 10, calls: &calls},
		Model:    "work-model",
		Tools:    registry,
		MaxTurns: 3,
	})
	events := make(chan Event, 64)
	_, err := runner.Run(context.Background(), []provider.Message{provider.UserText("do the work")}, events)
	if err == nil || !strings.Contains(err.Error(), "stopped after 3 turns") {
		t.Fatalf("Run error = %v, want max-turns stop", err)
	}
	sawError := false
	for event := range events {
		if event.Kind == EvError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("turn-cap stop did not emit an error event")
	}
}
