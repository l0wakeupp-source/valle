package tools

import (
	"container/list"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"rick/internal/config"
)

//go:embed searx_instances.json
var searxJSON []byte

// chromeUA is the single User-Agent used for every provider.
const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

var (
	webSearchClient = &http.Client{Timeout: 15 * time.Second}

	bingResultRe      = regexp.MustCompile(`(?s)<li class="b_algo"[^>]*>(.*?)</li>`)
	bingTitleRe       = regexp.MustCompile(`<h2><a[^>]*href="([^"]*)"[^>]*>(.*?)</a></h2>`)
	bingSnippetRe     = regexp.MustCompile(`(?s)<p[^>]*class="[^"]*b_lineclamp[^"]*"[^>]*>(.*?)</p>`)
	ddgLinkRe         = regexp.MustCompile(`<a[^>]*rel="nofollow"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	ddgSnippetRe      = regexp.MustCompile(`(?s)<td[^>]*class="result-snippet"[^>]*>(.*?)</td>`)
	braveLinkRe       = regexp.MustCompile(`<a[^*]*href="(https?://[^"]*)"[^>]*>([\s\S]*?)</a>`)
	braveSnippetRe    = regexp.MustCompile(`<p[^>]*class="[^"]*(?:snippet|desc|description)[^"]*"[^>]*>([\s\S]*?)</p>`)
	braveTitleEndRe   = regexp.MustCompile(`</a>`)
	tagRe             = regexp.MustCompile(`<[^>]*>`)
	cleanHTMLReplacer = strings.NewReplacer(
		"<b>", "", "</b>", "", "<em>", "", "</em>", "", "<wbr>", "",
		"&quot;", "\"", "&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&#39;", "'", "&apos;", "'", "&nbsp;", " ",
		"\n", " ", "\r", " ", "	", " ",
	)
)

// --- rate limiting / host tracking ---

var (
	hostMu       sync.Mutex
	hostLastCall = map[string]time.Time{}
)

// waitHostGap blocks until at least d has elapsed since the last call to host.
// Waiting is cancellation-aware so a stopped agent is not held by rate limiting.
func waitHostGap(ctx context.Context, host string, d time.Duration) error {
	hostMu.Lock()
	now := time.Now()
	previous := hostLastCall[host]
	allowedAt := previous.Add(d)
	if allowedAt.Before(now) {
		allowedAt = now
	}
	hostLastCall[host] = allowedAt
	wait := time.Until(allowedAt)
	hostMu.Unlock()

	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		hostMu.Lock()
		if hostLastCall[host].Equal(allowedAt) {
			hostLastCall[host] = previous
		}
		hostMu.Unlock()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// --- LRU cache ---

type cacheKey struct {
	provider   string
	query      string
	maxResults int
	variant    string
}

type cacheEntry struct {
	key       cacheKey
	results   []searchResult
	expiresAt time.Time
}

var (
	cacheMu  sync.RWMutex
	cacheLRU = list.New()
	cacheMap = map[cacheKey]*list.Element{}
	cacheTTL = map[string]time.Duration{
		"searxng":    60 * time.Second,
		"ddginstant": 60 * time.Second,
		"bing":       300 * time.Second,
		"ddglite":    300 * time.Second,
		"brave":      300 * time.Second,
	}
	cacheMaxLen = 100
)

const maxSearchResponseBytes = 4 << 20

func cacheGet(provider string, query string, maxResults int) ([]searchResult, bool) {
	return cacheGetVariant(provider, query, maxResults, "")
}

func cacheGetVariant(provider string, query string, maxResults int, variant string) ([]searchResult, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	key := cacheKey{provider: provider, query: query, maxResults: maxResults, variant: variant}
	elem, ok := cacheMap[key]
	if !ok {
		return nil, false
	}
	entry := elem.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		delete(cacheMap, key)
		cacheLRU.Remove(elem)
		return nil, false
	}
	cacheLRU.MoveToFront(elem)
	return append([]searchResult(nil), entry.results...), true
}

func cachePut(provider, query string, maxResults int, results []searchResult, ttl time.Duration) {
	cachePutVariant(provider, query, maxResults, results, ttl, "")
}

func cachePutVariant(provider, query string, maxResults int, results []searchResult, ttl time.Duration, variant string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	key := cacheKey{provider: provider, query: query, maxResults: maxResults, variant: variant}
	if elem, ok := cacheMap[key]; ok {
		cacheLRU.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry)
		entry.results = append([]searchResult(nil), results...)
		entry.expiresAt = time.Now().Add(ttl)
		return
	}
	if cacheLRU.Len() >= cacheMaxLen {
		oldest := cacheLRU.Back()
		if oldest != nil {
			evictKey := oldest.Value.(*cacheEntry).key
			cacheLRU.Remove(oldest)
			delete(cacheMap, evictKey)
		}
	}
	elem := cacheLRU.PushFront(&cacheEntry{results: append([]searchResult(nil), results...), expiresAt: time.Now().Add(ttl), key: key})
	cacheMap[key] = elem
}

// --- SearXNG instance tracking ---

type searxInstance struct {
	URL           string
	ConsecFails   int
	Disabled      bool
	DisabledUntil time.Time
}

var (
	searxMu        sync.Mutex
	searxInstances []*searxInstance
	searxLogged    bool
	searxCursor    int
)

func loadSearxInstances() []*searxInstance {
	urls := []string{}
	if err := json.Unmarshal(searxJSON, &urls); err != nil || len(urls) == 0 {
		urls = []string{
			"https://search.saptko.cloud",
			"https://searx.be",
			"https://searx.org",
		}
	}
	var out []*searxInstance
	for _, u := range urls {
		out = append(out, &searxInstance{URL: u})
	}
	return out
}

func getSearxInstance() *searxInstance {
	searxMu.Lock()
	defer searxMu.Unlock()
	if searxInstances == nil {
		searxInstances = loadSearxInstances()
	}
	now := time.Now()
	for i := 0; i < len(searxInstances); i++ {
		idx := (searxCursor + i) % len(searxInstances)
		si := searxInstances[idx]
		if si.Disabled && !si.DisabledUntil.IsZero() && now.After(si.DisabledUntil) {
			si.Disabled = false
			si.ConsecFails = 0
			si.DisabledUntil = time.Time{}
		}
		if !si.Disabled {
			searxCursor = (idx + 1) % len(searxInstances)
			return si
		}
	}
	if !searxLogged {
		fmt.Fprintln(os.Stderr, "websearch: all SearXNG instances exhausted, falling back to DDG Instant")
		searxLogged = true
	}
	return nil
}

func disableSearxInstance(si *searxInstance) {
	searxMu.Lock()
	si.Disabled = true
	si.DisabledUntil = time.Now().Add(30 * time.Second)
	searxMu.Unlock()
}

func markSearxFailure(si *searxInstance) {
	searxMu.Lock()
	si.ConsecFails++
	exhausted := si.ConsecFails >= 2
	searxMu.Unlock()
	if exhausted {
		disableSearxInstance(si)
	}
}

func markSearxSuccess(si *searxInstance) {
	searxMu.Lock()
	si.ConsecFails = 0
	searxMu.Unlock()
}

// --- per-session budget ---

var (
	budgetMu    sync.Mutex
	budgetCount = map[string]int{}
)

func checkBudget(sessionID string, max int) (bool, int) {
	budgetMu.Lock()
	defer budgetMu.Unlock()
	count := budgetCount[sessionID]
	if count >= max {
		return false, count
	}
	budgetCount[sessionID] = count + 1
	return true, count + 1
}

func resetBudget(sessionID string) {
	budgetMu.Lock()
	delete(budgetCount, sessionID)
	budgetMu.Unlock()
}

func releaseBudget(sessionID string) {
	budgetMu.Lock()
	if count := budgetCount[sessionID]; count <= 1 {
		delete(budgetCount, sessionID)
	} else {
		budgetCount[sessionID] = count - 1
	}
	budgetMu.Unlock()
}

// --- tool ---

type WebSearchTool struct {
	Restrictions *config.WebSearchConfig
}

func (WebSearchTool) Name() string   { return "websearch" }
func (WebSearchTool) ReadOnly() bool { return true }

func (WebSearchTool) Description() string {
	return "Search the web with automatic fallback. Use for current or post-training information; returns titles, URLs, and snippets."
}

func (WebSearchTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"max_results": map[string]any{
				"type":        "number",
				"description": "Maximum results to return (default 5, max 10)",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "Provider name, auto, or parallel",
			},
			"region": map[string]any{
				"type":        "string",
				"description": "DuckDuckGo region code, for example us-en or wt-wt",
			},
			"safe_search": map[string]any{
				"type":        "string",
				"description": "DuckDuckGo safe search: off, moderate, or strict",
			},
			"time_range": map[string]any{
				"type":        "string",
				"description": "Freshness filter: day, week, month, or year",
			},
			"ddg_backend": map[string]any{
				"type":        "string",
				"description": "DuckDuckGo backend: lite, instant, or auto",
			},
			"exa_type": map[string]any{
				"type":        "string",
				"description": "Exa search type: auto, fast, or deep",
			},
			"livecrawl": map[string]any{
				"type":        "string",
				"description": "Exa live crawling: fallback, preferred, always, or never",
			},
			"include_domains": map[string]any{
				"type":        "array",
				"description": "Provider-level domain allowlist",
			},
			"exclude_domains": map[string]any{
				"type":        "array",
				"description": "Provider-level domain denylist",
			},
		},
		"required": []string{"query"},
	}
}

type searchArgs struct {
	Query          string   `json:"query"`
	MaxResults     int      `json:"max_results"`
	Provider       string   `json:"provider"`
	Region         string   `json:"region"`
	SafeSearch     string   `json:"safe_search"`
	TimeRange      string   `json:"time_range"`
	DDGBackend     string   `json:"ddg_backend"`
	ExaType        string   `json:"exa_type"`
	Livecrawl      string   `json:"livecrawl"`
	IncludeDomains []string `json:"include_domains"`
	ExcludeDomains []string `json:"exclude_domains"`
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func (t WebSearchTool) Run(ctx context.Context, tc Context, in json.RawMessage) (Result, error) {
	var args searchArgs
	if err := json.Unmarshal(in, &args); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return Errf("query is required"), nil
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 5
	}
	if args.MaxResults > 10 {
		args.MaxResults = 10
	}
	maxSession := 10
	if t.Restrictions != nil && t.Restrictions.MaxSearchesPerSession > 0 {
		maxSession = t.Restrictions.MaxSearchesPerSession
	}
	allowed, n := checkBudget(tc.SessionID, maxSession)
	if !allowed {
		return Result{
			Output: fmt.Sprintf("search budget exhausted (max %d per session)", maxSession),
			Title:  "web search (budget exceeded)",
			Meta:   map[string]any{"query": args.Query, "results": 0, "budget_exceeded": true},
		}, nil
	}
	if t.Restrictions != nil && t.Restrictions.MaxResults > 0 && args.MaxResults > t.Restrictions.MaxResults {
		args.MaxResults = t.Restrictions.MaxResults
	}

	options, err := t.searchOptions(args)
	if err != nil {
		return Errf("invalid search options: %v", err), nil
	}
	forced := strings.ToLower(strings.TrimSpace(args.Provider))
	if forced == "" && t.Restrictions != nil {
		forced = strings.ToLower(strings.TrimSpace(t.Restrictions.Provider))
	}
	if forced == "ddg" {
		forced = "duckduckgo"
	}
	if forced == "lite" {
		forced = "duckduckgo"
	}
	providers := t.configuredProviders(options, forced)
	if len(providers) == 0 {
		return Errf("no enabled web search providers are configured"), nil
	}

	searchCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	variant := options.cacheVariant()
	for _, provider := range providers {
		variant += "&" + provider.name + "=" + providerConfig(t.Restrictions, provider.name).BaseURL
	}
	parallel := forced == "parallel"
	if !parallel && t.Restrictions != nil && t.Restrictions.Parallel != nil {
		parallel = *t.Restrictions.Parallel
	}
	maxParallel := defaultMaxParallel
	if t.Restrictions != nil && t.Restrictions.MaxParallel > 0 {
		maxParallel = t.Restrictions.MaxParallel
	}

	var batches []providerBatch
	if parallel {
		batches = t.runParallelProviders(searchCtx, providers, args.Query, args.MaxResults, maxParallel, variant)
	} else {
		batches = make([]providerBatch, 0, len(providers))
		for _, provider := range providers {
			batch := t.runProvider(searchCtx, provider, args.Query, args.MaxResults, variant)
			batches = append(batches, batch)
			if batch.err != nil && (errors.Is(batch.err, context.Canceled) || errors.Is(batch.err, context.DeadlineExceeded)) {
				break
			}
			if len(batch.results) > 0 {
				break
			}
		}
	}

	var lastErr error
	providerNames := make([]string, 0, len(batches))
	for _, batch := range batches {
		providerNames = append(providerNames, batch.name)
		if batch.err != nil {
			lastErr = fmt.Errorf("%s: %w", batch.name, batch.err)
		}
	}
	merged := mergeSearchResults(batches, args.MaxResults)
	if len(merged) == 0 {
		if errors.Is(searchCtx.Err(), context.Canceled) || errors.Is(searchCtx.Err(), context.DeadlineExceeded) {
			return Errf("web search canceled: %v", searchCtx.Err()), nil
		}
		if lastErr != nil {
			return Errf("all web search providers failed for query: %s (last error: %v)", args.Query, lastErr), nil
		}
		return Errf("all web search results were empty for query: %s", args.Query), nil
	}
	original := len(merged)
	filtered := filterResults(merged, t.Restrictions)
	if len(filtered) == 0 {
		return Errf("all search results were filtered out by domain restrictions for query: %s", args.Query), nil
	}
	result := formatResultsFiltered(args.Query, filtered, original, t.Restrictions)
	if n >= 3 {
		result.Output = fmt.Sprintf("[note: %d searches in this turn]\n%s", n, result.Output)
	}
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	result.Meta["providers"] = providerNames
	result.Meta["parallel"] = parallel
	return result, nil
}

func (t WebSearchTool) tryWithRetry(ctx context.Context, fn func(context.Context, string, int) ([]searchResult, error), query string, maxResults int) ([]searchResult, error) {
	results, err := fn(ctx, query, maxResults)
	if err == nil {
		return results, nil
	}
	if !retryableSearchError(err) {
		return nil, err
	}

	base := 500 * time.Millisecond
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	timer := time.NewTimer(base + jitter)
	select {
	case <-ctx.Done():
		timer.Stop()
		return nil, ctx.Err()
	case <-timer.C:
	}
	return fn(ctx, query, maxResults)
}

func retryableSearchError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"429", "408", "500", "502", "503", "504", "timeout", "temporarily", "connection reset", "eof"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// filterResults removes results whose domain does not match the allow list
// or matches the deny list. Deny always wins over allow.
func filterResults(results []searchResult, cfg *config.WebSearchConfig) []searchResult {
	if cfg == nil {
		return results
	}
	if len(cfg.AllowDomains) == 0 && len(cfg.DenyDomains) == 0 {
		return results
	}
	var out []searchResult
	for _, r := range results {
		host := extractHost(r.URL)
		if host == "" {
			continue
		}
		if matchesDomainList(host, cfg.DenyDomains) {
			continue
		}
		if len(cfg.AllowDomains) > 0 && !matchesDomainList(host, cfg.AllowDomains) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	return strings.ToLower(h)
}

func matchesDomainList(host string, patterns []string) bool {
	for _, p := range patterns {
		if matchDomain(host, p) {
			return true
		}
	}
	return false
}

func matchDomain(host, pattern string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]
		return host == pattern[2:] || strings.HasSuffix(host, suffix)
	}
	return host == pattern
}

func formatResultsFiltered(query string, results []searchResult, original int, cfg *config.WebSearchConfig) Result {
	var b strings.Builder
	fmt.Fprintf(&b, "Search results for %q:\n\n", query)
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n", i+1, r.Title)
		fmt.Fprintf(&b, "   %s\n", r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
		b.WriteString("\n")
	}
	if cfg != nil && (len(cfg.AllowDomains) > 0 || len(cfg.DenyDomains) > 0) {
		filtered := original - len(results)
		if filtered > 0 {
			fmt.Fprintf(&b, "(%d result(s) filtered by domain restrictions)\n", filtered)
		}
	}
	return Result{
		Output: b.String(),
		Title:  fmt.Sprintf("web search (%d results)", len(results)),
		Meta:   map[string]any{"query": query, "results": len(results)},
	}
}

// --- provider implementations ---

func bingSearch(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	if err := waitHostGap(ctx, "www.bing.com", 2*time.Second); err != nil {
		return nil, err
	}
	u := "https://www.bing.com/search?q=" + url.QueryEscape(query) + "&setlang=en"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := webSearchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("bing: HTTP 429 rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bing: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseBytes))
	if err != nil {
		return nil, err
	}
	html := string(body)

	var results []searchResult
	for _, match := range bingResultRe.FindAllStringSubmatch(html, -1) {
		block := match[1]
		titleMatch := bingTitleRe.FindStringSubmatch(block)
		if titleMatch == nil {
			continue
		}
		title := strings.TrimSpace(tagRe.ReplaceAllString(titleMatch[2], ""))
		rawURL := titleMatch[1]

		finalURL := decodeBingURL(rawURL)
		snippet := ""
		if sm := bingSnippetRe.FindStringSubmatch(block); sm != nil {
			snippet = strings.TrimSpace(tagRe.ReplaceAllString(sm[1], ""))
		}

		if title != "" && !strings.HasPrefix(finalURL, "https://www.bing.com/ck/") {
			results = append(results, searchResult{Title: title, URL: finalURL, Snippet: snippet})
			if len(results) >= maxResults {
				break
			}
		}
	}

	return results, nil
}

func decodeBingURL(rawURL string) string {
	if !strings.Contains(rawURL, "bing.com/ck/") {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if v := u.Query().Get("u"); v != "" {
		if decoded, err := url.QueryUnescape(v); err == nil {
			return decoded
		}
	}
	return rawURL
}

func duckDuckGoLite(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	if err := waitHostGap(ctx, "lite.duckduckgo.com", 2*time.Second); err != nil {
		return nil, err
	}
	u := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := webSearchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("ddg-lite: HTTP 429 rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ddg-lite: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseBytes))
	if err != nil {
		return nil, err
	}
	html := string(body)

	links := ddgLinkRe.FindAllStringSubmatch(html, -1)
	snippets := ddgSnippetRe.FindAllStringSubmatch(html, -1)

	var results []searchResult
	for i, m := range links {
		if i >= maxResults {
			break
		}
		title := strings.TrimSpace(tagRe.ReplaceAllString(m[2], ""))
		if title == "" {
			continue
		}

		rawURL := m[1]
		finalURL := rawURL
		if strings.Contains(rawURL, "duckduckgo.com/l/") {
			if u, err := url.Parse(rawURL); err == nil {
				if v := u.Query().Get("uddg"); v != "" {
					if decoded, err := url.QueryUnescape(v); err == nil {
						finalURL = decoded
					}
				}
			}
		}

		snippet := ""
		if i < len(snippets) {
			snippet = strings.TrimSpace(tagRe.ReplaceAllString(snippets[i][1], ""))
		}

		results = append(results, searchResult{Title: title, URL: finalURL, Snippet: snippet})
	}

	return results, nil
}

func braveSearch(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	if err := waitHostGap(ctx, "search.brave.com", 2*time.Second); err != nil {
		return nil, err
	}
	u := "https://search.brave.com/search?q=" + url.QueryEscape(query) + "&source=web"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := webSearchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("brave: HTTP 429 rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseBytes))
	if err != nil {
		return nil, err
	}
	html := string(body)

	links := braveLinkRe.FindAllStringSubmatch(html, -1)
	snippets := braveSnippetRe.FindAllStringSubmatch(html, -1)

	var results []searchResult
	snippetIdx := 0
	for _, m := range links {
		rawURL := m[1]
		if !strings.HasPrefix(rawURL, "http") {
			continue
		}
		if strings.Contains(rawURL, "search.brave.com") {
			continue
		}

		titleBlock := m[2]
		titleEnd := braveTitleEndRe.FindStringIndex(titleBlock)
		if titleEnd != nil {
			titleBlock = titleBlock[:titleEnd[0]]
		}
		title := strings.TrimSpace(tagRe.ReplaceAllString(titleBlock, ""))
		if title == "" || len(title) < 5 {
			continue
		}

		snippet := ""
		if snippetIdx < len(snippets) {
			snippet = strings.TrimSpace(tagRe.ReplaceAllString(snippets[snippetIdx][1], ""))
			snippetIdx++
		}

		results = append(results, searchResult{Title: title, URL: rawURL, Snippet: snippet})
		if len(results) >= maxResults {
			break
		}
	}

	return results, nil
}

func searXNGSearch(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	inst := getSearxInstance()
	if inst == nil {
		return nil, fmt.Errorf("all SearXNG instances failed")
	}

	if err := waitHostGap(ctx, inst.URL, 2*time.Second); err != nil {
		return nil, err
	}
	u := inst.URL + "/search?q=" + url.QueryEscape(query) + "&format=json&language=en"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "application/json")

	searchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req = req.WithContext(searchCtx)
	resp, err := webSearchClient.Do(req)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			markSearxFailure(inst)
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		resp.Body.Close()
		markSearxFailure(inst)
		return nil, fmt.Errorf("searxng: HTTP 429 rate limited")
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		markSearxFailure(inst)
		return nil, fmt.Errorf("searxng: HTTP %d", resp.StatusCode)
	}

	var data struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseBytes+1))
	if err != nil {
		markSearxFailure(inst)
		return nil, err
	}
	if len(body) > maxSearchResponseBytes {
		markSearxFailure(inst)
		return nil, fmt.Errorf("searxng: response exceeds %d bytes", maxSearchResponseBytes)
	}
	if err := json.Unmarshal(body, &data); err != nil {
		markSearxFailure(inst)
		return nil, err
	}

	var results []searchResult
	for _, r := range data.Results {
		if r.URL != "" {
			results = append(results, searchResult{
				Title:   cleanHTML(r.Title),
				URL:     r.URL,
				Snippet: cleanHTML(r.Content),
			})
			if len(results) >= maxResults {
				break
			}
		}
	}

	if len(results) > 0 {
		markSearxSuccess(inst)
		return results, nil
	}

	markSearxFailure(inst)
	return nil, fmt.Errorf("searxng: no results")
}

func duckDuckGoInstant(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	if err := waitHostGap(ctx, "api.duckduckgo.com", 2*time.Second); err != nil {
		return nil, err
	}
	u := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)

	resp, err := webSearchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("ddginstant: HTTP 429 rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var data struct {
		AbstractText string `json:"AbstractText"`
		AbstractURL  string `json:"AbstractURL"`
		Results      []struct {
			FirstURL string `json:"FirstURL"`
			Text     string `json:"Text"`
		} `json:"Results"`
		RelatedTopics []struct {
			FirstURL string `json:"FirstURL"`
			Text     string `json:"Text"`
			Topics   []struct {
				FirstURL string `json:"FirstURL"`
				Text     string `json:"Text"`
			} `json:"Topics"`
		} `json:"RelatedTopics"`
		Heading string `json:"Heading"`
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSearchResponseBytes {
		return nil, fmt.Errorf("ddginstant: response exceeds %d bytes", maxSearchResponseBytes)
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	var results []searchResult
	if data.AbstractText != "" {
		results = append(results, searchResult{
			Title:   data.Heading,
			URL:     data.AbstractURL,
			Snippet: data.AbstractText,
		})
	}

	for _, r := range data.Results {
		if r.Text != "" && r.FirstURL != "" {
			results = append(results, searchResult{
				Title:   cleanHTML(r.Text),
				URL:     r.FirstURL,
				Snippet: cleanHTML(r.Text),
			})
		}
	}

	for _, rt := range data.RelatedTopics {
		if rt.Text != "" && rt.FirstURL != "" {
			results = append(results, searchResult{
				Title:   cleanHTML(rt.Text),
				URL:     rt.FirstURL,
				Snippet: cleanHTML(rt.Text),
			})
		}
		for _, t := range rt.Topics {
			if t.Text != "" && t.FirstURL != "" {
				results = append(results, searchResult{
					Title:   cleanHTML(t.Text),
					URL:     t.FirstURL,
					Snippet: cleanHTML(t.Text),
				})
			}
		}
	}

	return results, nil
}

func cleanHTML(s string) string {
	s = cleanHTMLReplacer.Replace(s)

	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		switch r {
		case ' ', '\n', '\r', '	', '\u00a0':
			if b.Len() > 0 {
				pendingSpace = true
			}
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			pendingSpace = false
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
