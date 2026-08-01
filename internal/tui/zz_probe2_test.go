package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func focusedModel() *Model {
	m := newModelChoiceTestModel()
	m.input = textarea.New()
	m.input.Focus()
	m.input.SetWidth(60)
	return m
}

func TestProbe2KeyShadowing(t *testing.T) {
	m := focusedModel()
	m.input.SetValue("abcdef")
	m.input.SetCursor(6)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlB})
	fmt.Println("ctrl+b col (want 5):", m.input.LineInfo().ColumnOffset)
	m.input.SetCursor(0)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlF})
	fmt.Println("ctrl+f col (want 1):", m.input.LineInfo().ColumnOffset)
	m.input.SetCursor(0)
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnd})
	fmt.Println("end col (want 6):", m.input.LineInfo().ColumnOffset)
	m.input.SetCursor(0)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlE})
	fmt.Println("ctrl+e col (want 6, not intercepted):", m.input.LineInfo().ColumnOffset)

	// multi-line cursor navigation
	m2 := focusedModel()
	m2.inputHist = []string{"OLD PROMPT"}
	m2.histIdx = -1
	m2.input.SetValue("line1\nline2")
	m2.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	fmt.Printf("multi-line up: value=%q row=%d\n", m2.input.Value(), m2.input.Line())
}

func TestProbe2WindowsPasteSim(t *testing.T) {
	// Windows console input delivers a pasted multi-line block as individual
	// key events, with newlines arriving as KeyEnter.
	m := focusedModel()


	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune("b")},
	} {
		m.handleKey(k)
	}
	fmt.Printf("value after 'a' ENTER 'b': %q\n", m.input.Value())
}

func TestProbe2CtrlVTextClipboard(t *testing.T) {
	m := focusedModel()
	m.input.SetValue("hi ")
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlV})
	fmt.Printf("after ctrl+v: value=%q status=%q\n", m.input.Value(), m.status)
}

func TestProbe2BracketedPasteString(t *testing.T) {
	k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter"), Paste: true}
	fmt.Printf("paste key string: %q\n", k.String())
	k2 := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")}
	fmt.Printf("typed key string: %q\n", k2.String())
	m := focusedModel()
	m.handleKey(k)
	fmt.Printf("value after bracketed paste of 'enter': %q\n", m.input.Value())
}
