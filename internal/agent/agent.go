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
	"unicode/utf8"

	"rick/internal/config"
	"rick/internal/goal"
	"rick/internal/permission"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/tools"
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
	CallID  string
	Name    string
	Title   string
	Input   json.RawMessage
	Output  string
	Meta    map[string]any
	IsError bool
	Elapsed time.Duration
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
	Temperature  *float64
	Reasoning    provider.ReasoningEffort
	Tools        tools.ToolSet
	ToolFilter   func(string) bool
	Perms        *permission.Engine
	Ask          PermissionAsker
	Cwd          string
	SandboxRoot  string
	SessionID    string
	AgentName    string
	Depth        int
	MaxTurns     int
	Snapshotter  Snapshotter
	Parallel     bool // allow concurrent read-only tools
	Plugins      *plugin.Registry
	Goals        *goal.Store
	Creds        *config.Credentials // for key rotation on rate-limit
	Registry     *Registry           // optional live hierarchy registry
	AgentID      string              // registry ID for this run
}

// Runner executes the loop.
type Runner struct {
	cfg Config
}

// New builds a Runner.
func New(cfg Config) *Runner {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 50
	}
	if cfg.SandboxRoot == "" && cfg.Perms != nil {
		cfg.SandboxRoot = cfg.Perms.SandboxRoot()
	}
	return &Runner{cfg: cfg}
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

	schemas := r.cfg.Tools.Schemas(r.cfg.ToolFilter)

	for turn := 0; turn < r.cfg.MaxTurns; turn++ {
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

		req := provider.Request{
			Model:        r.cfg.Model,
			System:       r.cfg.System,
			SystemStable: r.cfg.SystemStable,
			Messages:     msgs,
			Tools:        schemas,
			MaxTokens:    r.cfg.MaxTokens,
			Temperature:  r.cfg.Temperature,
			Reasoning:    r.cfg.Reasoning,
		}

		ch := make(chan provider.Event, 256)
		go r.cfg.Provider.Stream(ctx, req, ch)

		var (
			textBuf    strings.Builder
			thinkBuf   strings.Builder
			calls      []provider.ToolCall
			streamErr  error
			stopReason string
			streamDone bool
		)

		for ev := range ch {
			switch ev.Kind {
			case provider.EventText:
				textBuf.WriteString(ev.Text)
				if !emit(Event{Kind: EvText, Text: ev.Text}) {
					go drain(ch)
					return appended, ctx.Err()
				}
			case provider.EventThinking:
				thinkBuf.WriteString(ev.Text)
				if !emit(Event{Kind: EvThinking, Text: ev.Text}) {
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
							total := ev.Usage.InputTokens + ev.Usage.OutputTokens +
								ev.Usage.CacheReadTokens + ev.Usage.CacheWriteTokens
							_ = r.cfg.Goals.AddTokens(g.ID, total)
							if g2, err := r.cfg.Goals.Load(g.ID); err == nil {
								if ok, _ := goal.CheckBudget(g2); !ok {
									budgetErr := fmt.Errorf("goal token budget exhausted")
									emit(Event{Kind: EvError, Err: budgetErr})
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
			case provider.EventError:
				streamErr = ev.Err
			}
		}

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
		assistant := provider.Message{Role: provider.RoleAssistant}
		for i := range calls {
			if calls[i].ID == "" {
				calls[i].ID = fmt.Sprintf("rick-tool-%d-%d", time.Now().UnixNano(), i)
			}
		}
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

	lastErr = fmt.Errorf("agent: stopped after %d turns without a final answer", r.cfg.MaxTurns)
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

	return provider.ToolResultBlock(call.ID, capModelToolOutput(ev.Output), ev.IsError), ev
}

const maxModelToolResultBytes = 32 << 10

func capModelToolOutput(output string) string {
	if len(output) <= maxModelToolResultBytes {
		return output
	}
	suffix := fmt.Sprintf("\n… <tool output truncated; %d bytes omitted; narrow the next request to continue>", len(output)-maxModelToolResultBytes)
	limit := maxModelToolResultBytes - len(suffix)
	if limit < 1 {
		return suffix[1:]
	}
	for limit > 0 && !utf8.RuneStart(output[limit]) {
		limit--
	}
	return output[:limit] + fmt.Sprintf("\n… <tool output truncated; %d bytes omitted; narrow the next request to continue>", len(output)-limit)
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
