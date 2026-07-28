package session

import (
	"bytes"
	"encoding/json"
	"errors"

	"rick/internal/provider"
)

// sqliteMagic is the 16-byte header every SQLite database file starts with.
var sqliteMagic = []byte("SQLite format 3")

// kiloSession is the JSON shape of a kilo conversation.
type kiloSession struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Model     string          `json:"model"`
	Messages  []kiloMessage   `json:"messages"`
	Timestamp json.RawMessage `json:"timestamp"`
}

// kiloMessage is one kilo turn. Content is either a string or an array of
// typed parts.
type kiloMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ParseKilo converts a kilo session payload into a rick session. Kilo stores
// sessions either as JSON or as a SQLite database; the latter needs cgo and is
// therefore detected and rejected rather than parsed.
func ParseKilo(data []byte) (*Session, error) {
	if bytes.HasPrefix(data, sqliteMagic) {
		return nil, errors.New("kilo: SQLite session databases are not supported in this build (no cgo); export the session to JSON first")
	}

	var raw kiloSession
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Messages == nil {
		return nil, errors.New("kilo: no messages field")
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
		return nil, errors.New("kilo: no usable messages")
	}

	created := parseTime(raw.Timestamp)
	sess := &Session{
		ID:       raw.ID,
		Title:    raw.Title,
		Model:    raw.Model,
		Messages: msgs,
		Created:  created,
		Updated:  created,
	}
	finalize(sess)
	return sess, nil
}
