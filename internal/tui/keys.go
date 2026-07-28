package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/agent"
	"rick/internal/provider"
)

func (m *Model) syncInputHeight() bool {
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > 8 {
		lines = 8
	}
	if m.input.Height() == lines {
		return false
	}
	m.input.SetHeight(lines)
	return true
}

// handleKey routes a keypress through modals, picker, leader, then the input.
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// ctrl+c: interrupt a run, otherwise if there are attachments or input, clear them.
	// Second press quits.
	if key == "ctrl+c" {
		if m.running {
			m.interrupt()
			m.quitArmed = false
			return m, nil
		}
		// If there are attachments, clear the last one (remove its marker from input)
		if len(m.attachments) > 0 {
			input := m.input.Value()
			if cleaned, idx := removeLastAttachmentMarker(input); idx >= 0 {
				m.input.SetValue(cleaned)
				m.attachments = m.attachments[:len(m.attachments)-1]
				m.setStatus(fmt.Sprintf("removed [image/file #%d]", idx))
				m.quitArmed = false
				return m, nil
			}
			// Fallback: just clear all attachments
			m.attachments = nil
			m.setStatus("attachments cleared")
			m.quitArmed = false
			return m, nil
		}
		if m.input.Value() != "" {
			m.input.SetValue("") // first press clears the line, like a shell
			m.quitArmed = true
			m.quitAt = time.Now()
			m.setStatus("ctrl+c again to exit")
			return m, nil
		}
		if m.quitArmed && time.Since(m.quitAt) < 3*time.Second {
			m.quitting = true
			return m, tea.Quit
		}
		m.quitArmed = true
		m.quitAt = time.Now()
		m.setStatus("ctrl+c again to exit")
		return m, nil
	}
	// Any other key disarms the pending quit.
	m.quitArmed = false

	// Modal permission is rendered inline via permissionView + pending input.
	// We don't capture keys here so the textarea can receive them.

	// Leader sequence.
	if m.leaderActive {
		m.leaderActive = false
		return m.handleLeaderKey(key)
	}
	if key == m.leaderKey {
		m.leaderActive = true
		return m, nil
	}

	// File picker.
	if m.picker.active {
		if handled, mm, cmd := m.handlePickerKey(key); handled {
			return mm, cmd
		}
	}

	switch key {
	case "esc":
		if m.running {
			m.interrupt()
			return m, nil
		}
		if m.input.Value() != "" {
			m.input.SetValue("")
			return m, nil
		}
		return m, nil

	case "ctrl+u":
		m.input.SetValue("")
		return m, nil

	case "tab":
		// Slash completion first, then agent cycling.
		v := m.input.Value()
		if v == "" {
			m.cycleAgent()
			return m, nil
		}
		return m, nil

	case "alt+enter", "shift+enter", "ctrl+enter", "ctrl+j":
		// Insert a newline directly into the textarea. On Windows terminals,
		// Ctrl+Enter is commonly delivered as LF (ctrl+j), not as a modified
		// KeyEnter event.
		val := m.input.Value()
		m.input.SetValue(val + "\n")
		m.input.CursorEnd()
		lines := strings.Count(m.input.Value(), "\n") + 1
		if lines > 8 {
			lines = 8
		}
		if lines < 1 {
			lines = 1
		}
		m.input.SetHeight(lines)
		m.handleResize(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return m, nil

	case "enter":
		// If the textarea has multiple logical lines, let user submit with enter
		// (single line submits, multi-line submits on enter)
		val := m.input.Value()
		if strings.Contains(val, "\n") {
			// Multi-line: submit the whole block
			v := strings.TrimSpace(val)
			if v == "" {
				if m.pending.kind != pendingNone {
					mm, cmd, _ := m.handlePendingInput("")
					return mm, cmd
				}
				return m, nil
			}
			m.input.SetValue("")
			m.input.SetHeight(1)
			m.histIdx = -1
			m.pushHistory(v)
			return m.submit(v)
		}
		v := strings.TrimSpace(val)
		if v == "" {
			// A bare enter cancels an armed selection; otherwise it is a no-op.
			if m.pending.kind != pendingNone {
				mm, cmd, _ := m.handlePendingInput("")
				return mm, cmd
			}
			return m, nil
		}
		m.input.SetValue("")
		m.input.SetHeight(1)
		m.histIdx = -1
		m.pushHistory(v)
		return m.submit(v)

	case "up", "down":
		// Arrow keys scroll the chat transcript, not the input history.
		// History recall is on Alt+up/down.
		if key == "up" {
			m.scrollBy(-m.scrollStep())
		} else {
			m.scrollBy(m.scrollStep())
		}
		return m, nil

	case "pgup":
		m.scrollBy(-(m.viewport.Height - 2))
		return m, nil
	case "pgdown":
		m.scrollBy(m.viewport.Height - 2)
		return m, nil
	case "shift+up", "ctrl+up", "shift+pgup":
		m.scrollBy(-m.scrollStep())
		return m, nil
	case "shift+down", "ctrl+down", "shift+pgdown":
		m.scrollBy(m.scrollStep())
		return m, nil
	case "alt+up":
		m.historyUp()
		return m, nil
	case "alt+down":
		m.historyDown()
		return m, nil
	case "ctrl+b":
		m.scrollBy(-(m.viewport.Height - 2))
		return m, nil
	case "ctrl+f":
		m.scrollBy(m.viewport.Height - 2)
		return m, nil
	case "ctrl+home":
		m.viewport.GotoTop()
		m.tx.userScrolled(&m.viewport)
		return m, nil
	case "ctrl+v", "ctrl+shift+v":
		if time.Since(m.lastClipboardPaste) > 250*time.Millisecond {
			m.handleClipboardPaste()
		}
		return m, nil
	case "ctrl+end", "end":
		m.tx.jumpToBottom(&m.viewport)
		return m, nil
	}

	prevLines := m.input.LineCount()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	// Keep the input bar synchronized in both directions. Deletions can reduce
	// the logical line count without triggering the old grow-only path.
	if m.syncInputHeight() {
		m.handleResize(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	}
	if m.input.LineCount() > prevLines {
		return m, cmd
	}

	// Opening the @ picker.
	v := m.input.Value()
	if strings.HasSuffix(v, "@") && !m.picker.active {
		m.openPicker()
	} else if m.picker.active {
		m.updatePickerQuery()
	}

	if m.input.LineCount() != prevLines {
		m.handleResize(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	}
	return m, cmd
}

func (m *Model) handleClipboardPaste() {
	m.lastClipboardPaste = time.Now()
	if path, err := readClipboardImage(); err == nil {
		if att, addErr := addAttachment(path); addErr == nil {
			m.attachments = append(m.attachments, *att)
			m.input.SetValue(m.input.Value() + fmt.Sprintf("[image #%d]", len(m.attachments)))
			m.input.CursorEnd()
			return
		}
	}

	files, err := readClipboardFiles()
	if err != nil || len(files) == 0 {
		m.setStatus("no image/files in clipboard")
		return
	}
	for _, path := range files {
		att, addErr := addAttachment(path)
		if addErr != nil {
			continue
		}
		m.attachments = append(m.attachments, *att)
		kind := "file"
		if att.IsImage {
			kind = "image"
		}
		m.input.SetValue(m.input.Value() + fmt.Sprintf("[%s #%d]", kind, len(m.attachments)))
	}
	m.input.CursorEnd()
}

func (m *Model) handleLeaderKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "h":
		return m.cmdHelp()
	case "m":
		return m.cmdModels()
	case "t":
		return m.cmdThemes()
	case "n":
		return m.cmdNew()
	case "l":
		return m.cmdSessions()
	case "u":
		return m.cmdUndo()
	case "r":
		return m.cmdRedo()
	case "d":
		m.toolDetails = !m.toolDetails
		m.tx.invalidateAll(m.contentWidth())
		m.refresh()
		m.setStatus(fmt.Sprintf("tool details %s", onOff(m.toolDetails)))
		return m, nil
	case "c":
		return m.cmdCompact()
	case "esc":
		return m, nil
	}
	return m, nil
}

// scrollStep is the line count for one scroll increment.
func (m *Model) scrollStep() int {
	n := m.deps.Loaded.TUI.ScrollSpeed
	if n <= 0 {
		n = 3
	}
	return n
}

// scrollBy moves the viewport and updates the follow policy. All scrolling
// goes through here so "am I following the tail?" can never drift.
func (m *Model) scrollBy(lines int) {
	switch {
	case lines < 0:
		m.viewport.ScrollUp(-lines)
	case lines > 0:
		m.viewport.ScrollDown(lines)
	default:
		return
	}
	m.tx.userScrolled(&m.viewport)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (m *Model) cycleAgent() {
	if m.agentName == "build" {
		m.agentName = "plan"
	} else {
		m.agentName = "build"
	}
	m.applyAgentPermissions()
	m.setStatus("agent: " + m.agentName)
}

func (m *Model) applyAgentPermissions() {
	base := m.deps.Loaded.Config.Permission
	if m.agentName == "plan" {
		ask := "ask"
		p := *base
		p.Edit, p.Write = ask, ask
		if p.Bash == nil {
			p.Bash = map[string]string{}
		} else {
			cp := map[string]string{}
			for k, v := range p.Bash {
				cp[k] = v
			}
			p.Bash = cp
		}
		p.Bash["*"] = ask
		m.deps.Perms.SetPermission(&p)
		return
	}
	m.deps.Perms.SetPermission(base)
}

// ---------- input history ----------

func (m *Model) pushHistory(text string) {
	if text == "" {
		return
	}
	if n := len(m.inputHist); n > 0 && m.inputHist[n-1] == text {
		return
	}
	m.inputHist = append(m.inputHist, text)
	if len(m.inputHist) > 200 {
		m.inputHist = m.inputHist[len(m.inputHist)-200:]
	}
}

func (m *Model) historyUp() {
	if len(m.inputHist) == 0 {
		return
	}
	if m.histIdx == -1 {
		m.histDraft = m.input.Value()
		m.histIdx = len(m.inputHist) - 1
	} else if m.histIdx > 0 {
		m.histIdx--
	}
	m.input.SetValue(m.inputHist[m.histIdx])
	m.input.CursorEnd()
}

func (m *Model) historyDown() {
	if m.histIdx == -1 {
		return
	}
	if m.histIdx < len(m.inputHist)-1 {
		m.histIdx++
		m.input.SetValue(m.inputHist[m.histIdx])
	} else {
		m.histIdx = -1
		m.input.SetValue(m.histDraft)
	}
	m.input.CursorEnd()
}

// ---------- submit ----------

func (m *Model) submit(text string) (tea.Model, tea.Cmd) {
	// Slash commands remain available while an agent is running so control
	// commands such as /new and /stop can cancel or reset the active run.
	if len(text) > 0 && text[0] == '/' {
		return m.runSlash(text)
	}

	// Ordinary prompts stay blocked while the agent is running.
	if m.running {
		m.setStatus("still working — esc to interrupt")
		return m, nil
	}

	// An armed inline selection gets first refusal on the input.
	if mm, cmd, handled := m.handlePendingInput(text); handled {
		return mm, cmd
	}

	// Shell escape.
	if len(text) > 0 && text[0] == '!' {
		cmdline := strings.TrimSpace(text[1:])
		if cmdline == "" {
			return m, nil
		}
		return m.runShell(cmdline)
	}

	// A leading @subagent mention becomes a task delegation.
	if expanded, ok := m.expandAgentMentions(text); ok {
		m.appendMsg(ChatMsg{Kind: MsgUser, Text: text, Time: time.Now()})
		return m, m.startAgent(expanded)
	}

	// Expand @file references into the prompt.
	prompt, attached := m.expandFileRefs(text)

	// Parse attachment markers like [image #1] or [file #2] from the prompt.
	// These are inserted when the user pastes images/files from clipboard.
	indices, cleaned := parseAttachmentMarkers(prompt)

	// Build the user message with attachments
	userMsg := provider.Message{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock(cleaned)}}
	for _, idx := range indices {
		if idx > 0 && idx <= len(m.attachments) {
			att := m.attachments[idx-1]
			if att.IsImage && att.Base64 != "" {
				userMsg.Content = append(userMsg.Content, provider.ImageBlock(att.MediaType, att.Base64))
			}
		}
	}

	m.appendMsg(ChatMsg{Kind: MsgUser, Text: text, Time: time.Now()})
	if len(attached) > 0 {
		m.setStatus(fmt.Sprintf("attached %d file(s)", len(attached)))
	}
	if len(indices) > 0 {
		m.setStatus(fmt.Sprintf("sending %d attachment(s)", len(indices)))
	}
	m.attachments = nil

	return m, m.startAgentWithMessage(userMsg)
}

// startAgentWithMessage kicks off a run with a pre-built user message.
func (m *Model) startAgentWithMessage(userMsg provider.Message) tea.Cmd {
	m.history = append(m.history, userMsg)
	return m.startAgent("")
}

func (m *Model) runShell(cmdline string) (tea.Model, tea.Cmd) {
	m.appendMsg(ChatMsg{Kind: MsgUser, Text: "!" + cmdline, Time: time.Now()})
	m.appendMsg(ChatMsg{
		Kind: MsgTool, ToolName: "bash", ToolTitle: cmdline,
		ToolRunning: true, Time: time.Now(),
	})
	return m, nil
}

// answerPermission delivers the user's decision to a waiting permission prompt.
func (m *Model) answerPermission(decision agent.PermissionDecision) {
	if m.permReply != nil {
		m.permReply <- decision
		m.permReply = nil
	}
}

// permissionView renders the inline permission prompt.
func (m *Model) permissionView() string {
	// This is a placeholder - the actual permission view is rendered inline.
	return ""
}

// shellDoneMsg is delivered when a shell command finishes.
type shellDoneMsg struct {
	idx    int
	output string
	err    error
}
