package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/config"
)

// editModal represents an in-TUI editor for skills, agents, and MCP configs.
type editModal struct {
	kind    string
	name    string
	path    string
	content string
	ta      textarea.Model
	err     error
	saved   bool
}

// showEditModal opens an in-TUI editor for the given item.
func (m *Model) showEditModal(kind, name, path string) (tea.Model, tea.Cmd) {
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		m.setStatus(fmt.Sprintf("edit: %v", err))
		return m, nil
	}

	ta := textarea.New()
	ta.SetValue(string(content))
	ta.Focus()
	ta.SetWidth(m.width - 4)
	ta.SetHeight(m.height - 8)
	ta.CharLimit = 0
	ta.ShowLineNumbers = true

	m.pending.edit = &editModal{
		kind:    kind,
		name:    name,
		path:    path,
		content: string(content),
		ta:      ta,
	}

	return m, nil
}

// cmdEdit opens an editor for skills or agents.
func (m *Model) cmdEdit(args string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(args)
	if len(fields) < 2 {
		m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "usage: /edit <skill|agent|mcp> <name>", Time: time.Now()})
		return m, nil
	}
	kind := strings.ToLower(fields[0])
	name := fields[1]

	var path string
	switch kind {
	case "skill":
		if p := filepath.Join(m.deps.Cwd, ".agents", "skills", name+".md"); fileExists(p) {
			path = p
		} else if p := filepath.Join(config.GlobalDir(), "skills", name+".md"); fileExists(p) {
			path = p
		} else {
			path = filepath.Join(m.deps.Cwd, ".agents", "skills", name+".md")
		}
	case "agent":
		if p := filepath.Join(m.deps.Cwd, ".rick", "agents", name+".md"); fileExists(p) {
			path = p
		} else if p := filepath.Join(config.GlobalDir(), "agents", name+".md"); fileExists(p) {
			path = p
		} else {
			path = filepath.Join(m.deps.Cwd, ".rick", "agents", name+".md")
		}
	case "mcp":
		if p := filepath.Join(m.deps.Cwd, ".rick", "mcp", name+".json"); fileExists(p) {
			path = p
		} else if p := filepath.Join(config.GlobalDir(), "mcp", name+".json"); fileExists(p) {
			path = p
		} else {
			path = filepath.Join(m.deps.Cwd, ".rick", "mcp", name+".json")
		}
	default:
		m.appendMsg(ChatMsg{Kind: MsgSystem, Text: fmt.Sprintf("unknown edit type: %s (use skill, agent, mcp)", kind), Time: time.Now()})
		return m, nil
	}

	return m.showEditModal(kind, name, path)
}

// renderEditModal renders the edit overlay.
func (m *Model) renderEditModal() string {
	if m.pending.edit == nil {
		return ""
	}
	ed := m.pending.edit
	s := m.styles

	var b strings.Builder
	title := fmt.Sprintf(" editing %s: %s ", ed.kind, ed.name)
	b.WriteString(s.Accent.Render(title) + "\n")
	b.WriteString(s.Faint.Render("  ctrl+s save · esc discard") + "\n\n")
	b.WriteString(ed.ta.View())

	if ed.err != nil {
		b.WriteString(s.Error.Render(fmt.Sprintf("\n  error: %v", ed.err)))
	}
	if ed.saved {
		b.WriteString(s.Success.Render("\n  saved!"))
	}

	return b.String()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
