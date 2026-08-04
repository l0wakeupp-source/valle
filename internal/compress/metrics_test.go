package compress

import (
	"fmt"
	"strings"
	"testing"

	"rick/internal/tokens"
)

func TestRepresentativeSavings(t *testing.T) {
	fixtures := []Input{
		{
			Command:  "go test ./...",
			IsError:  true,
			Text:     strings.Repeat("ok example/package 0.01s\n", 120) + "--- FAIL: TestBroken (0.00s)\n/path/project/main_test.go:42: expected 1, got 2\nFAIL\n",
			MaxBytes: 32 << 10,
		},
		{
			Command:  "grep",
			Text:     strings.Repeat("internal/agent/agent.go:100: match\n", 80) + "internal/agent/agent.go:101: distinct\n",
			MaxBytes: 32 << 10,
		},
		{
			Command:  "custom-tool",
			Text:     strings.Repeat("trace line with payload\n", 3000),
			MaxBytes: 32 << 10,
		},
	}
	for _, fixture := range fixtures {
		result := ForTool(fixture)
		before := tokens.Count(fixture.Text, tokens.EncodingCl100kBase).Count
		after := tokens.Count(result.Text, tokens.EncodingCl100kBase).Count
		saved := before - after
		percent := 0.0
		if before > 0 {
			percent = float64(saved) * 100 / float64(before)
		}
		t.Logf("stage=%s before_tokens=%d after_tokens=%d saved_tokens=%d savings_percent=%.2f before_bytes=%d after_bytes=%d truncated=%t",
			result.Stage, before, after, saved, percent, len(fixture.Text), len(result.Text), result.Truncated)
		if before <= 0 || after <= 0 || saved <= 0 {
			t.Fatalf("fixture did not produce a measurable reduction: %s", fmt.Sprint(result))
		}
	}
}
