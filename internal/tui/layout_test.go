package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/config"
	"rick/internal/theme"
	"rick/internal/tools"
)

func TestTodoPanelReservesRowsForChatbar(t *testing.T) {
	store := tools.NewTodoStore()
	m := &Model{
		deps:   Deps{Loaded: &config.Loaded{}, Todos: store},
		styles: NewStyles(theme.Load().Get("pickle-rick")),
		input:  textarea.New(),
		tx:     newTranscript(),
		jobs:   NewJobTracker(50),
		width:  100,
		height: 24,
	}

	store.Set([]tools.TodoItem{
		{ID: "1", Content: "first", Status: "completed"},
		{ID: "2", Content: "second", Status: "completed"},
		{ID: "3", Content: "third", Status: "completed"},
		{ID: "4", Content: "fourth", Status: "completed"},
		{ID: "5", Content: "fifth", Status: "completed"},
	})

	panel := m.renderTodos(store.Items(), m.contentWidth())
	wantReserved := strings.Count(panel, "\n") + 2
	if got := m.todoPanelHeight(); got != wantReserved {
		t.Fatalf("todoPanelHeight() = %d, want %d", got, wantReserved)
	}

	m.handleResize(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	wantViewportHeight := m.height - m.inputHeight() - 4 - wantReserved
	if m.viewport.Height != wantViewportHeight {
		t.Fatalf("viewport height = %d, want %d", m.viewport.Height, wantViewportHeight)
	}

	view := stripANSI(m.View())
	if !strings.Contains(view, "╰") {
		t.Fatalf("chatbar bottom border was clipped from view:\n%s", view)
	}
}
