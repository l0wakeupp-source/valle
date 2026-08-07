package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"rick/internal/agent"
	"rick/internal/goal"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/session"
)

type testSnapshotter struct{}

func (testSnapshotter) Snapshot(string) (string, error) { return "", nil }

func TestRecordRunErrorKeepsDiagnosticOutOfProviderHistory(t *testing.T) {
	model := &Model{
		sess:    &session.Session{},
		history: []provider.Message{provider.UserText("hello")},
	}

	model.recordRunError(errors.New("provider tool_calls rejected malformed arguments"))

	if model.sess.RunError != "provider tool_calls rejected malformed arguments" {
		t.Fatalf("RunError = %q", model.sess.RunError)
	}
	if len(model.history) != 1 || model.history[0].Text() != "hello" {
		t.Fatalf("diagnostic contaminated provider history: %#v", model.history)
	}

	model.recordRunError(nil)
	if model.sess.RunError != "" {
		t.Fatalf("successful run did not clear stale diagnostic: %q", model.sess.RunError)
	}

	model.lastRunError = "stale failure"
	model.resetStats()
	if model.lastRunError != "" {
		t.Fatalf("new-session reset retained stale diagnostic: %q", model.lastRunError)
	}

	model.restoreRunError(&session.Session{RunError: "persisted failure"})
	if model.lastRunError != "persisted failure" {
		t.Fatalf("resume did not hydrate persisted diagnostic: %q", model.lastRunError)
	}
}

func TestSaveSessionReturnsPersistenceFailure(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	model := &Model{
		deps:    Deps{Store: store},
		sess:    &session.Session{ID: "../invalid"},
		history: []provider.Message{provider.UserText("hello")},
		msgs:    []ChatMsg{{Kind: MsgUser, Text: "hello"}},
	}

	err = model.saveSession()
	if err == nil || !strings.Contains(err.Error(), "invalid session id") {
		t.Fatalf("saveSession error = %v, want invalid session id", err)
	}
}

func TestSaveSessionRepairsCurrentPointerForEagerSession(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const cwd = `C:\work`
	model := &Model{
		deps:    Deps{Store: store, Cwd: cwd},
		sess:    &session.Session{ID: "eager-session", Cwd: cwd},
		history: []provider.Message{provider.UserText("hello")},
		msgs:    []ChatMsg{{Kind: MsgUser, Text: "hello"}},
	}

	if err := model.saveSession(); err != nil {
		t.Fatal(err)
	}
	if current := store.GetCurrent(cwd); current != "eager-session" {
		t.Fatalf("current session = %q, want eager-session", current)
	}
}

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

func TestRebuildHistoryPreservesThinkingBlockForReasoningEcho(t *testing.T) {
	model := &Model{msgs: []ChatMsg{
		{Kind: MsgUser, Text: "inspect"},
		{Kind: MsgThinking, Text: "step one"},
		{Kind: MsgAssistant, Text: "ok"},
		{Kind: MsgTool, CallID: "call-1", ToolName: "read", ToolInput: json.RawMessage(`{"path":"a"}`), ToolOutput: "A"},
	}}

	model.rebuildHistory()

	blocks := model.history[1].Content
	if model.history[1].Role != provider.RoleAssistant {
		t.Fatalf("reasoning turn role = %q, want assistant", model.history[1].Role)
	}
	if len(blocks) < 1 || blocks[0].Type != "thinking" || blocks[0].Text != "step one" {
		t.Fatalf("thinking block dropped from rebuilt history: %#v", model.history[1].Content)
	}
}

func TestRebuildHistoryKeepsReasoningInItsOriginalToolTurn(t *testing.T) {
	model := &Model{msgs: []ChatMsg{
		{Kind: MsgUser, Text: "inspect"},
		{Kind: MsgThinking, Text: "first reasoning"},
		{Kind: MsgTool, CallID: "call-1", ToolName: "read", ToolInput: json.RawMessage(`{"path":"a"}`), ToolOutput: "A", TurnBoundary: true},
		{Kind: MsgThinking, Text: "second reasoning"},
		{Kind: MsgTool, CallID: "call-2", ToolName: "read", ToolInput: json.RawMessage(`{"path":"b"}`), ToolOutput: "B", TurnBoundary: true},
		{Kind: MsgAssistant, Text: "done"},
	}}

	model.rebuildHistory()

	if len(model.history) != 6 {
		t.Fatalf("history has %d messages, want user + two tool exchanges + final assistant", len(model.history))
	}
	for _, index := range []int{1, 3} {
		blocks := model.history[index].Content
		if len(blocks) != 2 || blocks[0].Type != "thinking" || blocks[1].Type != "tool_use" {
			t.Fatalf("assistant turn %d was regrouped: %#v", index, blocks)
		}
	}
	if model.history[2].Content[0].ToolUseID != "call-1" || model.history[4].Content[0].ToolUseID != "call-2" {
		t.Fatalf("tool results crossed turn boundaries: %#v", model.history)
	}
	if model.history[5].Text() != "done" {
		t.Fatalf("final answer = %#v", model.history[5])
	}
}

func TestAgentEventsPlaceTurnBoundaryAfterToolResults(t *testing.T) {
	model := &Model{
		pendingTools: map[string]int{},
		toolOutputs:  map[string]string{},
		tx:           newTranscript(),
	}
	model.applyAgentEvent(agent.Event{Kind: agent.EvThinking, Text: "first reasoning"})
	model.applyAgentEvent(agent.Event{Kind: agent.EvTurnEnd})
	model.applyAgentEvent(agent.Event{Kind: agent.EvToolStart, Tool: &agent.ToolEvent{
		CallID: "call-1", Name: "read", Input: json.RawMessage(`{"path":"a"}`),
	}})
	model.applyAgentEvent(agent.Event{Kind: agent.EvToolEnd, Tool: &agent.ToolEvent{
		CallID: "call-1", Name: "read", Input: json.RawMessage(`{"path":"a"}`), Output: "A",
	}})
	model.applyAgentEvent(agent.Event{Kind: agent.EvThinking, Text: "second reasoning"})
	model.flushStream()
	model.rebuildHistory()

	if len(model.history) != 3 {
		t.Fatalf("history = %#v, want first assistant, tool result, second assistant", model.history)
	}
	if blocks := model.history[0].Content; len(blocks) != 2 || blocks[0].Type != "thinking" || blocks[1].Type != "tool_use" {
		t.Fatalf("first turn blocks = %#v", blocks)
	}
	if model.history[1].Content[0].ToolUseID != "call-1" {
		t.Fatalf("tool result = %#v", model.history[1])
	}
	if blocks := model.history[2].Content; len(blocks) != 1 || blocks[0].Type != "thinking" || blocks[0].Text != "second reasoning" {
		t.Fatalf("second turn blocks = %#v", blocks)
	}
}

func TestAgentEventsSeparateConsecutiveToolOnlyTurns(t *testing.T) {
	model := &Model{
		pendingTools: map[string]int{},
		toolOutputs:  map[string]string{},
		tx:           newTranscript(),
	}
	for index, callID := range []string{"call-1", "call-2"} {
		model.applyAgentEvent(agent.Event{Kind: agent.EvTurnEnd})
		model.applyAgentEvent(agent.Event{Kind: agent.EvToolStart, Tool: &agent.ToolEvent{
			CallID: callID, Name: "read", Input: json.RawMessage(`{"path":"a"}`),
		}})
		model.applyAgentEvent(agent.Event{Kind: agent.EvToolEnd, Tool: &agent.ToolEvent{
			CallID: callID, Name: "read", Input: json.RawMessage(`{"path":"a"}`), Output: fmt.Sprintf("result-%d", index+1),
		}})
	}
	model.applyAgentEvent(agent.Event{Kind: agent.EvText, Text: "done"})
	model.flushStream()
	model.rebuildHistory()

	if len(model.history) != 5 {
		t.Fatalf("history = %#v, want two separate tool exchanges and final answer", model.history)
	}
	for _, index := range []int{0, 2} {
		if blocks := model.history[index].Content; len(blocks) != 1 || blocks[0].Type != "tool_use" {
			t.Fatalf("tool-only turn %d was regrouped: %#v", index, blocks)
		}
	}
	if model.history[1].Content[0].ToolUseID != "call-1" || model.history[3].Content[0].ToolUseID != "call-2" {
		t.Fatalf("tool results crossed turns: %#v", model.history)
	}
	if model.history[4].Text() != "done" {
		t.Fatalf("final answer = %#v", model.history[4])
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

func TestRebuildHistoryBoundsLargeToolResultsButPreservesFullLocalHistory(t *testing.T) {
	longOutput := strings.Repeat("header\n", 900) + "final diagnostic"
	model := &Model{
		msgs: []ChatMsg{{Kind: MsgUser, Text: "inspect"}, {
			Kind: MsgTool, CallID: "call-1", ToolName: "bash", ToolOutput: "preview",
		}},
		toolOutputs: map[string]string{"call-1": longOutput},
	}

	model.rebuildHistory()
	bounded := model.history[2].Content[0].Content
	if len([]rune(bounded)) > historyToolOutputChars {
		t.Fatalf("bounded tool result has %d chars, want <= %d", len([]rune(bounded)), historyToolOutputChars)
	}
	if !strings.Contains(bounded, "tool output truncated") || !strings.Contains(bounded, "final diagnostic") {
		t.Fatalf("bounded result lost truncation marker or tail: %q", bounded[len(bounded)-120:])
	}

	full := model.buildHistory(0)[2].Content[0].Content
	if full != longOutput {
		t.Fatalf("full local history changed: got %d chars want %d", len(full), len(longOutput))
	}
}

func TestMessagesToChatUsesCompactPreview(t *testing.T) {
	output := strings.Repeat("x", toolOutputPreviewChars+50)
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "tool_use", ID: "call-1", Name: "read"}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "tool_result", ToolUseID: "call-1", Content: output}}},
	}
	chat := messagesToChat(msgs)
	if len(chat) != 1 || chat[0].ToolOutput == output {
		t.Fatalf("resume preview was not compacted: %+v", chat)
	}
}

func TestMessagesToChatPreservesThinkingForRebuiltHistory(t *testing.T) {
	messages := []provider.Message{{
		Role: provider.RoleAssistant,
		Content: []provider.ContentBlock{
			{Type: "thinking", Text: "prior reasoning"},
			provider.TextBlock("answer"),
		},
	}}

	model := &Model{msgs: messagesToChat(messages)}
	model.rebuildHistory()

	blocks := model.history[0].Content
	if len(blocks) != 2 || blocks[0].Type != "thinking" || blocks[0].Text != "prior reasoning" {
		t.Fatalf("resume round-trip dropped thinking: %#v", blocks)
	}
}

func TestMessagesToChatPreservesToolTurnBoundariesOnResume(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
			{Type: "thinking", Text: "first reasoning"},
			{Type: "tool_use", ID: "call-1", Name: "read", Input: json.RawMessage(`{"path":"a"}`)},
		}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{
			provider.ToolResultBlock("call-1", "A", false),
		}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
			{Type: "thinking", Text: "second reasoning"},
		}},
	}

	model := &Model{msgs: messagesToChat(messages)}
	model.rebuildHistory()

	if len(model.history) != 3 {
		t.Fatalf("resume regrouped turns: %#v", model.history)
	}
	if blocks := model.history[0].Content; len(blocks) != 2 || blocks[0].Type != "thinking" || blocks[1].Type != "tool_use" {
		t.Fatalf("first resumed turn = %#v", blocks)
	}
	if blocks := model.history[2].Content; len(blocks) != 1 || blocks[0].Text != "second reasoning" {
		t.Fatalf("second resumed turn = %#v", blocks)
	}
}
