package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/config"
	"rick/internal/provider"
)

// cmdReasoning shows or sets the reasoning effort for the active model.
//
// The level is a spectrum, not a switch: reasoning models bill thinking
// tokens and get slower as the budget grows, so "off / minimal / low /
// medium / high" is the useful control. Models that cannot reason say so
// rather than silently accepting a setting that does nothing.
func (m *Model) cmdReasoning(args string) (tea.Model, tea.Cmd) {
	_, modelID := config.SplitModel(m.modelID)

	if m.reasoningStyle == provider.ReasoningStyleNone {
		m.appendMsg(ChatMsg{Kind: MsgSystem,
			Text: shortModel(m.modelID) + " is not a reasoning model — nothing to set",
			Time: time.Now()})
		return m, nil
	}
	if m.reasoningStyle == provider.ReasoningStyleAlways {
		m.appendMsg(ChatMsg{Kind: MsgSystem,
			Text: shortModel(m.modelID) + " always reasons; the level cannot be changed",
			Time: time.Now()})
		return m, nil
	}

	// An argument sets the level directly.
	if strings.TrimSpace(args) != "" {
		lvl, ok := provider.ParseEffort(args)
		if !ok {
			m.appendMsg(ChatMsg{Kind: MsgError,
				Text: "unknown level " + strconvQuote(args) + " — try off, minimal, low, medium or high",
				Time: time.Now()})
			return m, nil
		}
		return m.applyReasoning(lvl)
	}

	// Otherwise offer the levels this model actually supports.
	var opts []choiceOption
	for _, lvl := range provider.ReasoningLevels() {
		if lvl == provider.ReasoningMinimal && m.reasoningStyle == provider.ReasoningStyleAnthropic {
			continue // Anthropic's floor is 1024 tokens; minimal is meaningless
		}
		opts = append(opts, choiceOption{
			value:  string(lvl),
			label:  string(lvl),
			detail: effortDetail(lvl, m.reasoningStyle, m.maxTokens()),
			active: lvl == m.reasoning,
		})
	}
	m.armChoice("reasoning effort · "+modelID, pendingReasoning, "", opts)
	return m, nil
}

func (m *Model) applyReasoning(lvl provider.ReasoningEffort) (tea.Model, tea.Cmd) {
	m.reasoning = lvl
	// Showing the reasoning stream only makes sense when there is one.
	m.showThinking = lvl != provider.ReasoningOff
	m.tx.invalidateAll(m.contentWidth())
	m.refresh()
	m.appendMsg(ChatMsg{Kind: MsgSystem,
		Text: "reasoning: " + string(lvl), Time: time.Now()})
	m.setStatus("reasoning: " + string(lvl))
	return m, nil
}

// effortDetail explains what a level costs in the model's own terms.
func effortDetail(lvl provider.ReasoningEffort, style provider.ReasoningStyle, maxTok int) string {
	if lvl == provider.ReasoningOff {
		return "no thinking, fastest"
	}
	if style == provider.ReasoningStyleAnthropic {
		if b := lvl.Budget(maxTok); b > 0 {
			return fmt.Sprintf("%s thinking budget", humanTokens(b))
		}
		return "unavailable at this max_tokens"
	}
	switch lvl {
	case provider.ReasoningMinimal:
		return "barely thinks, fastest"
	case provider.ReasoningLow:
		return "quick reasoning"
	case provider.ReasoningMedium:
		return "balanced (default)"
	case provider.ReasoningHigh:
		return "deepest, slowest"
	}
	return ""
}

// maxTokens is the response limit for the active model.
func (m *Model) maxTokens() int {
	if n := m.deps.Loaded.Config.MaxTokens; n > 0 {
		return n
	}
	return 8192
}

func strconvQuote(s string) string { return "\"" + strings.TrimSpace(s) + "\"" }

// reasoningSegment renders the level for the status line, or "".
func (m *Model) reasoningSegment() string {
	switch m.reasoningStyle {
	case provider.ReasoningStyleNone:
		return ""
	case provider.ReasoningStyleAlways:
		return "reasoning"
	}
	if m.reasoning == provider.ReasoningOff || m.reasoning == "" {
		return ""
	}
	return "reasoning:" + string(m.reasoning)
}
