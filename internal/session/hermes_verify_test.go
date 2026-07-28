package session

import "testing"

func TestUsageCacheFields(t *testing.T) {
	u := Usage{Input: 100, Output: 50, CacheRead: 200, CacheWrite: 30}
	if u.Input != 100 {
		t.Fatalf("Input: got %d want 100", u.Input)
	}
	if u.Output != 50 {
		t.Fatalf("Output: got %d want 50", u.Output)
	}
	if u.CacheRead != 200 {
		t.Fatalf("CacheRead: got %d want 200", u.CacheRead)
	}
	if u.CacheWrite != 30 {
		t.Fatalf("CacheWrite: got %d want 30", u.CacheWrite)
	}
	if u.CacheRead+u.CacheWrite != 230 {
		t.Fatalf("sum wrong: %d", u.CacheRead+u.CacheWrite)
	}
}
