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
