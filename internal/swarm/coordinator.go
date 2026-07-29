package swarm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Coordinator tracks agent status and detects swarm completion.
type Coordinator struct {
	swarm      *Swarm
	timeout    time.Duration
	onComplete func()
}

// NewCoordinator creates a coordinator for the given swarm.
func NewCoordinator(s *Swarm, timeout time.Duration) *Coordinator {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &Coordinator{
		swarm:   s,
		timeout: timeout,
	}
}

// Run starts monitoring the swarm until completion or timeout.
func (c *Coordinator) Run() {
	deadline := time.After(c.timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			c.swarm.Terminate()
			return
		case <-c.swarm.Ctx.Done():
			return
		case <-ticker.C:
			if c.swarm.IsDone() {
				if c.onComplete != nil {
					c.onComplete()
				}
				return
			}
		}
	}
}

// SwarmProcess manages the execution of a single swarm.
type SwarmProcess struct {
	swarm   *Swarm
	runners map[string]Runner
	result  string
	err     error
	started bool
	mu      sync.Mutex
}

// NewSwarmProcess creates a process for running a swarm.
func NewSwarmProcess(s *Swarm) *SwarmProcess {
	return &SwarmProcess{
		swarm:   s,
		runners: map[string]Runner{},
	}
}

// RegisterRunner adds an agent runner.
func (p *SwarmProcess) RegisterRunner(name string, r Runner) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runners[name] = r
}

// Start launches all agents and streams events back via the swarm's event channel.
func (p *SwarmProcess) Start(ctx context.Context) (string, error) {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return "", errors.New("swarm: process already started")
	}
	p.started = true
	p.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	go func() {
		select {
		case <-p.swarm.Ctx.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()

	p.mu.Lock()
	runners := make(map[string]Runner, len(p.runners))
	for name, runner := range p.runners {
		runners[name] = runner
	}
	p.mu.Unlock()

	var wg sync.WaitGroup
	for name, runner := range runners {
		wg.Add(1)
		go func(agentName string, agentRunner Runner) {
			defer wg.Done()
			agentState, err := p.swarm.GetAgent(agentName)
			if err != nil {
				return
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					agentState.SetStatus(StatusFailed)
					p.swarm.Emit(Event{Kind: EventAgentFailed, Agent: agentName, Detail: fmt.Sprintf("runner panicked: %v", recovered)})
				}
			}()
			agentState.SetStatus(StatusWorking)
			p.swarm.Emit(Event{Kind: EventAgentStart, Agent: agentName, Detail: "started"})
			if agentRunner == nil {
				agentState.SetStatus(StatusFailed)
				p.swarm.Emit(Event{Kind: EventAgentFailed, Agent: agentName, Detail: "runner unavailable"})
				return
			}
			_, err = agentRunner.Run(runCtx, func(any) {})
			if err != nil {
				agentState.SetStatus(StatusFailed)
				p.swarm.Emit(Event{Kind: EventAgentFailed, Agent: agentName, Detail: err.Error()})
				return
			}
			agentState.SetStatus(StatusDone)
			p.swarm.Emit(Event{Kind: EventAgentDone, Agent: agentName, Detail: "completed"})
		}(name, runner)
	}

	workersDone := make(chan struct{}, 1)
	go func() {
		wg.Wait()
		workersDone <- struct{}{}
	}()
	select {
	case <-workersDone:
	case <-runCtx.Done():
		p.mu.Lock()
		p.err = runCtx.Err()
		p.mu.Unlock()
		return "", runCtx.Err()
	}

	p.swarm.Emit(Event{Kind: EventComplete, Detail: "all agents finished"})
	entries := p.swarm.Board.List()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(entries) > 0 {
		p.result = fmt.Sprintf("Swarm %q completed. Board has %d entries.", p.swarm.Name, len(entries))
	}
	return p.result, p.err
}

// SwarmManager manages multiple running swarms.
type SwarmManager struct {
	mu      sync.RWMutex
	swarms  map[string]*Swarm
	process map[string]*SwarmProcess
}

// NewSwarmManager creates a new swarm manager.
func NewSwarmManager() *SwarmManager {
	return &SwarmManager{
		swarms:  map[string]*Swarm{},
		process: map[string]*SwarmProcess{},
	}
}

// Add registers a swarm.
func (m *SwarmManager) Add(s *Swarm) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.swarms[s.ID] = s
}

// Get returns a swarm by ID.
func (m *SwarmManager) Get(id string) (*Swarm, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.swarms[id]
	if !ok {
		return nil, fmt.Errorf("swarm %q not found", id)
	}
	return s, nil
}

// Process returns the process for a swarm.
func (m *SwarmManager) Process(id string) (*SwarmProcess, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.process[id]
	if !ok {
		return nil, fmt.Errorf("swarm process %q not found", id)
	}
	return p, nil
}

// SetProcess registers a swarm process.
func (m *SwarmManager) SetProcess(id string, p *SwarmProcess) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.process[id] = p
}

// List returns all swarm IDs.
func (m *SwarmManager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.swarms))
	for id := range m.swarms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Remove unregisters a swarm.
func (m *SwarmManager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.swarms, id)
	delete(m.process, id)
}

// Kill terminates a swarm.
func (m *SwarmManager) Kill(id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.Terminate()
	return nil
}
