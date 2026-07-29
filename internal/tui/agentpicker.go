package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/agent"
)

func (m *Model) applyAgentManage(id string) (tea.Model, tea.Cmd) {
	entry, ok := m.deps.AgentRegistry.Get(id)
	if !ok {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "agent no longer exists: " + id, Time: time.Now()})
		return m, nil
	}
	m.armChoice(fmt.Sprintf("agent %s · %s", entry.Name, string(entry.Status)), pendingAgentAction, id, []choiceOption{
		{value: "view", label: "view details", detail: "status, output, children"},
		{value: "chat", label: "chat", detail: "send a message before its next turn"},
		{value: "steer", label: "steer", detail: "inject a live instruction"},
		{value: "kill", label: "kill", detail: "cancel this agent and descendants"},
		{value: "attach", label: "attach", detail: "show the latest live result"},
	})
	return m, nil
}

func (m *Model) applyAgentAction(action, id string) (tea.Model, tea.Cmd) {
	entry, ok := m.deps.AgentRegistry.Get(id)
	if !ok {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "agent no longer exists: " + id, Time: time.Now()})
		return m, nil
	}
	switch action {
	case "view", "attach":
		return m.showAgentDetail(entry, action == "attach")
	case "chat":
		m.armInput("message to "+entry.ID+":", pendingAgentChat, id)
	case "steer":
		m.armInput("instruction for "+entry.ID+":", pendingAgentSteer, id)
	case "kill":
		if m.deps.AgentRegistry.Kill(id) {
			m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "killed agent: " + id, Time: time.Now()})
		} else {
			m.appendMsg(ChatMsg{Kind: MsgError, Text: "could not kill agent: " + id, Time: time.Now()})
		}
	}
	return m, nil
}

func (m *Model) showAgentDetail(entry agent.AgentSnapshot, attached bool) (tea.Model, tea.Cmd) {
	title := fmt.Sprintf("agent %s (%s)", entry.ID, entry.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", title)
	fmt.Fprintf(&b, "status: %s · depth: %d · parent: %s\n", entry.Status, entry.Depth, orDash(entry.ParentID))
	if !entry.Started.IsZero() {
		fmt.Fprintf(&b, "started: %s\n", humanAge(entry.Started))
	}
	if entry.Description != "" {
		fmt.Fprintf(&b, "task: %s\n", entry.Description)
	}
	if len(entry.Children) > 0 {
		fmt.Fprintf(&b, "children: %s\n", strings.Join(entry.Children, ", "))
	}
	if entry.Output != "" {
		fmt.Fprintf(&b, "\noutput:\n%s\n", entry.Output)
	}
	if entry.Err != nil {
		fmt.Fprintf(&b, "error: %s\n", entry.Err)
	}
	if attached {
		b.WriteString("\nattached view: future live events remain available through /agents\n")
	}
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: b.String(), Time: time.Now()})
	return m, nil
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func (m *Model) cmdJobs() (tea.Model, tea.Cmd) {
	total, active := m.jobs.Count()
	if total == 0 {
		m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "no tracked background jobs", Time: time.Now()})
		return m, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "jobs: %d total · %d active\n", total, active)
	for _, job := range m.jobs.Recent(20) {
		job.mu.RLock()
		fmt.Fprintf(&b, "%s %s [%s] %s\n", job.ID, job.Kind, job.Status, job.Label)
		job.mu.RUnlock()
	}
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: b.String(), Time: time.Now()})
	return m, nil
}
