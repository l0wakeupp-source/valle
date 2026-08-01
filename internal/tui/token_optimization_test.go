package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rick/internal/config"
	"rick/internal/provider"
	"rick/internal/session"
)

func TestAutoCompactCooldownExpires(t *testing.T) {
	model := &Model{
		deps:      Deps{Loaded: &config.Loaded{}},
		ctxWindow: 100,
		history:   make([]provider.Message, 7),
		usage:     session.Usage{Input: 80},
	}

	model.lastAutoCompact = time.Now()
	model.maybeAutoCompact()
	if model.autoCompactPending {
		t.Fatal("auto-compaction re-triggered during cooldown")
	}

	model.lastAutoCompact = time.Now().Add(-autoCompactCooldown - time.Second)
	model.maybeAutoCompact()
	if !model.autoCompactPending {
		t.Fatal("auto-compaction did not trigger after cooldown")
	}
}

func TestContextCompactionThresholdUsesConfiguredReserve(t *testing.T) {
	if got := contextCompactionThreshold(200000, 24000); got != 176000 {
		t.Fatalf("threshold = %d, want 176000", got)
	}
	if got := contextCompactionThreshold(100, 0); got != 70 {
		t.Fatalf("default threshold = %d, want 70", got)
	}
	if got := contextCompactionThreshold(100, 100); got != 0 {
		t.Fatalf("full reserve threshold = %d, want 0", got)
	}
	if got := compactionTokenLimit(16384); got != compactionMaxTokens {
		t.Fatalf("compaction token limit = %d, want %d", got, compactionMaxTokens)
	}
	if got := compactionTokenLimit(512); got != 512 {
		t.Fatalf("configured small token limit = %d, want 512", got)
	}
}

func TestFailedAutoCompactProviderResolutionDoesNotStartCompaction(t *testing.T) {
	model := &Model{
		deps:               Deps{Loaded: &config.Loaded{}},
		tx:                 newTranscript(),
		history:            make([]provider.Message, 7),
		ctxWindow:          100,
		autoCompactPending: true,
	}

	_, cmd := model.cmdCompact()
	if cmd != nil {
		t.Fatal("compaction command created without a provider")
	}
	if model.compactionActive {
		t.Fatal("failed provider resolution left compaction marked active")
	}
}

func TestOverlappingCompactionIsRejected(t *testing.T) {
	model := &Model{compactionActive: true}
	_, cmd := model.cmdCompact()
	if cmd != nil {
		t.Fatal("overlapping compaction unexpectedly created a command")
	}
}

func TestStaleCompactionResultIsIgnored(t *testing.T) {
	model := &Model{compactionActive: true, compactionRunID: 2}
	model.Update(compactDoneMsg{runID: 1, summary: "stale"})
	if !model.compactionActive {
		t.Fatal("stale compaction result changed active state")
	}
}

func TestAddProviderUsagePreservesCacheAccounting(t *testing.T) {
	var total provider.Usage
	addProviderUsage(&total, provider.Usage{
		InputTokens: 11, OutputTokens: 7, CacheReadTokens: 13, CacheWriteTokens: 17,
	})
	addProviderUsage(&total, provider.Usage{
		InputTokens: 19, OutputTokens: 23, CacheReadTokens: 29, CacheWriteTokens: 31,
	})

	want := provider.Usage{InputTokens: 30, OutputTokens: 30, CacheReadTokens: 42, CacheWriteTokens: 48}
	if total != want {
		t.Fatalf("usage = %#v, want %#v", total, want)
	}
}

func TestSystemPromptPlacesProjectContextBeforeVolatileEnvironment(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "RICK.md"), []byte("project conventions"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := buildSystemPrompt("build", "test-model", root, root, config.Config{}, nil, "")
	projectIndex := strings.Index(prompt, "## Project instructions")
	environmentIndex := strings.Index(prompt, "## Environment")
	if projectIndex < 0 || environmentIndex < 0 {
		t.Fatalf("prompt missing stable or volatile section: %q", prompt)
	}
	if projectIndex > environmentIndex {
		t.Fatalf("project context follows environment: project=%d environment=%d", projectIndex, environmentIndex)
	}
}

func TestCompactHistoryKeepsOnlyLatestThinkingMessage(t *testing.T) {
	history := []provider.Message{
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "thinking", Text: "old"}, {Type: "text", Text: "answer"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "thinking", Text: "latest"}, {Type: "text", Text: "final"}}},
	}
	compacted := compactHistory(history)
	if len(compacted[0].Content) != 1 || compacted[0].Content[0].Type != "text" {
		t.Fatalf("old thinking was retained: %#v", compacted[0].Content)
	}
	if len(compacted[1].Content) != 2 || compacted[1].Content[0].Text != "latest" {
		t.Fatalf("latest thinking was not retained: %#v", compacted[1].Content)
	}
}
