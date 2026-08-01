package tools

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"rick/internal/config"
)

func safeEndpointVariant(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "configured"
	}
	return parsed.Scheme + "://" + parsed.Hostname() + parsed.EscapedPath()
}

func SupportedWebProviderIDs() []string {
	return []string{
		"duckduckgo", "ddginstant", "ollama", "searxng", "brave", "exa", "tavily", "serper", "you", "firecrawl", "serpapi", "google_cse", "jina", "gdelt", "mediawiki", "arxiv", "crossref", "openalex", "github", "stackexchange", "hackernews", "archive",
	}
}

func defaultProviderWeight(name string) float64 {
	if name == "mediawiki" || name == "hackernews" || name == "github" || name == "stackexchange" {
		return 0.8
	}
	if name == "jina" || name == "firecrawl" {
		return 1.1
	}
	return 1.0
}

func defaultProviderPriority(name string) int {
	priorities := map[string]int{
		"duckduckgo": 10, "ddginstant": 20, "searxng": 30, "ollama": 40,
		"brave": 50, "exa": 60, "tavily": 70, "serper": 80, "you": 90,
		"firecrawl": 100, "serpapi": 110, "google_cse": 120, "jina": 130,
		"gdelt": 140, "mediawiki": 150, "arxiv": 160, "crossref": 170,
		"openalex": 180, "github": 190, "stackexchange": 200, "hackernews": 210, "archive": 220,
	}
	if priority, ok := priorities[name]; ok {
		return priority
	}
	return 1000
}

type optionalJSONResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Link        string `json:"link"`
	HTMLURL     string `json:"html_url"`
	Snippet     string `json:"snippet"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Text        string `json:"text"`
	Name        string `json:"name"`
}

func optionalResult(item optionalJSONResult) searchResult {
	resultURL := firstNonEmpty(item.URL, item.Link, item.HTMLURL)
	return searchResult{Title: cleanHTML(firstNonEmpty(item.Title, item.Name)), URL: resultURL, Snippet: cleanHTML(firstNonEmpty(item.Snippet, item.Description, item.Content, item.Text))}
}

func decodeOptionalResults(body []byte, provider string) ([]searchResult, error) {
	var envelope struct {
		Results        []optionalJSONResult `json:"results"`
		Organic        []optionalJSONResult `json:"organic"`
		OrganicResults []optionalJSONResult `json:"organic_results"`
		Data           []optionalJSONResult `json:"data"`
		Web            struct {
			Results []optionalJSONResult `json:"results"`
		} `json:"web"`
		Items []optionalJSONResult `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, invalidResponseError(provider, "response is not valid JSON")
	}
	items := envelope.Results
	if len(items) == 0 {
		items = envelope.Organic
	}
	if len(items) == 0 {
		items = envelope.OrganicResults
	}
	if len(items) == 0 {
		items = envelope.Data
	}
	if len(items) == 0 {
		items = envelope.Web.Results
	}
	if len(items) == 0 {
		items = envelope.Items
	}
	results := make([]searchResult, 0, len(items))
	for _, item := range items {
		result := optionalResult(item)
		if result.URL != "" {
			results = append(results, result)
		}
	}
	return results, nil
}

func providerEndpoint(base, fallback, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return fallback
	}
	if suffix == "" || strings.HasSuffix(base, suffix) || strings.HasSuffix(base, "/search.json") || strings.HasSuffix(base, "/search") {
		return base
	}
	return base + "/" + strings.TrimLeft(suffix, "/")
}

func braveConfiguredSearch(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig) ([]searchResult, error) {
	key := providerAPIKey(provider, "BRAVE_SEARCH_API_KEY")
	if key == "" {
		return nil, newProviderError("brave", ProviderMissingConfig, "API key is not configured")
	}
	endpoint := providerEndpoint(provider.BaseURL, "https://api.search.brave.com/res/v1/web/search", "search")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, invalidResponseError("brave", "invalid endpoint")
	}
	values := parsed.Query()
	values.Set("q", query)
	values.Set("count", strconv.Itoa(maxResults))
	parsed.RawQuery = values.Encode()
	body, err := doSearchHTTP(ctx, http.MethodGet, parsed.String(), nil, map[string]string{"Accept": "application/json", "X-Subscription-Token": key}, "brave")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Web struct {
			Results []optionalJSONResult `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, invalidResponseError("brave", "response is not valid JSON")
	}
	results := make([]searchResult, 0, len(envelope.Web.Results))
	for _, item := range envelope.Web.Results {
		result := optionalResult(item)
		if result.URL != "" {
			results = append(results, result)
		}
	}
	return results, nil
}

func searXNGSearchWithConfig(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig) ([]searchResult, error) {
	base := provider.BaseURL
	if base == "" && len(provider.Instances) > 0 {
		base = provider.Instances[0]
	}
	if base == "" {
		return nil, newProviderError("searxng", ProviderMissingConfig, "no self-hosted endpoint is configured")
	}
	endpoint := providerEndpoint(base, base, "search")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, invalidResponseError("searxng", "invalid endpoint")
	}
	values := parsed.Query()
	values.Set("q", query)
	values.Set("format", "json")
	values.Set("language", "en")
	parsed.RawQuery = values.Encode()
	body, err := doSearchHTTP(ctx, http.MethodGet, parsed.String(), nil, map[string]string{"Accept": "application/json"}, "searxng")
	if err != nil {
		return nil, err
	}
	return decodeOptionalResults(body, "searxng")
}

func optionalProviderSearch(ctx context.Context, name, query string, maxResults int, provider config.WebSearchProviderConfig) ([]searchResult, error) {
	name = strings.ToLower(name)
	switch name {
	case "brave":
		return braveConfiguredSearch(ctx, query, maxResults, provider)
	case "searxng":
		return searXNGSearchWithConfig(ctx, query, maxResults, provider)
	case "serper":
		key := providerAPIKey(provider, "SERPER_API_KEY")
		if key == "" {
			return nil, newProviderError(name, ProviderMissingConfig, "API key is not configured")
		}
		endpoint := providerEndpoint(provider.BaseURL, "https://google.serper.dev/search", "search")
		payload, _ := json.Marshal(map[string]any{"q": query, "num": maxResults})
		body, err := doSearchHTTP(ctx, http.MethodPost, endpoint, payload, map[string]string{"Content-Type": "application/json", "X-API-KEY": key}, name)
		if err != nil {
			return nil, err
		}
		return decodeOptionalResults(body, name)
	case "you":
		key := providerAPIKey(provider, "YDC_API_KEY")
		if key == "" {
			return nil, newProviderError(name, ProviderMissingConfig, "API key is not configured")
		}
		endpoint := providerEndpoint(provider.BaseURL, "https://ydc-index.io/v1/search", "search")
		payload, _ := json.Marshal(map[string]any{"query": query, "num_web_results": maxResults})
		body, err := doSearchHTTP(ctx, http.MethodPost, endpoint, payload, map[string]string{"Content-Type": "application/json", "X-API-Key": key}, name)
		if err != nil {
			return nil, err
		}
		return decodeOptionalResults(body, name)
	case "firecrawl":
		key := providerAPIKey(provider, "FIRECRAWL_API_KEY")
		if key == "" {
			return nil, newProviderError(name, ProviderMissingConfig, "API key is not configured")
		}
		endpoint := providerEndpoint(provider.BaseURL, "https://api.firecrawl.dev/v1/search", "search")
		payload, _ := json.Marshal(map[string]any{"query": query, "limit": maxResults})
		body, err := doSearchHTTP(ctx, http.MethodPost, endpoint, payload, map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + key}, name)
		if err != nil {
			return nil, err
		}
		return decodeOptionalResults(body, name)
	case "serpapi":
		key := providerAPIKey(provider, "SERPAPI_API_KEY")
		if key == "" {
			return nil, newProviderError(name, ProviderMissingConfig, "API key is not configured")
		}
		endpoint := providerEndpoint(provider.BaseURL, "https://serpapi.com/search.json", "search.json")
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, invalidResponseError(name, "invalid endpoint")
		}
		values := parsed.Query()
		values.Set("engine", "google")
		values.Set("q", query)
		values.Set("num", strconv.Itoa(maxResults))
		values.Set("api_key", key)
		parsed.RawQuery = values.Encode()
		body, err := doSearchHTTP(ctx, http.MethodGet, parsed.String(), nil, nil, name)
		if err != nil {
			return nil, err
		}
		return decodeOptionalResults(body, name)
	case "google_cse":
		key := providerAPIKey(provider, "GOOGLE_CSE_API_KEY")
		if key == "" || provider.Backend == "" {
			return nil, newProviderError(name, ProviderMissingConfig, "API key and search engine ID are required")
		}
		endpoint := providerEndpoint(provider.BaseURL, "https://www.googleapis.com/customsearch/v1", "search")
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, invalidResponseError(name, "invalid endpoint")
		}
		values := parsed.Query()
		values.Set("key", key)
		values.Set("cx", provider.Backend)
		values.Set("q", query)
		values.Set("num", strconv.Itoa(maxResults))
		parsed.RawQuery = values.Encode()
		body, err := doSearchHTTP(ctx, http.MethodGet, parsed.String(), nil, nil, name)
		if err != nil {
			return nil, err
		}
		return decodeOptionalResults(body, name)
	case "jina":
		endpoint := providerEndpoint(provider.BaseURL, "https://s.jina.ai/", "")
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, invalidResponseError(name, "invalid endpoint")
		}
		values := parsed.Query()
		values.Set("q", query)
		parsed.RawQuery = values.Encode()
		headers := map[string]string{"Accept": "text/markdown"}
		if key := providerAPIKey(provider, "JINA_API_KEY"); key != "" {
			headers["Authorization"] = "Bearer " + key
		}
		body, err := doSearchHTTP(ctx, http.MethodGet, parsed.String(), nil, headers, name)
		if err != nil {
			return nil, err
		}
		return parseMarkdownResults(body, maxResults), nil
	case "arxiv":
		return arxivProviderSearch(ctx, query, maxResults, provider)
	case "gdelt":
		endpoint := providerEndpoint(provider.BaseURL, "https://api.gdeltproject.org/api/v2/doc/doc", "doc")
		return getJSONProvider(ctx, name, endpoint, url.Values{"query": {query}, "mode": {"artlist"}, "format": {"json"}, "maxrecords": {strconv.Itoa(maxResults)}}, maxResults)
	case "mediawiki":
		endpoint := providerEndpoint(provider.BaseURL, "https://en.wikipedia.org/w/api.php", "api.php")
		return getJSONProvider(ctx, name, endpoint, url.Values{"action": {"query"}, "list": {"search"}, "srsearch": {query}, "format": {"json"}, "srlimit": {strconv.Itoa(maxResults)}}, maxResults)
	case "crossref":
		endpoint := providerEndpoint(provider.BaseURL, "https://api.crossref.org/works", "works")
		return getJSONProvider(ctx, name, endpoint, url.Values{"query": {query}, "rows": {strconv.Itoa(maxResults)}}, maxResults)
	case "openalex":
		endpoint := providerEndpoint(provider.BaseURL, "https://api.openalex.org/works", "works")
		return getJSONProvider(ctx, name, endpoint, url.Values{"search": {query}, "per-page": {strconv.Itoa(maxResults)}}, maxResults)
	case "github":
		endpoint := providerEndpoint(provider.BaseURL, "https://api.github.com/search/repositories", "repositories")
		return getJSONProvider(ctx, name, endpoint, url.Values{"q": {query}, "per_page": {strconv.Itoa(maxResults)}}, maxResults)
	case "stackexchange":
		endpoint := providerEndpoint(provider.BaseURL, "https://api.stackexchange.com/2.3/search/advanced", "search/advanced")
		return getJSONProvider(ctx, name, endpoint, url.Values{"q": {query}, "site": {firstNonEmpty(provider.Region, "stackoverflow")}, "pagesize": {strconv.Itoa(maxResults)}}, maxResults)
	case "hackernews":
		endpoint := providerEndpoint(provider.BaseURL, "https://hn.algolia.com/api/v1/search", "search")
		return getJSONProvider(ctx, name, endpoint, url.Values{"query": {query}, "hitsPerPage": {strconv.Itoa(maxResults)}}, maxResults)
	case "archive":
		endpoint := providerEndpoint(provider.BaseURL, "https://archive.org/advancedsearch.php", "advancedsearch.php")
		return getJSONProvider(ctx, name, endpoint, url.Values{"q": {query}, "fl[]": {"title"}, "output": {"json"}, "rows": {strconv.Itoa(maxResults)}}, maxResults)
	default:
		return nil, newProviderError(name, ProviderNotSupported, "provider adapter is not available")
	}
}

var markdownResultPattern = regexp.MustCompile(`(?m)^\s*(?:[-*]\s*)?\[([^\]]+)\]\((https?://[^)]+)\)(?:\s*[-:—]\s*(.*))?$`)

func parseMarkdownResults(body []byte, maxResults int) []searchResult {
	matches := markdownResultPattern.FindAllSubmatch(body, maxResults)
	results := make([]searchResult, 0, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		snippet := ""
		if len(match) > 3 {
			snippet = strings.TrimSpace(string(match[3]))
		}
		results = append(results, searchResult{Title: cleanHTML(string(match[1])), URL: string(match[2]), Snippet: cleanHTML(snippet)})
	}
	return results
}

func getJSONProvider(ctx context.Context, name, endpoint string, values url.Values, maxResults int) ([]searchResult, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, invalidResponseError(name, "invalid endpoint")
	}
	parsed.RawQuery = values.Encode()
	body, err := doSearchHTTP(ctx, http.MethodGet, parsed.String(), nil, map[string]string{"Accept": "application/json"}, name)
	if err != nil {
		return nil, err
	}
	if name == "mediawiki" {
		var envelope struct {
			Query struct {
				Search []struct {
					Title   string `json:"title"`
					Snippet string `json:"snippet"`
					PageID  int    `json:"pageid"`
				} `json:"search"`
			} `json:"query"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, invalidResponseError(name, "response is not valid JSON")
		}
		results := make([]searchResult, 0, len(envelope.Query.Search))
		for _, item := range envelope.Query.Search {
			results = append(results, searchResult{Title: item.Title, URL: "https://en.wikipedia.org/?curid=" + strconv.Itoa(item.PageID), Snippet: cleanHTML(item.Snippet)})
			if len(results) >= maxResults {
				break
			}
		}
		return results, nil
	}
	return decodeOptionalResults(body, name)
}

type arxivEntry struct {
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Summary string `xml:"summary"`
}
type arxivFeed struct {
	Entries []arxivEntry `xml:"entry"`
}

func arxivProviderSearch(ctx context.Context, query string, maxResults int, provider config.WebSearchProviderConfig) ([]searchResult, error) {
	endpoint := providerEndpoint(provider.BaseURL, "https://export.arxiv.org/api/query", "query")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, invalidResponseError("arxiv", "invalid endpoint")
	}
	values := parsed.Query()
	values.Set("search_query", "all:"+query)
	values.Set("max_results", strconv.Itoa(maxResults))
	parsed.RawQuery = values.Encode()
	body, err := doSearchHTTP(ctx, http.MethodGet, parsed.String(), nil, map[string]string{"Accept": "application/atom+xml"}, "arxiv")
	if err != nil {
		return nil, err
	}
	var feed arxivFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, invalidResponseError("arxiv", "response is not valid XML")
	}
	results := make([]searchResult, 0, len(feed.Entries))
	for _, item := range feed.Entries {
		results = append(results, searchResult{Title: cleanHTML(item.Title), URL: strings.TrimSpace(item.ID), Snippet: cleanHTML(item.Summary)})
	}
	return results, nil
}
