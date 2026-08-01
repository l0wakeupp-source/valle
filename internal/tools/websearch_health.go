package tools

import (
	"sort"
	"strings"
	"sync"
	"time"

	"rick/internal/config"
)

type providerHealthState struct {
	Failures    int
	Attempts    int
	Successes   int
	LastResults int
	LastError   *ProviderError
	LastSuccess time.Time
	LastAttempt time.Time
	LastLatency time.Duration
	OpenUntil   time.Time
	HalfOpen    bool
	Endpoint    string
}

var providerHealth = struct {
	sync.Mutex
	states map[string]*providerHealthState
}{states: map[string]*providerHealthState{}}

func healthKey(provider, endpoint string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "|" + strings.ToLower(strings.TrimSpace(endpoint))
}

func beginProviderHealth(provider, endpoint string) (string, *ProviderError) {
	key := healthKey(provider, endpoint)
	providerHealth.Lock()
	defer providerHealth.Unlock()
	state := providerHealth.states[key]
	if state == nil {
		state = &providerHealthState{Endpoint: endpoint}
		providerHealth.states[key] = state
	}
	now := time.Now()
	if !state.OpenUntil.IsZero() && now.Before(state.OpenUntil) {
		return key, &ProviderError{
			Provider:    provider,
			Class:       ProviderCooldown,
			RetryAt:     state.OpenUntil,
			Message:     "provider is cooling down",
			TryFallback: true,
		}
	}
	if !state.OpenUntil.IsZero() && !state.HalfOpen {
		state.HalfOpen = true
	}
	if state.HalfOpen && state.Attempts > state.Successes && now.Sub(state.LastAttempt) < 30*time.Second {
		return key, &ProviderError{
			Provider:    provider,
			Class:       ProviderCooldown,
			RetryAt:     state.LastAttempt.Add(30 * time.Second),
			Message:     "provider probe is already in progress",
			TryFallback: true,
		}
	}
	state.Attempts++
	state.LastAttempt = now
	return key, nil
}

func finishProviderHealth(key string, outcome *ProviderError, resultCount int, latency time.Duration) {
	providerHealth.Lock()
	defer providerHealth.Unlock()
	state := providerHealth.states[key]
	if state == nil {
		return
	}
	state.LastLatency = latency
	state.LastResults = resultCount
	if outcome == nil && resultCount > 0 {
		state.Successes++
		state.Failures = 0
		state.LastError = nil
		state.LastSuccess = time.Now()
		state.OpenUntil = time.Time{}
		state.HalfOpen = false
		return
	}
	if outcome == nil {
		outcome = &ProviderError{Class: ProviderNoResults, Message: "no results", TryFallback: true}
	}
	state.Failures++
	copy := *outcome
	state.LastError = &copy
	shouldOpen := outcome.OpenCircuit || outcome.Class == ProviderQuotaExhausted || outcome.Class == ProviderRateLimited
	if !shouldOpen && state.Failures >= 3 {
		switch outcome.Class {
		case ProviderTemporarilyUnavail, ProviderTimeout, ProviderNetwork, ProviderInvalidResponse:
			shouldOpen = true
		}
	}
	if shouldOpen {
		cooldown := 10 * time.Second
		if !outcome.ResetAt.IsZero() && time.Until(outcome.ResetAt) > cooldown {
			cooldown = time.Until(outcome.ResetAt)
		} else if !outcome.RetryAt.IsZero() && time.Until(outcome.RetryAt) > cooldown {
			cooldown = time.Until(outcome.RetryAt)
		}
		if cooldown > 15*time.Minute {
			cooldown = 15 * time.Minute
		}
		state.OpenUntil = time.Now().Add(cooldown)
		state.HalfOpen = false
	}
}

// WebProviderStatus is a redacted health snapshot for /webproviders and
// diagnostics. It contains no API keys or response bodies.
type WebProviderStatus struct {
	ID          string
	State       string
	Endpoint    string
	Attempts    int
	Successes   int
	Failures    int
	LastResults int
	LastError   string
	LastClass   ProviderErrorClass
	RetryAt     time.Time
	LastSuccess time.Time
	LastLatency time.Duration
}

func providerStatus(id, endpoint string) WebProviderStatus {
	key := healthKey(id, endpoint)
	providerHealth.Lock()
	defer providerHealth.Unlock()
	state := providerHealth.states[key]
	status := WebProviderStatus{ID: id, Endpoint: safeEndpointVariant(endpoint), State: "ready"}
	if state == nil {
		return status
	}
	status.Attempts = state.Attempts
	status.Successes = state.Successes
	status.Failures = state.Failures
	status.LastResults = state.LastResults
	status.LastSuccess = state.LastSuccess
	status.LastLatency = state.LastLatency
	if state.LastError != nil {
		status.LastError = state.LastError.Error()
		status.LastClass = state.LastError.Class
	}
	if !state.OpenUntil.IsZero() && time.Now().Before(state.OpenUntil) {
		status.State = "cooldown"
		status.RetryAt = state.OpenUntil
	}
	return status
}

// ProviderStatuses returns configured and built-in provider health without
// resolving or displaying credentials.
func ProviderStatuses(cfg *config.WebSearchConfig) []WebProviderStatus {
	ids := SupportedWebProviderIDs()
	if cfg != nil {
		for id := range cfg.Providers {
			if !containsString(ids, strings.ToLower(id)) {
				ids = append(ids, strings.ToLower(id))
			}
		}
	}
	sort.Strings(ids)
	statuses := make([]WebProviderStatus, 0, len(ids))
	for _, id := range ids {
		provider := config.WebSearchProviderConfig{}
		if cfg != nil {
			provider = cfg.Providers[id]
		}
		endpoint := provider.BaseURL
		if endpoint == "" {
			endpoint = id
		}
		statuses = append(statuses, providerStatus(id, endpoint))
	}
	return statuses
}

// ResetProviderHealth clears cooldown and circuit state for one provider.
func ResetProviderHealth(provider string) {
	providerHealth.Lock()
	defer providerHealth.Unlock()
	for key, state := range providerHealth.states {
		if strings.HasPrefix(key, strings.ToLower(strings.TrimSpace(provider))+"|") {
			state.OpenUntil = time.Time{}
			state.HalfOpen = false
			state.Failures = 0
			state.LastError = nil
		}
	}
}
