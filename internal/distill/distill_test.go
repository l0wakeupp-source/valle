package distill

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"rick/internal/provider"
)

type stubSummarizer struct {
	summary string
	err     error
	calls   int
}

func (s *stubSummarizer) Summarize(_ context.Context, messages []provider.Message) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return fmt.Sprintf("%s\n%s", s.summary, messages[0].Text()), nil
}

// pair builds one atomic assistant tool_use + user tool_result turn.
func pair(id, payload string) []provider.Message {
	return []provider.Message{
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "tool_use", ID: id, Name: "read", Input: []byte(`{"path":"a.go"}`)}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock(id, payload, false)}},
	}
}

// bigHistory builds 3 old turns (a user request plus a tool pair each) plus a
// live zone of two logical turns and a final request.
func bigHistory() []provider.Message {
	var messages []provider.Message
	for i := 0; i < 3; i++ {
		messages = append(messages, provider.UserText(fmt.Sprintf("old request %d", i)))
		messages = append(messages, pair(fmt.Sprintf("t%d", i), strings.Repeat("payload", 500))...)
	}
	messages = append(messages, provider.UserText("live request"))
	messages = append(messages, pair("tlive", strings.Repeat("live", 200))...)
	messages = append(messages, provider.UserText("latest request"))
	return messages
}

func baseOptions(s *stubSummarizer) Options {
	return Options{
		Summarizer:         s,
		MaxMessages:        12,
		LiveZoneTurns:      2,
		MinCacheBreakBytes: 1,
		MinHistoryTokens:   1,
		MinRatio:           0.0,
		MinLiveRatio:       0.5,
	}
}

func TestDistillReplacesOldPrefix(t *testing.T) {
	messages := bigHistory()
	result := Distill(messages, map[int]bool{1: true}, baseOptions(&stubSummarizer{summary: "goal: fix build"}))

	if !result.Replaced {
		t.Fatalf("expected distillation: %+v", result.Err)
	}
	// Cache prefix + summary + the newer half (13 - 6 = 7 messages).
	if len(result.Messages) != 9 {
		t.Fatalf("got %d messages, want 9: %d", len(result.Messages), len(result.Messages))
	}
	if result.Messages[0].Text() != "old request 0" {
		t.Fatalf("cache prefix lost: %+v", result.Messages[0])
	}
	if !strings.Contains(result.Messages[1].Text(), "goal: fix build") {
		t.Fatalf("summary message missing: %+v", result.Messages[1])
	}
	if result.Messages[len(result.Messages)-1].Text() != "latest request" {
		t.Fatalf("live zone lost: %+v", result.Messages[len(result.Messages)-1])
	}
	// The oldest half after the breakpoint (index 1..5) is distilled away.
	if result.OmittedCount != 5 {
		t.Fatalf("omitted %d messages, want 5", result.OmittedCount)
	}
	if result.AfterBytes >= result.BeforeBytes {
		t.Fatal("distillation did not save bytes")
	}
}

func TestDistillNeverSplitsToolPair(t *testing.T) {
	messages := bigHistory()
	result := Distill(messages, map[int]bool{1: true}, baseOptions(&stubSummarizer{summary: "s"}))

	if !result.Replaced {
		t.Fatalf("expected distillation: %+v", result.Err)
	}
	// Every remaining tool_use must be immediately followed by its tool_result.
	for index, message := range result.Messages {
		if hasBlock(message, "tool_use") {
			if index+1 >= len(result.Messages) || !hasBlock(result.Messages[index+1], "tool_result") {
				t.Fatalf("tool pair was split at index %d", index)
			}
		}
	}
}

func TestDistillRequiresCacheBreakpoint(t *testing.T) {
	messages := bigHistory()
	result := Distill(messages, map[int]bool{}, baseOptions(&stubSummarizer{summary: "s"}))
	if result.Replaced {
		t.Fatal("distillation without a stable cache breakpoint must not replace history")
	}
	if result.Err == nil {
		t.Fatal("expected an error explaining why distillation was skipped")
	}
}

func TestDistillDisabledWithoutSummarizer(t *testing.T) {
	result := Distill(bigHistory(), map[int]bool{1: true}, Options{})
	if result.Replaced || result.Err != nil {
		t.Fatal("distillation without a summarizer must be a no-op")
	}
}

func TestDistillSummarizerFailureLeavesHistoryUntouched(t *testing.T) {
	messages := bigHistory()
	result := Distill(messages, map[int]bool{1: true}, baseOptions(&stubSummarizer{err: context.Canceled}))
	if result.Replaced {
		t.Fatal("a failed summarizer must not replace history")
	}
	if result.Err == nil {
		t.Fatal("expected a wrapped summarizer error")
	}
	if len(result.Messages) != len(messages) {
		t.Fatal("history must be returned unchanged")
	}
}
