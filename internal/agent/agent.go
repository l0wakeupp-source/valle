// Package agent implements the tool-calling loop: prompt -> model -> tool
// calls -> results -> model, until the model returns plain text.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"rick/internal/budget"
	"rick/internal/compress"
	"rick/internal/config"
	"rick/internal/distill"
	"rick/internal/goal"
	"rick/internal/history"
	"rick/internal/permission"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/tokens"
	"rick/internal/tools"
	"rick/pkg/contextbudget"
	"rick/pkg/repomap"
)

// EventKind enumerates agent-level events surfaced to the UI.
type EventKind int

// Agent event kinds.
const (
	EvText            EventKind = iota // assistant text delta
	EvThinking                         // reasoning delta
	EvToolStart                        // tool execution began
	EvToolEnd                          // tool execution finished
	EvPermissionAsk                    // waiting on the user
	EvTurnEnd                          // one model turn completed
	EvUsage                            // token accounting
	EvDone                             // whole run finished
	EvError                            // fatal error
	EvAgentBackground                  // background agent started
	EvAgentReattached                  // result surfaced to a parent
	EvAgentMessage                     // live chat or steering message injected
)

// ToolEvent describes a tool execution.
type ToolEvent struct {
	CallID       string
	Name         string
	Title        string
	Input        json.RawMessage
	Output       string
	Meta         map[string]any
	IsError      bool
	Elapsed      time.Duration
	Optimization *OptimizationStats
}

// OptimizationStats describes the provider-facing reduction for one tool
// result. Original tool output remains in ToolEvent.Output unchanged.
type OptimizationStats struct {
	Stage            string
	Fallback         bool
	OriginalBytes    int
	CompressedBytes  int
	OriginalTokens   int
	CompressedTokens int
	SavedTokens      int
	Truncated        bool
}

// Event is one item on the agent's output stream.
type Event struct {
	Kind  EventKind
	Text  string
	Tool  *ToolEvent
	Usage *provider.Usage
	Err   error
}

// PermissionDecision is the user's answer to an approval prompt.
type PermissionDecision int

// Decisions.
const (
	DecideReject PermissionDecision = iota
	DecideAccept
	DecideAlways
)

// PermissionAsker prompts the user. Implementations must be safe to call from
// a goroutine and should block until the user answers or ctx is cancelled.
type PermissionAsker func(ctx context.Context, req permission.Request) PermissionDecision

// Snapshotter captures file state before a mutating turn (undo support).
type Snapshotter interface {
	Snapshot(label string) (string, error)
}

// Config wires an agent run.
type Config struct {
	Provider     provider.Provider
	Model        string
	System       string
	SystemStable string
	MaxTokens    int
	// ContextWindow overrides provider/model discovery when positive. A zero
	// value uses provider metadata and then the conservative budget fallback.
	ContextWindow      int
	SafetyMarginTokens int
	TokenEncoding      tokens.Encoding
	Temperature        *float64
	Reasoning          provider.ReasoningEffort
	// Budget is the shared session context manager (content-addressed dedup,
	// cache boundaries, reversible live-zone compression). When nil the runner
	// creates a private one that still deduplicates and picks boundaries but
	// never replaces tool output without a reversible store.
	Budget *contextbudget.Budget
	// RepoMapRoot enables the RepoMap structural skeleton in the system
	// prompt when non-empty.
	RepoMapRoot string
	// RepoMapBlock is a precomputed RepoMap block. When non-empty it is used
	// verbatim on every turn, so a long-lived session keeps a byte-identical
	// system prompt and the provider prompt cache is never disturbed. When
	// empty, the runner builds its own map once per run.
	RepoMapBlock string
	// RepoMapMaxTokens bounds the RepoMap block; zero means the package
	// default (1024).
	RepoMapMaxTokens int
	// EnableDistillation turns on state distillation when the transcript
	// approaches the context budget. Requires a provider (for the default
	// summarizer) or an injected DistillSummarizer.
	EnableDistillation bool
	// DistillModel is the fast model used for the background summary call.
	// Empty falls back to the primary model.
	DistillModel string
	// DistillSummarizer overrides the background summarizer; tests inject a
	// stub here.
	DistillSummarizer distill.Summarizer
	// DistillOptions tunes the distillation policy. A zero value applies the
	// package defaults; the Summarizer field is filled from
	// DistillSummarizer or the primary provider when left nil.
	DistillOptions distill.Options
	Tools          tools.ToolSet
	ToolFilter     func(string) bool
	Perms          *permission.Engine
	Ask            PermissionAsker
	Cwd            string
	SandboxRoot    string
	SessionID      string
	AgentName      string
	Depth          int
	MaxTurns       int // cap on agent turns; <= 0 means unlimited
	Snapshotter    Snapshotter
	Parallel       bool // allow concurrent read-only tools
	Plugins        *plugin.Registry
	Goals          *goal.Store
	Creds          *config.Credentials // for key rotation on rate-limit
	Registry       *Registry           // optional live hierarchy registry
	AgentID        string              // registry ID for this run
	// CacheRetention is the prompt-cache policy for every request of this
	// run: "" = provider default, "long" = extended TTL, "none" = disabled.
	CacheRetention provider.CacheRetention
	// PinnedToolSchemas fixes the provider-facing tool list for the whole
	// run, so mid-session tool toggles or plugin churn never change the
	// cached prefix bytes. When nil the registry + ToolFilter are used.
	PinnedToolSchemas []provider.ToolSchema
}

// Runner executes the loop.
type Runner struct {
	cfg Config

	// budget is the shared session context manager, or a private fallback.
	budget *contextbudget.Budget

	// repoMapOnce/repoMapBlock compute the RepoMap once per run using the
	// active chat prompt; the block is byte-identical across turns so it does
	// not disturb the provider prompt cache.
	repoMapOnce  sync.Once
	repoMapBlock string
}

// New builds a Runner.
func New(cfg Config) *Runner {
	if cfg.SandboxRoot == "" && cfg.Perms != nil {
		cfg.SandboxRoot = cfg.Perms.SandboxRoot()
	}
	budget := cfg.Budget
	if budget == nil {
		budget = contextbudget.New(contextbudget.Options{})
	}
	return &Runner{cfg: cfg, budget: budget}
}

// Cfg exposes the runner configuration (read-only use).
func (r *Runner) Cfg() Config { return r.cfg }

// Run drives the loop until the model stops requesting tools.
//
// history is the conversation so far; it is NOT mutated. The appended messages
// produced during the run are returned so the caller can persist them.
// The runner owns out and closes it exactly once.
func (r *Runner) Run(ctx context.Context, history []provider.Message, out chan<- Event) (appended []provider.Message, runErr error) {
	defer close(out)
	if r.cfg.Registry != nil && r.cfg.AgentID != "" {
		r.cfg.Registry.Update(r.cfg.AgentID, AgentRunning, "", nil)
		defer func() {
			status := AgentDone
			if runErr != nil {
				status = AgentFailed
				if ctx.Err() != nil {
					status = AgentKilled
				}
			}
			output := ""
			if runErr == nil {
				for i := len(appended) - 1; i >= 0; i-- {
					for _, block := range appended[i].Content {
						if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
							output = block.Text
							break
						}
					}
					if output != "" {
						break
					}
				}
			}
			r.cfg.Registry.Update(r.cfg.AgentID, status, output, runErr)
		}()
	}

	emit := func(ev Event) bool {
		if r.cfg.Registry != nil && r.cfg.AgentID != "" {
			r.cfg.Registry.Publish(r.cfg.AgentID, ev)
		}
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	msgs := append([]provider.Message(nil), history...)
	var lastErr error
	lastCallBatch := ""
	repeatedCallCount := 0

	schemas := r.cfg.PinnedToolSchemas
	if len(schemas) == 0 {
		schemas = r.cfg.Tools.Schemas(r.cfg.ToolFilter)
	}

	// MaxTurns caps the loop; <= 0 disables the cap so long tasks can run to
	// completion. Pathological loops are still caught by the repeated-call
	// guard below and by the goal token budget.
	for turn := 0; r.cfg.MaxTurns <= 0 || turn < r.cfg.MaxTurns; turn++ {
		if ctx.Err() != nil {
			return appended, ctx.Err()
		}
		r.injectControlMessages(&msgs, &appended, emit)

		// Lifecycle hook: turn start.
		if r.cfg.Plugins != nil && r.cfg.Plugins.Len() > 0 {
			pluginErrs := r.cfg.Plugins.DispatchTurnStart(ctx, &plugin.TurnStartEvent{
				SessionID: r.cfg.SessionID, Agent: r.cfg.AgentName, TurnNumber: turn,
			})
			for _, pluginErr := range pluginErrs {
				emit(Event{Kind: EvAgentMessage, Text: "plugin hook error: " + pluginErr.Error()})
			}
		}

		req := r.buildRequest(msgs, schemas)

		ch := make(chan provider.Event, 256)
		streamCtx, cancelStream := context.WithCancel(ctx)
		go r.cfg.Provider.Stream(streamCtx, req, ch)

		var (
			textBuf    strings.Builder
			thinkBuf   strings.Builder
			calls      []provider.ToolCall
			streamErr  error
			stopReason string
			streamDone bool
		)

	streamEvents:
		for ev := range ch {
			switch ev.Kind {
			case provider.EventText:
				textBuf.WriteString(ev.Text)
				if !emit(Event{Kind: EvText, Text: ev.Text}) {
					cancelStream()
					go drain(ch)
					return appended, ctx.Err()
				}
			case provider.EventThinking:
				thinkBuf.WriteString(ev.Text)
				if !emit(Event{Kind: EvThinking, Text: ev.Text}) {
					cancelStream()
					go drain(ch)
					return appended, ctx.Err()
				}
			case provider.EventToolCall:
				if ev.ToolCall != nil {
					calls = append(calls, *ev.ToolCall)
				}
			case provider.EventUsage:
				if ev.Usage != nil {
					emit(Event{Kind: EvUsage, Usage: ev.Usage})
					// Enforce the active goal's token budget, if any.
					if r.cfg.Goals != nil {
						if g, _ := r.cfg.Goals.GetActive(); g != nil && g.Status == "active" {
							// Providers report InputTokens as uncached input
							// and cache reads/writes disjointly, so summing
							// all four counts every token once.
							total := ev.Usage.InputTokens + ev.Usage.OutputTokens +
								ev.Usage.CacheReadTokens + ev.Usage.CacheWriteTokens
							_ = r.cfg.Goals.AddTokens(g.ID, total)
							if g2, err := r.cfg.Goals.Load(g.ID); err == nil {
								if ok, _ := goal.CheckBudget(g2); !ok {
									budgetErr := fmt.Errorf("goal token budget exhausted")
									emit(Event{Kind: EvError, Err: budgetErr})
									cancelStream()
									drain(ch)
									return appended, budgetErr
								}
							}
						}
					}
				}
			case provider.EventDone:
				streamDone = true
				stopReason = ev.StopReason
				break streamEvents
			case provider.EventError:
				if ev.Err == nil {
					ev.Err = fmt.Errorf("provider returned an unspecified error")
				}
				streamErr = ev.Err
				break streamEvents
			}
		}
		cancelStream()

		if streamErr != nil {
			// Check for rate-limit / quota errors and rotate keys if configured.
			if r.cfg.Provider != nil && r.cfg.Creds != nil && isRateLimitError(streamErr) {
				provID, _ := config.SplitModel(r.cfg.Model)
				key, rotateErr := rotateKeyForProviderWithCreds(r.cfg.Creds, provID)
				if rotateErr != nil {
					rotationErr := fmt.Errorf("rate-limit key rotation failed: %w", rotateErr)
					emit(Event{Kind: EvError, Err: rotationErr})
					return appended, rotationErr
				}
				if key != "" {
					keySetter, ok := r.cfg.Provider.(interface{ SetAPIKey(string) })
					if !ok {
						rotationErr := fmt.Errorf("provider %q cannot accept rotated API keys", r.cfg.Provider.Name())
						emit(Event{Kind: EvError, Err: rotationErr})
						return appended, rotationErr
					}
					keySetter.SetAPIKey(key)
					emit(Event{Kind: EvAgentMessage, Text: "rate limited; retrying with next key"})
					continue
				}
			}
			emit(Event{Kind: EvError, Err: streamErr})
			return appended, streamErr
		}
		if !streamDone {
			streamErr = fmt.Errorf("agent: provider stream ended without a completion event")
			emit(Event{Kind: EvError, Err: streamErr})
			return appended, streamErr
		}
		if strings.TrimSpace(textBuf.String()) == "" && strings.TrimSpace(thinkBuf.String()) == "" && len(calls) == 0 {
			streamErr = fmt.Errorf("agent: provider returned an empty completion")
			emit(Event{Kind: EvError, Err: streamErr})
			return appended, streamErr
		}
		seenCallIDs := make(map[string]struct{}, len(calls))
		for index := range calls {
			call := &calls[index]
			if strings.TrimSpace(call.Name) == "" {
				streamErr = fmt.Errorf("agent: malformed tool call: missing function name")
				emit(Event{Kind: EvError, Err: streamErr})
				return appended, streamErr
			}
			input := strings.TrimSpace(string(call.Input))
			if !json.Valid(call.Input) {
				streamErr = fmt.Errorf("agent: malformed arguments for tool %q", call.Name)
				emit(Event{Kind: EvError, Err: streamErr})
				return appended, streamErr
			}
			if input == "" || input[0] != '{' {
				streamErr = fmt.Errorf("agent: arguments for tool %q must be a JSON object", call.Name)
				emit(Event{Kind: EvError, Err: streamErr})
				return appended, streamErr
			}
			if call.ID == "" {
				call.ID = fmt.Sprintf("rick-tool-%d-%d", time.Now().UnixNano(), index)
			}
			if _, duplicate := seenCallIDs[call.ID]; duplicate {
				streamErr = fmt.Errorf("agent: duplicate tool call ID %q", call.ID)
				emit(Event{Kind: EvError, Err: streamErr})
				return appended, streamErr
			}
			seenCallIDs[call.ID] = struct{}{}
		}
		assistant := provider.Message{Role: provider.RoleAssistant}
		if thinkBuf.Len() > 0 {
			assistant.Content = append(assistant.Content, provider.ContentBlock{
				Type: "thinking", Text: thinkBuf.String(),
			})
		}
		if strings.TrimSpace(textBuf.String()) != "" {
			assistant.Content = append(assistant.Content, provider.TextBlock(textBuf.String()))
		}
		for _, c := range calls {
			assistant.Content = append(assistant.Content, provider.ContentBlock{
				Type: "tool_use", ID: c.ID, Name: c.Name, Input: c.Input,
			})
		}
		if len(assistant.Content) > 0 {
			msgs = append(msgs, assistant)
			appended = append(appended, assistant)
		}

		emit(Event{Kind: EvTurnEnd, Text: stopReason})

		// Lifecycle hook: turn end.
		if r.cfg.Plugins != nil && r.cfg.Plugins.Len() > 0 {
			pluginErrs := r.cfg.Plugins.DispatchTurnEnd(ctx, &plugin.TurnEndEvent{
				SessionID: r.cfg.SessionID, Agent: r.cfg.AgentName,
				TurnNumber: turn, StopReason: stopReason,
			})
			for _, pluginErr := range pluginErrs {
				emit(Event{Kind: EvAgentMessage, Text: "plugin hook error: " + pluginErr.Error()})
			}
		}

		if len(calls) == 0 {
			emit(Event{Kind: EvDone})
			return appended, nil
		}

		batchKey := ""
		for _, c := range calls {
			batchKey += c.Name + "\x00" + canonicalToolInput(c.Input) + "\x01"
		}
		if batchKey == lastCallBatch {
			repeatedCallCount++
		} else {
			lastCallBatch = batchKey
			repeatedCallCount = 1
		}
		if repeatedCallCount > 2 {
			message := "agent: repeated tool call limit reached"
			if len(calls) == 1 {
				message = fmt.Sprintf("agent: repeated tool call limit reached for %s", calls[0].Name)
			}
			err := errors.New(message)
			emit(Event{Kind: EvError, Err: err})
			return appended, err
		}
		results := r.execTools(ctx, calls, emit)
		if ctx.Err() != nil {
			return appended, ctx.Err()
		}

		userMsg := provider.Message{Role: provider.RoleUser, Content: results}
		msgs = append(msgs, userMsg)
		appended = append(appended, userMsg)
	}

	lastErr = fmt.Errorf("agent: stopped after %d turns without a final answer (max-turns reached; set --max-turns 0 for unlimited)", r.cfg.MaxTurns)
	emit(Event{Kind: EvError, Err: lastErr})
	return appended, lastErr
}

func (r *Runner) injectControlMessages(msgs *[]provider.Message, appended *[]provider.Message, emit func(Event) bool) {
	if r.cfg.Registry == nil || r.cfg.AgentID == "" {
		return
	}
	input, ok := r.cfg.Registry.Input(r.cfg.AgentID)
	if !ok {
		return
	}
	for {
		select {
		case msg := <-input:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			prefix := "Message"
			if msg.Steering {
				prefix = "Steering instruction"
			}
			text := fmt.Sprintf("[%s from %s]\n%s", prefix, msg.From, msg.Content)
			block := provider.TextBlock(text)
			message := provider.Message{Role: provider.RoleUser, Content: []provider.ContentBlock{block}}
			*msgs = append(*msgs, message)
			*appended = append(*appended, message)
			emit(Event{Kind: EvAgentMessage, Text: text})
		default:
			return
		}
	}
}

func drain(ch <-chan provider.Event) {
	for range ch {
	}
}

// isRateLimitError reports whether err looks like a rate-limit or quota error.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "rate-limit") ||
		(strings.Contains(s, "limit") && strings.Contains(s, "exceeded")) ||
		strings.Contains(s, "too many requests")
}

// rotateKeyForProviderWithCreds rotates the key for the given provider using the credentials store.
// Returns the new key, or "" if rotation is not possible.
func rotateKeyForProviderWithCreds(creds *config.Credentials, provID string) (string, error) {
	if creds == nil {
		return "", nil
	}
	return creds.RotateKeyAndSave(provID)
}

// execTools runs a batch of tool calls, honouring permissions and executing
// read-only tools concurrently when enabled.
func (r *Runner) execTools(ctx context.Context, calls []provider.ToolCall, emit func(Event) bool) []provider.ContentBlock {
	results := make([]provider.ContentBlock, len(calls))
	events := make([]*ToolEvent, len(calls))
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("rick-tool-%d-%d", time.Now().UnixNano(), i)
		}
	}

	// Partition: read-only calls can run in parallel; the rest run in order.
	parallelIdx := []int{}
	serialIdx := []int{}
	for i, c := range calls {
		t, ok := r.cfg.Tools.Get(c.Name)
		if r.cfg.Parallel && ok && t.ReadOnly() {
			parallelIdx = append(parallelIdx, i)
		} else {
			serialIdx = append(serialIdx, i)
		}
	}
	if r.cfg.Snapshotter != nil {
		for _, i := range serialIdx {
			t, ok := r.cfg.Tools.Get(calls[i].Name)
			if ok && !t.ReadOnly() {
				_, _ = r.cfg.Snapshotter.Snapshot(calls[i].Name)
				break
			}
		}
	}

	emitStart := func(i int) {
		emit(Event{Kind: EvToolStart, Tool: &ToolEvent{
			CallID: calls[i].ID, Name: calls[i].Name, Title: calls[i].Name, Input: calls[i].Input,
		}})
	}
	emitEnd := func(i int) {
		if events[i] != nil {
			emit(Event{Kind: EvToolEnd, Tool: events[i]})
		}
	}

	run := func(i int) {
		res, ev := r.execOne(ctx, calls[i])
		results[i] = res
		events[i] = ev
	}

	if len(parallelIdx) > 1 {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for _, i := range parallelIdx {
			wg.Add(1)
			emitStart(i)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				run(i)
			}(i)
		}
		wg.Wait()
		for _, i := range parallelIdx {
			emitEnd(i)
		}
	} else {
		for _, i := range parallelIdx {
			emitStart(i)
			run(i)
			emitEnd(i)
		}
	}
	for _, i := range serialIdx {
		if ctx.Err() != nil {
			results[i] = provider.ToolResultBlock(calls[i].ID, "cancelled by user", true)
			continue
		}
		emitStart(i)
		run(i)
		emitEnd(i)
	}

	return results
}

func (r *Runner) buildRequest(messages []provider.Message, schemas []provider.ToolSchema) provider.Request {
	encoding := r.cfg.TokenEncoding
	if encoding == "" {
		encoding = tokens.EncodingForModel(r.cfg.Model)
	}

	contextWindow := r.cfg.ContextWindow
	if contextWindow <= 0 && r.cfg.Provider != nil {
		contextWindow = provider.KnownProviderContextWindow(r.cfg.Provider.Name(), r.cfg.Model)
	}
	reservedOutput := r.cfg.MaxTokens
	if reservedOutput <= 0 {
		reservedOutput = 4096
	}

	system := r.cfg.System
	if block := r.repoMap(lastUserText(messages)); block != "" {
		system += "\n\n" + block
	}
	if manifest := toolManifest(schemas); manifest != "" {
		system += "\n\n" + manifest
	}
	stableSystem := r.cfg.SystemStable
	volatileSystem := system
	if stableSystem != "" && strings.HasPrefix(volatileSystem, stableSystem) {
		volatileSystem = strings.TrimPrefix(volatileSystem, stableSystem)
	}
	plan := budget.Plan(budget.Input{
		ContextWindow:        contextWindow,
		StableSystemTokens:   countTokens(stableSystem, encoding),
		VolatileSystemTokens: countTokens(volatileSystem, encoding),
		ToolSchemaTokens:     countJSONValues(schemas, encoding),
		MessageTokens:        countMessages(messages, encoding),
		ReservedOutputTokens: reservedOutput,
		SafetyMarginTokens:   r.cfg.SafetyMarginTokens,
	})

	// Content-addressed dedup runs before trimming: the replacement set is
	// persistent across turns, so the surviving bytes stay stable even when
	// trimming later moves a payload's first occurrence out of the view.
	view := messages
	if r.budget.Enabled() {
		view = r.budget.ApplyDedup(view).View
	}
	retained := retainMessages(view, plan.RetainedMessageTokens, encoding)
	boundaries := r.budget.ChooseBoundaries(retained)

	// State distillation: when the transcript approaches the context budget,
	// collapse the oldest stable prefix into a structured summary placed just
	// after the cache breakpoint. Best-effort: every failure keeps the view.
	if r.shouldDistill(plan, contextWindow) {
		if result := distill.Distill(retained, boundaries, r.distillOptions()); result.Replaced {
			retained = result.Messages
			boundaries = r.budget.ChooseBoundaries(retained)
		}
	}

	return provider.Request{
		Model:           r.cfg.Model,
		System:          system,
		SystemStable:    r.cfg.SystemStable,
		Messages:        retained,
		Tools:           schemas,
		MaxTokens:       r.cfg.MaxTokens,
		Temperature:     r.cfg.Temperature,
		Reasoning:       r.cfg.Reasoning,
		CacheBoundaries: boundaries,
		CacheRetention:  r.cfg.CacheRetention,
		SessionID:       r.cfg.SessionID,
	}
}

// repoMap renders the repository skeleton once per run, keyed to the active
// chat prompt. The result is stable for the whole run so the provider cache
// sees an identical system suffix on every turn.
func (r *Runner) repoMap(prompt string) string {
	if r.cfg.RepoMapRoot == "" && r.cfg.RepoMapBlock == "" {
		return ""
	}
	// A precomputed block (built once per session by the caller) is used
	// verbatim so every turn sends a byte-identical system suffix and the
	// provider prompt cache stays warm.
	if r.cfg.RepoMapBlock != "" {
		return r.cfg.RepoMapBlock
	}
	r.repoMapOnce.Do(func() {
		block, err := repomap.Build(repomap.Options{
			Root:      r.cfg.RepoMapRoot,
			Prompt:    prompt,
			MaxTokens: r.cfg.RepoMapMaxTokens,
			Encoding:  tokens.EncodingForModel(r.cfg.Model),
		})
		if err == nil {
			r.repoMapBlock = block
		}
	})
	return r.repoMapBlock
}

// lastUserText returns the most recent plain-text user request, which is the
// "active chat prompt" used to weight the RepoMap.
func lastUserText(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != provider.RoleUser {
			continue
		}
		for _, block := range messages[i].Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				return block.Text
			}
		}
	}
	return ""
}

func countTokens(text string, encoding tokens.Encoding) int {
	return tokens.Count(text, encoding).Count
}

func countJSONValues(value any, encoding tokens.Encoding) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return countTokens(fmt.Sprint(value), encoding)
	}
	return countTokens(string(encoded), encoding) + 4
}

func countMessages(messages []provider.Message, encoding tokens.Encoding) int {
	total := 0
	for _, message := range messages {
		total += countJSONValues(message, encoding)
	}
	return total
}

func retainMessages(messages []provider.Message, maxTokens int, encoding tokens.Encoding) []provider.Message {
	retained, _ := history.Retain(messages, maxTokens, encoding)
	return retained
}

func containsBlock(message provider.Message, blockType string) bool {
	for _, block := range message.Content {
		if block.Type == blockType {
			return true
		}
	}
	return false
}

func (r *Runner) execOne(ctx context.Context, call provider.ToolCall) (provider.ContentBlock, *ToolEvent) {
	start := time.Now()
	ev := &ToolEvent{CallID: call.ID, Name: call.Name, Input: call.Input, Title: call.Name}

	t, ok := r.cfg.Tools.Get(call.Name)
	if !ok {
		ev.Output = fmt.Sprintf("unknown tool %q; available: %s", call.Name, strings.Join(r.cfg.Tools.Names(), ", "))
		ev.IsError = true
		return provider.ToolResultBlock(call.ID, ev.Output, true), ev
	}

	preq := describe(call, r.cfg.Cwd)
	level := permission.Level(permission.Allow)
	if r.cfg.Perms != nil {
		level = r.cfg.Perms.Check(preq)
	}
	switch level {
	case permission.Deny:
		ev.Output = fmt.Sprintf("permission denied by policy: %s", preq.Title)
		ev.IsError = true
		ev.Title = "denied: " + preq.Title
		return provider.ToolResultBlock(call.ID, ev.Output, true), ev
	case permission.Ask:
		if r.cfg.Ask == nil {
			ev.Output = "permission required but no approval channel is available"
			ev.IsError = true
			return provider.ToolResultBlock(call.ID, ev.Output, true), ev
		}
		switch r.cfg.Ask(ctx, preq) {
		case DecideReject:
			ev.Output = "the user rejected this action; stop and ask them how to proceed"
			ev.IsError = true
			ev.Title = "rejected: " + preq.Title
			return provider.ToolResultBlock(call.ID, ev.Output, true), ev
		case DecideAlways:
			if r.cfg.Perms != nil {
				r.cfg.Perms.GrantSession(permission.SessionKey(preq))
			}
		}
	}

	input := call.Input

	// plugin hook: tool.execute.before
	if r.cfg.Plugins != nil && r.cfg.Plugins.Len() > 0 {
		before := &plugin.ToolBeforeEvent{
			SessionID: r.cfg.SessionID, Agent: r.cfg.AgentName,
			Tool: call.Name, CallID: call.ID, Input: input,
		}
		if err := r.cfg.Plugins.DispatchToolBefore(ctx, before); err != nil {
			ev.Output = "plugin error: " + err.Error()
			ev.IsError = true
			return provider.ToolResultBlock(call.ID, ev.Output, true), ev
		}
		if before.Skip {
			reason := before.Reason
			if reason == "" {
				reason = "blocked by a plugin"
			}
			ev.Output = reason
			ev.IsError = true
			ev.Title = "blocked: " + preq.Title
			return provider.ToolResultBlock(call.ID, reason, true), ev
		}
		if len(before.Input) > 0 {
			input = before.Input
		}
	}
	if !bytes.Equal(input, call.Input) && r.cfg.Perms != nil {
		modifiedRequest := describe(provider.ToolCall{Name: call.Name, Input: input}, r.cfg.Cwd)
		switch r.cfg.Perms.Check(modifiedRequest) {
		case permission.Deny:
			ev.Output = fmt.Sprintf("permission denied by policy: %s", modifiedRequest.Title)
			ev.IsError = true
			return provider.ToolResultBlock(call.ID, ev.Output, true), ev
		case permission.Ask:
			if r.cfg.Ask == nil || r.cfg.Ask(ctx, modifiedRequest) == DecideReject {
				ev.Output = "the user rejected the plugin-modified action"
				ev.IsError = true
				return provider.ToolResultBlock(call.ID, ev.Output, true), ev
			}
		}
	}

	tc := tools.Context{
		Cwd:       r.cfg.Cwd,
		SessionID: r.cfg.SessionID,
		Agent:     r.cfg.AgentName,
		AgentID:   r.cfg.AgentID,
		CallID:    call.ID,
		Depth:     r.cfg.Depth,
	}
	res, err := t.Run(ctx, tc, input)
	ev.Elapsed = time.Since(start)
	if err != nil {
		ev.Output = err.Error()
		ev.IsError = true
		return provider.ToolResultBlock(call.ID, "tool error: "+err.Error(), true), ev
	}
	ev.Output = res.Output
	ev.Meta = res.Meta
	ev.IsError = res.IsError
	if res.Title != "" {
		ev.Title = res.Title
	}

	// plugin hook: tool.execute.after
	if r.cfg.Plugins != nil && r.cfg.Plugins.Len() > 0 {
		after := &plugin.ToolAfterEvent{
			SessionID: r.cfg.SessionID, Agent: r.cfg.AgentName,
			Tool: call.Name, CallID: call.ID, Input: input,
			Output: ev.Output, IsError: ev.IsError,
		}
		if err := r.cfg.Plugins.DispatchToolAfter(ctx, after); err == nil {
			ev.Output = after.Output
			ev.IsError = after.IsError
		}
	}

	modelOutput, stats := r.capToolOutput(call, ev.Output, ev.IsError)
	ev.Optimization = stats
	return provider.ToolResultBlock(call.ID, modelOutput, ev.IsError), ev
}

const maxModelToolResultBytes = 32 << 10

// capToolOutput applies the deterministic command-aware reducer and then the
// reversible live-zone pass. The pre-live-zone payload is stored under the
// call id so the model can retrieve it via retrieve_uncompressed_context.
func (r *Runner) capToolOutput(call provider.ToolCall, output string, isError bool) (string, *OptimizationStats) {
	modelOutput, stats := capToolOutputStatic(call, output, isError)
	if r.budget != nil && r.budget.Enabled() && !isError && len(modelOutput) > 0 {
		key := call.ID
		if key == "" {
			key = "tool:" + call.Name
		}
		live, changed := r.budget.CompressLive(key, modelOutput)
		if changed {
			stats = &OptimizationStats{
				Stage:            stats.Stage + "+live-zone",
				Fallback:         stats.Fallback,
				OriginalBytes:    stats.OriginalBytes,
				CompressedBytes:  len(live),
				OriginalTokens:   stats.OriginalTokens,
				CompressedTokens: countTokens(live, tokens.EncodingCl100kBase),
				SavedTokens:      maxInt(0, stats.OriginalTokens-stats.CompressedTokens),
				Truncated:        stats.Truncated,
			}
			modelOutput = live
		}
	}
	return modelOutput, stats
}

func capToolOutputStatic(call provider.ToolCall, output string, isError bool) (string, *OptimizationStats) {
	compressed := compress.ForTool(compress.Input{
		Text:     output,
		Command:  compressionCommand(call),
		MaxBytes: maxModelToolResultBytes,
		IsError:  isError,
	})
	modelOutput := compressed.Text
	encoding := tokens.EncodingCl100kBase
	originalTokens := countTokens(output, encoding)
	compressedTokens := countTokens(modelOutput, encoding)
	return modelOutput, &OptimizationStats{
		Stage:            compressed.Stage,
		Fallback:         compressed.Fallback,
		OriginalBytes:    compressed.OriginalBytes,
		CompressedBytes:  len(modelOutput),
		OriginalTokens:   originalTokens,
		CompressedTokens: compressedTokens,
		SavedTokens:      maxInt(0, originalTokens-compressedTokens),
		Truncated:        compressed.Truncated,
	}
}
func compressionCommand(call provider.ToolCall) string {
	if call.Name == "bash" {
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(call.Input, &input) == nil && input.Command != "" {
			return input.Command
		}
	}
	return call.Name
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func canonicalToolInput(input json.RawMessage) string {
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return string(input)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return string(input)
	}
	canonical, err := canonicalJSONValue(value)
	if err != nil {
		return string(input)
	}
	return canonical
}

func canonicalJSONValue(value any) (string, error) {
	switch value := value.(type) {
	case nil:
		return "null", nil
	case bool:
		return fmt.Sprintf("bool:%t", value), nil
	case string:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return "string:" + string(encoded), nil
	case json.Number:
		normalized, ok := normalizeJSONNumber(string(value))
		if !ok {
			return "", fmt.Errorf("invalid JSON number %q", value)
		}
		return "number:" + normalized, nil
	case []any:
		var builder strings.Builder
		builder.WriteString("array:[")
		for index, item := range value {
			if index > 0 {
				builder.WriteByte(',')
			}
			canonical, err := canonicalJSONValue(item)
			if err != nil {
				return "", err
			}
			builder.WriteString(canonical)
		}
		builder.WriteByte(']')
		return builder.String(), nil
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		var builder strings.Builder
		builder.WriteString("object:{")
		for index, key := range keys {
			if index > 0 {
				builder.WriteByte(',')
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return "", err
			}
			canonical, err := canonicalJSONValue(value[key])
			if err != nil {
				return "", err
			}
			builder.Write(encodedKey)
			builder.WriteByte(':')
			builder.WriteString(canonical)
		}
		builder.WriteByte('}')
		return builder.String(), nil
	default:
		return "", fmt.Errorf("unsupported JSON value %T", value)
	}
}

func normalizeJSONNumber(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	negative := raw[0] == '-'
	if negative {
		raw = raw[1:]
	}

	exponent := new(big.Int)
	if exponentIndex := strings.IndexAny(raw, "eE"); exponentIndex >= 0 {
		exponentText := raw[exponentIndex+1:]
		if _, ok := exponent.SetString(exponentText, 10); !ok {
			return "", false
		}
		raw = raw[:exponentIndex]
	}

	integerPart := raw
	fractionPart := ""
	if decimalIndex := strings.IndexByte(raw, '.'); decimalIndex >= 0 {
		integerPart = raw[:decimalIndex]
		fractionPart = raw[decimalIndex+1:]
	}
	digits := integerPart + fractionPart
	leadingZeros := 0
	for leadingZeros < len(digits) && digits[leadingZeros] == '0' {
		leadingZeros++
	}
	if leadingZeros == len(digits) {
		return "0", true
	}
	digits = digits[leadingZeros:]
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}

	decimalPosition := new(big.Int).SetInt64(int64(len(integerPart)))
	decimalPosition.Add(decimalPosition, exponent)
	decimalPosition.Sub(decimalPosition, big.NewInt(int64(leadingZeros)))
	decimalPosition.Sub(decimalPosition, big.NewInt(int64(len(digits))))
	if negative {
		return "-" + digits + "e" + decimalPosition.String(), true
	}
	return digits + "e" + decimalPosition.String(), true
}

// describe converts a tool call into a permission request with a readable
// title and preview body.
func describe(call provider.ToolCall, cwd string) permission.Request {
	req := permission.Request{Tool: call.Name, Title: call.Name}
	var m map[string]any
	_ = json.Unmarshal(call.Input, &m)

	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}

	switch call.Name {
	case "bash":
		req.Command = str("command")
		req.Title = str("description")
		if req.Title == "" {
			req.Title = req.Command
		}
		req.Body = req.Command
	case "write":
		req.Path = str("path")
		req.Title = "write " + req.Path
		body := str("content")
		if len(body) > 4000 {
			body = body[:4000] + "\n…"
		}
		req.Body = body
	case "edit":
		req.Path = str("path")
		req.Title = "edit " + req.Path
		req.Body = "- " + oneLine(str("old_string")) + "\n+ " + oneLine(str("new_string"))
	case "apply_patch":
		req.Title = "apply patch"
		body := str("patch")
		if paths, err := tools.PatchPaths(body); err == nil {
			req.Paths = paths
			if len(paths) > 0 {
				req.Path = paths[0]
			}
		}
		if len(body) > 4000 {
			body = body[:4000] + "\n…"
		}
		req.Body = body
	case "read", "grep", "glob", "list", "tree", "code_symbols":
		req.Path = str("path")
		req.Title = call.Name + " " + str("path") + str("pattern")
	case "webfetch", "fetch":
		raw := str("url")
		if raw == "" {
			raw = str("uri")
		}
		req.Host = hostOf(raw)
		req.Title = "fetch " + raw
		req.Body = raw
	case "websearch":
		req.Title = "web search: " + str("query")
		req.Body = str("query")
	default:
		req.Title = call.Name
		req.Body = string(call.Input)
	}
	return req
}

// hostOf extracts the hostname from a URL so host rules can match it.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
