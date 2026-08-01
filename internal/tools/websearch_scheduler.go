package tools

import (
	"context"
	"sync"
	"time"
)

const defaultGlobalWebConcurrency = 4

type providerGate struct {
	slots    chan struct{}
	mu       sync.Mutex
	nextCall time.Time
}

type webSearchScheduler struct {
	globalMu     sync.Mutex
	globalWake   chan struct{}
	globalActive int
	mu           sync.Mutex
	providers    map[string]*providerGate
}

var sharedWebSearchScheduler = &webSearchScheduler{
	globalWake: make(chan struct{}),
	providers:  map[string]*providerGate{},
}

func (s *webSearchScheduler) gate(name string, maxConcurrency int) *providerGate {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	if maxConcurrency > defaultGlobalWebConcurrency {
		maxConcurrency = defaultGlobalWebConcurrency
	}
	gate := s.providers[name]
	if gate == nil {
		gate = &providerGate{slots: make(chan struct{}, maxConcurrency)}
		s.providers[name] = gate
	}
	return gate
}

func (s *webSearchScheduler) acquire(ctx context.Context, name string, maxConcurrency, maxRPM, globalLimit int) (func(), error) {
	if globalLimit <= 0 || globalLimit > defaultGlobalWebConcurrency {
		globalLimit = defaultGlobalWebConcurrency
	}
	if err := s.acquireGlobal(ctx, globalLimit); err != nil {
		return nil, err
	}
	gate := s.gate(name, maxConcurrency)
	select {
	case gate.slots <- struct{}{}:
	case <-ctx.Done():
		s.releaseGlobal()
		return nil, ctx.Err()
	}

	interval := time.Duration(0)
	if maxRPM > 0 {
		interval = time.Minute / time.Duration(maxRPM)
	}
	if interval > 0 {
		gate.mu.Lock()
		previous := gate.nextCall
		allowed := time.Now()
		if previous.Add(interval).After(allowed) {
			allowed = previous.Add(interval)
		}
		gate.nextCall = allowed
		gate.mu.Unlock()
		if err := waitUntil(ctx, allowed); err != nil {
			gate.mu.Lock()
			if gate.nextCall.Equal(allowed) {
				gate.nextCall = previous
			}
			gate.mu.Unlock()
			<-gate.slots
			s.releaseGlobal()
			return nil, err
		}
	}
	return func() {
		<-gate.slots
		s.releaseGlobal()
	}, nil
}

func (s *webSearchScheduler) acquireGlobal(ctx context.Context, limit int) error {
	for {
		s.globalMu.Lock()
		if s.globalActive < limit {
			s.globalActive++
			s.globalMu.Unlock()
			return nil
		}
		wake := s.globalWake
		s.globalMu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *webSearchScheduler) releaseGlobal() {
	s.globalMu.Lock()
	if s.globalActive > 0 {
		s.globalActive--
	}
	close(s.globalWake)
	s.globalWake = make(chan struct{})
	s.globalMu.Unlock()
}

func waitUntil(ctx context.Context, at time.Time) error {
	wait := time.Until(at)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
