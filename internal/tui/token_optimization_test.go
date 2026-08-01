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
