package anthropic

import (
	"encoding/json"
	"testing"

	"rick/internal/provider"
)

func TestWireRequestAddsPromptCacheBreakpoints(t *testing.T) {
	body := wireRequest{
		Model:        "claude-test",
		MaxTokens:    128,
		CacheControl: &cacheControl{Type: "ephemeral"},
		System:       wireSystem("stable instructions\nvolatile environment", "stable instructions"),
		Messages:     []wireMessage{{Role: provider.RoleUser, Content: []wireBlock{{Type: "text", Text: "hello"}}}},
		Tools: wireTools([]provider.ToolSchema{
			{Name: "read", Description: "read files", InputSchema: map[string]any{"type": "object"}},
			{Name: "write", Description: "write files", InputSchema: map[string]any{"type": "object"}},
		}),
		Stream: true,
	}

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var decoded struct {
		CacheControl *struct {
			Type string `json:"type"`
		} `json:"cache_control"`
		System []struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			CacheControl *struct {
				Type string `json:"type"`
			} `json:"cache_control"`
		} `json:"system"`
		Tools []struct {
			Name         string `json:"name"`
			CacheControl *struct {
				Type string `json:"type"`
			} `json:"cache_control"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.CacheControl == nil || decoded.CacheControl.Type != "ephemeral" {
		t.Fatalf("top-level cache control = %#v, want ephemeral", decoded.CacheControl)
	}

	if len(decoded.System) != 2 || decoded.System[0].Type != "text" || decoded.System[0].Text != "stable instructions" ||
		decoded.System[1].Text != "\nvolatile environment" {
		t.Fatalf("system = %#v, want stable and volatile text blocks", decoded.System)
	}
	if decoded.System[0].CacheControl == nil || decoded.System[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("system cache control = %#v, want ephemeral", decoded.System[0].CacheControl)
	}
	if decoded.System[1].CacheControl != nil {
		t.Fatal("volatile system block unexpectedly has a cache breakpoint")
	}
	if len(decoded.Tools) != 2 {
		t.Fatalf("tools = %#v, want two tools", decoded.Tools)
	}
	if decoded.Tools[0].CacheControl != nil {
		t.Fatal("non-final tool unexpectedly has a cache breakpoint")
	}
	if decoded.Tools[1].CacheControl == nil || decoded.Tools[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("final tool cache control = %#v, want ephemeral", decoded.Tools[1].CacheControl)
	}
}
