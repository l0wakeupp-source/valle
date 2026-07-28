package tui

import "rick/internal/tools"

// TodosChanged wraps a todo list update as a tea.Msg. The CLI wires this into
// the todo store's OnChange callback so the checklist panel re-renders.
func TodosChanged(items []tools.TodoItem) any {
	return todosChangedMsg{items: items}
}
