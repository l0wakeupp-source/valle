package security

import (
	"testing"
	"time"
)

func TestAuditCacheStoresFindingsWithoutResponseBody(t *testing.T) {
	sourceMtime := time.Unix(123, 0)
	dep := dependency{
		Name:        "example",
		Version:     "1.2.3",
		Ecosystem:   "Go",
		SourceMtime: sourceMtime,
	}
	cache := &auditCache{}
	findings := []Finding{{Package: dep.Name, Version: dep.Version, OSVID: "OSV-1"}}

	cache.store(dep, findings, t.TempDir())
	entry := cache.lookup(dep, t.TempDir())
	if entry == nil {
		t.Fatal("cache lookup returned nil")
	}
	if entry.ResponseBody != nil {
		t.Fatalf("cache retained a response body: %d bytes", len(entry.ResponseBody))
	}
	if len(entry.Findings) != 1 || entry.Findings[0].OSVID != "OSV-1" {
		t.Fatalf("cached findings = %#v", entry.Findings)
	}
}

func TestAuditCacheAcceptsLegacyResponseBody(t *testing.T) {
	sourceMtime := time.Unix(123, 0)
	dep := dependency{Name: "example", Version: "1.2.3", Ecosystem: "Go", SourceMtime: sourceMtime}
	cache := &auditCache{Entries: []auditCacheEntry{{
		Fingerprint:  cacheFingerprint(dep),
		ResponseBody: []byte(`{"vulns":[{"id":"OSV-1","affected":[{"package":{"name":"example"},"ranges":[]}]}]}`),
		SourceMtime:  sourceMtime,
	}}}
	entry := cache.lookup(dep, t.TempDir())
	if entry == nil || len(entry.ResponseBody) == 0 {
		t.Fatal("legacy cache entry was not returned")
	}
}
