package swarm

import (
	"context"
	"sync"
	"time"
)

// AgentStatus tracks where an agent is in its lifecycle.
type AgentStatus string

const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusDone    AgentStatus = "done"
	StatusFailed  AgentStatus = "failed"
)

// OutputEntry is one item in an agent's output history.
type OutputEntry struct {
	Time    time.Time
	Kind    string
	Content string
}

// AgentOutput tracks what an agent has done for live viewing.
type AgentOutput struct {
	mu      sync.RWMutex
	entries []OutputEntry
}

// Agent is one participant in a swarm.
type Agent struct {
	Name      string
	Role      string
	Inbox     chan Message
	Status    AgentStatus
	mu        sync.RWMutex
	Messages  []Message
	CreatedAt time.Time
	DoneAt    time.Time
	Output    *AgentOutput
}

// NewAgent creates an agent with the given name and role.
func NewAgent(name, role string) *Agent {
	return &Agent{
		Name:      name,
		Role:      role,
		Inbox:     make(chan Message, 64),
		Status:    StatusIdle,
		Messages:  []Message{},
		CreatedAt: time.Now(),
		Output:    &AgentOutput{},
	}
}

// AddOutput adds an entry to the agent's output.
func (a *Agent) AddOutput(kind, content string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	a.Output.Add(kind, content)
}

// GetOutput returns a copy of the agent's output entries.
func (a *Agent) GetOutput() []OutputEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Output.List()
}

// Add adds an entry to the output.
func (o *AgentOutput) Add(kind, content string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries = append(o.entries, OutputEntry{
		Time:    time.Now(),
		Kind:    kind,
		Content: content,
	})
}

// List returns all entries.
func (o *AgentOutput) List() []OutputEntry {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]OutputEntry, len(o.entries))
	copy(out, o.entries)
	return out
}

// SetStatus updates the agent's status thread-safely.
func (a *Agent) SetStatus(s AgentStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Status = s
	if s == StatusDone || s == StatusFailed {
		a.DoneAt = time.Now()
	}
}

// GetStatus returns the current status thread-safely.
func (a *Agent) GetStatus() AgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Status
}

// AddMessage records a message in the agent's history.
func (a *Agent) AddMessage(m Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Messages = append(a.Messages, m)
}

// GetMessages returns a copy of the agent's message history.
func (a *Agent) GetMessages() []Message {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Message, len(a.Messages))
	copy(out, a.Messages)
	return out
}

// ReadMessages consumes and returns the currently unread inbox messages.
// GetMessages remains the durable history used by reports and inspection.
func (a *Agent) ReadMessages() []Message {
	messages := make([]Message, 0, len(a.Inbox))
	for {
		select {
		case message := <-a.Inbox:
			messages = append(messages, message)
		default:
			return messages
		}
	}
}

// IsDone returns true if the agent is in a terminal state.
func (a *Agent) IsDone() bool {
	s := a.GetStatus()
	return s == StatusDone || s == StatusFailed
}

// Runner is the interface for executing an agent's work loop.
type Runner interface {
	Run(ctx context.Context, onEvent func(any)) (string, error)
}
