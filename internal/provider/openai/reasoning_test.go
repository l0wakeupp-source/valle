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

func TestStreamUsesGLMThinkingAndReasoningContent(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New("zai", "test-key", server.URL)
	client.HTTP = server.Client()
	events := make(chan provider.Event)
	go client.Stream(context.Background(), provider.Request{
		Model:       "glm-4.7",
		Messages:    []provider.Message{provider.UserText("hello")},
		MaxTokens:   4096,
		Reasoning:   provider.ReasoningMedium,
		Temperature: func() *float64 { value := 0.2; return &value }(),
	}, events)

	var thinking string
	for event := range events {
		if event.Kind == provider.EventThinking {
			thinking += event.Text
		}
	}

	if thinking != "thinking" {
		t.Fatalf("thinking stream = %q, want %q", thinking, "thinking")
	}
	if _, ok := request["reasoning_effort"]; ok {
		t.Fatal("GLM request unexpectedly used reasoning_effort")
	}
	thinkingBody, ok := request["thinking"].(map[string]any)
	if !ok || thinkingBody["type"] != "enabled" || thinkingBody["clear_thinking"] != false {
		t.Fatalf("thinking body = %#v, want enabled with preserved thinking", request["thinking"])
	}
	if _, ok := request["temperature"]; ok {
		t.Fatal("GLM reasoning request unexpectedly included temperature")
	}

	messages, ok := request["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", request["messages"])
	}
	prior := provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "thinking", Text: "prior reasoning"}}}
	wire := toWireWithReasoning("", []provider.Message{prior}, true)
	encoded, err := json.Marshal(wire[0])
	if err != nil {
		t.Fatalf("encode assistant message: %v", err)
	}
	if !strings.Contains(string(encoded), `"reasoning_content":"prior reasoning"`) {
		t.Fatalf("assistant reasoning content missing from %s", encoded)
	}
}

func TestOpenRouterUsesNormalizedReasoningObject(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New("openrouter", "test-key", server.URL)
	client.HTTP = server.Client()
	client.SetModels([]provider.ModelInfo{{
		ID:                    "vendor/model",
		ReasoningKnown:        true,
		ReasoningEffortsKnown: true,
		ReasoningEfforts:      []provider.ReasoningEffort{provider.ReasoningLow, provider.ReasoningHigh, provider.ReasoningMax},
	}})
	events := make(chan provider.Event)
	go client.Stream(context.Background(), provider.Request{
		Model:     "vendor/model",
		Messages:  []provider.Message{provider.UserText("hello")},
		Reasoning: provider.ReasoningMax,
	}, events)
	for range events {
	}

	reasoning, ok := request["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != string(provider.ReasoningMax) {
		t.Fatalf("reasoning = %#v, want normalized max effort", request["reasoning"])
	}
	if _, ok := request["reasoning_effort"]; ok {
		t.Fatal("OpenRouter request unexpectedly used root reasoning_effort")
	}
}

func TestOpenRouterEnablementOnlyUsesEnabledFlag(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New("openrouter", "test-key", server.URL)
	client.HTTP = server.Client()
	client.SetModels([]provider.ModelInfo{{ID: "qwen/model", ReasoningKnown: true, ReasoningSupportsMaxTokens: true}})
	events := make(chan provider.Event)
	go client.Stream(context.Background(), provider.Request{
		Model:     "qwen/model",
		Messages:  []provider.Message{provider.UserText("hello")},
		Reasoning: provider.ReasoningOn,
	}, events)
	for range events {
	}

	reasoning, ok := request["reasoning"].(map[string]any)
	if !ok || reasoning["enabled"] != true {
		t.Fatalf("reasoning = %#v, want enabled flag", request["reasoning"])
	}
	if _, ok := reasoning["effort"]; ok {
		t.Fatal("enablement-only request unexpectedly selected an effort")
	}
}

func TestUnknownModelOnlyGetsGenericReasoningWhenEnabled(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New("custom-gateway", "test-key", server.URL)
	client.HTTP = server.Client()
	for _, test := range []struct {
		reasoning provider.ReasoningEffort
		wantField bool
	}{
		{reasoning: provider.ReasoningOff, wantField: false},
		{reasoning: provider.ReasoningMedium, wantField: true},
	} {
		request = nil
		events := make(chan provider.Event)
		go client.Stream(context.Background(), provider.Request{
			Model:     "vendor/new-model",
			Messages:  []provider.Message{provider.UserText("hello")},
			Reasoning: test.reasoning,
		}, events)
		for range events {
		}
		_, found := request["reasoning_effort"]
		if found != test.wantField {
			t.Fatalf("reasoning_effort present=%v for level %q, want %v", found, test.reasoning, test.wantField)
		}
		if test.wantField && request["reasoning_effort"] != string(provider.ReasoningMedium) {
			t.Fatalf("reasoning_effort = %#v, want %q", request["reasoning_effort"], provider.ReasoningMedium)
		}
	}
}
