package tools

import (
	"fmt"
	"strings"
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

// lcsDiff runs a standard LCS dynamic program. Guards against pathological
// sizes by falling back to a wholesale replace.
func lcsDiff(a, b []string, offset int) []DiffOp {
	const maxCells = 4_000_000
	if len(a)*len(b) > maxCells {
		var ops []DiffOp
		for i, l := range a {
			ops = append(ops, DiffOp{Kind: '-', Text: l, OldLine: offset + i + 1})
		}
		for i, l := range b {
			ops = append(ops, DiffOp{Kind: '+', Text: l, NewLine: offset + i + 1})
		}
		return ops
	}

	n, m := len(a), len(b)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}

	var ops []DiffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, DiffOp{Kind: ' ', Text: a[i], OldLine: offset + i + 1, NewLine: offset + j + 1})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, DiffOp{Kind: '-', Text: a[i], OldLine: offset + i + 1})
			i++
		default:
			ops = append(ops, DiffOp{Kind: '+', Text: b[j], NewLine: offset + j + 1})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, DiffOp{Kind: '-', Text: a[i], OldLine: offset + i + 1})
	}
	for ; j < m; j++ {
		ops = append(ops, DiffOp{Kind: '+', Text: b[j], NewLine: offset + j + 1})
	}
	return ops
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
