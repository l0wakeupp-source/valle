// Package openai implements provider.Provider against the OpenAI
// chat-completions wire format, which OpenRouter, Groq, Together, LM Studio,
// Ollama and most gateways also speak.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"rick/internal/provider"
	"rick/internal/provider/catalog"
)

// Client is an OpenAI-compatible provider.
type Client struct {
	ID      string // registry name: "openai", "openrouter", ...
	APIKey  string
	BaseURL string
	Headers map[string]string
	HTTP    *http.Client

	models []provider.ModelInfo
}

// New builds a client. baseURL defaults to the public OpenAI API.
func New(id, apiKey, baseURL string) *Client {
	if baseURL == "" {
		switch id {
		case "openrouter":
			baseURL = "https://openrouter.ai/api/v1"
		case "groq":
			baseURL = "https://api.groq.com/openai/v1"
		default:
			baseURL = "https://api.openai.com/v1"
		}
	}
	c := &Client{
		ID:      id,
		APIKey:  catalog.CleanSecret(apiKey),
		BaseURL: strings.TrimRight(strings.ReplaceAll(baseURL, "\x00", ""), "/"),
		HTTP:    &http.Client{Timeout: 15 * time.Minute},
		Headers: map[string]string{},
	}
	if id == "openrouter" {
		c.Headers["HTTP-Referer"] = "https://github.com/rick-agent/rick"
		c.Headers["X-Title"] = "rick"
	}
	c.models = defaultModels(id)
	return c
}

func defaultModels(id string) []provider.ModelInfo {
	switch id {
	case "openrouter":
		return []provider.ModelInfo{
			{ID: "anthropic/claude-sonnet-4.5", Name: "Claude Sonnet 4.5", ContextWindow: 200000, MaxOutput: 64000},
			{ID: "openai/gpt-5", Name: "GPT-5", ContextWindow: 400000, MaxOutput: 128000},
			{ID: "google/gemini-2.5-pro", Name: "Gemini 2.5 Pro", ContextWindow: 1000000, MaxOutput: 65000},
			{ID: "deepseek/deepseek-chat", Name: "DeepSeek Chat", ContextWindow: 128000, MaxOutput: 8192},
			{ID: "qwen/qwen3-coder", Name: "Qwen3 Coder", ContextWindow: 256000, MaxOutput: 32000},
		}
	default:
		return []provider.ModelInfo{
			{ID: "gpt-5", Name: "GPT-5", ContextWindow: 400000, MaxOutput: 128000},
			{ID: "gpt-5-mini", Name: "GPT-5 mini", ContextWindow: 400000, MaxOutput: 128000},
			{ID: "gpt-4.1", Name: "GPT-4.1", ContextWindow: 1000000, MaxOutput: 32000},
			{ID: "o4-mini", Name: "o4-mini", ContextWindow: 200000, MaxOutput: 100000},
		}
	}
}

// Name implements provider.Provider.
func (c *Client) Name() string { return c.ID }

// Models implements provider.Provider.
func (c *Client) Models() []provider.ModelInfo { return c.models }

// SetModels overrides the advertised catalogue.
func (c *Client) SetModels(m []provider.ModelInfo) {
	if len(m) > 0 {
		c.models = m
	}
}

func (c *Client) modelInfo(id string) *provider.ModelInfo {
	for _, model := range c.models {
		if model.ID == id {
			copy := model
			return &copy
		}
	}
	return nil
}

// SetAPIKey updates the API key for this client.
func (c *Client) SetAPIKey(key string) {
	c.APIKey = key
}

// ---------- wire types ----------

type wireToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type wireImageContent struct {
	Type     string       `json:"type"`
	ImageURL wireImageURL `json:"image_url"`
}

type wireImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type wireMessage struct {
	Role             string         `json:"role"`
	Content          any            `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type wireRequest struct {
	Model          string        `json:"model"`
	Messages       []wireMessage `json:"messages"`
	Tools          []wireTool    `json:"tools,omitempty"`
	Stream         bool          `json:"stream"`
	StreamOpts     *streamOpts   `json:"stream_options,omitempty"`
	MaxTokens      int           `json:"max_completion_tokens,omitempty"`
	Temperature    *float64      `json:"temperature,omitempty"`
	PromptCacheKey string        `json:"prompt_cache_key,omitempty"`
	// Effort is OpenAI's reasoning control; Qwen-style endpoints use the
	// boolean instead. Only one is ever set.
	Effort         string         `json:"reasoning_effort,omitempty"`
	EnableThinking *bool          `json:"enable_thinking,omitempty"`
	Thinking       *wireThinking  `json:"thinking,omitempty"`
	Reasoning      *wireReasoning `json:"reasoning,omitempty"`
}

func promptCacheKey(model, stableSystem string) string {
	if strings.TrimSpace(stableSystem) == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(model + "\x00" + stableSystem))
	return hex.EncodeToString(digest[:])
}

// wireThinking is GLM's native OpenAI-compatible thinking switch.
type wireThinking struct {
	Type          string `json:"type"`
	ClearThinking *bool  `json:"clear_thinking,omitempty"`
}

type wireReasoning struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

// toWire flattens rick's block model onto OpenAI's message model.
func toWire(system string, msgs []provider.Message) []wireMessage {
	return toWireWithReasoning(system, msgs, false)
}

// toWireWithReasoning preserves reasoning_content for providers such as GLM
// and DeepSeek that require it when a tool call is followed by another turn.
func toWireWithReasoning(system string, msgs []provider.Message, includeReasoning bool) []wireMessage {
	return toWireWithStable(system, "", msgs, includeReasoning)
}

// toWireWithStable keeps the stable prompt in an earlier message than the
// per-turn tail. Direct OpenAI caching can then retain the stable prefix while
// the volatile environment and skill instructions continue to be sent.
func toWireWithStable(system, stable string, msgs []provider.Message, includeReasoning bool) []wireMessage {
	var out []wireMessage
	if strings.TrimSpace(stable) != "" && strings.HasPrefix(system, stable) {
		out = append(out, wireMessage{Role: "system", Content: stable})
		if tail := strings.TrimPrefix(system, stable); strings.TrimSpace(tail) != "" {
			out = append(out, wireMessage{Role: "system", Content: tail})
		}
	} else if strings.TrimSpace(system) != "" {
		out = append(out, wireMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		var text strings.Builder
		var reasoning strings.Builder
		var calls []wireToolCall
		var results []wireMessage
		var imageBlocks []wireImageContent

		for _, b := range m.Content {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "thinking":
				if includeReasoning {
					reasoning.WriteString(b.Text)
				}
			case "tool_use":
				var tc wireToolCall
				tc.ID = b.ID
				tc.Type = "function"
				tc.Function.Name = b.Name
				args := string(b.Input)
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				tc.Function.Arguments = args
				calls = append(calls, tc)
			case "tool_result":
				results = append(results, wireMessage{
					Role: "tool", ToolCallID: b.ToolUseID, Content: b.Content,
				})
			case "image":
				if b.Source == "base64" && b.Data != "" {
					imageBlocks = append(imageBlocks, wireImageContent{
						Type: "image_url",
						ImageURL: wireImageURL{
							URL:    "data:" + b.MediaType + ";base64," + b.Data,
							Detail: "low",
						},
					})
				}
			}
		}

		if m.Role == provider.RoleAssistant && (text.Len() > 0 || reasoning.Len() > 0 || len(calls) > 0) {
			wm := wireMessage{Role: "assistant", ToolCalls: calls}
			if text.Len() > 0 {
				wm.Content = text.String()
			}
			if reasoning.Len() > 0 {
				wm.ReasoningContent = reasoning.String()
			}
			out = append(out, wm)
		} else if m.Role == provider.RoleUser && (text.Len() > 0 || len(imageBlocks) > 0) {
			wm := wireMessage{Role: "user", Content: text.String()}
			if len(imageBlocks) > 0 {
				// OpenAI vision: content is an array of text + image_url blocks
				var contentArray []map[string]interface{}
				if text.Len() > 0 {
					contentArray = append(contentArray, map[string]interface{}{
						"type": "text",
						"text": text.String(),
					})
				}
				for _, img := range imageBlocks {
					contentArray = append(contentArray, map[string]interface{}{
						"type":      img.Type,
						"image_url": img.ImageURL,
					})
				}
				wm.Content = contentArray
			}
			out = append(out, wm)
		}
		out = append(out, results...)
	}
	return out
}

func toWireTools(ts []provider.ToolSchema) []wireTool {
	out := make([]wireTool, 0, len(ts))
	for _, t := range ts {
		var wt wireTool
		wt.Type = "function"
		wt.Function.Name = t.Name
		wt.Function.Description = t.Description
		wt.Function.Parameters = t.InputSchema
		out = append(out, wt)
	}
	return out
}

// Stream implements provider.Provider. It owns ch and closes it exactly once.
func (c *Client) Stream(ctx context.Context, req provider.Request, ch chan<- provider.Event) {
	defer close(ch)

	emit := func(ev provider.Event) bool {
		select {
		case ch <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	if c.APIKey == "" && !isLocal(c.BaseURL) {
		emit(provider.Event{Kind: provider.EventError,
			Err: fmt.Errorf("%s: no API key configured", c.ID)})
		return
	}

	style, _ := provider.DetectReasoningForProvider(c.ID, req.Model)
	advertised := c.modelInfo(req.Model)
	preserveReasoning := style == provider.ReasoningStyleGLM || style == provider.ReasoningStyleDeepSeek ||
		style == provider.ReasoningStyleAlways || style == provider.ReasoningStyleQwen ||
		style == provider.ReasoningStyleUnknown
	if c.ID == "openrouter" && advertised != nil && advertised.ReasoningKnown {
		preserveReasoning = true
	}
	body := wireRequest{
		Model:          req.Model,
		Messages:       toWireWithReasoning(req.System, req.Messages, preserveReasoning),
		Tools:          toWireTools(req.Tools),
		Stream:         true,
		StreamOpts:     &streamOpts{IncludeUsage: true},
		MaxTokens:      req.MaxTokens,
		Temperature:    req.Temperature,
		PromptCacheKey: promptCacheKey(req.Model, req.SystemStable),
	}
	if c.ID == "openai" {
		body.Messages = toWireWithStable(req.System, req.SystemStable, req.Messages, preserveReasoning)
	}
	if c.ID != "openai" {
		// OpenAI-compatible gateways do not all accept OpenAI's cache-routing
		// hint. The stable system prefix is still sent to every provider.
		body.PromptCacheKey = ""
	}

	// OpenRouter has one normalized reasoning object. Use it for every
	// explicitly selected effort so model-specific metadata is not translated
	// into a potentially unsupported root reasoning_effort field.
	if c.ID == "openrouter" && style != provider.ReasoningStyleAlways && req.Reasoning != "" &&
		(req.Reasoning != provider.ReasoningOff || style != provider.ReasoningStyleNone && style != provider.ReasoningStyleUnknown) {
		body.Reasoning = &wireReasoning{}
		if req.Reasoning == provider.ReasoningOn {
			on := true
			body.Reasoning.Enabled = &on
		} else {
			body.Reasoning.Effort = map[provider.ReasoningEffort]string{
				provider.ReasoningOff: "none",
			}[req.Reasoning]
			if body.Reasoning.Effort == "" {
				body.Reasoning.Effort = string(req.Reasoning)
			}
		}
		body.Temperature = nil
	} else if req.Reasoning != "" && req.Reasoning != provider.ReasoningOff {
		// Direct providers use their native reasoning dialect.
		switch style {
		case provider.ReasoningStyleOpenAI:
			effort := req.Reasoning
			if effort == provider.ReasoningOn {
				// Unknown/gateway-normalized models use the common medium
				// effort as their explicit opt-in.
				effort = provider.ReasoningMedium
			}
			body.Effort = string(effort)
			// Reasoning models reject a custom temperature.
			body.Temperature = nil
		case provider.ReasoningStyleQwen:
			on := true
			body.EnableThinking = &on
		case provider.ReasoningStyleGLM:
			clearThinking := false
			body.Thinking = &wireThinking{Type: "enabled", ClearThinking: &clearThinking}
			if req.Reasoning != provider.ReasoningOn && strings.Contains(strings.ToLower(req.Model), "glm-5.2") {
				body.Effort = string(req.Reasoning)
			}
			// Reasoning models reject a custom temperature.
			body.Temperature = nil
		case provider.ReasoningStyleDeepSeek:
			body.Effort = string(req.Reasoning)
			body.Thinking = &wireThinking{Type: "enabled"}
			// Reasoning models reject a custom temperature.
			body.Temperature = nil
		case provider.ReasoningStyleUnknown:
			// Custom gateways often omit capability metadata. Only use the
			// generic OpenAI field after the user explicitly enables thinking;
			// the default off path never sends an unsupported parameter.
			effort := req.Reasoning
			if effort == provider.ReasoningOn {
				effort = provider.ReasoningMedium
			}
			body.Effort = string(effort)
			body.Temperature = nil
		}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		emit(provider.Event{Kind: provider.EventError, Err: err})
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		emit(provider.Event{Kind: provider.EventError, Err: err})
		return
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")
	if c.APIKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		emit(provider.Event{Kind: provider.EventError, Err: err})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		emit(provider.Event{Kind: provider.EventError,
			Err: fmt.Errorf("%s: http %d: %s", c.ID, resp.StatusCode, strings.TrimSpace(string(b)))})
		return
	}

	c.readSSE(ctx, resp.Body, emit)
}

func isLocal(u string) bool {
	return strings.Contains(u, "localhost") || strings.Contains(u, "127.0.0.1")
}

type callAccum struct {
	id   string
	name string
	args strings.Builder
}

func (c *Client) readSSE(ctx context.Context, r io.Reader, emit func(provider.Event) bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)

	calls := map[int]*callAccum{}
	usage := provider.Usage{}
	stopReason := ""

	flushCalls := func() bool {
		// Emit in index order for determinism without assuming call indexes are
		// contiguous or bounded by the number of accumulated calls.
		indices := make([]int, 0, len(calls))
		for index := range calls {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		for _, index := range indices {
			acc := calls[index]
			args := strings.TrimSpace(acc.args.String())
			if args == "" {
				args = "{}"
			}
			delete(calls, index)
			if !emit(provider.Event{Kind: provider.EventToolCall,
				ToolCall: &provider.ToolCall{ID: acc.id, Name: acc.name, Input: json.RawMessage(args)}}) {
				return false
			}
		}
		return true
	}

	for sc.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := strings.TrimRight(sc.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string         `json:"content"`
					Reasoning        string         `json:"reasoning"`
					ReasoningContent string         `json:"reasoning_content"`
					ToolCalls        []wireToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			emit(provider.Event{Kind: provider.EventError,
				Err: fmt.Errorf("%s: %s", c.ID, chunk.Error.Message)})
			return
		}
		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens - chunk.Usage.PromptTokensDetails.CachedTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
			usage.CacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
		}
		for _, choice := range chunk.Choices {
			if t := choice.Delta.Content; t != "" {
				if !emit(provider.Event{Kind: provider.EventText, Text: t}) {
					return
				}
			}
			if t := choice.Delta.Reasoning + choice.Delta.ReasoningContent; t != "" {
				if !emit(provider.Event{Kind: provider.EventThinking, Text: t}) {
					return
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				if existing, ok := calls[tc.Index]; ok && tc.ID != "" && existing.id != "" && existing.id != tc.ID {
					if !flushCalls() {
						return
					}
				}
				acc, ok := calls[tc.Index]
				if !ok {
					acc = &callAccum{}
					calls[tc.Index] = acc
					if tc.ID != "" {
						acc.id = tc.ID
					}
					if tc.Function.Name != "" {
						acc.name = tc.Function.Name
						emit(provider.Event{Kind: provider.EventToolCallStart,
							ToolCall: &provider.ToolCall{ID: tc.ID, Name: tc.Function.Name}})
					}
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" && acc.name == "" {
					acc.name = tc.Function.Name
				}
				acc.args.WriteString(tc.Function.Arguments)
			}
			if choice.FinishReason != "" {
				stopReason = choice.FinishReason
			}
		}
	}

	if err := sc.Err(); err != nil && ctx.Err() == nil {
		emit(provider.Event{Kind: provider.EventError, Err: err})
		return
	}
	if !flushCalls() {
		return
	}
	emit(provider.Event{Kind: provider.EventUsage, Usage: &usage})
	emit(provider.Event{Kind: provider.EventDone, StopReason: stopReason})
}

// ListModels queries the /models endpoint.
func (c *Client) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("authorization", "Bearer "+c.APIKey)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: models http %d", c.ID, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	models, _, err := catalog.ParseModels(body)
	if err != nil {
		return nil, err
	}
	infos := make([]provider.ModelInfo, 0, len(models))
	for _, model := range catalog.FilterChatModels(models) {
		contextWindow := model.Context
		contextSource := model.ContextSource
		if override, ok := provider.ProviderContextWindow(c.ID, model.ID); ok {
			contextWindow = override
			contextSource = provider.ContextSourceCatalog
		}
		infos = append(infos, provider.ModelInfo{
			ID: model.ID, Name: model.Name, ContextWindow: contextWindow,
			ContextSource: contextSource, SupportsImages: model.SupportsImages,
			CapabilitiesKnown: model.CapabilitiesKnown, ChatCapable: model.ChatCapable,
			ReasoningEfforts:      append([]provider.ReasoningEffort(nil), model.ReasoningEfforts...),
			ReasoningEffortsKnown: model.ReasoningEffortsKnown, ReasoningEffortsAll: model.ReasoningEffortsAll,
			ReasoningDefault: model.ReasoningDefault, ReasoningDefaultEnabled: model.ReasoningDefaultEnabled,
			ReasoningDefaultEnabledKnown: model.ReasoningDefaultEnabledKnown, ReasoningMandatory: model.ReasoningMandatory,
			ReasoningSupportsMaxTokens: model.ReasoningSupportsMaxTokens,
			ReasoningKnown:             model.ReasoningKnown,
		})
	}
	return infos, nil
}
