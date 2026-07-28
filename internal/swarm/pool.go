package swarm

import (
	"context"
	"fmt"
	"sync"
)

// Pool manages agent goroutines and their lifecycle.
type Pool struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewPool creates an empty pool.
func NewPool() *Pool {
	return &Pool{running: map[string]context.CancelFunc{}}
}

// Spawn launches a goroutine for an agent task and tracks it.
func (p *Pool) Spawn(name string, fn func(ctx context.Context) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.running[name]; exists {
		return fmt.Errorf("pool: agent %q is already running", name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.running[name] = cancel
	go func() {
		defer func() {
			p.mu.Lock()
			delete(p.running, name)
			p.mu.Unlock()
		}()
		if err := fn(ctx); err != nil {
			// agent status set externally
		}
		cancel()
	}()
	return nil
}

// Kill stops a specific agent by name.
func (p *Pool) Kill(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cancel, ok := p.running[name]; ok {
		cancel()
		delete(p.running, name)
	}
}

// KillAll stops every agent in the pool.
func (p *Pool) KillAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, cancel := range p.running {
		cancel()
		delete(p.running, name)
	}
}

// Count returns the number of running agents.
func (p *Pool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.running)
}

// List returns the names of running agents.
func (p *Pool) List() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.running))
	for n := range p.running {
		names = append(names, n)
	}
	return names
}
