package tui

import (
	"testing"

	"rick/internal/provider"
	"rick/internal/usage"
)

func TestRecordChildUsageUsesChildModelAndPersists(t *testing.T) {
	tracker := usage.New(t.TempDir())
	model := &Model{deps: Deps{Usage: tracker}}

	model.recordChildUsage("openai/child-model", provider.Usage{
		InputTokens:      120,
		OutputTokens:     30,
		CacheReadTokens:  7,
		CacheWriteTokens: 3,
	})

	got := tracker.ModelTotal("openai/child-model")
	if got.Input != 120 || got.Output != 30 || got.CacheRead != 7 || got.CacheWrite != 3 {
		t.Fatalf("child usage = %+v", got)
	}
}
