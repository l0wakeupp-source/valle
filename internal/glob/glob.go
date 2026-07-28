// Package glob implements the wildcard matching rick uses for bash command
// patterns, tool enable/disable rules and file lookups.
//
// It lives in its own package because the permission engine, the MCP manager
// and the search tools all need identical semantics — three copies of a
// backtracking matcher is three chances to disagree.
package glob

import "strings"

// Match reports whether s matches pattern. Matching is anchored at both ends
// and case-sensitive.
//
//   - matches any run of characters, including none and including '/'
//     ?  matches exactly one character
//
// The implementation backtracks iteratively rather than recursing, so a
// pathological pattern cannot blow the stack.
func Match(pattern, s string) bool {
	var pi, si, star, mark int
	star = -1
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == s[si] || pattern[pi] == '?'):
			pi++
			si++
		case pi < len(pattern) && pattern[pi] == '*':
			star = pi
			mark = si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// MatchAny reports whether s matches at least one pattern.
func MatchAny(patterns []string, s string) bool {
	for _, p := range patterns {
		if Match(p, s) {
			return true
		}
	}
	return false
}

// MatchPath is Match with `**` collapsed to `*`. Since Match's `*` already
// crosses '/', the two are equivalent — this just accepts the familiar
// `**/*.go` spelling.
func MatchPath(pattern, path string) bool {
	pattern = strings.ReplaceAll(pattern, "**/", "*")
	pattern = strings.ReplaceAll(pattern, "**", "*")
	return Match(pattern, path)
}

// Lookup resolves name against a map whose keys may be exact names or globs.
// Exact keys win; globs are only consulted as a fallback. The second return
// value reports whether any key matched.
func Lookup(m map[string]bool, name string) (bool, bool) {
	if v, ok := m[name]; ok {
		return v, true
	}
	for pat, v := range m {
		if strings.ContainsAny(pat, "*?") && Match(pat, name) {
			return v, true
		}
	}
	return false, false
}
