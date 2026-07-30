package tui

import (
	"testing"

	"rick/internal/provider"
)

func TestBetterContextCandidatePrefersAPIMetadata(t *testing.T) {
	value, source := betterContextCandidate(1_000_000, provider.ContextSourceInferred, 128_000, provider.ContextSourceAPI)
	if value != 128_000 || source != provider.ContextSourceAPI {
		t.Fatalf("candidate = %d/%q, want API value", value, source)
	}

	value, source = betterContextCandidate(128_000, provider.ContextSourceAPI, 1_000_000, provider.ContextSourceInferred)
	if value != 128_000 || source != provider.ContextSourceAPI {
		t.Fatalf("inferred candidate replaced API value: %d/%q", value, source)
	}
}

func TestBetterContextCandidateUsesLargerValueWithinSameSource(t *testing.T) {
	value, source := betterContextCandidate(128_000, provider.ContextSourceAPI, 256_000, provider.ContextSourceAPI)
	if value != 256_000 || source != provider.ContextSourceAPI {
		t.Fatalf("same-source candidate = %d/%q", value, source)
	}
}
