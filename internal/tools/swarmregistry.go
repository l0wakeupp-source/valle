package tools

import (
	"sort"
	"sync"

	"rick/internal/provider"
)

var nestedDelegationTools = map[string]struct{}{
	"swarm": {}, "task": {}, "parallel_tasks": {},
}

// SwarmRegistry wraps a base registry with teammate-specific tools. Delegation
// tools are deliberately invisible so a worker cannot recursively spawn work.
type SwarmRegistry struct {
	ToolSet
	mu     sync.RWMutex
	extra  map[string]Tool
	allow  map[string]bool
	filter func(string) bool
}

func NewSwarmRegistry(base *Registry, allowed ...string) *SwarmRegistry {
	return NewFilteredSwarmRegistry(base, nil, allowed...)
}

func NewFilteredSwarmRegistry(base *Registry, filter func(string) bool, allowed ...string) *SwarmRegistry {
	var allow map[string]bool
	if len(allowed) > 0 {
		allow = make(map[string]bool, len(allowed))
		for _, name := range allowed {
			allow[name] = true
		}
	}
	return &SwarmRegistry{ToolSet: base, extra: map[string]Tool{}, allow: allow, filter: filter}
}

func (r *SwarmRegistry) Register(t Tool) {
	if t == nil {
		return
	}
	if _, blocked := nestedDelegationTools[t.Name()]; blocked {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extra[t.Name()] = t
}

func (r *SwarmRegistry) Get(name string) (Tool, bool) {
	if _, blocked := nestedDelegationTools[name]; blocked {
		return nil, false
	}
	r.mu.RLock()
	t, ok := r.extra[name]
	r.mu.RUnlock()
	if ok {
		return t, true
	}
	if r.ToolSet == nil {
		return nil, false
	}
	if r.allow != nil && !r.allow[name] {
		return nil, false
	}
	if r.filter != nil && !r.filter(name) {
		return nil, false
	}
	return r.ToolSet.Get(name)
}

func (r *SwarmRegistry) Names() []string {
	seen := map[string]bool{}
	out := []string{}
	if r.ToolSet != nil {
		for _, name := range r.ToolSet.Names() {
			if _, blocked := nestedDelegationTools[name]; !blocked && !seen[name] && (r.allow == nil || r.allow[name]) && (r.filter == nil || r.filter(name)) {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	r.mu.RLock()
	for name := range r.extra {
		if !seen[name] {
			out = append(out, name)
		}
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

func (r *SwarmRegistry) Schemas(enabled func(string) bool) []provider.ToolSchema {
	names := r.Names()
	out := make([]provider.ToolSchema, 0, len(names))
	for _, name := range names {
		if enabled != nil && !enabled(name) {
			continue
		}
		t, ok := r.Get(name)
		if !ok {
			continue
		}
		out = append(out, provider.ToolSchema{Name: name, Description: t.Description(), InputSchema: t.Schema()})
	}
	return out
}

var _ ToolSet = (*SwarmRegistry)(nil)
