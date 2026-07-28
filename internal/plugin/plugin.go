// Package plugin provides rick's hook system. v1 supports compiled-in Go
// plugins registered by the host; the dispatcher is designed so a script
// runtime can be added later without changing call sites.
package plugin

import (
    "context"
    "encoding/json"
    "fmt"
    "sort"
    "sync"
)

// ToolBeforeEvent is dispatched before a tool executes. Handlers may mutate
// Input, or set Skip/Reason to prevent execution.
type ToolBeforeEvent struct {
    SessionID string
    Agent     string
    Tool      string
    CallID    string
    Input     json.RawMessage

    // Set by a handler to block the call.
    Skip   bool
    Reason string
}

// ToolAfterEvent is dispatched after a tool executes. Handlers may rewrite
// Output.
type ToolAfterEvent struct {
    SessionID string
    Agent     string
    Tool      string
    CallID    string
    Input     json.RawMessage
    Output    string
    IsError   bool
}

// SessionEvent is dispatched on idle / error.
type SessionEvent struct {
    SessionID string
    Agent     string
    Err       error
}

// TurnStartEvent is dispatched at the beginning of each agent turn.
type TurnStartEvent struct {
    SessionID  string
    Agent      string
    TurnNumber int
}

// TurnEndEvent is dispatched at the end of each agent turn.
type TurnEndEvent struct {
    SessionID  string
    Agent      string
    TurnNumber int
    StopReason string
}

// SubagentStartEvent is dispatched when a subagent is spawned.
type SubagentStartEvent struct {
    SessionID    string
    Agent        string
    SubagentName string
    Task         string
}

// SubagentEndEvent is dispatched when a subagent finishes.
type SubagentEndEvent struct {
    SessionID    string
    Agent        string
    SubagentName string
    Result       string
}

// SessionStartEvent is dispatched when a session begins.
type SessionStartEvent struct {
    SessionID string
    Agent     string
}

// SessionEndEvent is dispatched when a session ends.
type SessionEndEvent struct {
    SessionID string
    Agent     string
}

// Hooks is the set of callbacks a plugin may implement. All fields optional.
type Hooks struct {
    Name string

    ToolExecuteBefore func(ctx context.Context, ev *ToolBeforeEvent) error
    ToolExecuteAfter  func(ctx context.Context, ev *ToolAfterEvent) error
    SessionIdle       func(ctx context.Context, ev *SessionEvent) error
    SessionError      func(ctx context.Context, ev *SessionEvent) error

    // Lifecycle hooks added in v2.
    TurnStart     func(ctx context.Context, ev *TurnStartEvent) error
    TurnEnd       func(ctx context.Context, ev *TurnEndEvent) error
    SubagentStart func(ctx context.Context, ev *SubagentStartEvent) error
    SubagentEnd   func(ctx context.Context, ev *SubagentEndEvent) error
    SessionStart  func(ctx context.Context, ev *SessionStartEvent) error
    SessionEnd    func(ctx context.Context, ev *SessionEndEvent) error
}

// Registry holds every loaded plugin.
type Registry struct {
    mu      sync.RWMutex
    plugins []Hooks
    enabled map[string]bool
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
    return &Registry{enabled: map[string]bool{}}
}

// Register adds a plugin. Plugins are enabled by default.
func (r *Registry) Register(h Hooks) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.plugins = append(r.plugins, h)
    if _, exists := r.enabled[h.Name]; !exists {
        r.enabled[h.Name] = true
    }
}

// Names lists loaded plugin names.
func (r *Registry) Names() []string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]string, 0, len(r.plugins))
    for _, p := range r.plugins {
        out = append(out, p.Name)
    }
    sort.Strings(out)
    return out
}

// Len returns the plugin count.
func (r *Registry) Len() int {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return len(r.plugins)
}

// SetEnabled enables or disables a plugin by name.
func (r *Registry) SetEnabled(name string, enabled bool) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.enabled[name] = enabled
}

// IsEnabled reports whether a plugin is enabled.
func (r *Registry) IsEnabled(name string) bool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    v, ok := r.enabled[name]
    return !ok || v // default to enabled
}

// Toggle flips a plugin's enabled state and returns the new state.
func (r *Registry) Toggle(name string) bool {
    r.mu.Lock()
    defer r.mu.Unlock()
    cur, ok := r.enabled[name]
    if !ok {
        cur = true
    }
    r.enabled[name] = !cur
    return !cur
}

// PluginInfo describes a loaded plugin for listing.
type PluginInfo struct {
    Name        string
    Description string
    Enabled     bool
    Source      string
}

// List returns info about every registered plugin.
func (r *Registry) List() []PluginInfo {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]PluginInfo, 0, len(r.plugins))
    for _, p := range r.plugins {
        enabled, ok := r.enabled[p.Name]
        if !ok {
            enabled = true
        }
        out = append(out, PluginInfo{
            Name:    p.Name,
            Enabled: enabled,
        })
    }
    return out
}

// Remove deletes a plugin by name. Returns true if found.
func (r *Registry) Remove(name string) bool {
    r.mu.Lock()
    defer r.mu.Unlock()
    for i, p := range r.plugins {
        if p.Name == name {
            r.plugins = append(r.plugins[:i], r.plugins[i+1:]...)
            delete(r.enabled, name)
            return true
        }
    }
    return false
}

// activePlugins returns a snapshot of enabled plugins.
func (r *Registry) activePlugins() []Hooks {
    r.mu.RLock()
    defer r.mu.RUnlock()
    var out []Hooks
    for _, p := range r.plugins {
        if enabled, ok := r.enabled[p.Name]; !ok || enabled {
            out = append(out, p)
        }
    }
    return out
}

// DispatchToolBefore runs every before-hook in registration order. The first
// handler to set Skip stops the chain.
func (r *Registry) DispatchToolBefore(ctx context.Context, ev *ToolBeforeEvent) error {
    for _, p := range r.activePlugins() {
        if p.ToolExecuteBefore == nil {
            continue
        }
        if err := p.ToolExecuteBefore(ctx, ev); err != nil {
            return err
        }
        if ev.Skip {
            return nil
        }
    }
    return nil
}

// DispatchToolAfter runs every after-hook.
func (r *Registry) DispatchToolAfter(ctx context.Context, ev *ToolAfterEvent) error {
    for _, p := range r.activePlugins() {
        if p.ToolExecuteAfter == nil {
            continue
        }
        if err := p.ToolExecuteAfter(ctx, ev); err != nil {
            return err
        }
    }
    return nil
}

// DispatchSessionIdle runs every idle hook.
func (r *Registry) DispatchSessionIdle(ctx context.Context, ev *SessionEvent) {
    for _, p := range r.activePlugins() {
        if p.SessionIdle != nil {
            _ = p.SessionIdle(ctx, ev)
        }
    }
}

// DispatchSessionError runs every error hook.
func (r *Registry) DispatchSessionError(ctx context.Context, ev *SessionEvent) {
    for _, p := range r.activePlugins() {
        if p.SessionError != nil {
            _ = p.SessionError(ctx, ev)
        }
    }
}

// DispatchTurnStart runs every turn-start hook.
func (r *Registry) DispatchTurnStart(ctx context.Context, ev *TurnStartEvent) {
    for _, p := range r.activePlugins() {
        if p.TurnStart != nil {
            _ = p.TurnStart(ctx, ev)
        }
    }
}

// DispatchTurnEnd runs every turn-end hook.
func (r *Registry) DispatchTurnEnd(ctx context.Context, ev *TurnEndEvent) {
    for _, p := range r.activePlugins() {
        if p.TurnEnd != nil {
            _ = p.TurnEnd(ctx, ev)
        }
    }
}

// DispatchSubagentStart runs every subagent-start hook.
func (r *Registry) DispatchSubagentStart(ctx context.Context, ev *SubagentStartEvent) {
    for _, p := range r.activePlugins() {
        if p.SubagentStart != nil {
            _ = p.SubagentStart(ctx, ev)
        }
    }
}

// DispatchSubagentEnd runs every subagent-end hook.
func (r *Registry) DispatchSubagentEnd(ctx context.Context, ev *SubagentEndEvent) {
    for _, p := range r.activePlugins() {
        if p.SubagentEnd != nil {
            _ = p.SubagentEnd(ctx, ev)
        }
    }
}

// DispatchSessionStart runs every session-start hook.
func (r *Registry) DispatchSessionStart(ctx context.Context, ev *SessionStartEvent) {
    for _, p := range r.activePlugins() {
        if p.SessionStart != nil {
            _ = p.SessionStart(ctx, ev)
        }
    }
}

// DispatchSessionEnd runs every session-end hook.
func (r *Registry) DispatchSessionEnd(ctx context.Context, ev *SessionEndEvent) {
    for _, p := range r.activePlugins() {
        if p.SessionEnd != nil {
            _ = p.SessionEnd(ctx, ev)
        }
    }
}

// knownHookNames enumerates valid hook keys in a manifest.
var knownHookNames = map[string]bool{
    "tool_before":    true,
    "tool_after":     true,
    "session_start":  true,
    "session_end":    true,
    "session_idle":   true,
    "session_error":  true,
    "turn_start":     true,
    "turn_end":       true,
    "subagent_start": true,
    "subagent_end":   true,
}

// Manifest is the JSON shape of a plugin file (.rick-plugin or .json).
type Manifest struct {
    Name        string            `json:"name"`
    Version     string            `json:"version,omitempty"`
    Description string            `json:"description,omitempty"`
    Hooks       map[string]string `json:"hooks,omitempty"`
    Enabled     *bool             `json:"enabled,omitempty"`
    Source      string            `json:"-"` // file path or URL, not serialized
}

// Validate checks a manifest for structural correctness.
func (m *Manifest) Validate() error {
    if m.Name == "" {
        return fmt.Errorf("plugin manifest: name is required")
    }
    for hook := range m.Hooks {
        if !knownHookNames[hook] {
            return fmt.Errorf("plugin %q: unknown hook %q", m.Name, hook)
        }
    }
    return nil
}

// IsEnabled returns the manifest's enabled state (default true).
func (m *Manifest) IsEnabled() bool {
    return m.Enabled == nil || *m.Enabled
}
