package sandbox

import "sync"

// Holder carries the active policy and lets the TUI swap it at runtime
// (/sandbox, /yolo, agent switches) while tools read it concurrently.
//
// Tools hold a *Holder rather than a Policy so a mid-session change takes
// effect on the very next command instead of requiring a restart.
type Holder struct {
	mu     sync.RWMutex
	policy Policy
}

// NewHolder wraps a policy.
func NewHolder(p Policy) *Holder { return &Holder{policy: p} }

// Policy returns the active policy.
func (h *Holder) Policy() Policy {
	if h == nil {
		return Off()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.policy
}

// Set replaces the active policy.
func (h *Holder) Set(p Policy) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.policy = p
}

// SetMode changes only the mode, keeping every other setting. Read-only mode
// implies no network, which Normalize enforces.
func (h *Holder) SetMode(m Mode) Policy {
	if h == nil {
		return Off()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	next := h.policy
	next.Mode = m
	if m != ModeReadOnly {
		// Leaving read-only restores the network unless the user turned it
		// off explicitly, which SetNetwork tracks separately.
		next.Network = true
	}
	h.policy = next.Normalize(next.Workspace)
	return h.policy
}

// SetNetwork toggles network access.
func (h *Holder) SetNetwork(on bool) Policy {
	if h == nil {
		return Off()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	next := h.policy
	next.Network = on
	h.policy = next.Normalize(next.Workspace)
	return h.policy
}
