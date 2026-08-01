package swarm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Topology defines how agents can communicate.
type Topology string

const (
	TopologyMesh     Topology = "mesh"
	TopologyStar     Topology = "star"
	TopologyRing     Topology = "ring"
	TopologyPipeline Topology = "pipeline"
)

// EventType classifies swarm-level events surfaced to the primary agent.
type EventType string

const (
	EventAgentStart  EventType = "agent_start"
	EventAgentTool   EventType = "agent_tool"
	EventAgentDone   EventType = "agent_done"
	EventAgentFailed EventType = "agent_failed"
	EventBoardWrite  EventType = "board_write"
	EventTaskUpdate  EventType = "task_update"
	EventMessage     EventType = "message"
	EventComplete    EventType = "complete"
)

// Event is one item on the swarm's event stream.
type Event struct {
	Swarm  string         `json:"swarm"`
	Kind   EventType      `json:"kind"`
	Agent  string         `json:"agent"`
	Detail string         `json:"detail"`
	Count  int            `json:"count,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
	Time   time.Time      `json:"time"`
}

// Swarm is a group of named agents working toward a shared goal.
type Swarm struct {
	ID         string
	Name       string
	Goal       string
	Agents     map[string]*Agent
	AgentOrder []string
	Board      *Board
	Tasks      *TaskBoard
	Topology   Topology
	Primary    string
	Ctx        context.Context
	Cancel     context.CancelFunc
	mu         sync.RWMutex
	eventCh    chan Event
	started    time.Time
}

// NewSwarm creates a swarm with the given name, goal, and topology.
func NewSwarm(id, name, goal string, topo Topology) *Swarm {
	return NewSwarmContext(context.Background(), id, name, goal, topo)
}

// NewSwarmContext creates a swarm whose lifetime is bounded by its parent.
func NewSwarmContext(parent context.Context, id, name, goal string, topo Topology) *Swarm {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Swarm{
		ID:       id,
		Name:     name,
		Goal:     goal,
		Agents:   map[string]*Agent{},
		Board:    NewBoard(),
		Tasks:    NewTaskBoard(),
		Topology: topo,
		Ctx:      ctx,
		Cancel:   cancel,
		eventCh:  make(chan Event, 256),
		started:  time.Now(),
	}
}

// Events returns the event stream channel.
func (s *Swarm) Events() <-chan Event {
	return s.eventCh
}

// Emit sends an event to the swarm's event stream.
func (s *Swarm) Emit(ev Event) {
	ev.Swarm = s.ID
	ev.Time = time.Now()
	select {
	case s.eventCh <- ev:
	default:
	}
}

// AddAgent registers an agent with the swarm.
func (s *Swarm) AddAgent(name, role string) *Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.Agents[name]; ok {
		return existing
	}
	ag := NewAgent(name, role)
	s.Agents[name] = ag
	s.AgentOrder = append(s.AgentOrder, name)
	return ag
}

// GetAgent returns an agent by name.
func (s *Swarm) GetAgent(name string) (*Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ag, ok := s.Agents[name]
	if !ok {
		return nil, fmt.Errorf("swarm: agent %q not found", name)
	}
	return ag, nil
}

// Message routes a message between agents based on topology.
func (s *Swarm) Message(m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Agents[m.From]; !ok {
		return fmt.Errorf("swarm: sender agent %q not found", m.From)
	}
	m.Time = time.Now()

	if m.IsBroadcast() {
		switch s.Topology {
		case TopologyMesh:
		case TopologyStar:
			if m.From != s.Primary {
				return fmt.Errorf("star topology: only primary can broadcast")
			}
		case TopologyRing, TopologyPipeline:
			return fmt.Errorf("%s topology does not support broadcast", s.Topology)
		default:
			return fmt.Errorf("swarm: unsupported topology %q", s.Topology)
		}
		for name, agent := range s.Agents {
			if name != m.From && len(agent.Inbox) >= cap(agent.Inbox) {
				return fmt.Errorf("swarm: inbox full for agent %q", name)
			}
		}
		for name, agent := range s.Agents {
			if name == m.From {
				continue
			}
			agent.Inbox <- m
			agent.AddMessage(m)
		}
		s.Emit(Event{Kind: EventMessage, Agent: m.From, Detail: "broadcast: " + m.Content})
		return nil
	}

	switch s.Topology {
	case TopologyMesh:
	case TopologyStar:
		if m.From != s.Primary && m.To != s.Primary {
			return fmt.Errorf("star topology: only primary can relay messages")
		}
	case TopologyRing:
		allowed := s.nextInRing(m.From)
		if allowed != m.To {
			return fmt.Errorf("ring topology: %s can only message %s", m.From, allowed)
		}
	case TopologyPipeline:
		allowed := s.nextInPipeline(m.From)
		if allowed == "" || allowed != m.To {
			return fmt.Errorf("pipeline topology: %s can only message %s", m.From, allowed)
		}
	}

	target, ok := s.Agents[m.To]
	if !ok {
		return fmt.Errorf("swarm: target agent %q not found", m.To)
	}
	select {
	case target.Inbox <- m:
		target.AddMessage(m)
	default:
		return fmt.Errorf("swarm: inbox full for agent %q", m.To)
	}
	s.Emit(Event{Kind: EventMessage, Agent: m.From, Detail: m.To + ": " + m.Content})
	return nil
}

// BoardPut writes to the board and emits an event.
func (s *Swarm) BoardPut(key, value, author string) {
	s.Board.Put(key, value, author)
	s.Emit(Event{Kind: EventBoardWrite, Agent: author, Detail: key + "=" + value})
}

// nextInRing returns the next agent in declaration order.
func (s *Swarm) nextInRing(name string) string {
	for i, agentName := range s.AgentOrder {
		if agentName == name {
			if i+1 < len(s.AgentOrder) {
				return s.AgentOrder[i+1]
			}
			if len(s.AgentOrder) > 0 {
				return s.AgentOrder[0]
			}
			return ""
		}
	}
	return ""
}

func (s *Swarm) nextInPipeline(name string) string {
	for i, agentName := range s.AgentOrder {
		if agentName == name && i+1 < len(s.AgentOrder) {
			return s.AgentOrder[i+1]
		}
	}
	return ""
}

// Completion returns the number of agents in each status.
func (s *Swarm) Completion() map[AgentStatus]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := map[AgentStatus]int{}
	for _, ag := range s.Agents {
		counts[ag.GetStatus()]++
	}
	return counts
}

// IsDone returns true when all agents are either done or failed.
func (s *Swarm) IsDone() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ag := range s.Agents {
		st := ag.GetStatus()
		if st != StatusDone && st != StatusFailed {
			return false
		}
	}
	return true
}

// Terminate cancels the swarm's context, stopping all agents.
func (s *Swarm) Terminate() {
	s.Cancel()
	s.Emit(Event{Kind: EventComplete, Detail: "terminated"})
}

// Report generates a status report of the swarm for the primary agent.
func (s *Swarm) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Swarm: %s\n", s.Name)
	fmt.Fprintf(&b, "Goal: %s\n", s.Goal)
	fmt.Fprintf(&b, "Topology: %s\n", s.Topology)
	fmt.Fprintf(&b, "Board entries: %d\n", s.Board.Len())

	s.mu.RLock()
	agents := make(map[string]*Agent, len(s.Agents))
	for name, agent := range s.Agents {
		agents[name] = agent
	}
	s.mu.RUnlock()
	fmt.Fprintf(&b, "Agents:\n")
	for name, ag := range agents {
		status := ag.GetStatus()
		msgCount := len(ag.GetMessages())
		fmt.Fprintf(&b, "  %s [%s] (%d messages)\n", name, status, msgCount)
	}

	if s.Board.Len() > 0 {
		fmt.Fprintf(&b, "Board:\n")
		for _, e := range s.Board.List() {
			fmt.Fprintf(&b, "  [%s] %s = %s\n", e.Author, e.Key, e.Value)
		}
	}

	return b.String()
}
