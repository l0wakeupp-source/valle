package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"rick/internal/tools"
)

// DiffMode controls the diff layout.
type DiffMode string

// Diff modes.
const (
	DiffAuto    DiffMode = "auto"
	DiffStacked DiffMode = "stacked"
	DiffSplit   DiffMode = "split"
)

// RenderDiff renders a file diff, choosing side-by-side or stacked based on
// the available width and the configured mode.
func (s *Styles) RenderDiff(name, oldText, newText string, width int, mode DiffMode, threshold int, links bool) string {
	if threshold <= 0 {
		threshold = 120
	}
	ops := tools.Diff(oldText, newText)
	hunks := tools.Hunks(ops, 3)
	if len(hunks) == 0 {
		return s.Muted.Render("  (no changes)")
	}
	added, removed := 0, 0
	for _, o := range ops {
		switch o.Kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}

	headerName := s.DiffHeader.Render(name)
	if links {
		headerName = s.DiffHeader.Render(OSC8(FileLink(name, 0), name))
	}
	header := fmt.Sprintf("%s  %s %s",
		headerName,
		s.DiffAdded.Render(fmt.Sprintf("+%d", added)),
		s.DiffRemoved.Render(fmt.Sprintf("-%d", removed)))

	useSplit := mode == DiffSplit || (mode != DiffStacked && width >= threshold)
	var body string
	if useSplit {
		body = s.renderSplit(hunks, width)
	} else {
		body = s.renderStacked(hunks, width)
	}
	return header + "\n" + body
}

func (s *Styles) renderStacked(hunks []tools.Hunk, width int) string {
	var b strings.Builder
	inner := width - 8
	if inner < 20 {
		inner = 20
	}
	for hi, h := range hunks {
		if hi > 0 {
			b.WriteString(s.Faint.Render("  ┈┈┈") + "\n")
		}
		for _, o := range h.Ops {
			num := "     "
			switch o.Kind {
			case '+':
				num = fmt.Sprintf("%4d ", o.NewLine)
			case '-':
				num = fmt.Sprintf("%4d ", o.OldLine)
			default:
				num = fmt.Sprintf("%4d ", o.NewLine)
			}
			text := expandTabs(o.Text)
			if lipgloss.Width(text) > inner {
				text = truncate(text, inner)
			}
			line := string(o.Kind) + " " + text
			var st lipgloss.Style
			switch o.Kind {
			case '+':
				st = s.DiffAdded
			case '-':
				st = s.DiffRemoved
			default:
				st = s.DiffContext
			}
			b.WriteString(s.DiffLineNum.Render(num) + st.Render(line) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *Styles) renderSplit(hunks []tools.Hunk, width int) string {
	gutter := 5
	colW := (width - gutter*2 - 3) / 2
	if colW < 16 {
		return s.renderStacked(hunks, width)
	}

	var b strings.Builder
	sep := s.Faint.Render(" │ ")

	for hi, h := range hunks {
		if hi > 0 {
			b.WriteString(s.Faint.Render("  ┈┈┈") + "\n")
		}
		// Pair removals with additions inside each change run.
		i := 0
		ops := h.Ops
		for i < len(ops) {
			if ops[i].Kind == ' ' {
				text := truncate(expandTabs(ops[i].Text), colW)
				left := s.DiffLineNum.Render(fmt.Sprintf("%4d ", ops[i].OldLine)) +
					s.DiffContext.Render(padRight(text, colW))
				right := s.DiffLineNum.Render(fmt.Sprintf("%4d ", ops[i].NewLine)) +
					s.DiffContext.Render(padRight(text, colW))
				b.WriteString(left + sep + right + "\n")
				i++
				continue
			}
			var dels, adds []tools.DiffOp
			for i < len(ops) && ops[i].Kind == '-' {
				dels = append(dels, ops[i])
				i++
			}
			for i < len(ops) && ops[i].Kind == '+' {
				adds = append(adds, ops[i])
				i++
			}
			n := len(dels)
			if len(adds) > n {
				n = len(adds)
			}
			for k := 0; k < n; k++ {
				left := s.DiffLineNum.Render("     ") + padRight("", colW)
				right := s.DiffLineNum.Render("     ") + padRight("", colW)
				if k < len(dels) {
					t := truncate(expandTabs(dels[k].Text), colW)
					left = s.DiffLineNum.Render(fmt.Sprintf("%4d ", dels[k].OldLine)) +
						s.DiffRemoved.Render(padRight("- "+t, colW))
				}
				if k < len(adds) {
					t := truncate(expandTabs(adds[k].Text), colW)
					right = s.DiffLineNum.Render(fmt.Sprintf("%4d ", adds[k].NewLine)) +
						s.DiffAdded.Render(padRight("+ "+t, colW))
				}
				b.WriteString(left + sep + right + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }
