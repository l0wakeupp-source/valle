package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"rick/internal/provider"
	"rick/pkg/contextbudget"
)

func TestRetrieveUncompressedToolRoundTrip(t *testing.T) {
	store := contextbudget.New(contextbudget.Options{})
	payload := `{"rows":[` + strings.Repeat(`"x",`, 200) + `"end"]}`
	compressed, _ := store.CompressLive("call-9", payload)

	tool := RetrieveUncompressedTool{Store: store}
	// List mode reports the stored key.
	listResult, err := tool.Run(context.Background(), Context{}, mustJSON(t, map[string]any{"list": true}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listResult.Output, "call-9") {
		t.Fatalf("list output missing key: %s", listResult.Output)
	}
	if strings.Contains(listResult.Output, payload) {
		t.Fatal("list output leaked the full payload")
	}
	// Retrieve mode returns the exact original.
	result, err := tool.Run(context.Background(), Context{}, mustJSON(t, map[string]any{"key": "call-9"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != payload {
		t.Fatalf("retrieved payload differs from original (%d vs %d bytes)", len(result.Output), len(payload))
	}
	_ = compressed
}

func TestRetrieveUncompressedToolByHash(t *testing.T) {
	store := contextbudget.New(contextbudget.Options{})
	payload := strings.Repeat("identical large payload ", 200)
	// Seed the content-addressed store through the dedup path.
	_ = store.ApplyDedup([]provider.Message{{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "tool_result", ToolUseID: "c", Content: payload}},
	}})

	hash := contextbudget.Hash(payload)
	tool := RetrieveUncompressedTool{Store: store}
	result, err := tool.Run(context.Background(), Context{}, mustJSON(t, map[string]any{"key": hash}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != payload {
		t.Fatal("payload not retrievable by content address")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
