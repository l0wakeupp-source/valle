package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type clipboardShortcutTickMsg time.Time

func clipboardShortcutTick() tea.Cmd {
	return tea.Tick(20*time.Millisecond, func(now time.Time) tea.Msg {
		return clipboardShortcutTickMsg(now)
	})
}
