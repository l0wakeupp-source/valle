package tools

import (
	"container/list"
	"context"
	"errors"
	"testing"
	"time"

	"rick/internal/config"
)

func TestFilterResultsNilConfig(t *testing.T) {
	results := []searchResult{
		{Title: "A", URL: "https://example.com/page"},
		{Title: "B", URL: "https://test.org/page"},
	}
	got := filterResults(results, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 results with nil config, got %d", len(got))
	}
}

func TestFilterResultsEmptyConfig(t *testing.T) {
	results := []searchResult{
		{Title: "A", URL: "https://example.com/page"},
	}
	cfg := &config.WebSearchConfig{}
	got := filterResults(results, cfg)
	if len(got) != 1 {
		t.Fatalf("expected 1 result with empty config, got %d", len(got))
	}
}

func TestFilterResultsAllowDomains(t *testing.T) {
	results := []searchResult{
		{Title: "GitHub", URL: "https://github.com/repo"},
		{Title: "SO", URL: "https://stackoverflow.com/q/1"},
		{Title: "Pinterest", URL: "https://pinterest.com/pin"},
	}
	cfg := &config.WebSearchConfig{
		AllowDomains: []string{"github.com", "stackoverflow.com"},
	}
	got := filterResults(results, cfg)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	for _, r := range got {
		host := extractHost(r.URL)
		if host != "github.com" && host != "stackoverflow.com" {
			t.Errorf("unexpected host %q in results", host)
		}
	}
}

func TestFilterResultsDenyDomains(t *testing.T) {
	results := []searchResult{
		{Title: "GitHub", URL: "https://github.com/repo"},
		{Title: "Pinterest", URL: "https://pinterest.com/pin"},
		{Title: "Example", URL: "https://example.com/page"},
	}
	cfg := &config.WebSearchConfig{
		DenyDomains: []string{"pinterest.com"},
	}
	got := filterResults(results, cfg)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	for _, r := range got {
		if extractHost(r.URL) == "pinterest.com" {
			t.Error("pinterest.com should have been filtered out")
		}
	}
}

func TestFilterResultsDenyWinsOverAllow(t *testing.T) {
	results := []searchResult{
		{Title: "GitHub", URL: "https://github.com/repo"},
		{Title: "Gist", URL: "https://gist.github.com/file"},
	}
	cfg := &config.WebSearchConfig{
		AllowDomains: []string{"*.github.com"},
		DenyDomains:  []string{"gist.github.com"},
	}
	got := filterResults(results, cfg)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if extractHost(got[0].URL) != "github.com" {
		t.Errorf("expected github.com, got %q", extractHost(got[0].URL))
	}
}

func TestMatchDomainExact(t *testing.T) {
	tests := []struct {
		host    string
		pattern string
		want    bool
	}{
		{"github.com", "github.com", true},
		{"api.github.com", "github.com", false},
		{"github.com", "GitHub.COM", true},
		{"example.com", "github.com", false},
	}
	for _, tt := range tests {
		if got := matchDomain(tt.host, tt.pattern); got != tt.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.host, tt.pattern, got, tt.want)
		}
	}
}

func TestMatchDomainGlob(t *testing.T) {
	tests := []struct {
		host    string
		pattern string
		want    bool
	}{
		{"api.github.com", "*.github.com", true},
		{"github.com", "*.github.com", true},
		{"gist.github.com", "*.github.com", true},
		{"notgithub.com", "*.github.com", false},
		{"sub.api.github.com", "*.github.com", true},
		{"github.com.evil.com", "*.github.com", false},
	}
	for _, tt := range tests {
		if got := matchDomain(tt.host, tt.pattern); got != tt.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.host, tt.pattern, got, tt.want)
		}
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/repo", "github.com"},
		{"http://Example.COM/path", "example.com"},
		{"https://sub.domain.org:8080/path?q=1", "sub.domain.org"},
		{"not-a-url", ""},
	}
	for _, tt := range tests {
		if got := extractHost(tt.url); got != tt.want {
			t.Errorf("extractHost(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestCleanHTMLUsesSinglePassNormalization(t *testing.T) {
	input := "  <b>Hello</b>&nbsp; &quot;world&quot; &amp; &lt;tag&gt;\n"
	if got, want := cleanHTML(input), `Hello "world" & <tag>`; got != want {
		t.Fatalf("cleanHTML(%q) = %q, want %q", input, got, want)
	}
	if got, want := cleanHTML("&copy;"), "&copy;"; got != want {
		t.Fatalf("cleanHTML preserved entity = %q, want %q", got, want)
	}
}

func TestWaitHostGapHonorsCancellation(t *testing.T) {
	host := "regression-host"
	hostMu.Lock()
	hostLastCall[host] = time.Now()
	hostMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := waitHostGap(ctx, host, time.Hour); err == nil {
		t.Fatal("expected canceled wait to return an error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("canceled wait took too long: %s", elapsed)
	}
}

func TestRetryableSearchErrorIncludesTransientStatuses(t *testing.T) {
	for _, message := range []string{"bing: HTTP 503", "request timeout", "connection reset by peer"} {
		if !retryableSearchError(errors.New(message)) {
			t.Errorf("expected %q to be retryable", message)
		}
	}
	if retryableSearchError(errors.New("HTTP 400")) {
		t.Fatal("HTTP 400 should not be retried")
	}
}

func TestRetryCancellationDoesNotWaitForExpiredTimer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	start := time.Now()
	_, err := (WebSearchTool{}).tryWithRetry(ctx, func(context.Context, string, int) ([]searchResult, error) {
		calls++
		cancel()
		return nil, errors.New("HTTP 503")
	}, "query", 5)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("tryWithRetry error = %v, want context cancellation", err)
	}
	if calls != 1 {
		t.Fatalf("retry function called %d times after cancellation", calls)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("canceled retry took too long: %s", elapsed)
	}
}

func TestCacheHitCopiesAndPromotesEntry(t *testing.T) {
	cacheMu.Lock()
	oldLRU, oldMap, oldMax := cacheLRU, cacheMap, cacheMaxLen
	cacheLRU, cacheMap, cacheMaxLen = list.New(), map[cacheKey]*list.Element{}, 2
	cacheMu.Unlock()
	defer func() {
		cacheMu.Lock()
		cacheLRU, cacheMap, cacheMaxLen = oldLRU, oldMap, oldMax
		cacheMu.Unlock()
	}()

	cachePut("test", "a", 1, []searchResult{{Title: "original"}}, time.Minute)
	cachePut("test", "b", 1, []searchResult{{Title: "b"}}, time.Minute)
	got, ok := cacheGet("test", "a", 1)
	if !ok {
		t.Fatal("expected cache hit")
	}
	got[0].Title = "mutated"
	cachePut("test", "c", 1, []searchResult{{Title: "c"}}, time.Minute)
	if _, ok := cacheGet("test", "a", 1); !ok {
		t.Fatal("recently used cache entry was evicted instead of promoted")
	}
	got, ok = cacheGet("test", "a", 1)
	if !ok || got[0].Title != "original" {
		t.Fatalf("cache returned mutable internal data: %#v", got)
	}
}

func TestSearchBudgetIsCumulative(t *testing.T) {
	sessionID := "budget-regression"
	resetBudget(sessionID)
	defer resetBudget(sessionID)
	if ok, _ := checkBudget(sessionID, 2); !ok {
		t.Fatal("first search should be allowed")
	}
	if ok, _ := checkBudget(sessionID, 2); !ok {
		t.Fatal("second search should be allowed")
	}
	if ok, count := checkBudget(sessionID, 2); ok || count != 2 {
		t.Fatalf("third search = allowed=%v count=%d, want exhausted at count 2", ok, count)
	}
}
