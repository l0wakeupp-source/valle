// Package anthropic implements provider.Provider against the Anthropic
// Messages API using raw HTTP + SSE (no SDK dependency).
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rick/internal/provider"
	"rick/internal/provider/catalog"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
)

// Client is an Anthropic provider.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// New builds a client. baseURL may be empty for the public API.
func New(apiKey, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		APIKey:  catalog.CleanSecret(apiKey),
		BaseURL: strings.TrimRight(strings.ReplaceAll(baseURL, "\x00", ""), "/"),
		HTTP:    &http.Client{Timeout: 15 * time.Minute},
	}
}

// Name implements provider.Provider.
func (c *Client) Name() string { return "anthropic" }

// SetAPIKey updates the API key for this client.
func (c *Client) SetAPIKey(key string) {
	c.APIKey = key
}
func (c *Client) Models() []provider.ModelInfo {
	return []provider.ModelInfo{
		{ID: "claude-sonnet-4-5-20250929", Name: "Claude Sonnet 4.5", ContextWindow: 200000, MaxOutput: 64000},
		{ID: "claude-opus-4-5-20251101", Name: "Claude Opus 4.5", ContextWindow: 200000, MaxOutput: 64000},
		{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", ContextWindow: 200000, MaxOutput: 32000},
		{ID: "claude-3-5-haiku-20241022", Name: "Claude 3.5 Haiku", ContextWindow: 200000, MaxOutput: 8192},
	}
}

// ---------- wire types ----------

type wireBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      any             `json:"content,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	Source       wireImageSource `json:"source,omitempty"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type wireTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *cacheControl  `json:"cache_control,omitempty"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	System      []wireBlock   `json:"system,omitempty"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Stream      bool          `json:"stream"`
	Temperature *float64      `json:"temperature,omitempty"`
	Thinking    *wireThinking `json:"thinking,omitempty"`
}

func wireSystem(system, stable string) []wireBlock {
	if strings.TrimSpace(system) == "" {
		return nil
	}
	if stable != "" && strings.HasPrefix(system, stable) && strings.TrimSpace(stable) != "" {
		blocks := []wireBlock{{
			Type: "text", Text: stable,
			CacheControl: &cacheControl{Type: "ephemeral"},
		}}
		if suffix := strings.TrimPrefix(system, stable); strings.TrimSpace(suffix) != "" {
			blocks = append(blocks, wireBlock{Type: "text", Text: suffix})
		}
		return blocks
	}
	return []wireBlock{{
		Type: "text", Text: system,
		CacheControl: &cacheControl{Type: "ephemeral"},
	}}
}

func wireTools(tools []provider.ToolSchema) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireTool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, wireTool{
			Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
		})
	}
	out[len(out)-1].CacheControl = &cacheControl{Type: "ephemeral"}
	return out
}

// wireThinking is Anthropic's extended-thinking block.
type wireThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// wireImageSource is Anthropic's image source for vision.
type wireImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func toWire(msgs []provider.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		wm := wireMessage{Role: m.Role}
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				wm.Content = append(wm.Content, wireBlock{Type: "text", Text: b.Text})
			case "thinking":
				if b.Signature == "" {
					continue // unsigned thinking cannot be replayed
				}
				wm.Content = append(wm.Content, wireBlock{Type: "thinking", Thinking: b.Text, Signature: b.Signature})
			case "tool_use":
				in := b.Input
				if len(in) == 0 {
					in = json.RawMessage(`{}`)
				}
				wm.Content = append(wm.Content, wireBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: in})
			case "tool_result":
				wm.Content = append(wm.Content, wireBlock{
					Type: "tool_result", ToolUseID: b.ToolUseID, Content: b.Content, IsError: b.IsError,
				})
			case "image":
				if b.Source == "base64" && b.Data != "" {
					wm.Content = append(wm.Content, wireBlock{
						Type: "image",
						Source: wireImageSource{
							Type:      "base64",
							MediaType: b.MediaType,
							Data:      b.Data,
						},
					})
				}
			}
		}
		if len(wm.Content) == 0 {
			continue // Anthropic rejects empty content arrays
		}
		out = append(out, wm)
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

	if c.APIKey == "" {
		emit(provider.Event{Kind: provider.EventError, Err: fmt.Errorf("anthropic: no API key (set ANTHROPIC_API_KEY)")})
		return
	}

	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 8192
	}
	body := wireRequest{
		Model:       req.Model,
		MaxTokens:   maxTok,
		System:      wireSystem(req.System, req.SystemStable),
		Messages:    toWire(req.Messages),
		Tools:       wireTools(req.Tools),
		Stream:      true,
		Temperature: req.Temperature,
	}

	// Extended thinking, when the model supports it and a level is asked for.
	if style, _ := provider.DetectReasoningForProvider("anthropic", req.Model); style == provider.ReasoningStyleAnthropic ||
		(style == provider.ReasoningStyleUnknown && req.Reasoning != provider.ReasoningOff) {
		if budget := req.Reasoning.Budget(maxTok); budget > 0 {
			body.Thinking = &wireThinking{Type: "enabled", BudgetTokens: budget}
			// Anthropic rejects a temperature alongside thinking.
			body.Temperature = nil
		}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		emit(provider.Event{Kind: provider.EventError, Err: err})
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		emit(provider.Event{Kind: provider.EventError, Err: err})
		return
	}
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", apiVersion)
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		emit(provider.Event{Kind: provider.EventError, Err: err})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		emit(provider.Event{Kind: provider.EventError,
			Err: fmt.Errorf("anthropic: http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))})
		return
	}

	c.readSSE(ctx, resp.Body, emit)
}

// toolAccum accumulates streamed partial JSON for one tool_use block.
type toolAccum struct {
	id   string
	name string
	json strings.Builder
}

func (c *Client) readSSE(ctx context.Context, r io.Reader, emit func(provider.Event) bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)

	var event string
	var data strings.Builder
	blocks := map[int]*toolAccum{}
	usage := provider.Usage{}
	stopReason := ""

	flush := func() bool {
		if event == "" && data.Len() == 0 {
			return true
		}
		payload := data.String()
		ev, dat := event, payload
		event = ""
		data.Reset()
		return c.handleEvent(ev, dat, blocks, &usage, &stopReason, emit)
	}

	for sc.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case line == "":
			if !flush() {
				return
			}
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(line[len("data:"):]))
		}
	}
	flush()

	if err := sc.Err(); err != nil && ctx.Err() == nil {
		emit(provider.Event{Kind: provider.EventError, Err: err})
		return
	}
	emit(provider.Event{Kind: provider.EventUsage, Usage: &usage})
	emit(provider.Event{Kind: provider.EventDone, StopReason: stopReason})
}

func (c *Client) handleEvent(event, data string, blocks map[int]*toolAccum,
	usage *provider.Usage, stopReason *string, emit func(provider.Event) bool) bool {

	if data == "" {
		return true
	}
	switch event {
	case "message_start":
		var d struct {
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(data), &d) == nil {
			usage.InputTokens = d.Message.Usage.InputTokens
			usage.CacheReadTokens = d.Message.Usage.CacheReadInputTokens
			usage.CacheWriteTokens = d.Message.Usage.CacheCreationInputTokens
		}

	case "content_block_start":
		var d struct {
			Index        int       `json:"index"`
			ContentBlock wireBlock `json:"content_block"`
		}
		if json.Unmarshal([]byte(data), &d) != nil {
			return true
		}
		if d.ContentBlock.Type == "tool_use" {
			blocks[d.Index] = &toolAccum{id: d.ContentBlock.ID, name: d.ContentBlock.Name}
			return emit(provider.Event{Kind: provider.EventToolCallStart,
				ToolCall: &provider.ToolCall{ID: d.ContentBlock.ID, Name: d.ContentBlock.Name}})
		}

	case "content_block_delta":
		var d struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &d) != nil {
			return true
		}
		switch d.Delta.Type {
		case "text_delta":
			return emit(provider.Event{Kind: provider.EventText, Text: d.Delta.Text})
		case "thinking_delta":
			return emit(provider.Event{Kind: provider.EventThinking, Text: d.Delta.Thinking})
		case "input_json_delta", "input_json_partial":
			if b := blocks[d.Index]; b != nil {
				b.json.WriteString(d.Delta.PartialJSON)
			}
		}

	case "content_block_stop":
		var d struct {
			Index int `json:"index"`
		}
		if json.Unmarshal([]byte(data), &d) != nil {
			return true
		}
		if b := blocks[d.Index]; b != nil {
			in := strings.TrimSpace(b.json.String())
			if in == "" {
				in = "{}"
			}
			delete(blocks, d.Index)
			return emit(provider.Event{Kind: provider.EventToolCall,
				ToolCall: &provider.ToolCall{ID: b.id, Name: b.name, Input: json.RawMessage(in)}})
		}

	case "message_delta":
		var d struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &d) == nil {
			if d.Delta.StopReason != "" {
				*stopReason = d.Delta.StopReason
			}
			if d.Usage.OutputTokens > 0 {
				usage.OutputTokens = d.Usage.OutputTokens
			}
		}

	case "error":
		var d struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &d) == nil {
			return emit(provider.Event{Kind: provider.EventError,
				Err: fmt.Errorf("anthropic: %s: %s", d.Error.Type, d.Error.Message)})
		}
	}
	return true
}

// ListModels queries the live model catalogue.
func (c *Client) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/models?limit=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", apiVersion)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: models http %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	models := make([]provider.ModelInfo, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, provider.ModelInfo{
			ID: m.ID, Name: m.DisplayName, ContextWindow: 200000, MaxOutput: 32000,
		})
	}
	return models, nil
}
