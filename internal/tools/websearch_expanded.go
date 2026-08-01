package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"rick/internal/config"
)

const (
	defaultDDGRegion       = "wt-wt"
	defaultDDGSafeSearch   = "moderate"
	defaultDDGBackend      = "lite"
	defaultOllamaSearchURL = "https://ollama.com/api"
	defaultExaSearchURL    = "https://api.exa.ai"
	defaultTavilySearchURL = "https://api.tavily.com"
	defaultMaxParallel     = 4
)

type searchOptions struct {
	Region               string
	SafeSearch           string
	TimeRange            string
	DDGBackend           string
	ExaType              string
	Livecrawl            string
	IncludeDomains       []string
	ExcludeDomains       []string
	TavilyTimeRange      string
	TavilyIncludeDomains []string
	TavilyExcludeDomains []string
}

func (o searchOptions) cacheVariant() string {
	var b strings.Builder
	b.WriteString("region=")
	b.WriteString(o.Region)
	b.WriteString("&safe=")
	b.WriteString(o.SafeSearch)
	b.WriteString("&time=")
	b.WriteString(o.TimeRange)
	b.WriteString("&backend=")
	b.WriteString(o.DDGBackend)
	b.WriteString("&exa_type=")
	b.WriteString(o.ExaType)
	b.WriteString("&livecrawl=")
	b.WriteString(o.Livecrawl)
	b.WriteString("&include=")
	b.WriteString(strings.Join(o.IncludeDomains, ","))
	b.WriteString("&exclude=")
	b.WriteString(strings.Join(o.ExcludeDomains, ","))
	b.WriteString("&tavily_time=")
	b.WriteString(o.TavilyTimeRange)
	b.WriteString("&tavily_include=")
	b.WriteString(strings.Join(o.TavilyIncludeDomains, ","))
	b.WriteString("&tavily_exclude=")
	b.WriteString(strings.Join(o.TavilyExcludeDomains, ","))
	return b.String()
}

func providerConfig(cfg *config.WebSearchConfig, name string) config.WebSearchProviderConfig {
	if cfg == nil || cfg.Providers == nil {
		return config.WebSearchProviderConfig{}
	}
	name = strings.ToLower(name)
	aliases := []string{name}
	if name == "ddg" || name == "ddglite" || name == "ddginstant" || name == "duckduckgo" {
		aliases = append(aliases, "duckduckgo", "ddg")
	}
	for _, alias := range aliases {
		if value, ok := cfg.Providers[alias]; ok {
			return value
		}
	}
	return config.WebSearchProviderConfig{}
}

func providerConfigured(cfg *config.WebSearchConfig, name string) bool {
	if cfg == nil || cfg.Providers == nil {
		return false
	}
	name = strings.ToLower(name)
	if _, ok := cfg.Providers[name]; ok {
		return true
	}
	if name == "ddg" || name == "ddglite" || name == "ddginstant" || name == "duckduckgo" {
		_, ok := cfg.Providers["duckduckgo"]
		if !ok {
			_, ok = cfg.Providers["ddg"]
		}
		return ok
	}
	return false
}

func providerEnabled(cfg *config.WebSearchConfig, name string) bool {
	name = strings.ToLower(name)
	if name == "ddglite" || name == "ddginstant" {
		name = "duckduckgo"
	}
	provider := providerConfig(cfg, name)
	if provider.Enabled != nil {
		return *provider.Enabled
	}
	switch name {
	case "duckduckgo":
		return true
	default:
		return providerConfigured(cfg, name)
	}
}

func envAPIKey(name, configured string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	return strings.TrimSpace(os.Getenv(name))
}

func providerAPIKey(provider config.WebSearchProviderConfig, fallbackEnv string) string {
	envName := firstNonEmpty(provider.APIKeyEnv, fallbackEnv)
	return envAPIKey(envName, provider.APIKey)
}

func (t WebSearchTool) searchOptions(args searchArgs) (searchOptions, error) {
	ddg := providerConfig(t.Restrictions, "duckduckgo")
	exa := providerConfig(t.Restrictions, "exa")
	tavily := providerConfig(t.Restrictions, "tavily")
	options := searchOptions{
		Region:          firstNonEmpty(args.Region, ddg.Region, defaultDDGRegion),
		SafeSearch:      firstNonEmpty(args.SafeSearch, ddg.SafeSearch, defaultDDGSafeSearch),
		TimeRange:       firstNonEmpty(args.TimeRange, ddg.TimeRange),
		DDGBackend:      firstNonEmpty(args.DDGBackend, ddg.Backend, defaultDDGBackend),
		ExaType:         firstNonEmpty(args.ExaType, exa.Type, "auto"),
		Livecrawl:       firstNonEmpty(args.Livecrawl, exa.Livecrawl, "fallback"),
		IncludeDomains:  append([]string(nil), args.IncludeDomains...),
		ExcludeDomains:  append([]string(nil), args.ExcludeDomains...),
		TavilyTimeRange: firstNonEmpty(args.TimeRange, tavily.TimeRange),
	}
	if len(options.IncludeDomains) == 0 {
		options.IncludeDomains = append([]string(nil), exa.IncludeDomains...)
	}
	if len(options.ExcludeDomains) == 0 {
		options.ExcludeDomains = append([]string(nil), exa.ExcludeDomains...)
	}
	options.TavilyIncludeDomains = append([]string(nil), options.IncludeDomains...)
	options.TavilyExcludeDomains = append([]string(nil), options.ExcludeDomains...)
	if len(args.IncludeDomains) == 0 {
		options.TavilyIncludeDomains = append([]string(nil), tavily.IncludeDomains...)
	}
	if len(args.ExcludeDomains) == 0 {
		options.TavilyExcludeDomains = append([]string(nil), tavily.ExcludeDomains...)
	}
	if err := validateSearchOptions(&options); err != nil {
		return searchOptions{}, err
	}
	return options, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func validateSearchOptions(options *searchOptions) error {
	options.Region = strings.ToLower(strings.TrimSpace(options.Region))
	options.SafeSearch = strings.ToLower(strings.TrimSpace(options.SafeSearch))
	options.TimeRange = strings.ToLower(strings.TrimSpace(options.TimeRange))
	options.DDGBackend = strings.ToLower(strings.TrimSpace(options.DDGBackend))
	options.ExaType = strings.ToLower(strings.TrimSpace(options.ExaType))
	options.Livecrawl = strings.ToLower(strings.TrimSpace(options.Livecrawl))
	if options.Region == "" {
		options.Region = defaultDDGRegion
	}
	if _, ok := duckDuckGoSafeSearchValue(options.SafeSearch); !ok {
		return fmt.Errorf("safe_search must be off, moderate, or strict")
	}
	if options.TimeRange != "" {
		if _, ok := duckDuckGoTimeRangeValue(options.TimeRange); !ok {
			return fmt.Errorf("time_range must be day, week, month, or year")
		}
	}
	if options.DDGBackend != "lite" && options.DDGBackend != "instant" && options.DDGBackend != "auto" {
		return fmt.Errorf("ddg_backend must be lite, instant, or auto")
	}
	if options.ExaType != "auto" && options.ExaType != "fast" && options.ExaType != "deep" {
		return fmt.Errorf("exa_type must be auto, fast, or deep")
	}
	switch options.Livecrawl {
	case "fallback", "preferred", "always", "never":
	default:
		return fmt.Errorf("livecrawl must be fallback, preferred, always, or never")
	}
	return nil
}

type configuredSearchProvider struct {
	name        string
	weight      float64
	priority    int
	globalLimit int
	config      config.WebSearchProviderConfig
	fn          func(context.Context, string, int) ([]searchResult, error)
}

func (t WebSearchTool) configuredProviders(options searchOptions, forced string) []configuredSearchProvider {
	return t.configuredProvidersFor(t.Restrictions, options, forced)
}

func (t WebSearchTool) configuredProvidersFor(cfg *config.WebSearchConfig, options searchOptions, forced string) []configuredSearchProvider {
	providers := make([]configuredSearchProvider, 0, 16)
	add := func(name string, weight float64, fn func(context.Context, string, int) ([]searchResult, error)) {
		if forced != "" && forced != "auto" && forced != "parallel" && forced != name {
			return
		}
		provider := providerConfig(cfg, name)
		if provider.CacheTTLSeconds == 0 && cfg != nil && cfg.CacheTTLSeconds > 0 {
			provider.CacheTTLSeconds = cfg.CacheTTLSeconds
		}
		priority := provider.Priority
		if priority <= 0 {
			priority = defaultProviderPriority(name)
		}
		globalLimit := defaultGlobalWebConcurrency
		if cfg != nil && cfg.MaxConcurrent > 0 {
			globalLimit = cfg.MaxConcurrent
		}
		if globalLimit > defaultGlobalWebConcurrency {
			globalLimit = defaultGlobalWebConcurrency
		}
		providers = append(providers, configuredSearchProvider{name: name, weight: weight, priority: priority, globalLimit: globalLimit, config: provider, fn: fn})
	}

	if providerEnabled(cfg, "ollama") || forced == "ollama" {
		ollama := providerConfig(cfg, "ollama")
		add("ollama", providerWeight(ollama.Weight, 1.05), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			return ollamaSearch(ctx, query, maxResults, ollama)
		})
	}
	if providerEnabled(cfg, "duckduckgo") || forced == "duckduckgo" || forced == "ddg" || forced == "ddglite" {
		ddg := providerConfig(cfg, "duckduckgo")
		add("duckduckgo", providerWeight(ddg.Weight, 1.0), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			return duckDuckGoSearch(ctx, query, maxResults, ddg, options)
		})
	}
	if forced == "bing" {
		add("bing", 0.1, func(context.Context, string, int) ([]searchResult, error) {
			return nil, &ProviderError{Provider: "bing", Class: ProviderNotSupported, Message: "Bing Search API is retired; remove the bing provider", TryFallback: true}
		})
	}
	if providerEnabled(cfg, "brave") || forced == "brave" {
		brave := providerConfig(cfg, "brave")
		add("brave", providerWeight(brave.Weight, 1.1), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			return braveConfiguredSearch(ctx, query, maxResults, brave)
		})
	}
	if providerEnabled(cfg, "searxng") || forced == "searxng" {
		searx := providerConfig(cfg, "searxng")
		add("searxng", providerWeight(searx.Weight, 0.9), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			return searXNGSearchWithConfig(ctx, query, maxResults, searx)
		})
	}
	if forced == "ddginstant" {
		ddg := providerConfig(cfg, "duckduckgo")
		add("ddginstant", providerWeight(ddg.Weight, 0.75), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			return duckDuckGoInstantWithOptions(ctx, query, maxResults, ddg, options)
		})
	} else if (forced == "" || forced == "auto" || forced == "parallel") && providerConfigured(cfg, "ddginstant") {
		ddg := providerConfig(cfg, "duckduckgo")
		add("ddginstant", providerWeight(ddg.Weight, 0.75), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			return duckDuckGoInstantWithOptions(ctx, query, maxResults, ddg, options)
		})
	}
	if providerEnabled(cfg, "exa") || forced == "exa" {
		exa := providerConfig(cfg, "exa")
		add("exa", providerWeight(exa.Weight, 1.35), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			return exaSearch(ctx, query, maxResults, exa, options)
		})
	}
	if providerEnabled(cfg, "tavily") || forced == "tavily" {
		tavily := providerConfig(cfg, "tavily")
		add("tavily", providerWeight(tavily.Weight, 1.2), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			return tavilySearch(ctx, query, maxResults, tavily, options)
		})
	}
	for _, name := range []string{"serper", "you", "firecrawl", "serpapi", "google_cse", "jina", "gdelt", "mediawiki", "arxiv", "crossref", "openalex", "github", "stackexchange", "hackernews", "archive"} {
		if providerEnabled(cfg, name) || forced == name {
			provider := providerConfig(cfg, name)
			providerName := name
			add(providerName, providerWeight(provider.Weight, defaultProviderWeight(providerName)), func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
				return optionalProviderSearch(ctx, providerName, query, maxResults, provider)
			})
		}
	}
	sort.SliceStable(providers, func(i, j int) bool {
		if providers[i].priority != providers[j].priority {
			return providers[i].priority < providers[j].priority
		}
		return providers[i].name < providers[j].name
	})
	return providers
}

func providerWeight(configured, fallback float64) float64 {
	if configured > 0 {
		return configured
	}
	return fallback
}

type providerBatch struct {
	name    string
	weight  float64
	results []searchResult
	err     error
}

func (t WebSearchTool) runProvider(ctx context.Context, provider configuredSearchProvider, query string, maxResults int, variant string) providerBatch {
	if cached, ok := cacheGetVariant(provider.name, query, maxResults, variant); ok {
		return providerBatch{name: provider.name, weight: provider.weight, results: normalizeResults(cached, maxResults)}
	}
	endpoint := provider.config.BaseURL
	if endpoint == "" {
		endpoint = provider.name
	}
	healthKeyValue, healthErr := beginProviderHealth(provider.name, endpoint)
	if healthErr != nil {
		return providerBatch{name: provider.name, weight: provider.weight, err: healthErr}
	}
	started := time.Now()
	release, err := sharedWebSearchScheduler.acquire(ctx, provider.name, provider.config.MaxConcurrency, provider.config.MaxRPM, provider.globalLimit)
	if err != nil {
		typed := providerErrorFrom(err, provider.name)
		finishProviderHealth(healthKeyValue, typed, 0, time.Since(started))
		return providerBatch{name: provider.name, weight: provider.weight, err: typed}
	}
	defer release()
	results, err := t.tryWithRetry(ctx, provider.fn, query, maxResults)
	if err != nil {
		typed := providerErrorFrom(err, provider.name)
		finishProviderHealth(healthKeyValue, typed, 0, time.Since(started))
		return providerBatch{name: provider.name, weight: provider.weight, err: typed}
	}
	results = normalizeResults(results, maxResults)
	finishProviderHealth(healthKeyValue, nil, len(results), time.Since(started))
	if len(results) > 0 {
		ttl := cacheTTL[provider.name]
		if provider.config.CacheTTLSeconds > 0 {
			ttl = time.Duration(provider.config.CacheTTLSeconds) * time.Second
		}
		if ttl == 0 {
			ttl = 300 * time.Second
		}
		cachePutVariant(provider.name, query, maxResults, results, ttl, variant)
	}
	return providerBatch{name: provider.name, weight: provider.weight, results: results}
}

func (t WebSearchTool) runParallelProviders(ctx context.Context, providers []configuredSearchProvider, query string, maxResults, maxParallel int, variant string) []providerBatch {
	if maxParallel <= 0 || maxParallel > len(providers) {
		maxParallel = len(providers)
	}
	if maxParallel > defaultMaxParallel {
		maxParallel = defaultMaxParallel
	}
	semaphore := make(chan struct{}, maxParallel)
	batches := make([]providerBatch, len(providers))
	var wg sync.WaitGroup
	for i, provider := range providers {
		i, provider := i, provider
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				batches[i] = providerBatch{name: provider.name, weight: provider.weight, err: ctx.Err()}
				return
			}
			defer func() { <-semaphore }()
			batches[i] = t.runProvider(ctx, provider, query, maxResults, variant)
		}()
	}
	wg.Wait()
	return batches
}

type mergedResult struct {
	result    searchResult
	score     float64
	votes     int
	firstSeen int
	providers map[string]struct{}
}

func mergeSearchResults(batches []providerBatch, maxResults int) []searchResult {
	merged := make(map[string]*mergedResult)
	order := make([]*mergedResult, 0)
	sequence := 0
	for _, batch := range batches {
		for rank, result := range normalizeResults(batch.results, maxResults) {
			key := resultKey(result)
			if key == "" {
				continue
			}
			contribution := batch.weight / float64(rank+1)
			item, ok := merged[key]
			if !ok {
				item = &mergedResult{
					result:    result,
					firstSeen: sequence,
					providers: map[string]struct{}{},
				}
				merged[key] = item
				order = append(order, item)
				sequence++
			} else if len(result.Snippet) > len(item.result.Snippet) {
				item.result.Snippet = result.Snippet
			}
			if _, ok := item.providers[batch.name]; !ok {
				item.votes++
				item.score += contribution
				item.providers[batch.name] = struct{}{}
				if item.votes > 1 {
					item.score += 0.45 * batch.weight
				}
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].score != order[j].score {
			return order[i].score > order[j].score
		}
		if order[i].votes != order[j].votes {
			return order[i].votes > order[j].votes
		}
		return order[i].firstSeen < order[j].firstSeen
	})
	if maxResults > 0 && len(order) > maxResults {
		order = order[:maxResults]
	}
	results := make([]searchResult, 0, len(order))
	for _, item := range order {
		results = append(results, item.result)
	}
	return results
}

func resultKey(result searchResult) string {
	if result.URL != "" {
		return "url:" + canonicalURL(result.URL)
	}
	title := strings.ToLower(cleanHTML(result.Title))
	title = strings.Join(strings.Fields(title), " ")
	if title != "" {
		return "title:" + title
	}
	return ""
}

func normalizeResults(results []searchResult, maxResults int) []searchResult {
	out := make([]searchResult, 0, len(results))
	seen := make(map[string]struct{})
	for _, result := range results {
		result.Title = cleanHTML(result.Title)
		result.URL = normalizeResultURL(result.URL)
		result.Snippet = cleanHTML(result.Snippet)
		if result.Title == "" || result.URL == "" {
			continue
		}
		key := resultKey(result)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, result)
		if maxResults > 0 && len(out) >= maxResults {
			break
		}
	}
	return out
}

func normalizeResultURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "&amp;", "&")
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	for i := 0; i < 2; i++ {
		parsed, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		host := strings.ToLower(parsed.Hostname())
		var redirected string
		switch {
		case strings.Contains(host, "duckduckgo.com") && strings.HasPrefix(parsed.Path, "/l/"):
			redirected = parsed.Query().Get("uddg")
		case strings.Contains(host, "bing.com") && strings.Contains(parsed.Path, "/ck/"):
			redirected = parsed.Query().Get("u")
		case strings.Contains(host, "google.") && parsed.Path == "/url":
			redirected = firstNonEmpty(parsed.Query().Get("q"), parsed.Query().Get("url"))
		}
		if redirected == "" {
			break
		}
		if decoded, err := url.QueryUnescape(redirected); err == nil {
			raw = decoded
		} else {
			raw = redirected
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "gclid" || lower == "fbclid" || lower == "msclkid" || lower == "uddg" || lower == "ck" || lower == "rut" {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func canonicalURL(raw string) string {
	parsed, err := url.Parse(normalizeResultURL(raw))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

func duckDuckGoSafeSearchValue(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "-2":
		return "-2", true
	case "moderate", "-1":
		return "-1", true
	case "strict", "on", "1":
		return "1", true
	default:
		return "", false
	}
}

func duckDuckGoTimeRangeValue(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "day", "d":
		return "d", true
	case "week", "w":
		return "w", true
	case "month", "m":
		return "m", true
	case "year", "y":
		return "y", true
	default:
		return "", false
	}
}

func buildDuckDuckGoURL(endpoint, query string, options searchOptions, instant bool) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	values.Set("q", query)
	if instant {
		values.Set("format", "json")
		values.Set("no_html", "1")
		values.Set("skip_disambig", "1")
	} else {
		values.Set("kl", options.Region)
		safe, _ := duckDuckGoSafeSearchValue(options.SafeSearch)
		values.Set("kp", safe)
		if freshness, ok := duckDuckGoTimeRangeValue(options.TimeRange); ok {
			values.Set("df", freshness)
		} else {
			values.Del("df")
		}
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func duckDuckGoSearch(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig, options searchOptions) ([]searchResult, error) {
	backend := firstNonEmpty(options.DDGBackend, provider.Backend, defaultDDGBackend)
	if backend == "instant" {
		return duckDuckGoInstantWithOptions(ctx, query, maxResults, provider, options)
	}
	return duckDuckGoLiteWithOptions(ctx, query, maxResults, provider, options)
}

func duckDuckGoLiteWithOptions(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig, options searchOptions) ([]searchResult, error) {
	endpoint := firstNonEmpty(provider.BaseURL, "https://lite.duckduckgo.com/lite/")
	requestURL, err := buildDuckDuckGoURL(endpoint, query, options, false)
	if err != nil {
		return nil, err
	}
	body, err := doSearchHTTP(ctx, http.MethodGet, requestURL, nil, map[string]string{
		"Accept":          "text/html,application/xhtml+xml",
		"Accept-Language": "en-US,en;q=0.9",
	}, "ddg-lite")
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoLiteHTML(body, maxResults), nil
}

func duckDuckGoInstantWithOptions(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig, options searchOptions) ([]searchResult, error) {
	endpoint := firstNonEmpty(provider.BaseURL, "https://api.duckduckgo.com/")
	requestURL, err := buildDuckDuckGoURL(endpoint, query, options, true)
	if err != nil {
		return nil, err
	}
	body, err := doSearchHTTP(ctx, http.MethodGet, requestURL, nil, map[string]string{
		"Accept": "application/json",
	}, "ddginstant")
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoInstantJSON(body, maxResults)
}

func parseDuckDuckGoLiteHTML(body []byte, maxResults int) []searchResult {
	type link struct {
		title string
		url   string
	}
	links := make([]link, 0)
	for _, match := range ddgLiteAnchorMatches(string(body)) {
		attrs := parseHTMLAttributes(match[0])
		classes := strings.Fields(attrs["class"])
		if !containsString(classes, "result-link") {
			continue
		}
		rawURL := strings.TrimSpace(attrs["href"])
		if rawURL == "" || !strings.Contains(rawURL, "duckduckgo.com/l/") {
			continue
		}
		links = append(links, link{title: cleanHTML(match[1]), url: rawURL})
	}
	snippets := ddgLiteSnippetMatches(string(body))
	results := make([]searchResult, 0, len(links))
	for i, item := range links {
		if item.title == "" {
			continue
		}
		resultURL := normalizeResultURL(item.url)
		if isDuckDuckGoAdvertisement(item.url, resultURL) {
			continue
		}
		snippet := ""
		if i < len(snippets) {
			snippet = snippets[i]
		}
		results = append(results, searchResult{Title: item.title, URL: resultURL, Snippet: snippet})
		if maxResults > 0 && len(results) >= maxResults {
			break
		}
	}
	return results
}

func ddgLiteAnchorMatches(body string) [][2]string {
	return extractHTMLPairs(body, "a")
}

func ddgLiteSnippetMatches(body string) []string {
	matches := extractHTMLPairs(body, "td")
	snippets := make([]string, 0)
	for _, match := range matches {
		attrs := parseHTMLAttributes(match[0])
		if containsString(strings.Fields(attrs["class"]), "result-snippet") {
			snippets = append(snippets, cleanHTML(match[1]))
		}
	}
	return snippets
}

func extractHTMLPairs(body, element string) [][2]string {
	lower := strings.ToLower(body)
	open := "<" + element
	close := "</" + element + ">"
	pairs := make([][2]string, 0)
	for start := 0; start < len(body); {
		relative := strings.Index(lower[start:], open)
		if relative < 0 {
			break
		}
		tagStart := start + relative
		tagEndRelative := strings.IndexByte(body[tagStart:], '>')
		if tagEndRelative < 0 {
			break
		}
		tagEnd := tagStart + tagEndRelative
		endRelative := strings.Index(lower[tagEnd+1:], close)
		if endRelative < 0 {
			break
		}
		end := tagEnd + 1 + endRelative
		pairs = append(pairs, [2]string{body[tagStart : tagEnd+1], body[tagEnd+1 : end]})
		start = end + len(close)
	}
	return pairs
}

func parseHTMLAttributes(tag string) map[string]string {
	attrs := make(map[string]string)
	for i := 0; i < len(tag); {
		for i < len(tag) && (tag[i] == '<' || tag[i] == '>' || tag[i] == '/' || tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\r' || tag[i] == '\n') {
			i++
		}
		nameStart := i
		for i < len(tag) && tag[i] != '=' && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '>' {
			i++
		}
		if nameStart == i {
			i++
			continue
		}
		name := strings.ToLower(tag[nameStart:i])
		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\r' || tag[i] == '\n') {
			i++
		}
		if i >= len(tag) || tag[i] != '=' {
			continue
		}
		i++
		for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\r' || tag[i] == '\n') {
			i++
		}
		if i >= len(tag) || (tag[i] != '\'' && tag[i] != '"') {
			continue
		}
		quote := tag[i]
		i++
		valueStart := i
		for i < len(tag) && tag[i] != quote {
			i++
		}
		attrs[name] = tag[valueStart:i]
		if i < len(tag) {
			i++
		}
	}
	return attrs
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func isDuckDuckGoAdvertisement(rawURL, normalizedURL string) bool {
	lower := strings.ToLower(rawURL + " " + normalizedURL)
	return strings.Contains(lower, "ad_provider") || strings.Contains(lower, "/y.js?") || strings.Contains(lower, "/aclick")
}

func parseDuckDuckGoInstantJSON(body []byte, maxResults int) ([]searchResult, error) {
	var data struct {
		AbstractText string `json:"AbstractText"`
		AbstractURL  string `json:"AbstractURL"`
		Heading      string `json:"Heading"`
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
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	results := make([]searchResult, 0)
	appendResult := func(title, resultURL, snippet string) {
		if resultURL == "" || title == "" || (maxResults > 0 && len(results) >= maxResults) {
			return
		}
		results = append(results, searchResult{Title: title, URL: resultURL, Snippet: snippet})
	}
	if data.AbstractText != "" {
		appendResult(firstNonEmpty(data.Heading, data.AbstractURL), data.AbstractURL, data.AbstractText)
	}
	for _, item := range data.Results {
		appendResult(item.Text, item.FirstURL, item.Text)
	}
	for _, topic := range data.RelatedTopics {
		appendResult(topic.Text, topic.FirstURL, topic.Text)
		for _, nested := range topic.Topics {
			appendResult(nested.Text, nested.FirstURL, nested.Text)
		}
	}
	return results, nil
}

type ollamaSearchResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

func ollamaSearch(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig) ([]searchResult, error) {
	endpoint := joinProviderEndpoint(firstNonEmpty(provider.BaseURL, defaultOllamaSearchURL), "web_search")
	request := map[string]any{"query": query, "max_results": maxResults}
	headers := map[string]string{"Accept": "application/json"}
	if key := providerAPIKey(provider, "OLLAMA_API_KEY"); key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	body, err := doSearchJSON(ctx, http.MethodPost, endpoint, request, headers, "ollama")
	if err != nil {
		return nil, err
	}
	var response ollamaSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("ollama: invalid response: %w", err)
	}
	results := make([]searchResult, 0, len(response.Results))
	for _, item := range response.Results {
		results = append(results, searchResult{Title: item.Title, URL: item.URL, Snippet: item.Content})
	}
	return results, nil
}

type exaContentsRequest struct {
	Highlights  bool `json:"highlights,omitempty"`
	MaxAgeHours *int `json:"maxAgeHours,omitempty"`
}

type exaSearchRequest struct {
	Query          string              `json:"query"`
	Type           string              `json:"type,omitempty"`
	NumResults     int                 `json:"numResults,omitempty"`
	IncludeDomains []string            `json:"includeDomains,omitempty"`
	ExcludeDomains []string            `json:"excludeDomains,omitempty"`
	Contents       *exaContentsRequest `json:"contents,omitempty"`
}

type exaSearchResponse struct {
	Results []struct {
		Title      string   `json:"title"`
		URL        string   `json:"url"`
		Text       string   `json:"text"`
		Summary    string   `json:"summary"`
		Highlights []string `json:"highlights"`
	} `json:"results"`
}

func exaSearch(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig, options searchOptions) ([]searchResult, error) {
	key := providerAPIKey(provider, "EXA_API_KEY")
	if key == "" {
		return nil, newProviderError("exa", ProviderMissingConfig, "API key is not configured")
	}
	request := exaSearchRequest{
		Query:          query,
		Type:           options.ExaType,
		NumResults:     maxResults,
		IncludeDomains: append([]string(nil), options.IncludeDomains...),
		ExcludeDomains: append([]string(nil), options.ExcludeDomains...),
		Contents:       &exaContentsRequest{Highlights: true, MaxAgeHours: provider.MaxAgeHours},
	}
	switch options.Livecrawl {
	case "always", "preferred":
		age := 0
		request.Contents.MaxAgeHours = &age
	case "never":
		age := -1
		request.Contents.MaxAgeHours = &age
	}
	endpoint := joinProviderEndpoint(firstNonEmpty(provider.BaseURL, defaultExaSearchURL), "search")
	body, err := doSearchJSON(ctx, http.MethodPost, endpoint, request, map[string]string{
		"Accept":    "application/json",
		"x-api-key": key,
	}, "exa")
	if err != nil {
		return nil, err
	}
	var response exaSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("exa: invalid response: %w", err)
	}
	results := make([]searchResult, 0, len(response.Results))
	for _, item := range response.Results {
		snippet := firstNonEmpty(strings.Join(item.Highlights, " "), item.Summary, item.Text)
		results = append(results, searchResult{Title: item.Title, URL: item.URL, Snippet: snippet})
	}
	return results, nil
}

type tavilySearchRequest struct {
	Query          string   `json:"query"`
	SearchDepth    string   `json:"search_depth"`
	MaxResults     int      `json:"max_results"`
	Topic          string   `json:"topic"`
	TimeRange      string   `json:"time_range,omitempty"`
	IncludeAnswer  bool     `json:"include_answer"`
	IncludeContent bool     `json:"include_raw_content"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

type tavilySearchResponse struct {
	Results []struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
	} `json:"results"`
}

func tavilySearch(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig, options searchOptions) ([]searchResult, error) {
	key := providerAPIKey(provider, "TAVILY_API_KEY")
	if key == "" {
		return nil, newProviderError("tavily", ProviderMissingConfig, "API key is not configured")
	}
	tavilyTimeRange := firstNonEmpty(options.TavilyTimeRange, options.TimeRange)
	tavilyIncludeDomains := options.TavilyIncludeDomains
	if len(tavilyIncludeDomains) == 0 {
		tavilyIncludeDomains = options.IncludeDomains
	}
	tavilyExcludeDomains := options.TavilyExcludeDomains
	if len(tavilyExcludeDomains) == 0 {
		tavilyExcludeDomains = options.ExcludeDomains
	}
	request := tavilySearchRequest{
		Query:          query,
		SearchDepth:    "basic",
		MaxResults:     maxResults,
		Topic:          firstNonEmpty(provider.Type, "general"),
		IncludeDomains: append([]string(nil), tavilyIncludeDomains...),
		ExcludeDomains: append([]string(nil), tavilyExcludeDomains...),
	}
	if freshness, ok := duckDuckGoTimeRangeValue(tavilyTimeRange); ok {
		request.TimeRange = map[string]string{"d": "day", "w": "week", "m": "month", "y": "year"}[freshness]
	}
	endpoint := joinProviderEndpoint(firstNonEmpty(provider.BaseURL, defaultTavilySearchURL), "search")
	body, err := doSearchJSON(ctx, http.MethodPost, endpoint, request, map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + key,
	}, "tavily")
	if err != nil {
		return nil, err
	}
	var response tavilySearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("tavily: invalid response: %w", err)
	}
	results := make([]searchResult, 0, len(response.Results))
	for _, item := range response.Results {
		results = append(results, searchResult{Title: item.Title, URL: item.URL, Snippet: item.Content})
	}
	return results, nil
}

func joinProviderEndpoint(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func doSearchHTTP(ctx context.Context, method, endpoint string, body []byte, headers map[string]string, provider string) ([]byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, newProviderError(provider, ProviderPermanent, "invalid endpoint")
	}
	if err := waitHostGap(ctx, parsed.Hostname(), 2*time.Second); err != nil {
		return nil, providerErrorFrom(err, provider)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, newProviderError(provider, ProviderPermanent, "invalid request")
	}
	request.Header.Set("User-Agent", chromeUA)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := webSearchClient.Do(request)
	if err != nil {
		return nil, providerErrorFrom(err, provider)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, providerHTTPError(provider, response.StatusCode, response.Header)
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, maxSearchResponseBytes+1))
	if err != nil {
		return nil, providerErrorFrom(err, provider)
	}
	if len(result) > maxSearchResponseBytes {
		return nil, newProviderError(provider, ProviderInvalidResponse, "response exceeds configured limit")
	}
	return result, nil
}

func doSearchJSON(ctx context.Context, method, endpoint string, payload any, headers map[string]string, provider string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/json"
	return doSearchHTTP(ctx, method, endpoint, body, headers, provider)
}
