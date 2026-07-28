package catalog

import "strings"

// CleanSecret strips everything a terminal paste can smuggle into an API key.
//
// Go's net/http refuses to send a header value containing CR, LF or NUL and
// fails the whole request with "invalid header field value for Authorization"
// — an error that says nothing about the real cause. A key copied from a web
// page or a chat window very often carries a trailing newline, a bracketed
// paste wrapper, non-breaking spaces or smart quotes, so scrub the value
// before it ever reaches a header.
func CleanSecret(s string) string {
	// Bracketed-paste wrappers arrive as literal escape sequences when a
	// terminal is in that mode and the app reads raw bytes.
	s = strings.ReplaceAll(s, "\x1b[200~", "")
	s = strings.ReplaceAll(s, "\x1b[201~", "")

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\r', '\n', '\t', 0:
			continue // never legal in a header value
		case '\u00a0', // non-breaking space
			'\u200b', '\u200c', '\u200d', // zero-width family
			'\ufeff': // BOM
			continue
		case '\u201c', '\u201d': // smart double quotes
			continue
		case '\u2018', '\u2019': // smart single quotes
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue // any other control character
		}
		b.WriteRune(r)
	}

	out := strings.TrimSpace(b.String())
	// Straight quotes wrapping the whole value are a paste artefact too.
	for _, q := range []string{`"`, `'`, "`"} {
		if len(out) >= 2 && strings.HasPrefix(out, q) && strings.HasSuffix(out, q) {
			out = strings.TrimSpace(out[1 : len(out)-1])
		}
	}
	// A leading "Bearer " is a common copy-the-whole-header mistake.
	if len(out) > 7 && strings.EqualFold(out[:7], "bearer ") {
		out = strings.TrimSpace(out[7:])
	}
	return out
}

// SecretIsClean reports whether a value can be sent as a header as-is.
func SecretIsClean(s string) bool { return s == CleanSecret(s) }
