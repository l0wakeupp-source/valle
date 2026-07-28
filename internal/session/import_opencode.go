package session

import (
	"encoding/json"
	"errors"

	"rick/internal/provider"
)

// opencodeSession is the on-disk shape of an opencode conversation.
type opencodeSession struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Model     string            `json:"model"`
	Messages  []opencodeMessage `json:"messages"`
	CreatedAt json.RawMessage   `json:"createdAt"`
	UpdatedAt json.RawMessage   `json:"updatedAt"`
}

// opencodeMessage is one opencode turn. Content is either a string or an array
// of typed parts.
type opencodeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ParseOpencode converts an opencode session JSON payload into a rick session.
func ParseOpencode(data []byte) (*Session, error) {
	var raw opencodeSession
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Messages == nil {
		return nil, errors.New("opencode: no messages field")
	}

	var msgs []provider.Message
	for _, m := range raw.Messages {
		role, ok := normalizeRole(m.Role)
		if !ok {
			continue
		}
		blocks := contentBlocks(m.Content)
		if len(blocks) == 0 {
			continue
		}
		msgs = append(msgs, provider.Message{Role: role, Content: blocks})
	}
	if len(msgs) == 0 {
		return nil, errors.New("opencode: no usable messages")
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
