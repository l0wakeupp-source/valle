package openai

import (
	"encoding/json"
	"testing"
)

// TestCacheTokensParse verifies that OpenAI's prompt_tokens_details.cached_tokens
// unmarshals correctly, which is what the adapter uses to split input vs cache hit.
func TestCacheTokensParse(t *testing.T) {
	line := `{"prompt_tokens":1000,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":800}}`

	var result struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	}
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.PromptTokens != 1000 {
		t.Fatalf("PromptTokens: got %d want 1000", result.PromptTokens)
	}
	if result.PromptTokensDetails.CachedTokens != 800 {
		t.Fatalf("CachedTokens: got %d want 800", result.PromptTokensDetails.CachedTokens)
	}

	// The adapter computes: input = prompt - cached
	inputTokens := result.PromptTokens - result.PromptTokensDetails.CachedTokens
	if inputTokens != 200 {
		t.Fatalf("input tokens (cache miss): got %d want 200", inputTokens)
	}
}

func TestPromptCacheKeyIsStableAndNamespacedByModel(t *testing.T) {
	first := promptCacheKey("gpt-5", "stable instructions")
	if first == "" || len(first) != 64 {
		t.Fatalf("key = %q, want a 64-character digest", first)
	}
	if got := promptCacheKey("gpt-5", "stable instructions"); got != first {
		t.Fatalf("same stable prefix produced different keys: %q vs %q", first, got)
	}
	if got := promptCacheKey("gpt-4o", "stable instructions"); got == first {
		t.Fatal("different models shared a prompt cache key")
	}
	if got := promptCacheKey("gpt-5", ""); got != "" {
		t.Fatalf("empty stable prefix produced key %q", got)
	}
}

func TestStableSystemPrefixIsSentBeforeVolatileTail(t *testing.T) {
	wire := toWireWithStable("stable instructions\nvolatile environment", "stable instructions", nil, false)
	if len(wire) != 2 {
		t.Fatalf("wire message count = %d, want 2", len(wire))
	}
	if wire[0].Role != "system" || wire[0].Content != "stable instructions" {
		t.Fatalf("stable message = %#v", wire[0])
	}
	if wire[1].Role != "system" || wire[1].Content != "\nvolatile environment" {
		t.Fatalf("volatile message = %#v", wire[1])
	}
}
