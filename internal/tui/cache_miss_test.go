package tui

import (
	"testing"
	"time"

	"rick/internal/provider"
)

// TestObserveCacheUsageRequiresReportedCacheTokens verifies that a turn which
// omits the cache fields entirely (gateways do this on some usage chunks)
// never counts as a full-prompt cache miss. The pre-fix latch kept the
// previous "reported" state forever, so one cached turn made every later
// cache-less turn look like the whole history was re-billed.
func TestObserveCacheUsageRequiresReportedCacheTokens(t *testing.T) {
	m := &Model{}

	// Turn 1: warm request, cache read reported. Baseline established.
	m.observeCacheUsage(&provider.Usage{InputTokens: 100, CacheReadTokens: 10000})
	if m.cacheMissCount != 0 {
		t.Fatalf("turn 1 miss count = %d, want 0", m.cacheMissCount)
	}

	// Turn 2: same footprint but the provider reports no cache fields at all.
	// This must not be read as a ~10k-token re-bill.
	m.observeCacheUsage(&provider.Usage{InputTokens: 10100})
	if m.cacheMissCount != 0 {
		t.Fatalf("cache-less turn miss count = %d, want 0 (no cache reported)", m.cacheMissCount)
	}
	if m.cacheMissTokens != 0 {
		t.Fatalf("cache-less turn miss tokens = %d, want 0", m.cacheMissTokens)
	}
}

// TestObserveCacheUsageCountsRealMiss verifies a genuine drop in cache reads
// (prefix change or idle gap) is still detected and counted.
func TestObserveCacheUsageCountsRealMiss(t *testing.T) {
	m := &Model{}

	m.observeCacheUsage(&provider.Usage{InputTokens: 100, CacheReadTokens: 10000})
	m.observeCacheUsage(&provider.Usage{InputTokens: 9000, CacheReadTokens: 1200})

	if m.cacheMissCount != 1 {
		t.Fatalf("miss count = %d, want 1", m.cacheMissCount)
	}
	// missed = min(prev 10100, prompt 10200) - read 1200 = 8900 > floor 1024
	if m.cacheMissTokens != 8900 {
		t.Fatalf("miss tokens = %d, want 8900", m.cacheMissTokens)
	}
}

// TestObserveCacheUsageNoiseFloorUntouched verifies steady cache growth with
// only a small new tail stays under the 1024-token noise floor.
func TestObserveCacheUsageNoiseFloorUntouched(t *testing.T) {
	m := &Model{}

	m.observeCacheUsage(&provider.Usage{InputTokens: 100, CacheReadTokens: 10000})
	m.observeCacheUsage(&provider.Usage{InputTokens: 500, CacheReadTokens: 10200})

	if m.cacheMissCount != 0 {
		t.Fatalf("noise-floor turn miss count = %d, want 0", m.cacheMissCount)
	}
}

// TestObserveCacheUsageMissReasonDistinguishesIdleGap verifies the miss
// notice can tell an idle-gap cache expiry (gap outlived the TTL) apart from
// a genuine prefix change, so regressions are diagnosable at a glance.
func TestObserveCacheUsageMissReasonDistinguishesIdleGap(t *testing.T) {
	m := &Model{}

	// Gap longer than the default 5-minute TTL is an idle-gap expiry.
	m.cacheLastUsage = time.Now().Add(-10 * time.Minute)
	if got := m.cacheMissReason(); got != "idle gap (cache expired)" {
		t.Fatalf("stale gap reason = %q, want idle gap (cache expired)", got)
	}

	// A fresh turn (or nil deps with zero cacheLastUsage) is a prefix change.
	m.cacheLastUsage = time.Now()
	if got := m.cacheMissReason(); got != "prefix change" {
		t.Fatalf("fresh turn reason = %q, want prefix change", got)
	}
}
