package tools

import (
	"container/list"
	"context"
	_ "embed"
	"encoding/json"
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

// --- rate limiting / host tracking ---

var (
	hostMu       sync.Mutex
	hostLastCall = map[string]time.Time{}
)

// waitHostGap blocks until at least d has elapsed since the last call to host.
func waitHostGap(host string, d time.Duration) {
	hostMu.Lock()
	last := hostLastCall[host]
	hostMu.Unlock()
	if elapsed := time.Since(last); elapsed < d {
		time.Sleep(d - elapsed)
	}
	hostMu.Lock()
	hostLastCall[host] = time.Now()
	hostMu.Unlock()
}

// --- LRU cache ---

type cacheKey struct {
	provider   string
	query      string
	maxResults int
}

type cacheEntry struct {
	key       cacheKey
	results   []searchResult
	expiresAt time.Time
}

var (
	cacheMu    sync.RWMutex
	cacheLRU   = list.New()
	cacheMap   = map[cacheKey]*list.Element{}
	cacheTTL   = map[string]time.Duration{
		"searxng":   60 * time.Second,
		"ddginstant": 60 * time.Second,
		"bing":      300 * time.Second,
		"ddglite":   300 * time.Second,
		"brave":     300 * time.Second,
	}
	cacheMaxLen = 100
)

func cacheGet(provider string, query string, maxResults int) ([]searchResult, bool) {
	cacheMu.RLock()
	elem, ok := cacheMap[cacheKey{provider, query, maxResults}]
	cacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	entry := elem.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		cacheMu.Lock()
		delete(cacheMap, cacheKey{provider, query, maxResults})
		cacheLRU.Remove(elem)
		cacheMu.Unlock()
		return nil, false
	}
	return entry.results, true
}

func cachePut(provider, query string, maxResults int, results []searchResult, ttl time.Duration) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	key := cacheKey{provider, query, maxResults}
	if elem, ok := cacheMap[key]; ok {
		cacheLRU.MoveToFront(elem)
		elem.Value.(*cacheEntry).results = results
		elem.Value.(*cacheEntry).expiresAt = time.Now().Add(ttl)
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
	elem := cacheLRU.PushFront(&cacheEntry{results: results, expiresAt: time.Now().Add(ttl), key: key})
	cacheMap[key] = elem
}

// --- SearXNG instance tracking ---

type searxInstance struct {
	URL         string
	ConsecFails int
	Disabled    bool
}

var (
	searxMu        sync.Mutex
	searxInstances []*searxInstance
	searxLogged    bool
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
	for _, si := range searxInstances {
		if !si.Disabled {
			return si
		}
	}
	return nil
}

func disableSearxInstance(si *searxInstance) {
	searxMu.Lock()
	si.Disabled = true
	searxMu.Unlock()
	if !searxLogged {
		fmt.Fprintf(os.Stderr, "websearch: all SearXNG instances exhausted, falling back to DDG Instant\n")
		searxLogged = true
	}
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

// --- tool ---

type WebSearchTool struct {
	Restrictions *config.WebSearchConfig
}

func (WebSearchTool) Name() string   { return "websearch" }
func (WebSearchTool) ReadOnly() bool { return true }

func (WebSearchTool) Description() string {
	return "Search the web with automatic provider fallback.\n" +
		"Tries Bing, DuckDuckGo Lite, Brave, SearXNG, then DuckDuckGo Instant Answer.\n" +
		"No API key required.\n" +
		"Use this for current events, facts, or any information beyond the model's training data.\n" +
		"Returns titles, URLs, and snippets for fast research."
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
		},
		"required": []string{"query"},
	}
}

type searchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func (t WebSearchTool) Run(ctx context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a searchArgs
	if err := json.Unmarshal(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Query == "" {
		return Errf("query is required"), nil
	}
	if a.MaxResults <= 0 {
		a.MaxResults = 5
	}
	if a.MaxResults > 10 {
		a.MaxResults = 10
	}
	maxSession := 10
	if t.Restrictions != nil && t.Restrictions.MaxSearchesPerSession > 0 {
		maxSession = t.Restrictions.MaxSearchesPerSession
	}
	sessionID := tc.SessionID
	allowed, n := checkBudget(sessionID, maxSession)
	if !allowed {
		return Result{
			Output: fmt.Sprintf("search budget exhausted (max %d per session)", maxSession),
			Title:  "web search (budget exceeded)",
			Meta:   map[string]any{"query": a.Query, "results": 0, "budget_exceeded": true},
		}, nil
	}
	if t.Restrictions != nil && t.Restrictions.MaxResults > 0 && a.MaxResults > t.Restrictions.MaxResults {
		a.MaxResults = t.Restrictions.MaxResults
	}

	providers := []struct {
		name string
		fn   func(context.Context, string, int) ([]searchResult, error)
	}{
		{"bing", bingSearch},
		{"ddglite", duckDuckGoLite},
		{"brave", braveSearch},
		{"searxng", searXNGSearch},
		{"ddginstant", duckDuckGoInstant},
	}

	for _, p := range providers {
		if cached, ok := cacheGet(p.name, a.Query, a.MaxResults); ok {
			original := len(cached)
			filtered := filterResults(cached, t.Restrictions)
			if len(filtered) == 0 {
				return Errf("all search results were filtered out by domain restrictions for query: %s", a.Query), nil
			}
			return formatResultsFiltered(a.Query, filtered, original, t.Restrictions), nil
		}

		results, err := t.tryWithRetry(ctx, p.fn, a.Query, a.MaxResults)
		if err == nil && len(results) > 0 {
			ttl, ok := cacheTTL[p.name]
			if !ok {
				ttl = 300 * time.Second
			}
			cachePut(p.name, a.Query, a.MaxResults, results, ttl)

			original := len(results)
			results = filterResults(results, t.Restrictions)
			if len(results) == 0 {
				return Errf("all search results were filtered out by domain restrictions for query: %s", a.Query), nil
			}
			res := formatResultsFiltered(a.Query, results, original, t.Restrictions)
			if n >= 3 {
				res.Output = fmt.Sprintf("[note: %d searches in this turn]\n%s", n, res.Output)
			}
			return res, nil
		}
	}

	return Errf("all web search providers failed for query: %s (try rephrasing or searching again later)", a.Query), nil
}

func (t WebSearchTool) tryWithRetry(ctx context.Context, fn func(context.Context, string, int) ([]searchResult, error), query string, maxResults int) ([]searchResult, error) {
	results, err := fn(ctx, query, maxResults)
	if err == nil {
		return results, nil
	}
	if strings.Contains(err.Error(), "HTTP 429") || strings.Contains(err.Error(), "429") {
		base := 2 * time.Second
		jitter := time.Duration(rand.Int63n(int64(1 * time.Second)))
		time.Sleep(base + jitter)
		results, err = fn(ctx, query, maxResults)
		if err == nil {
			return results, nil
		}
	}
	return nil, err
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
	waitHostGap("www.bing.com", 2*time.Second)
	u := "https://www.bing.com/search?q=" + url.QueryEscape(query) + "&setlang=en"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	html := string(body)

	resultRe := regexp.MustCompile(`(?s)<li class="b_algo"[^>]*>(.*?)</li>`)
	titleRe := regexp.MustCompile(`<h2><a[^>]*href="([^"]*)"[^>]*>(.*?)</a></h2>`)
	snippetRe := regexp.MustCompile(`(?s)<p[^>]*class="[^"]*b_lineclamp[^"]*"[^>]*>(.*?)</p>`)
	tagRe := regexp.MustCompile(`<[^>]*>`)

	var results []searchResult
	for _, match := range resultRe.FindAllStringSubmatch(html, -1) {
		block := match[1]
		titleMatch := titleRe.FindStringSubmatch(block)
		if titleMatch == nil {
			continue
		}
		title := strings.TrimSpace(tagRe.ReplaceAllString(titleMatch[2], ""))
		rawURL := titleMatch[1]

		finalURL := decodeBingURL(rawURL)
		snippet := ""
		if sm := snippetRe.FindStringSubmatch(block); sm != nil {
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
	waitHostGap("lite.duckduckgo.com", 2*time.Second)
	u := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	html := string(body)

	linkRe := regexp.MustCompile(`<a[^>]*rel="nofollow"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`(?s)<td[^>]*class="result-snippet"[^>]*>(.*?)</td>`)
	tagRe := regexp.MustCompile(`<[^>]*>`)

	links := linkRe.FindAllStringSubmatch(html, -1)
	snippets := snippetRe.FindAllStringSubmatch(html, -1)

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
	waitHostGap("search.brave.com", 2*time.Second)
	u := "https://search.brave.com/search?q=" + url.QueryEscape(query) + "&source=web"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	html := string(body)

	linkRe := regexp.MustCompile(`<a[^*]*href="(https?://[^"]*)"[^>]*>([\s\S]*?)</a>`)
	snippetRe := regexp.MustCompile(`<p[^>]*class="[^"]*(?:snippet|desc|description)[^"]*"[^>]*>([\s\S]*?)</p>`)
	titleEndRe := regexp.MustCompile(`</a>`)
	tagRe := regexp.MustCompile(`<[^>]*>`)

	links := linkRe.FindAllStringSubmatch(html, -1)
	snippets := snippetRe.FindAllStringSubmatch(html, -1)

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
		titleEnd := titleEndRe.FindStringIndex(titleBlock)
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

	waitHostGap(inst.URL, 2*time.Second)
	u := inst.URL + "/search?q=" + url.QueryEscape(query) + "&format=json&language=en"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		inst.ConsecFails++
		if inst.ConsecFails >= 2 {
			disableSearxInstance(inst)
		}
		return nil, err
	}

	if resp.StatusCode == 429 {
		resp.Body.Close()
		inst.ConsecFails++
		if inst.ConsecFails >= 2 {
			disableSearxInstance(inst)
		}
		return nil, fmt.Errorf("searxng: HTTP 429 rate limited")
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		inst.ConsecFails++
		if inst.ConsecFails >= 2 {
			disableSearxInstance(inst)
		}
		return nil, fmt.Errorf("searxng: HTTP %d", resp.StatusCode)
	}

	var data struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		resp.Body.Close()
		inst.ConsecFails++
		if inst.ConsecFails >= 2 {
			disableSearxInstance(inst)
		}
		return nil, err
	}
	resp.Body.Close()

	// Reset consecutive fails on success.
	inst.ConsecFails = 0

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
		return results, nil
	}

	return nil, fmt.Errorf("searxng: no results")
}

func duckDuckGoInstant(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	waitHostGap("api.duckduckgo.com", 2*time.Second)
	u := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", chromeUA)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
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
	s = strings.ReplaceAll(s, "<b>", "")
	s = strings.ReplaceAll(s, "</b>", "")
	s = strings.ReplaceAll(s, "<em>", "")
	s = strings.ReplaceAll(s, "</em>", "")
	s = strings.ReplaceAll(s, "<wbr>", "")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&apos;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
