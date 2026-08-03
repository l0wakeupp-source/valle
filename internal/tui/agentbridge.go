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
	m.agentRunID++
	runID := m.agentRunID
	m.agentCh = ch
	m.agentCancel = cancel
	m.running = true
	m.turnStart = time.Now()
	m.streamBuf.Reset()
	m.thinkBuf.Reset()
	if m.deps.AgentRegistry != nil {
		id, registerErr := m.deps.AgentRegistry.Register(&agent.AgentEntry{
			Name: m.agentName, Depth: 0, Status: agent.AgentIdle,
			Description: prompt, Cancel: cancel,
		})
		if registerErr != nil {
			cancel()
			m.running = false
			m.appendMsg(ChatMsg{Kind: MsgError, Text: "agent registry: " + registerErr.Error(), Time: time.Now()})
			return nil
		}
		m.agentID = id
	}
	agentID := m.agentID
	m.resizeForActivity()
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
	stableSystem, systemPrompt := buildSystemPromptParts(agentName, modelID, cwd, projectRoot, cfg, skills, userText)
	go func() {
		runner := agent.New(agent.Config{
			Provider:     prov,
			Model:        modelID,
			System:       systemPrompt,
			SystemStable: stableSystem,
			MaxTokens:    cfg.MaxTokens,
			Reasoning:    reasoning,
			Tools:        registry,
			ToolFilter:   toolFilter,
			Perms:        perms,
			Ask:          ask,
			Cwd:          cwd,
			SessionID:    sessionID,
			AgentName:    agentName,
			AgentID:      agentID,
			Registry:     m.deps.AgentRegistry,
			Snapshotter:  snapshotter,
			Plugins:      plugins,
			Parallel:     true,
			Goals:        m.deps.Goals,
		})
		appended, _ := runner.Run(ctx, history, ch)
		// Results are delivered through the channel; the appended slice is
		// recovered from the events we already saw, so nothing is written to
		// the model from here.
		_ = appended
	}()

	// Track appended messages by reconstructing them in the Update loop.
	m.pendingTools = map[string]int{}
	return tea.Batch(m.drainCmd(runID), m.spinnerCmd())
}

func (m *Model) drainCmd(runID uint64) tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return readAgentMsg{runID: runID} })
}

func (m *Model) sessionID() string {
	if m.sess != nil {
		return m.sess.ID
	}
	return ""
}

// drainAgent pulls whatever is available from the agent channel.
func (m *Model) drainAgent(runID uint64) (tea.Model, tea.Cmd) {
	if runID != m.agentRunID {
		return m, nil
	}
	if m.agentCh == nil {
		return m, nil
	}
	processed := false
	for i := 0; i < 64; i++ {
		select {
		case ev, ok := <-m.agentCh:
			processed = true
			if !ok {
				if m.agentCh != nil {
					return m, m.finishRun(fmt.Errorf("agent event stream ended unexpectedly"))
				}
				return m, nil
			}
			if cmd, stop := m.applyAgentEvent(ev); stop {
				return m, cmd
			}
		default:
			if processed {
				m.refresh()
			}
			return m, m.drainCmd(runID)
		}
	}
	if processed {
		m.refresh()
	}
	return m, m.drainCmd(runID)
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
			if ev.Tool.CallID != "" {
				if _, pending := m.pendingTools[ev.Tool.CallID]; pending {
					return nil, false
				}
				if _, completed := m.toolOutputs[ev.Tool.CallID]; completed {
					return nil, false
				}
			}
			idx := len(m.msgs)
			m.pendingTools[ev.Tool.CallID] = idx
			m.msgs = append(m.msgs, toolMsgFromEvent(ev.Tool, true))
			m.tx.noteAppend()
		}

	case agent.EvToolEnd:
		if ev.Tool != nil {
			if ev.Tool.CallID != "" {
				if _, completed := m.toolOutputs[ev.Tool.CallID]; completed {
					return nil, false
				}
			}
			if m.toolOutputs == nil {
				m.toolOutputs = make(map[string]string)
			}
			m.toolOutputs[ev.Tool.CallID] = ev.Tool.Output
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
			// CacheCreationTokens = cache write (newly written to cache)
			// Context occupancy is the total request footprint: miss + hit + write.
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

	case agent.EvAgentBackground, agent.EvAgentReattached, agent.EvAgentMessage:
		if strings.TrimSpace(ev.Text) != "" {
			m.msgs = append(m.msgs, ChatMsg{Kind: MsgSystem, Text: ev.Text, Time: time.Now()})
			m.tx.noteAppend()
		}

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

// recordChildUsage updates persistent accounting from a child runner. Child
// runners execute outside Bubble Tea's update loop, so the tracker is updated
// at the event source rather than relying on a UI message arriving.
func (m *Model) recordChildUsage(modelID string, usage provider.Usage) {
	if m.deps.Usage != nil {
		_ = m.deps.Usage.Record(modelID, usage.InputTokens, usage.OutputTokens,
			usage.CacheReadTokens, usage.CacheWriteTokens)
	}
	if p := m.program; p != nil {
		p.Send(childUsageMsg{usage: usage})
	}
}

func (m *Model) recordUsageOnly(modelID string, usage provider.Usage) {
	if m.deps.Usage != nil {
		_ = m.deps.Usage.Record(modelID, usage.InputTokens, usage.OutputTokens,
			usage.CacheReadTokens, usage.CacheWriteTokens)
	}
}
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
	m.resizeForActivity()

	// Rebuild the canonical history from what actually happened so the next
	// turn replays tool calls and results correctly.
	m.rebuildHistory()
	m.saveSession()
	m.refresh()

	if err != nil {
		m.setStatus("error: " + truncate(err.Error(), 60))
	}
	if m.autoCompactPending {
		m.autoCompactPending = false
		_, compactCmd := m.cmdCompact()
		if compactCmd != nil {
			m.lastAutoCompact = time.Now()
		}
		return compactCmd
	}
	return nil
}

// rebuildHistory reconstructs bounded provider messages from the rendered
// transcript. Large tool results remain available to local session export and
// in-memory replay, but are not replayed in full on every provider turn.
func (m *Model) rebuildHistory() {
	m.history = capHistory(m.buildHistory(historyToolOutputChars))
}

func (m *Model) buildHistory(toolOutputLimit int) []provider.Message {
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
				if pendingAssistant == nil {
					pendingAssistant = &provider.Message{Role: provider.RoleAssistant}
				}
				callID := msg.CallID
				if callID == "" {
					callID = fmt.Sprintf("interrupted-tool-%d", len(pendingResults))
				}
				input := msg.ToolInput
				if len(input) == 0 {
					input = []byte("{}")
				}
				pendingAssistant.Content = append(pendingAssistant.Content, provider.ContentBlock{
					Type: "tool_use", ID: callID, Name: msg.ToolName, Input: input,
				})
				pendingResults = append(pendingResults, provider.ToolResultBlock(callID, "interrupted", true))
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
				provider.ToolResultBlock(msg.CallID, compactToolOutput(m.fullToolOutput(msg), toolOutputLimit), msg.ToolErr))
		}
	}
	flushAssistant()
	flushResults()
	return out
}

const (
	maxHistoryMessages = 500
	maxHistoryBytes    = 2 << 20
)

func capHistory(history []provider.Message) []provider.Message {
	if len(history) <= maxHistoryMessages && historyByteSize(history) <= maxHistoryBytes {
		return history
	}

	removed := 0
	remainingBytes := historyByteSize(history)
	for removed < len(history)-1 && (len(history)-removed+1 > maxHistoryMessages || remainingBytes > maxHistoryBytes) {
		remainingBytes -= messageByteSize(history[removed])
		removed++
	}
	if removed == 0 {
		return history
	}

	summaryText := fmt.Sprintf("Earlier conversation compacted: %d messages omitted.", removed)
	summary := provider.Message{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "text", Text: summaryText}}}
	return append([]provider.Message{summary}, history[removed:]...)
}

func historyByteSize(history []provider.Message) int {
	total := 0
	for _, message := range history {
		total += messageByteSize(message)
	}
	return total
}

func messageByteSize(message provider.Message) int {
	total := len(message.Role) + 16
	for _, block := range message.Content {
		total += len(block.Type) + len(block.Text) + len(block.Signature)
		total += len(block.ID) + len(block.Name) + len(block.Input)
		total += len(block.ToolUseID) + len(block.Content)
		total += len(block.Source) + len(block.MediaType) + len(block.Data) + 32
	}
	return total
}

func (m *Model) interrupt() {
	m.cancelCompaction()
	if m.permReply != nil {
		m.answerPermission(agent.DecideReject)
	}
	if m.agentCh != nil {
		// Invalidate already-scheduled drain ticks before cancelling the runner.
		// The runner may still close its old channel after a new run starts.
		agentID := m.agentID
		m.agentRunID++
		_ = m.finishRun(context.Canceled)
		if m.deps.AgentRegistry != nil && agentID != "" {
			m.deps.AgentRegistry.Update(agentID, agent.AgentKilled, "", context.Canceled)
		}
		m.setStatus("interrupted")
		return
	}
	if m.agentCancel != nil {
		m.agentCancel()
		m.agentCancel = nil
	}
	m.running = false
	m.setStatus("interrupted")
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
	_, prompt := buildSystemPromptParts(agentName, modelID, cwd, projectRoot, cfg, skills, userText)
	return prompt
}

func buildSystemPromptParts(agentName, modelID, cwd, projectRoot string, cfg config.Config, skills []plugin.Skill, userText string) (string, string) {
	base := agent.BuildPrompt
	if agentName == "plan" {
		base = agent.PlanPrompt
	}
	if a, ok := cfg.Agents[agentName]; ok && a.Prompt != "" {
		base = a.Prompt
	}

	stable := base + agent.ProjectContext(projectRoot, cfg.Instructions)
	volatile := ""
	if len(skills) > 0 && userText != "" {
		volatile += plugin.SkillBlock(plugin.MatchSkills(skills, userText))
	}
	volatile += agent.Environment(cwd, modelID, agentName, session.GitInfo(cwd))
	return stable, stable + volatile
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
