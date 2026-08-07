package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rick/internal/provider"
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
	first := promptCacheKey("gpt-5", "session-abc")
	if first == "" || len(first) != 64 {
		t.Fatalf("key = %q, want a 64-character digest", first)
	}
	if got := promptCacheKey("gpt-5", "session-abc"); got != first {
		t.Fatalf("same session produced different keys: %q vs %q", first, got)
	}
	if got := promptCacheKey("gpt-4o", "session-abc"); got == first {
		t.Fatal("different models shared a prompt cache key")
	}
	if got := promptCacheKey("gpt-5", "session-def"); got == first {
		t.Fatal("different sessions shared a prompt cache key")
	}
	if got := promptCacheKey("gpt-5", ""); got != "" {
		t.Fatalf("empty session id produced key %q", got)
	}
}

func TestNoneRetentionOmitsCacheFields(t *testing.T) {
	body := wireRequest{
		Model:                "gpt-5",
		PromptCacheKey:       "",
		PromptCacheRetention: "",
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		PromptCacheKey       string `json:"prompt_cache_key"`
		PromptCacheRetention string `json:"prompt_cache_retention"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.PromptCacheKey != "" || decoded.PromptCacheRetention != "" {
		t.Fatalf("none retention leaked cache fields: key=%q retention=%q", decoded.PromptCacheKey, decoded.PromptCacheRetention)
	}
}

func TestStreamSendsRetentionAndAffinityForDirectOpenAI(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	client := New("openai", "test-key", server.URL)
	ch := make(chan provider.Event, 16)
	req := provider.Request{
		Model:          "gpt-5",
		System:         "sys",
		Messages:       []provider.Message{provider.UserText("hello")},
		SessionID:      "sess-123",
		CacheRetention: provider.CacheRetentionLong,
	}
	client.Stream(context.Background(), req, ch)
	for ev := range ch {
		if ev.Kind == provider.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
	}

	var decoded struct {
		PromptCacheKey       string `json:"prompt_cache_key"`
		PromptCacheRetention string `json:"prompt_cache_retention"`
	}
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if decoded.PromptCacheKey == "" {
		t.Fatal("session-keyed prompt_cache_key missing on direct OpenAI")
	}
	if decoded.PromptCacheRetention != "24h" {
		t.Fatalf("prompt_cache_retention = %q, want 24h", decoded.PromptCacheRetention)
	}
	if gotHeaders.Get("session_id") != "sess-123" || gotHeaders.Get("x-client-request-id") != "sess-123" {
		t.Fatalf("session affinity headers missing: %v", gotHeaders)
	}
}

func TestStreamOmitsCacheFieldsForNoneRetention(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	client := New("openai", "test-key", server.URL)
	ch := make(chan provider.Event, 16)
	client.Stream(context.Background(), provider.Request{
		Model:          "gpt-5",
		System:         "sys",
		Messages:       []provider.Message{provider.UserText("hello")},
		SessionID:      "sess-123",
		CacheRetention: provider.CacheRetentionNone,
	}, ch)
	for ev := range ch {
		if ev.Kind == provider.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
	}

	if strings.Contains(string(gotBody), "prompt_cache_key") || strings.Contains(string(gotBody), "prompt_cache_retention") {
		t.Fatalf("none retention sent cache fields: %s", gotBody)
	}
	if gotHeaders.Get("session_id") != "" || gotHeaders.Get("x-client-request-id") != "" {
		t.Fatal("none retention sent session-affinity headers")
	}
}

func TestStreamUsageAccountsCacheWritesSeparately(t *testing.T) {
	client := &Client{ID: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}
	line := `data: {"usage":{"prompt_tokens":1000,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":600,"cache_write_tokens":250}}}`
	var usage provider.Usage
	emit := func(ev provider.Event) bool {
		if ev.Kind == provider.EventUsage && ev.Usage != nil {
			usage = *ev.Usage
		}
		return true
	}
	client.readSSE(context.Background(), strings.NewReader(line+"\n\ndata: [DONE]\n\n"), emit)
	if usage.CacheReadTokens != 600 {
		t.Fatalf("cache read = %d, want 600", usage.CacheReadTokens)
	}
	if usage.CacheWriteTokens != 250 {
		t.Fatalf("cache write = %d, want 250", usage.CacheWriteTokens)
	}
	if usage.InputTokens != 150 {
		t.Fatalf("input = %d, want 150 (1000-600-250)", usage.InputTokens)
	}
	if usage.OutputTokens != 40 {
		t.Fatalf("output = %d, want 40", usage.OutputTokens)
	}
}

func TestStableSystemPrefixIsSentBeforeVolatileTail(t *testing.T) {
	wire := toWireWithStable("stable instructions\nvolatile environment", "stable instructions", nil, false, false)
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
