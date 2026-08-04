package history

import (
	"strings"
	"testing"

	"rick/internal/provider"
	"rick/internal/tokens"
)

func TestRetainKeepsToolCallAndResultTogether(t *testing.T) {
	messages := []provider.Message{
		provider.UserText(strings.Repeat("old ", 120)),
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "tool_use", ID: "call-1", Name: "bash"}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "tool_result", ToolUseID: "call-1", Content: "error: failed"}}},
		provider.UserText("fix the failure"),
	}
	retained, omitted := Retain(messages, 80, tokens.EncodingCl100kBase)
	if omitted == 0 {
		t.Fatal("Retain did not omit an old group")
	}
	foundPair := false
	for index := 0; index+1 < len(retained); index++ {
		if retained[index].Role == provider.RoleAssistant && retained[index+1].Role == provider.RoleUser &&
			strings.Contains(retained[index+1].Content[0].Content, "error: failed") {
			foundPair = true
		}
	}
	if !foundPair {
		t.Fatalf("tool pair was split or discarded: %#v", retained)
	}
}

func TestRetainPreservesCriticalOlderDiagnosticWhenBudgetAllows(t *testing.T) {
	messages := []provider.Message{
		provider.UserText("old request"),
		provider.UserText("/src/main.go:42: undefined: Missing"),
		provider.UserText("recent context"),
		provider.UserText("current request"),
	}
	retained, _ := Retain(messages, 80, tokens.EncodingCl100kBase)
	found := false
	for _, message := range retained {
		found = found || strings.Contains(message.Text(), "undefined: Missing")
	}
	if !found {
		t.Fatalf("critical diagnostic was not retained: %#v", retained)
	}
}
