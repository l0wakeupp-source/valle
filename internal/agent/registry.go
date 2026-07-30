package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxAllowedDepth is the hard safety ceiling for nested agent runs.
const MaxAllowedDepth = 10

// ValidateDepth validates the configured maximum nesting depth.
func ValidateDepth(depth int) error {
	if depth < 1 || depth > MaxAllowedDepth {
		return fmt.Errorf("subagent_depth must be 1..%d", MaxAllowedDepth)
	}
	return nil
}

// AgentStatus is the lifecycle state shown by the TUI and agent tools.
type AgentStatus string

const (
	AgentIdle    AgentStatus = "idle"
	AgentRunning AgentStatus = "running"
	AgentDone    AgentStatus = "done"
	AgentFailed  AgentStatus = "failed"
	AgentKilled  AgentStatus = "killed"
)

// AgentMessage is a user or agent instruction delivered to a live run.
type AgentMessage struct {
	From        string
	Content     string
	Steering    bool
	DeliveredAt time.Time
}

// AgentEntry describes one agent instance in the current session.
type AgentEntry struct {
	ID          string
	Name        string
	ParentID    string
	Depth       int
	Status      AgentStatus
	Started     time.Time
	Finished    time.Time
	EventCh     chan Event
	InputCh     chan AgentMessage
	Cancel      context.CancelFunc
	Description string
	Output      string
	Err         error
	Children    []string

	mu sync.RWMutex
}

// AgentSnapshot is a race-free, presentation-friendly copy of an entry.
type AgentSnapshot struct {
	ID          string
	Name        string
	ParentID    string
	Depth       int
	Status      AgentStatus
	Started     time.Time
	Finished    time.Time
	Description string
	Output      string
	Err         error
	Children    []string
}

func (e *AgentEntry) snapshot() AgentSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return AgentSnapshot{
		ID: e.ID, Name: e.Name, ParentID: e.ParentID, Depth: e.Depth,
		Status: e.Status, Started: e.Started, Finished: e.Finished,
		Description: e.Description, Output: e.Output, Err: e.Err,
		Children: append([]string(nil), e.Children...),
	}
}

// Registry tracks all agent instances for one interactive session.
type Registry struct {
	mu       sync.RWMutex
	entries  map[string]*AgentEntry
	sequence uint64
	maxDepth int
	maxBg    int
	bgSlots  chan struct{}
}

// NewRegistry creates a registry. Invalid limits are replaced with safe defaults.
func NewRegistry(maxDepth, maxBackground int) *Registry {
	if ValidateDepth(maxDepth) != nil {
		maxDepth = 1
	}
	if maxBackground <= 0 {
		maxBackground = 8
	}
	return &Registry{
		entries: make(map[string]*AgentEntry), maxDepth: maxDepth, maxBg: maxBackground,
		bgSlots: make(chan struct{}, maxBackground),
	}
}

// MaxDepth returns the configured nesting limit.
func (r *Registry) MaxDepth() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.maxDepth
}

// MaxBackground returns the configured concurrent background-agent limit.
func (r *Registry) MaxBackground() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.maxBg
}

// Register adds an entry and links it to its parent. The returned ID is stable
// for the lifetime of this registry.
func (r *Registry) Register(entry *AgentEntry) (string, error) {
	if entry == nil {
		return "", fmt.Errorf("agent entry is nil")
	}
	if entry.Depth < 0 || entry.Depth > r.MaxDepth() {
		return "", fmt.Errorf("agent depth %d exceeds configured limit %d", entry.Depth, r.MaxDepth())
	}
	if entry.Started.IsZero() {
		entry.Started = time.Now()
	}
	if entry.Status == "" {
		entry.Status = AgentIdle
	}
	if entry.EventCh == nil {
		entry.EventCh = make(chan Event, 128)
	}
	if entry.InputCh == nil {
		entry.InputCh = make(chan AgentMessage, 32)
	}
	if entry.ID == "" {
		entry.ID = r.newID(entry.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[entry.ID]; exists {
		return "", fmt.Errorf("agent %q is already registered", entry.ID)
	}
	if entry.ParentID != "" {
		parent, ok := r.entries[entry.ParentID]
		if !ok {
			return "", fmt.Errorf("parent agent %q is not registered", entry.ParentID)
		}
		if entry.Depth != parent.snapshot().Depth+1 {
			return "", fmt.Errorf("agent depth must be parent depth + 1")
		}
		parent.mu.Lock()
		parent.Children = append(parent.Children, entry.ID)
		parent.mu.Unlock()
	}
	r.entries[entry.ID] = entry
	return entry.ID, nil
}

func (r *Registry) newID(name string) string {
	r.mu.Lock()
	r.sequence++
	sequence := r.sequence
	r.mu.Unlock()
	prefix := strings.TrimSpace(name)
	if prefix == "" {
		prefix = "agent"
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, sequence)
	}
	return fmt.Sprintf("%s-%d-%s", prefix, sequence, hex.EncodeToString(random[:]))
}

// Get returns a race-free snapshot by ID.
func (r *Registry) Get(id string) (AgentSnapshot, bool) {
	r.mu.RLock()
	entry, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return AgentSnapshot{}, false
	}
	return entry.snapshot(), true
}

// Find resolves an exact ID first, then a unique name or description match.
func (r *Registry) Find(query string) (AgentSnapshot, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return AgentSnapshot{}, false
	}
	if entry, ok := r.Get(query); ok {
		return entry, true
	}
	var match AgentSnapshot
	count := 0
	for _, entry := range r.List() {
		if entry.Name == query || entry.Description == query {
			match = entry
			count++
		}
	}
	return match, count == 1
}

// List returns all agents in start order, with the orchestrator first when present.
func (r *Registry) List() []AgentSnapshot {
	r.mu.RLock()
	entries := make([]*AgentEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	r.mu.RUnlock()
	out := make([]AgentSnapshot, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.snapshot())
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].Started.Before(out[j].Started)
	})
	return out
}

// ListChildren returns direct children of parentID.
func (r *Registry) ListChildren(parentID string) []AgentSnapshot {
	var out []AgentSnapshot
	for _, entry := range r.List() {
		if entry.ParentID == parentID {
			out = append(out, entry)
		}
	}
	return out
}

// Update changes lifecycle state and final result fields.
func (r *Registry) Update(id string, status AgentStatus, output string, err error) bool {
	r.mu.RLock()
	entry, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	entry.mu.Lock()
	entry.Status = status
	if output != "" {
		entry.Output = output
	}
	if err != nil {
		entry.Err = err
	}
	if status != AgentRunning && status != AgentIdle {
		entry.Finished = time.Now()
	}
	entry.mu.Unlock()
	return true
}

// Kill cancels an agent and marks it killed. Descendants are killed too.
func (r *Registry) Kill(id string) bool {
	r.mu.RLock()
	entry, ok := r.entries[id]
	children := append([]string(nil), entryChildren(r, id)...)
	r.mu.RUnlock()
	if !ok {
		return false
	}
	if entry.Cancel != nil {
		entry.Cancel()
	}
	r.Update(id, AgentKilled, "", context.Canceled)
	for _, childID := range children {
		r.Kill(childID)
	}
	return true
}

func entryChildren(r *Registry, id string) []string {
	entry, ok := r.entries[id]
	if !ok {
		return nil
	}
	snap := entry.snapshot()
	return append([]string(nil), snap.Children...)
}

// Report stores a child result and notifies its parent, if any.
func (r *Registry) Report(id, summary, fullOutput string) error {
	entry, ok := r.entry(id)
	if !ok {
		return fmt.Errorf("unknown agent %q", id)
	}
	output := strings.TrimSpace(fullOutput)
	if output == "" {
		output = strings.TrimSpace(summary)
	}
	r.Update(id, AgentDone, output, nil)
	if _, ok := r.entry(entry.ParentID); ok {
		text := fmt.Sprintf("agent %s finished: %s", id, strings.TrimSpace(summary))
		if strings.TrimSpace(summary) == "" {
			text = fmt.Sprintf("agent %s finished", id)
		}
		r.Publish(entry.ParentID, Event{Kind: EvAgentReattached, Text: text})
	}
	return nil
}

// Send delivers a chat message to a live agent.
func (r *Registry) Send(target, from, content string) error {
	entry, ok := r.entry(target)
	if !ok {
		return fmt.Errorf("agent %q not found", target)
	}
	msg := AgentMessage{From: from, Content: content, DeliveredAt: time.Now()}
	select {
	case entry.InputCh <- msg:
		return nil
	default:
		return fmt.Errorf("agent %q input queue is full", target)
	}
}

// Steer delivers a live steering instruction to a target agent.
func (r *Registry) Steer(target, from, instruction string) error {
	entry, ok := r.entry(target)
	if !ok {
		return fmt.Errorf("agent %q not found", target)
	}
	msg := AgentMessage{From: from, Content: instruction, Steering: true, DeliveredAt: time.Now()}
	select {
	case entry.InputCh <- msg:
		return nil
	default:
		return fmt.Errorf("agent %q input queue is full", target)
	}
}

func (r *Registry) entry(id string) (*AgentEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[id]
	return entry, ok
}

// RunningBackgroundCount returns the number of non-root running agents.
func (r *Registry) RunningBackgroundCount() int {
	count := 0
	for _, entry := range r.List() {
		if entry.Depth > 0 && entry.Status == AgentRunning {
			count++
		}
	}
	return count
}

// AcquireBackground reserves one background slot until ReleaseBackground.
func (r *Registry) AcquireBackground() error {
	select {
	case r.bgSlots <- struct{}{}:
		return nil
	default:
		return fmt.Errorf("maximum background agents reached (%d)", r.MaxBackground())
	}
}

func (r *Registry) ReleaseBackground() {
	select {
	case <-r.bgSlots:
	default:
	}
}

// CanStartBackground enforces the configured background concurrency limit.
func (r *Registry) CanStartBackground() error {
	if r.RunningBackgroundCount() >= r.MaxBackground() {
		return fmt.Errorf("maximum background agents reached (%d)", r.MaxBackground())
	}
	return nil
}

// ContextFor creates a cancellable context and stores its cancel function.
func (r *Registry) ContextFor(parent context.Context, id string) (context.Context, error) {
	entry, ok := r.entry(id)
	if !ok {
		return nil, fmt.Errorf("agent %q not found", id)
	}
	ctx, cancel := context.WithCancel(parent)
	entry.mu.Lock()
	entry.Cancel = cancel
	entry.Status = AgentRunning
	entry.mu.Unlock()
	return ctx, nil
}

// Input returns a live agent's control channel for a runner.
func (r *Registry) Input(id string) (<-chan AgentMessage, bool) {
	entry, ok := r.entry(id)
	if !ok {
		return nil, false
	}
	return entry.InputCh, true
}

// Publish sends an event to the agent's observers without blocking the run.
func (r *Registry) Publish(id string, event Event) {
	entry, ok := r.entry(id)
	if !ok {
		return
	}
	select {
	case entry.EventCh <- event:
	default:
	}
}
