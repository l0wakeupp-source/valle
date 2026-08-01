package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestProbeTruncateANSI(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	st := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	out := truncate(st.Render("HELLOWORLD"), 5)
	fmt.Printf("truncate: %q hasReset=%v\n", out, strings.Contains(out, "\x1b[0m"))
}

func TestProbeNewlineInsertion(t *testing.T) {
	m := newModelChoiceTestModel()
	m.input = textarea.New()
	m.input.SetValue("hello world")
	m.input.SetCursor(5) // between "hello" and " world"
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j"), Alt: false})
	fmt.Printf("after typing j at col5: %q\n", m.input.Value())

	m2 := newModelChoiceTestModel()
	m2.input = textarea.New()
	m2.input.SetValue("hello world")
	m2.input.SetCursor(5)
	m2.handleKey(tea.KeyMsg{Type: tea.KeyCtrlJ})
	fmt.Printf("after ctrl+j at col5: %q (want %q)\n", m2.input.Value(), "hello\n world")
}

func TestProbeStaleInputHeight(t *testing.T) {
	m := newModelChoiceTestModel()
	m.input = textarea.New()
	m.input.SetValue("a\nb\nc\nd")
	m.syncInputHeight()
	fmt.Println("height with 4 lines:", m.input.Height(), "visualLines:", m.inputVisualLines())
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlU})
	fmt.Printf("after ctrl+u: value=%q height=%d visualLines=%d\n", m.input.Value(), m.input.Height(), m.inputVisualLines())
	m.input.SetValue("a\nb\nc\nd")
	m.syncInputHeight()
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	fmt.Printf("after esc: value=%q height=%d\n", m.input.Value(), m.input.Height())
	m.input.SetValue("a\nb\nc\nd")
	m.syncInputHeight()
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	fmt.Printf("after ctrl+c: value=%q height=%d\n", m.input.Value(), m.input.Height())
}

func TestProbeEnterOnFreshMenu(t *testing.T) {
	m := newModelChoiceTestModel()
	m.input = textarea.New()
	m.armChoice("pick one", pendingTheme, "", []choiceOption{
		{value: "a", label: "a"}, {value: "b", label: "b", active: true}, {value: "c", label: "c"},
	})
	fmt.Println("cursor after arm:", m.pending.cursor, "cursorMoved:", m.pending.cursorMoved)
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	fmt.Println("pending kind after enter (0 = cleared/cancelled):", m.pending.kind)
	last := m.msgs[len(m.msgs)-1]
	fmt.Printf("last msg kind=%v text=%q\n", last.Kind, last.Text)
}

func TestProbeBackspaceCancelsMenu(t *testing.T) {
	m := newModelChoiceTestModel()
	m.input = textarea.New()
	m.armChoice("pick", pendingTheme, "", []choiceOption{{value: "a", label: "a"}, {value: "b", label: "b"}})
	m.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	fmt.Println("pending kind after backspace:", m.pending.kind, "last:", m.msgs[len(m.msgs)-1].Text)
}

func TestProbeKeyShadowing(t *testing.T) {
	m := newModelChoiceTestModel()
	m.input = textarea.New()
	m.input.SetValue("abcdef")
	m.input.SetCursor(6)
	before := m.input.LineInfo().ColumnOffset
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlB})
	fmt.Println("ctrl+b col before/after:", before, m.input.LineInfo().ColumnOffset)
	m.input.SetCursor(0)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlF})
	fmt.Println("ctrl+f col (want 1):", m.input.LineInfo().ColumnOffset)
	m.input.SetCursor(0)
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnd})
	fmt.Println("end col (want 6):", m.input.LineInfo().ColumnOffset)
}
