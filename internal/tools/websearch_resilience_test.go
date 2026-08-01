package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rick/internal/config"
)

func TestProviderHTTPErrorClassifiesRateLimitWithoutLeakingHeaders(t *testing.T) {
	err := providerHTTPError("serper", http.StatusTooManyRequests, http.Header{
		"Retry-After":       []string{"2"},
		"X-Request-ID":      []string{"request-id"},
		"X-Api-Key":         []string{"secret-value"},
		"X-RateLimit-Reset": []string{"3"},
	})
	if err.Class != ProviderRateLimited || !err.Retryable || !err.OpenCircuit {
		t.Fatalf("unexpected rate-limit classification: %#v", err)
	}
	if err.RetryAt.IsZero() {
		t.Fatal("rate limit did not preserve Retry-After")
	}
	if strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "request-id") {
		t.Fatalf("provider diagnostic leaked response metadata: %q", err.Error())
	}
}

func TestWebSearchSnapshotIsDetached(t *testing.T) {
	enabled := true
	cfg := &config.WebSearchConfig{
		AllowDomains: []string{"example.com"},
		Providers: map[string]config.WebSearchProviderConfig{
			"searxng": {Enabled: &enabled, Instances: []string{"http://one"}},
		},
	}
	snapshot := config.CloneWebSearchConfig(cfg)
	snapshot.AllowDomains[0] = "changed.example"
	snapshot.Providers["searxng"].Instances[0] = "http://two"
	*snapshot.Providers["searxng"].Enabled = false
	if cfg.AllowDomains[0] != "example.com" || cfg.Providers["searxng"].Instances[0] != "http://one" || !*cfg.Providers["searxng"].Enabled {
		t.Fatal("web-search snapshot shares mutable state with live configuration")
	}
}

func TestAutoRoutingDoesNotUseRetiredBingOrPublicSearx(t *testing.T) {
	tool := WebSearchTool{}
	providers := tool.configuredProvidersFor(nil, searchOptions{DDGBackend: "lite", SafeSearch: "moderate", Region: "wt-wt"}, "auto")
	if len(providers) != 1 || providers[0].name != "duckduckgo" {
		names := make([]string, 0, len(providers))
		for _, provider := range providers {
			names = append(names, provider.name)
		}
		t.Fatalf("automatic provider set = %v, want only keyless DuckDuckGo", names)
	}
	for _, provider := range providers {
		if provider.name == "bing" || provider.name == "searxng" {
			t.Fatalf("retired/public provider entered automatic routing: %s", provider.name)
		}
	}
}

func TestExplicitBingReturnsTypedNotSupportedOutcome(t *testing.T) {
	tool := WebSearchTool{}
	providers := tool.configuredProvidersFor(nil, searchOptions{}, "bing")
	if len(providers) != 1 {
		t.Fatalf("explicit Bing provider count = %d", len(providers))
	}
	_, err := providers[0].fn(context.Background(), "query", 3)
	var typed *ProviderError
	if !errors.As(err, &typed) || typed.Class != ProviderNotSupported {
		t.Fatalf("explicit Bing error = %v, want typed not_supported", err)
	}
}

func TestOptionalMediaWikiAdapterPreservesEmptyAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"query":{"search":[]}}`))
	}))
	defer server.Close()
	results, err := optionalProviderSearch(context.Background(), "mediawiki", "empty", 5, config.WebSearchProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("empty MediaWiki response returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("empty MediaWiki response returned %d results", len(results))
	}
}

func TestSchedulerHonorsConfiguredGlobalLimit(t *testing.T) {
	scheduler := &webSearchScheduler{globalWake: make(chan struct{}), providers: map[string]*providerGate{}}
	if err := scheduler.acquireGlobal(context.Background(), 1); err != nil {
		t.Fatalf("first global acquisition failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := scheduler.acquireGlobal(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquisition error = %v, want deadline exceeded", err)
	}
	scheduler.releaseGlobal()
}

func TestInflightSearchKeysNormalizeWhitespaceAndCase(t *testing.T) {
	first, leader := beginInFlightSearch(normalizedSearchKey("  Same   Query ", 5, "auto", "v"))
	if !leader {
		t.Fatal("first identical search was not elected leader")
	}
	second, leader := beginInFlightSearch(normalizedSearchKey("same query", 5, "AUTO", "v"))
	if leader || first != second {
		t.Fatal("normalized identical searches were not coalesced")
	}
	finishInFlightSearch(normalizedSearchKey("same query", 5, "auto", "v"), first, []providerBatch{{name: "duckduckgo"}})
	select {
	case <-second.done:
	case <-time.After(time.Second):
		t.Fatal("coalesced search waiter was not released")
	}
}
