package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) moveActivityCursor(delta int) {
	items := m.activityItems()
	if len(items) == 0 {
		m.activityCursor = 0
		return
	}
	m.activityCursor += delta
	if m.activityCursor < 0 {
		m.activityCursor = 0
	}
	if m.activityCursor >= len(items) {
		m.activityCursor = len(items) - 1
	}
}

func (m *Model) openFocusedActivity() (tea.Model, tea.Cmd) {
	items := m.activityItems()
	if m.activityCursor < 0 || m.activityCursor >= len(items) {
		m.activityFocused = false
		return m, nil
	}
	return m.openActivity(items[m.activityCursor])
}

func (m *Model) resizeForActivity() {
	if m.ready {
		m.handleResize(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	}
}
