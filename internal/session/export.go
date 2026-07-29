package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"rick/internal/provider"
)

// SessionSource identifies the on-disk format a session is being imported from.
type SessionSource = string

// Supported import sources. SourceAuto sniffs the payload.
const (
	SourceAuto     SessionSource = "auto"
	SourceOpencode SessionSource = "opencode"
	SourceKilo     SessionSource = "kilo"
	SourceCodex    SessionSource = "codex"
)

// Parser converts a foreign session payload into a rick session.
type Parser func(data []byte) (*Session, error)

// parsers maps a source to its parser. SourceAuto is handled by Import.
var parsers = map[SessionSource]Parser{
	SourceOpencode: ParseOpencode,
	SourceKilo:     ParseKilo,
	SourceCodex:    ParseCodex,
}

// autoOrder is the fallback try-order used when sniffing is inconclusive.
var autoOrder = []SessionSource{SourceOpencode, SourceKilo, SourceCodex}

// Export writes the full session — messages, snapshots and metadata — to w as
// compact JSON. Use ExportPretty when a human-readable file is wanted.
func Export(sess *Session, w io.Writer) error {
	return export(sess, w, false)
}

// ExportPretty writes a human-readable indented session JSON document.
func ExportPretty(sess *Session, w io.Writer) error {
	return export(sess, w, true)
}

func export(sess *Session, w io.Writer, pretty bool) error {
	if sess == nil {
		return errors.New("session: export nil session")
	}
	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(sess)
}

// Import reads a session from r and normalizes it into rick's format. When
// source is SourceAuto the payload is sniffed and, failing that, every parser
// is tried in turn; the first success wins.
func Import(r io.Reader, source SessionSource) (*Session, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, errors.New("session: empty import payload")
	}

	if source != "" && source != SourceAuto {
		parse, ok := parsers[source]
		if !ok {
			return nil, fmt.Errorf("session: unknown import source %q", source)
		}
		return parse(data)
	}

	kind := detectKind(data)
	// A payload we exported ourselves round-trips verbatim; the foreign
	// parsers would flatten its tool blocks and drop snapshots/usage. Avoid
	// attempting this full decode for payloads already identified as foreign.
	if kind == SourceAuto {
		if sess, err := parseNative(data); err == nil {
			return sess, nil
		}
	}

	order := autoOrder
	if kind != SourceAuto {
		order = append([]SessionSource{kind}, autoOrder...)
	}

	var errs []string
	seen := map[SessionSource]bool{}
	for _, src := range order {
		if seen[src] {
			continue
		}
		seen[src] = true
		sess, err := parsers[src](data)
		if err == nil {
			return sess, nil
		}
		errs = append(errs, src+": "+err.Error())
	}
	return nil, fmt.Errorf("session: could not import (%s)", strings.Join(errs, "; "))
}

// parseNative decodes a payload rick itself exported. Unknown fields are
// rejected so foreign formats (createdAt, timestamp, created_at, tool_calls …)
// fall through to their own parsers instead of being silently flattened.
func parseNative(data []byte) (*Session, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var sess Session
	if err := dec.Decode(&sess); err != nil {
		return nil, err
	}
	if len(sess.Messages) == 0 {
		return nil, errors.New("session: native payload has no messages")
	}
	finalize(&sess)
	return &sess, nil
}

// detectKind peeks at the JSON payload and guesses which agent wrote it. It
// returns SourceAuto when the shape is ambiguous.
func detectKind(data []byte) SessionSource {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return SourceAuto
	}
	rawMsgs, hasMsgs := top["messages"]
	if !hasMsgs {
		return SourceAuto
	}
	// Native sessions carry both RFC3339 fields under their own names. Keep
	// this shape ambiguous so parseNative can preserve tool blocks and metadata.
	if _, hasCreated := top["created"]; hasCreated {
		if _, hasUpdated := top["updated"]; hasUpdated {
			return SourceAuto
		}
	}

	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(rawMsgs, &msgs); err != nil {
		return SourceAuto
	}

	typedContent := false
	for _, m := range msgs {
		if _, ok := m["tool_calls"]; ok {
			return SourceCodex
		}
		if _, ok := m["tool_outputs"]; ok {
			return SourceCodex
		}
		if hasTypedContent(m["content"]) {
			typedContent = true
		}
	}
	if _, ok := top["created_at"]; ok {
		return SourceCodex
	}
	if _, ok := top["timestamp"]; ok {
		return SourceKilo
	}
	if typedContent {
		return SourceOpencode
	}
	if _, ok := top["createdAt"]; ok {
		return SourceOpencode
	}
	if _, ok := top["updatedAt"]; ok {
		return SourceOpencode
	}
	return SourceAuto
}

// hasTypedContent reports whether a message content field is an array of
// {type,text} parts rather than a plain string.
func hasTypedContent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return false
	}
	for _, p := range parts {
		if _, ok := p["type"]; ok {
			return true
		}
		if _, ok := p["text"]; ok {
			return true
		}
	}
	return false
}

// contentBlocks normalizes a foreign content field — a plain string or an array
// of typed parts — into rick content blocks. Non-text parts are skipped.
func contentBlocks(raw json.RawMessage) []provider.ContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []provider.ContentBlock{provider.TextBlock(s)}
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	var out []provider.ContentBlock
	for _, p := range parts {
		if p.Type != "" && p.Type != "text" {
			continue
		}
		if p.Text == "" {
			continue
		}
		out = append(out, provider.TextBlock(p.Text))
	}
	return out
}

// normalizeRole maps a foreign role string onto a rick role, returning false
// for roles rick does not model.
func normalizeRole(role string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case provider.RoleUser, "human":
		return provider.RoleUser, true
	case provider.RoleAssistant, "ai", "model":
		return provider.RoleAssistant, true
	case provider.RoleSystem, "developer":
		return provider.RoleSystem, true
	}
	return "", false
}

// parseTime accepts an RFC3339 string, a numeric unix timestamp (seconds or
// milliseconds) or a numeric string, and returns the zero time on failure.
func parseTime(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return time.Time{}
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return unixAuto(n)
		}
		return time.Time{}
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return unixAuto(int64(n))
	}
	return time.Time{}
}

// unixAuto interprets n as unix seconds or milliseconds based on magnitude.
func unixAuto(n int64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	if n > 1e11 {
		return time.UnixMilli(n).UTC()
	}
	return time.Unix(n, 0).UTC()
}

// finalize fills in the derived fields every importer needs.
func finalize(sess *Session) {
	if sess.ID == "" {
		sess.ID = NewID()
	}
	if sess.Title == "" {
		sess.Title = Title(sess.Messages)
	}
	if sess.Created.IsZero() {
		sess.Created = time.Now()
	}
	if sess.Updated.IsZero() {
		sess.Updated = sess.Created
	}
}
