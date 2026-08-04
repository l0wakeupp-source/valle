package compress

import (
	"bytes"
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// Minify collapses insignificant whitespace in structured data formats. JSON
// is compacted exactly (only whitespace is removed). YAML is normalized
// through a yaml.v3 round-trip, which drops comments and redundant quoting;
// it is only returned when the round-trip is smaller than the input so a
// verbose-but-compact document is never expanded. ok is false when the text
// is not a single structured document or the minified form is not smaller.
func Minify(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	var best string
	bestLen := len(trimmed)

	if compact, ok := minifyJSON(trimmed); ok && len(compact) < bestLen {
		best, bestLen = compact, len(compact)
	}
	if compact, ok := minifyYAML(trimmed); ok && len(compact) < bestLen {
		best, bestLen = compact, len(compact)
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// minifyJSON returns the exact whitespace-free form of a JSON document.
func minifyJSON(text string) (string, bool) {
	if !strings.HasPrefix(text, "{") && !strings.HasPrefix(text, "[") {
		return "", false
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(text)); err != nil {
		return "", false
	}
	return buf.String(), true
}

// minifyYAML normalizes a YAML document through yaml.v3. Comments are dropped
// (they carry no data), which shrinks commented configs; the round-trip is
// only returned when it is smaller than the input, so an already-compact
// document is never expanded. Node styles are preserved, so quoted scalars
// keep their quoting and flow style stays flow style.
func minifyYAML(text string) (string, bool) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(text), &node); err != nil {
		return "", false
	}
	// A bare scalar (a single word or quoted string) is not worth a round-trip.
	if node.Kind == yaml.ScalarNode {
		return "", false
	}
	stripComments(&node)
	var buf bytes.Buffer
	if err := yaml.NewEncoder(&buf).Encode(&node); err != nil {
		return "", false
	}
	out := strings.TrimRight(buf.String(), "\n")
	if len(out) >= len(text) {
		return "", false
	}
	return out, true
}

// stripComments removes comment metadata from a parsed node tree so the
// re-marshal does not carry them.
func stripComments(node *yaml.Node) {
	node.HeadComment, node.LineComment, node.FootComment = "", "", ""
	for _, child := range node.Content {
		stripComments(child)
	}
}
