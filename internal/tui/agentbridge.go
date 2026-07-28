package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/agent"
	"rick/internal/config"
	"rick/internal/glob"
	"rick/internal/permission"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/session"
)

// startAgent kicks off a run and returns the drain command.
//
// The goroutine NEVER touches *Model — it only writes to m.agentCh, which the
// Update loop drains via readAgentMsg ticks.
func (m *Model) startAgent(prompt string) tea.Cmd {
	prov, modelID, err := m.resolveProvider()
	if err != nil {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: err.Error(), Time: time.Now()})
		return nil
	}

	if prompt != "" {
		m.history = append(m.history, provider.UserText(prompt))
	}

	ch := make(chan agent.Event, 128)
	ctx, cancel := context.WithCancel(context.Background())
	m.agentCh = ch
	m.agentCancel = cancel
	m.running = true
	m.turnStart = time.Now()
	m.streamBuf.Reset()
	m.thinkBuf.Reset()

	cfg := m.deps.Loaded.Config
	cfg.Instructions = append([]string(nil), cfg.Instructions...)
	agentName := m.agentName
	reasoning := m.reasoning
	cwd := m.deps.Cwd
	projectRoot := m.deps.Loaded.ProjectRoot
	registry := m.deps.Registry
	perms := m.deps.Perms
	plugins := m.deps.Plugins
	ask := m.makeAsker()
	toolFilter := m.toolFilter()
	sessionID := m.sessionID()

	var snapshotter agent.Snapshotter
	if m.deps.Snapshots.Enabled() {
		snapshotter = m.deps.Snapshots
	}

	history := append([]provider.Message(nil), m.history...)
	skills := m.deps.Skills
	// Extract the last user message text for skill matching.
	var userText string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == provider.RoleUser {
			for _, b := range history[i].Content {
				if b.Type == "text" {
					userText = b.Text
					break
				}
			}
			break
		}
	}
	go func() {
		runner := agent.New(agent.Config{
			Provider:    prov,
			Model:       modelID,
			System:      buildSystemPrompt(agentName, modelID, cwd, projectRoot, cfg, skills, userText),
			MaxTokens:   cfg.MaxTokens,
			Reasoning:   reasoning,
			Tools:       registry,
			ToolFilter:  toolFilter,
			Perms:       perms,
			Ask:         ask,
			Cwd:         cwd,
			SessionID:   sessionID,
			AgentName:   agentName,
			Snapshotter: snapshotter,
			Plugins:     plugins,
			Parallel:    true,
			Goals:       m.deps.Goals,
		})
		appended, _ := runner.Run(ctx, history, ch)
		// Results are delivered through the channel; the appended slice is
		// recovered from the events we already saw, so nothing is written to
		// the model from here.
		_ = appended
	}()

	// Track appended messages by reconstructing them in the Update loop.
	m.pendingTools = map[string]int{}
	return tea.Batch(m.drainCmd(), m.spinnerCmd())
}

func (m *Model) drainCmd() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return readAgentMsg{} })
}

func (m *Model) sessionID() string {
	if m.sess != nil {
		return m.sess.ID
	}
	return ""
}

// drainAgent pulls whatever is available from the agent channel.
func (m *Model) drainAgent() (tea.Model, tea.Cmd) {
	if m.agentCh == nil {
		return m, nil
	}
	for i := 0; i < 64; i++ {
		select {
		case ev, ok := <-m.agentCh:
			if !ok {
				return m, m.finishRun(nil)
			}
			if cmd, stop := m.applyAgentEvent(ev); stop {
				return m, cmd
			}
		default:
			m.refresh()
			return m, m.drainCmd()
		}
	}
	m.refresh()
	return m, m.drainCmd()
}

func (m *Model) applyAgentEvent(ev agent.Event) (tea.Cmd, bool) {
	switch ev.Kind {
	case agent.EvText:
		m.streamBuf.WriteString(ev.Text)

	case agent.EvThinking:
		m.thinkBuf.WriteString(ev.Text)

	case agent.EvToolStart:
		m.flushStream()
		if ev.Tool != nil {
			idx := len(m.msgs)
			m.pendingTools[ev.Tool.CallID] = idx
			m.msgs = append(m.msgs, toolMsgFromEvent(ev.Tool, true))
			m.tx.noteAppend()
		}

	case agent.EvToolEnd:
		if ev.Tool != nil {
			if idx, ok := m.pendingTools[ev.Tool.CallID]; ok && idx < len(m.msgs) {
				m.msgs[idx] = toolMsgFromEvent(ev.Tool, false)
				m.touch(idx)
				delete(m.pendingTools, ev.Tool.CallID)
			} else {
				m.msgs = append(m.msgs, toolMsgFromEvent(ev.Tool, false))
				m.tx.noteAppend()
			}
			if d, ok := diffMsgFromMeta(ev.Tool.Meta); ok {
				m.msgs = append(m.msgs, d)
				m.tx.noteAppend()
			}
		}

	case agent.EvUsage:
		if ev.Usage != nil {
			// Cumulative counters are for billing; the context gauge needs
			// occupancy. Every request resends the whole conversation, so
			// the newest call's input already includes all prior turns —
			// adding them up double-counts the history on every round trip.
			//
			// Anthropic reports:
			//   InputTokens = cache miss (new tokens billed at full price)
			//   CacheReadTokens = cache hit (discounted, previously cached)
			//   CacheCreationTokens = cache write (newly written to cache)
			// The context occupancy is the total tokens sent: miss + hit.
			m.usage.Input = ev.Usage.InputTokens
			m.usage.Output = ev.Usage.OutputTokens
			m.usage.CacheRead = ev.Usage.CacheReadTokens
			m.usage.CacheWrite = ev.Usage.CacheWriteTokens
			m.billed.Input += ev.Usage.InputTokens
			m.billed.Output += ev.Usage.OutputTokens
			m.billed.CacheRead += ev.Usage.CacheReadTokens
			m.billed.CacheWrite += ev.Usage.CacheWriteTokens
			if m.deps.Usage != nil {
				_ = m.deps.Usage.Record(m.modelID,
					ev.Usage.InputTokens, ev.Usage.OutputTokens,
					ev.Usage.CacheReadTokens, ev.Usage.CacheWriteTokens)
			}
			m.maybeAutoCompact()
		}

	case agent.EvTurnEnd:
		m.flushStream()

	case agent.EvError:
		m.flushStream()
		if ev.Err != nil {
			m.msgs = append(m.msgs, ChatMsg{Kind: MsgError, Text: ev.Err.Error(), Time: time.Now()})
		}
		return m.finishRun(ev.Err), true

	case agent.EvDone:
		m.flushStream()
		return m.finishRun(nil), true
	}
	return nil, false
}

// flushStream converts the live buffers into permanent chat entries.
func (m *Model) flushStream() {
	if m.thinkBuf.Len() > 0 {
		m.msgs = append(m.msgs, ChatMsg{Kind: MsgThinking, Text: m.thinkBuf.String(), Time: time.Now()})
		m.tx.noteAppend()
		m.history = append(m.history, provider.Message{
			Role:    provider.RoleAssistant,
			Content: []provider.ContentBlock{{Type: "thinking", Text: m.thinkBuf.String()}},
		})
		m.thinkBuf.Reset()
	}
	if m.streamBuf.Len() > 0 {
		text := m.streamBuf.String()
		m.msgs = append(m.msgs, ChatMsg{Kind: MsgAssistant, Text: text, Time: time.Now()})
		m.tx.noteAppend()
		m.streamBuf.Reset()
	}
}

func (m *Model) finishRun(err error) tea.Cmd {
	m.flushStream()
	m.running = false
	if !m.turnStart.IsZero() {
		m.turnElapsed = time.Since(m.turnStart)
	}
	if m.agentCancel != nil {
		m.agentCancel()
		m.agentCancel = nil
	}
	m.agentCh = nil

	// Rebuild the canonical history from what actually happened so the next
	// turn replays tool calls and results correctly.
	m.rebuildHistory()
	m.saveSession()
	m.refresh()

	if err != nil {
		m.setStatus("error: " + truncate(err.Error(), 60))
	}
	return nil
}

// rebuildHistory reconstructs provider messages from the rendered transcript.
// The transcript is the source of truth the user sees, so replaying from it
// keeps the model's view and the user's view identical.
func (m *Model) rebuildHistory() {
	var out []provider.Message
	var pendingAssistant *provider.Message
	var pendingResults []provider.ContentBlock

	flushAssistant := func() {
		if pendingAssistant != nil && len(pendingAssistant.Content) > 0 {
			out = append(out, *pendingAssistant)
			pendingAssistant = nil
		}
	}
	flushResults := func() {
		if len(pendingResults) > 0 {
			out = append(out, provider.Message{Role: provider.RoleUser, Content: pendingResults})
			pendingResults = nil
		}
	}

	for _, msg := range m.msgs {
		switch msg.Kind {
		case MsgUser:
			flushAssistant()
			flushResults()
			out = append(out, provider.UserText(msg.Text))
		case MsgAssistant:
			if len(pendingResults) > 0 {
				flushAssistant()
				flushResults()
			}
			if pendingAssistant == nil {
				pendingAssistant = &provider.Message{Role: provider.RoleAssistant}
			}
			pendingAssistant.Content = append(pendingAssistant.Content, provider.TextBlock(msg.Text))
		case MsgTool:
			if msg.ToolRunning {
				continue
			}
			if pendingAssistant == nil {
				pendingAssistant = &provider.Message{Role: provider.RoleAssistant}
			}
			input := msg.ToolInput
			if len(input) == 0 {
				input = []byte("{}")
			}
			pendingAssistant.Content = append(pendingAssistant.Content, provider.ContentBlock{
				Type: "tool_use", ID: msg.CallID, Name: msg.ToolName, Input: input,
			})
			pendingResults = append(pendingResults,
				provider.ToolResultBlock(msg.CallID, msg.ToolOutput, msg.ToolErr))
		}
	}
	flushAssistant()
	flushResults()
	m.history = out
}

func (m *Model) interrupt() {
	if m.agentCancel != nil {
		m.agentCancel()
	}
	if m.permReply != nil {
		m.answerPermission(agent.DecideReject)
	}
	m.running = false
	if !m.turnStart.IsZero() {
		m.turnElapsed = time.Since(m.turnStart)
	}
	m.setStatus("interrupted")
	m.flushStream()
	m.refresh()
}

// makeAsker builds a permission callback that hands the request to the UI and
// blocks the agent goroutine until the user answers.
func (m *Model) makeAsker() agent.PermissionAsker {
	program := m.program
	gate := m.permGate
	return func(ctx context.Context, req permission.Request) agent.PermissionDecision {
		if program == nil || gate == nil {
			return agent.DecideReject
		}
		select {
		case <-gate:
			defer func() { gate <- struct{}{} }()
		case <-ctx.Done():
			return agent.DecideReject
		}
		reply := make(chan agent.PermissionDecision, 1)
		program.Send(permAskMsg{req: req, reply: reply})
		select {
		case d := <-reply:
			return d
		case <-ctx.Done():
			return agent.DecideReject
		}
	}
}

// resolveProvider picks the provider implementation for the active model.
// Model ids from OpenAI-style endpoints may contain slashes (e.g.
// "nous/tencent/hy3:free"), so we try each '/' position and pick the first
// that matches a known provider.
func (m *Model) resolveProvider() (provider.Provider, string, error) {
	provID, modelID := config.SplitModel(m.modelID)
	if p, ok := m.deps.Providers[provID]; ok {
		return p, modelID, nil
	}
	// The direct split didn't match — try later slash positions so a model
	// like "nous/tencent/hy3:free" resolves to provider "nous" with model
	// "tencent/hy3:free" even if "tencent" isn't a configured provider.
	idx := strings.Index(m.modelID, "/")
	for idx >= 0 && idx < len(m.modelID)-1 {
		if p, ok := m.deps.Providers[m.modelID[:idx]]; ok {
			return p, m.modelID[idx+1:], nil
		}
		next := strings.Index(m.modelID[idx+1:], "/")
		if next < 0 {
			break
		}
		idx += 1 + next
	}
	avail := make([]string, 0, len(m.deps.Providers))
	for k := range m.deps.Providers {
		avail = append(avail, k)
	}
	if len(avail) == 0 {
		return nil, "", fmt.Errorf("no providers configured — set ANTHROPIC_API_KEY or add one to rick.json")
	}
	return nil, "", fmt.Errorf("unknown provider %q (have: %s)", provID, strings.Join(avail, ", "))
}

func buildSystemPrompt(agentName, modelID, cwd, projectRoot string, cfg config.Config, skills []plugin.Skill, userText string) string {
	base := agent.BuildPrompt
	if agentName == "plan" {
		base = agent.PlanPrompt
	}
	if a, ok := cfg.Agents[agentName]; ok && a.Prompt != "" {
		base = a.Prompt
	}
	gitInfo := session.GitInfo(cwd)
	prompt := base +
		agent.Environment(cwd, modelID, agentName, gitInfo) +
		agent.ProjectContext(projectRoot, cfg.Instructions)
	if len(skills) > 0 && userText != "" {
		prompt += plugin.SkillBlock(plugin.MatchSkills(skills, userText))
	}
	return prompt
}

// toolFilter honours config tool enable/disable globs, plan-mode limits,
// and the user's interactive /tools toggles.
func (m *Model) toolFilter() func(string) bool {
	cfg := m.deps.Loaded.Config
	agentCfg, hasAgent := cfg.Agents[m.agentName]
	disabled := make(map[string]bool, len(m.disabledTools))
	for name, value := range m.disabledTools {
		disabled[name] = value
	}
	return func(name string) bool {
		if disabled[name] {
			return false
		}
		if hasAgent && agentCfg.Tools != nil {
			if v, ok := glob.Lookup(agentCfg.Tools, name); ok {
				return v
			}
		}
		if cfg.Tools != nil {
			if v, ok := glob.Lookup(cfg.Tools, name); ok {
				return v
			}
		}
		return true
	}
}
