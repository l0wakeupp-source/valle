package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) cmdRAM() (tea.Model, tea.Cmd) {
	usage, err := currentProcessRAM()
	if err != nil {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "RAM usage unavailable: " + err.Error(), Time: nowFn()})
		return m, nil
	}
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: fmt.Sprintf("Rick terminal RAM: %.1f MB (working set)", float64(usage)/(1024*1024)), Time: nowFn()})
	return m, nil
}
