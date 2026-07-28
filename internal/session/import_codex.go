package session

import (
	"bytes"
	"encoding/json"
	"errors"

	"rick/internal/provider"
)

// codexSession is the on-disk shape of a codex conversation.
type codexSession struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Model     string          `json:"model"`
	Messages  []codexMessage  `json:"messages"`
	CreatedAt json.RawMessage `json:"created_at"`
	UpdatedAt json.RawMessage `json:"updated_at"`
}

// codexMessage is one codex turn, which may carry tool calls and their outputs
// alongside the textual content.
type codexMessage struct {
	Role        string            `json:"role"`
	Content     json.RawMessage   `json:"content"`
	ToolCalls   []codexToolCall   `json:"tool_calls"`
	ToolOutputs []codexToolOutput `json:"tool_outputs"`
}

// codexToolCall is an OpenAI-style function call.
type codexToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// codexToolOutput is the result of a tool call.
type codexToolOutput struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
}

// ParseCodex converts a codex session JSON payload into a rick session,
// preserving tool calls and tool results as content blocks.
func ParseCodex(data []byte) (*Session, error) {
	var raw codexSession
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Messages == nil {
		return nil, errors.New("codex: no messages field")
	}

	var msgs []provider.Message
	for _, m := range raw.Messages {
		role, ok := normalizeRole(m.Role)
		if !ok {
			continue
		}
		blocks := contentBlocks(m.Content)
		for _, c := range m.ToolCalls {
			blocks = append(blocks, provider.ContentBlock{
				Type:  "tool_use",
				ID:    c.ID,
				Name:  c.Function.Name,
				Input: codexArgs(c.Function.Arguments),
			})
		}
		for _, o := range m.ToolOutputs {
			blocks = append(blocks, provider.ContentBlock{
				Type:      "tool_result",
				ToolUseID: o.ToolCallID,
				Content:   o.Content,
				IsError:   o.IsError,
			})
		}
		if len(blocks) == 0 {
			continue
		}
		msgs = append(msgs, provider.Message{Role: role, Content: blocks})
	}
	if len(msgs) == 0 {
		return nil, errors.New("codex: no usable messages")
	}

	sess := &Session{
		ID:       raw.ID,
		Title:    raw.Title,
		Model:    raw.Model,
		Messages: msgs,
		Created:  parseTime(raw.CreatedAt),
		Updated:  parseTime(raw.UpdatedAt),
	}
	finalize(sess)
	return sess, nil
}

// codexArgs normalizes tool arguments, which codex may store either as a raw
// JSON object or as a JSON-encoded string. The result is compacted so exported
// sessions stay stable and free of nested indentation.
func codexArgs(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		if json.Valid([]byte(s)) {
			return compactJSON([]byte(s))
		}
		encoded, err := json.Marshal(s)
		if err != nil {
			return nil
		}
		return encoded
	}
	return compactJSON(raw)
}

// compactJSON strips insignificant whitespace, returning the input unchanged if
// it cannot be compacted.
func compactJSON(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return buf.Bytes()
}
