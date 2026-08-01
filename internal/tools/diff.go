package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// DiffOp is one line-level operation in a diff.
type DiffOp struct {
	Kind    byte // ' ' context, '-' removed, '+' added
	Text    string
	OldLine int // 1-indexed, 0 when not applicable
	NewLine int
}

// Hunk is a contiguous group of diff operations.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Ops                []DiffOp
}

// Diff computes a line diff between two texts using an LCS table with a
// cheap prefix/suffix trim so typical single-hunk edits stay fast.
func Diff(oldText, newText string) []DiffOp {
	a := splitLines(oldText)
	b := splitLines(newText)

	// Trim common prefix/suffix.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	midA := a[pre : len(a)-suf]
	midB := b[pre : len(b)-suf]

	var ops []DiffOp
	for i := 0; i < pre; i++ {
		ops = append(ops, DiffOp{Kind: ' ', Text: a[i], OldLine: i + 1, NewLine: i + 1})
	}
	ops = append(ops, lcsDiff(midA, midB, pre)...)
	for i := 0; i < suf; i++ {
		oi := len(a) - suf + i
		ni := len(b) - suf + i
		ops = append(ops, DiffOp{Kind: ' ', Text: a[oi], OldLine: oi + 1, NewLine: ni + 1})
	}
	return ops
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// A trailing newline yields a final empty element; drop it for diffing.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lcsDiff computes an LCS diff with Hirschberg's algorithm. It keeps only
// rolling score rows, so memory is O(min(n, m)) instead of O(n*m).
func lcsDiff(a, b []string, offset int) []DiffOp {
	return hirschbergDiff(a, b, offset, offset)
}

func hirschbergDiff(a, b []string, oldOffset, newOffset int) []DiffOp {
	if len(a) == 0 {
		ops := make([]DiffOp, 0, len(b))
		for i, line := range b {
			ops = append(ops, DiffOp{Kind: '+', Text: line, NewLine: newOffset + i + 1})
		}
		return ops
	}
	if len(b) == 0 {
		ops := make([]DiffOp, 0, len(a))
		for i, line := range a {
			ops = append(ops, DiffOp{Kind: '-', Text: line, OldLine: oldOffset + i + 1})
		}
		return ops
	}
	if len(a) == 1 {
		for j, line := range b {
			if a[0] == line {
				ops := make([]DiffOp, 0, len(b))
				for k := 0; k < j; k++ {
					ops = append(ops, DiffOp{Kind: '+', Text: b[k], NewLine: newOffset + k + 1})
				}
				ops = append(ops, DiffOp{Kind: ' ', Text: a[0], OldLine: oldOffset + 1, NewLine: newOffset + j + 1})
				for k := j + 1; k < len(b); k++ {
					ops = append(ops, DiffOp{Kind: '+', Text: b[k], NewLine: newOffset + k + 1})
				}
				return ops
			}
		}
		ops := []DiffOp{{Kind: '-', Text: a[0], OldLine: oldOffset + 1}}
		for j, line := range b {
			ops = append(ops, DiffOp{Kind: '+', Text: line, NewLine: newOffset + j + 1})
		}
		return ops
	}
	if len(b) == 1 {
		for i, line := range a {
			if b[0] == line {
				ops := make([]DiffOp, 0, len(a))
				for k := 0; k < i; k++ {
					ops = append(ops, DiffOp{Kind: '-', Text: a[k], OldLine: oldOffset + k + 1})
				}
				ops = append(ops, DiffOp{Kind: ' ', Text: b[0], OldLine: oldOffset + i + 1, NewLine: newOffset + 1})
				for k := i + 1; k < len(a); k++ {
					ops = append(ops, DiffOp{Kind: '-', Text: a[k], OldLine: oldOffset + k + 1})
				}
				return ops
			}
		}
		ops := make([]DiffOp, 0, len(a)+1)
		for i, line := range a {
			ops = append(ops, DiffOp{Kind: '-', Text: line, OldLine: oldOffset + i + 1})
		}
		ops = append(ops, DiffOp{Kind: '+', Text: b[0], NewLine: newOffset + 1})
		return ops
	}

	mid := len(a) / 2
	left := lcsLengths(a[:mid], b)
	reversedA := reverseStrings(a[mid:])
	reversedB := reverseStrings(b)
	right := lcsLengths(reversedA, reversedB)
	split := 0
	best := -1
	for j := 0; j <= len(b); j++ {
		score := left[j] + right[len(b)-j]
		if score > best {
			best, split = score, j
		}
	}
	return append(hirschbergDiff(a[:mid], b[:split], oldOffset, newOffset), hirschbergDiff(a[mid:], b[split:], oldOffset+mid, newOffset+split)...)
}

func lcsLengths(a, b []string) []int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for _, lineA := range a {
		clear(current)
		for j, lineB := range b {
			if lineA == lineB {
				current[j+1] = previous[j] + 1
			} else if previous[j+1] >= current[j] {
				current[j+1] = previous[j+1]
			} else {
				current[j+1] = current[j]
			}
		}
		previous, current = current, previous
	}
	return previous
}

func reverseStrings(lines []string) []string {
	reversed := make([]string, len(lines))
	for i, line := range lines {
		reversed[len(lines)-1-i] = line
	}
	return reversed
}

// Hunks groups diff ops into hunks with `contextLines` of surrounding context.
func Hunks(ops []DiffOp, contextLines int) []Hunk {
	if contextLines < 0 {
		contextLines = 0
	}
	changed := make([]bool, len(ops))
	any := false
	for i, o := range ops {
		if o.Kind != ' ' {
			changed[i] = true
			any = true
		}
	}
	if !any {
		return nil
	}
	keep := make([]bool, len(ops))
	for i, c := range changed {
		if !c {
			continue
		}
		lo := i - contextLines
		if lo < 0 {
			lo = 0
		}
		hi := i + contextLines
		if hi >= len(ops) {
			hi = len(ops) - 1
		}
		for k := lo; k <= hi; k++ {
			keep[k] = true
		}
	}

	var hunks []Hunk
	i := 0
	for i < len(ops) {
		if !keep[i] {
			i++
			continue
		}
		j := i
		for j < len(ops) && keep[j] {
			j++
		}
		h := Hunk{Ops: ops[i:j]}
		for _, o := range h.Ops {
			if o.OldLine > 0 && h.OldStart == 0 {
				h.OldStart = o.OldLine
			}
			if o.NewLine > 0 && h.NewStart == 0 {
				h.NewStart = o.NewLine
			}
			if o.Kind == ' ' || o.Kind == '-' {
				h.OldCount++
			}
			if o.Kind == ' ' || o.Kind == '+' {
				h.NewCount++
			}
		}
		if h.OldStart == 0 {
			h.OldStart = 1
		}
		if h.NewStart == 0 {
			h.NewStart = 1
		}
		hunks = append(hunks, h)
		i = j
	}
	return hunks
}

// UnifiedDiff renders a classic unified diff string.
func UnifiedDiff(name, oldText, newText string, contextLines int) string {
	hunks := Hunks(Diff(oldText, newText), contextLines)
	if len(hunks) == 0 {
		return "(no changes)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", name, name)
	for _, h := range hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
		for _, o := range h.Ops {
			b.WriteByte(o.Kind)
			b.WriteString(o.Text)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// UnifiedDiffLimited preserves the beginning of a diff while keeping large
// edit results from consuming the entire next model turn.
func UnifiedDiffLimited(name, oldText, newText string, contextLines, maxBytes int) string {
	full := UnifiedDiff(name, oldText, newText, contextLines)
	if maxBytes <= 0 || len(full) <= maxBytes {
		return full
	}
	suffix := fmt.Sprintf("\n… <diff truncated; %d bytes omitted>", len(full)-maxBytes)
	bodyLimit := maxBytes - len(suffix)
	if bodyLimit < 1 {
		return suffix[1:]
	}
	cut := bodyLimit
	for cut > 0 && !utf8.RuneStart(full[cut]) {
		cut--
	}
	if line := strings.LastIndex(full[:cut], "\n"); line > 0 {
		cut = line
	}
	body := full[:cut]
	return body + fmt.Sprintf("\n… <diff truncated; %d bytes omitted>", len(full)-len(body))
}

// DiffStat returns added/removed line counts.
func DiffStat(oldText, newText string) (added, removed int) {
	for _, o := range Diff(oldText, newText) {
		switch o.Kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return
}
