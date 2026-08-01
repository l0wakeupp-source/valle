package tui

import (
	"strings"
	"testing"

	"rick/internal/provider"
)

func TestCompactToolOutputKeepsUnicodeWithinLimit(t *testing.T) {
	output := strings.Repeat("界", 256)
	if got := compactToolOutput(output, 256); got != output {
		t.Fatalf("compactToolOutput changed a string whose rune count is within the limit")
	}
}

func TestCapHistoryBoundsMessageCountAndApproximateBytes(t *testing.T) {
	history := make([]provider.Message, 0, maxHistoryMessages+40)
	for i := 0; i < maxHistoryMessages+40; i++ {
		history = append(history, provider.Message{
			Role: provider.RoleUser,
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: strings.Repeat("x", 6000),
			}},
		})
	}

	capped := capHistory(history)
	if len(capped) > maxHistoryMessages {
		t.Fatalf("history length = %d, want <= %d", len(capped), maxHistoryMessages)
	}
	if historyByteSize(capped) > maxHistoryBytes+1024 {
		t.Fatalf("history estimate = %d, want approximately <= %d", historyByteSize(capped), maxHistoryBytes)
	}
	if capped[0].Role != provider.RoleUser || !strings.Contains(capped[0].Text(), "Earlier conversation compacted") {
		t.Fatalf("missing compaction summary: %#v", capped[0])
	}
}

func TestFilterFilesUsesPrecomputedLowercaseFields(t *testing.T) {
	files := []fileEntry{{path: "Src/Agent.go", name: "Agent.go", lowerPath: "src/agent.go", lowerName: "agent.go"}}
	results := filterFiles(files, "AGENT")
	if len(results) != 1 || results[0].path != "Src/Agent.go" {
		t.Fatalf("filterFiles returned %#v, want the pre-normalized match", results)
	}
}
