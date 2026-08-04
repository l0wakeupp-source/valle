// Package delta implements TokenDiff-based delta encoding for repeated reads
// of changed files: when a file changes between two reads, the second read
// returns only the semantic token-level diff against the previously delivered
// version instead of re-echoing the whole file. The unchanged majority is
// dropped; changed lines are word-annotated ([-removed-]{+added+}) by the
// pure-Go tokendiff engine, so `someFunction(SomeType var)` versus
// `someFunction(SomeOtherType var)` shows only the single changed identifier.
package delta

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dacharyc/tokendiff"
)

const (
	// DefaultMaxBytes bounds a serialized delta view.
	DefaultMaxBytes = 24 << 10
	// minSavingsRatio is the smallest reduction (delta size vs new file size)
	// that justifies a delta view over a full read.
	minSavingsRatio = 0.6
)

var diffOptions = tokendiff.Options{UsePunctuation: true, PreserveWhitespace: true}

// Encode builds the delta view of newText relative to oldText. ok is false
// when nothing changed, when the rewrite is large enough that a full read is
// cheaper, or when the view would exceed maxBytes.
func Encode(oldText, newText string, maxBytes int) (view string, ok bool) {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	lineDiffs := tokendiff.DiffTokens(oldLines, newLines)

	// Count change groups: a delete followed by an insert is one modification.
	changed := 0
	for i, d := range lineDiffs {
		switch d.Type {
		case tokendiff.Delete:
			changed++
		case tokendiff.Insert:
			if i == 0 || lineDiffs[i-1].Type != tokendiff.Delete {
				changed++
			}
		}
	}
	if changed == 0 {
		return "", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<delta: %d changed lines>\n", changed)
	for i := 0; i < len(lineDiffs); i++ {
		d := lineDiffs[i]
		switch d.Type {
		case tokendiff.Equal:
			continue
		case tokendiff.Delete:
			// A delete immediately followed by an insert is one modification:
			// render it as a single word-annotated line.
			if i+1 < len(lineDiffs) && lineDiffs[i+1].Type == tokendiff.Insert {
				annotated := tokendiff.FormatDiff(tokendiff.DiffStrings(d.Token, lineDiffs[i+1].Token, diffOptions))
				b.WriteString("| " + annotated + "\n")
				i++
				continue
			}
			b.WriteString("- " + d.Token + "\n")
		case tokendiff.Insert:
			b.WriteString("+ " + d.Token + "\n")
		}
	}
	view = strings.TrimSpace(b.String()) + "\n"
	if len(view) >= int(float64(len(newText))*minSavingsRatio) {
		return "", false
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if len(view) > maxBytes {
		return truncateDelta(view, maxBytes), true
	}
	return view, true
}

func truncateDelta(view string, maxBytes int) string {
	marker := "\n… <delta truncated>"
	if len(marker) >= maxBytes {
		return marker
	}
	keep := maxBytes - len(marker)
	for keep > 0 && !utf8.RuneStart(view[keep]) {
		keep--
	}
	return view[:keep] + marker
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Store tracks, per absolute path, the most recent file version the model was
// given (verbatim or as the base of a delta). It is safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	delivered map[string]string
}

// NewStore builds an empty Store.
func NewStore() *Store { return &Store{delivered: map[string]string{}} }

// Reset drops all delivered-version state (new session).
func (s *Store) Reset() {
	s.mu.Lock()
	s.delivered = map[string]string{}
	s.mu.Unlock()
}

// Deliver decides what to send for path's current content: a delta view when
// the model has already seen an older version and the change is small, or the
// full text otherwise. The returned text (in either form) becomes the model's
// baseline for the next read.
func (s *Store) Deliver(path, current string, maxBytes int) (out string, isDelta bool) {
	s.mu.Lock()
	previous, seen := s.delivered[path]
	s.delivered[path] = current
	s.mu.Unlock()
	if !seen || previous == current {
		return current, false
	}
	view, ok := Encode(previous, current, maxBytes)
	if !ok {
		return current, false
	}
	return view, true
}
