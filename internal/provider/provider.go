// Package provider defines the LLM provider abstraction used by rick.
//
// Every backend (Anthropic, OpenAI, OpenRouter, ...) implements Provider.
// The agent loop only ever talks to this interface, so adding a backend never
// touches the agent or the TUI.
package provider

import (
	"context"
	"encoding/json"
)

// Role constants for Message.Role.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// ContentBlock is one piece of a message. A message is a list of blocks so a
// single assistant turn can hold text plus several tool calls, and a single
// user turn can hold several tool results.
type ContentBlock struct {
	Type string `json:"type"` // "text" | "tool_use" | "tool_result" | "thinking" | "image"

	// Type == "text" or "thinking"
	Text string `json:"text,omitempty"`

	// Type == "thinking" (provider-signed, replayed verbatim)
	Signature string `json:"signature,omitempty"`

	// Type == "tool_use"
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// Type == "tool_result"
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// Type == "image" — base64-encoded image for vision models
	Source    string `json:"source,omitempty"`     // "base64" or "url"
	MediaType string `json:"media_type,omitempty"` // "image/png", "image/jpeg", etc.
	Data      string `json:"data,omitempty"`       // base64-encoded image data or URL
}

// TextBlock is a convenience constructor.
func TextBlock(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

// ImageBlock is a convenience constructor for base64-encoded images.
func ImageBlock(mediaType, base64Data string) ContentBlock {
	return ContentBlock{Type: "image", Source: "base64", MediaType: mediaType, Data: base64Data}
}

// ToolResultBlock is a convenience constructor.
func ToolResultBlock(id, content string, isErr bool) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolUseID: id, Content: content, IsError: isErr}
}

// Message is one conversational turn.
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// UserText builds a plain user message.
func UserText(s string) Message {
	return Message{Role: RoleUser, Content: []ContentBlock{TextBlock(s)}}
}

// AssistantText builds a plain assistant message.
func AssistantText(s string) Message {
	return Message{Role: RoleAssistant, Content: []ContentBlock{TextBlock(s)}}
}

// Text flattens all text blocks of a message.
func (m Message) Text() string {
	out := ""
	for _, b := range m.Content {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}

// ToolSchema describes a tool to the model.
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Request is a single completion request.
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolSchema
	MaxTokens   int
	Temperature *float64
	// Reasoning is the requested thinking level. Providers translate it into
	// their own dialect and ignore it when the model does not reason.
	Reasoning ReasoningEffort
}

// Usage reports token accounting for a turn.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
}

// EventKind enumerates stream event types.
type EventKind int

const (
	EventText          EventKind = iota // incremental assistant text
	EventThinking                       // incremental reasoning text
	EventToolCallStart                  // model began emitting a tool call
	EventToolCall                       // a complete tool call is ready
	EventUsage                          // token accounting
	EventDone                           // turn finished (StopReason set)
	EventError                          // fatal stream error
)

// ToolCall is a fully-parsed tool invocation from the model.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Event is one item in the provider stream.
type Event struct {
	Kind       EventKind
	Text       string
	ToolCall   *ToolCall
	Usage      *Usage
	StopReason string
	Err        error
}

// ModelInfo is a model advertised by a provider.
type ModelInfo struct {
	ID             string
	Name           string
	ContextWindow  int
	MaxOutput      int
	SupportsImages bool
}

// Provider is the single abstraction every backend implements.
//
// Stream owns ch: it must close ch exactly once before returning. Callers must
// never close it (see the double-close pitfall).
type Provider interface {
	Name() string
	Models() []ModelInfo
	Stream(ctx context.Context, req Request, ch chan<- Event)
}

// ModelLister is optionally implemented by providers that can enumerate models
// from a live endpoint.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
}
