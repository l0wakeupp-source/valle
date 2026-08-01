package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"rick/internal/provider"
	"rick/internal/tools"
)

type equivalentJSONProvider struct {
	calls atomic.Int32
}

func (p *equivalentJSONProvider) Name() string                 { return "equivalent-json-provider" }
func (p *equivalentJSONProvider) Models() []provider.ModelInfo { return nil }
func (p *equivalentJSONProvider) Stream(_ context.Context, _ provider.Request, ch chan<- provider.Event) {
	inputs := []json.RawMessage{
		json.RawMessage(`{"a":1,"b":2}`),
		json.RawMessage(`{"b":2,"a":1}`),
		json.RawMessage(`{ "a" : 1, "b" : 2 }`),
	}
	index := int(p.calls.Add(1)) - 1
	input := inputs[index%len(inputs)]
	defer close(ch)
	ch <- provider.Event{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
		ID: "call-" + string(rune('a'+index)), Name: "inspect", Input: input,
	}}
	ch <- provider.Event{Kind: provider.EventDone, StopReason: "tool_use"}
}

type equivalentJSONTool struct{}

func (equivalentJSONTool) Name() string           { return "inspect" }
func (equivalentJSONTool) Description() string    { return "inspects a value" }
func (equivalentJSONTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (equivalentJSONTool) ReadOnly() bool         { return true }
func (equivalentJSONTool) Run(context.Context, tools.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Output: "same"}, nil
}

func TestRunnerTreatsEquivalentJSONToolCallsAsRepeated(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(equivalentJSONTool{})
	runner := New(Config{
		Provider: &equivalentJSONProvider{},
		Model:    "equivalent-json-model",
		Tools:    registry,
		MaxTurns: 3,
	})

	_, err := runner.Run(context.Background(), []provider.Message{provider.UserText("repeat")}, make(chan Event, 32))
	if err == nil || err.Error() != "agent: repeated tool call limit reached for inspect" {
		t.Fatalf("Run error = %v, want equivalent-input repeat guard error", err)
	}
}

func TestCanonicalToolInputNormalizesEquivalentNumbers(t *testing.T) {
	want := canonicalToolInput(json.RawMessage(`{"value":1}`))
	for _, input := range []string{
		`{"value":1.0}`,
		`{"value":1e0}`,
		`{"value":10e-1}`,
		`{"value":0.1000e1}`,
	} {
		if got := canonicalToolInput(json.RawMessage(input)); got != want {
			t.Errorf("canonicalToolInput(%s) = %q, want %q", input, got, want)
		}
	}
}

type countingSnapshotter struct {
	count atomic.Int32
}

func (s *countingSnapshotter) Snapshot(string) (string, error) {
	s.count.Add(1)
	return "snapshot", nil
}

type mutatingRegressionTool struct {
	name string
}

func (t mutatingRegressionTool) Name() string        { return t.name }
func (t mutatingRegressionTool) Description() string { return "mutates state" }
func (t mutatingRegressionTool) Schema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t mutatingRegressionTool) ReadOnly() bool { return false }
func (t mutatingRegressionTool) Run(context.Context, tools.Context, json.RawMessage) (tools.Result, error) {
	return tools.Result{Output: t.name}, nil
}

func TestRunnerSnapshotsOnceForMultipleMutationsInOneTurn(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(mutatingRegressionTool{name: "write-one"})
	registry.Register(mutatingRegressionTool{name: "write-two"})
	snapshotter := &countingSnapshotter{}
	runner := New(Config{Tools: registry, Snapshotter: snapshotter})

	runner.execTools(context.Background(), []provider.ToolCall{
		{ID: "one", Name: "write-one", Input: json.RawMessage(`{"value":1}`)},
		{ID: "two", Name: "write-two", Input: json.RawMessage(`{"value":2}`)},
	}, func(Event) bool { return true })

	if got := snapshotter.count.Load(); got != 1 {
		t.Fatalf("snapshot count = %d, want one snapshot for the tool turn", got)
	}
}
