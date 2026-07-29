package tui

import (
	"bytes"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/maintenance"
)

// runSlash dispatches built-in slash commands from the chat input.
func (m *Model) runSlash(text string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(text, "/")))
	if len(fields) == 0 {
		return m.cmdHelp()
	}
	name := strings.ToLower(fields[0])
	args := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "/"))
	if len(fields) > 1 {
		args = strings.TrimSpace(strings.TrimPrefix(args, fields[0]))
	} else {
		args = ""
	}

	switch name {
	case "help", "h":
		return m.cmdHelp()
	case "new":
		return m.cmdNew()
	case "sessions", "session":
		if args != "" {
			return m.cmdSessionsArgs(args)
		}
		return m.cmdSessions()
	case "model":
		if args == "" {
			return m.cmdModels()
		}
		if m.running {
			m.interrupt()
		}
		m.setModel(args)
		m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "model: " + args, Time: nowFn()})
		m.setStatus("model: " + shortModel(args))
		return m, nil
	case "thinking":
		return m.cmdReasoning(args)
	case "models":
		return m.cmdModels()
	case "auth":
		return m.openAuth()
	case "config":
		return m.cmdConfig()
	case "theme", "themes":
		return m.cmdThemes()
	case "mcp":
		return m.cmdMcpMenu()
	case "stats":
		return m.cmdStats()
	case "compact":
		return m.cmdCompact()
	case "ram":
		return m.cmdRAM()
	case "goal":
		return m.cmdGoal(args)
	case "update":
		m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "updating Rick from the latest GitHub release…", Time: nowFn()})
		return m, runUpdate()
	case "uninstall":
		m.armChoice("uninstall Rick: choose a removal scope", pendingMaintenance, "", []choiceOption{
			{value: "full", label: "FULL removal", detail: "delete Rick, credentials, sessions, config, and user data"},
			{value: "part", label: "PART removal", detail: "delete Rick only; keep credentials, sessions, config, and user data"},
		})
		return m, nil
	default:
		m.appendMsg(ChatMsg{Kind: MsgError, Text: fmt.Sprintf("unknown command: /%s (try /help)", name), Time: nowFn()})
		return m, nil
	}
}

func runUpdate() tea.Cmd {
	return func() tea.Msg {
		var stdout, stderr bytes.Buffer
		if err := maintenance.Update(&stdout, &stderr); err != nil {
			return statusMsg{text: "update failed: " + err.Error()}
		}
		output := strings.TrimSpace(stdout.String())
		alreadyCurrent := strings.Contains(strings.ToLower(output), "already up to date")
		return statusMsg{text: output, quit: !alreadyCurrent}
	}
}

func runUninstall(mode string) tea.Cmd {
	return func() tea.Msg {
		var stdout, stderr bytes.Buffer
		if err := maintenance.UninstallMode(mode, &stdout, &stderr); err != nil {
			return statusMsg{text: "uninstall failed: " + err.Error()}
		}
		return statusMsg{text: strings.TrimSpace(stdout.String()), quit: true}
	}
}
