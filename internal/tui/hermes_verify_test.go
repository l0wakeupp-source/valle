package tui

import (
	"strings"
	"testing"
	"time"

	"rick/internal/session"
	"rick/internal/theme"
)

// TestTokenSplitCacheFields verifies the token split uses separate CacheRead/CacheWrite
// and that "input" only counts cache misses.
func TestTokenSplitCacheFields(t *testing.T) {
	th := theme.Load().Get("pickle-rick")
	if th == nil {
		t.Fatal("pickle-rick theme not loaded")
	}
	m := &Model{
		styles:    NewStyles(th),
		turnStart: time.Now().Add(-2 * time.Second),
	}
	m.billed = session.Usage{
		Input:      100, // cache miss
		Output:     50,
		CacheRead:  200, // cache hit
		CacheWrite: 30,  // cache write
	}
	m.ctxWindow = 100000

	split := m.tokenSplit()

	// Should contain all three: input (miss), cache read, cache write
	if !strings.Contains(split, "↑100") {
		t.Fatalf("expected ↑100 in split, got: %q", split)
	}
	if !strings.Contains(split, "↓50") {
		t.Fatalf("expected ↓50 in split, got: %q", split)
	}
	if !strings.Contains(split, "⚡200") {
		t.Fatalf("expected ⚡200 (cache read) in split, got: %q", split)
	}
	if !strings.Contains(split, "✏30") {
		t.Fatalf("expected ✏30 (cache write) in split, got: %q", split)
	}

	// Cache reads should count toward occupancy
	m.usage = session.Usage{Input: 500, CacheRead: 2000, Output: 100}
	pct := m.contextPct()
	// (500 + 2000 + 100) / 100000 = 2.6% -> should report ~3%
	if pct < 2 || pct > 5 {
		t.Fatalf("contextPct with cache reads: got %d, want ~3", pct)
	}
}
