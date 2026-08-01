package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rick/internal/config"
)

func readWebSearchFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestParseDuckDuckGoLiteCapturedFixture(t *testing.T) {
	results := parseDuckDuckGoLiteHTML(readWebSearchFixture(t, "ddg_lite_captured.html"), 5)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %#v", len(results), results)
	}
	if results[0].URL != "https://go.dev/doc/" {
		t.Fatalf("first URL = %q, want normalized Go URL", results[0].URL)
	}
	if results[0].Snippet != "Build simple, secure, scalable systems with Go. Official documentation and guides." {
		t.Fatalf("first snippet = %q", results[0].Snippet)
	}
	if strings.Contains(results[0].URL, "rut=") {
		t.Fatal("tracking parameter was not removed")
	}
}

func TestDuckDuckGoRequestParameters(t *testing.T) {
	options := searchOptions{Region: "us-en", SafeSearch: "strict", TimeRange: "week", DDGBackend: "lite"}
	endpoint, err := buildDuckDuckGoURL("https://example.test/lite/", "golang http", options, false)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	query := request.URL.Query()
	for key, want := range map[string]string{"q": "golang http", "kl": "us-en", "kp": "1", "df": "w"} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	instant, err := buildDuckDuckGoURL("https://example.test/", "golang", options, true)
	if err != nil {
		t.Fatal(err)
	}
	instantURL, err := url.Parse(instant)
	if err != nil {
		t.Fatal(err)
	}
	instantQuery := instantURL.Query()
	if instantQuery.Get("format") != "json" || instantQuery.Get("no_html") != "1" || instantQuery.Get("skip_disambig") != "1" {
		t.Fatalf("instant parameters = %v", instantQuery)
	}
	if instantQuery.Get("kp") != "" {
		t.Fatal("instant endpoint received Lite safe-search parameter")
	}
}

func TestDuckDuckGoLiteRequestParameters(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.URL.Query()
		_, _ = w.Write(readWebSearchFixture(t, "ddg_lite_captured.html"))
	}))
	defer server.Close()
	clearHostForTest(t, server.URL)

	results, err := duckDuckGoLiteWithOptions(context.Background(), "golang", 2, config.WebSearchProviderConfig{BaseURL: server.URL + "/lite/"}, searchOptions{
		Region: "us-en", SafeSearch: "strict", TimeRange: "week",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || received.Get("kl") != "us-en" || received.Get("kp") != "1" || received.Get("df") != "w" {
		t.Fatalf("results=%#v query=%v", results, received)
	}
}

func TestParseDuckDuckGoInstantCapturedFixture(t *testing.T) {
	results, err := parseDuckDuckGoInstantJSON(readWebSearchFixture(t, "ddg_instant_captured.json"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d instant results, want 3: %#v", len(results), results)
	}
	if results[0].URL != "https://en.wikipedia.org/wiki/Go_(programming_language)" {
		t.Fatalf("abstract URL = %q", results[0].URL)
	}
}

func TestProviderRequestParametersAndResponseMapping(t *testing.T) {
	fixtures := map[string][]byte{
		"/api/web_search": readWebSearchFixture(t, "ollama_search_captured.json"),
		"/search":         readWebSearchFixture(t, "exa_search_captured.json"),
	}
	var mu sync.Mutex
	requests := make(map[string]map[string]any)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		mu.Lock()
		requests[r.URL.Path] = decoded
		mu.Unlock()
		if r.URL.Path == "/search" && r.Header.Get("x-api-key") != "test-exa-key" {
			http.Error(w, "missing key", http.StatusUnauthorized)
			return
		}
		payload, ok := fixtures[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	clearHostForTest(t, server.URL)

	ollamaResults, err := ollamaSearch(context.Background(), "golang", 3, config.WebSearchProviderConfig{BaseURL: server.URL + "/api", APIKey: "not-logged"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ollamaResults) != 2 || ollamaResults[0].Snippet == "" {
		t.Fatalf("ollama results = %#v", ollamaResults)
	}
	maxAgeHours := 24
	exaResults, err := exaSearch(context.Background(), "golang", 2, config.WebSearchProviderConfig{BaseURL: server.URL, APIKey: "test-exa-key", MaxAgeHours: &maxAgeHours}, searchOptions{ExaType: "deep", Livecrawl: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(exaResults) != 2 || exaResults[0].Snippet != "Official documentation for the Go programming language." {
		t.Fatalf("exa results = %#v", exaResults)
	}
	mu.Lock()
	ollamaRequest := requests["/api/web_search"]
	exaRequest := requests["/search"]
	mu.Unlock()
	if ollamaRequest["query"] != "golang" || ollamaRequest["max_results"] != float64(3) {
		t.Fatalf("ollama request = %#v", ollamaRequest)
	}
	if exaRequest["type"] != "deep" || exaRequest["numResults"] != float64(2) {
		t.Fatalf("exa request = %#v", exaRequest)
	}
	contents, ok := exaRequest["contents"].(map[string]any)
	if !ok || contents["maxAgeHours"] != float64(24) || exaRequest["maxAgeHours"] != nil {
		t.Fatalf("exa freshness request = %#v", exaRequest)
	}
}

func TestTavilyRequestAndResponseMapping(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-tavily-key" {
			t.Fatalf("Authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &requestBody)
		_, _ = w.Write(readWebSearchFixture(t, "tavily_search_captured.json"))
	}))
	defer server.Close()
	clearHostForTest(t, server.URL)

	results, err := tavilySearch(context.Background(), "golang", 2, config.WebSearchProviderConfig{BaseURL: server.URL, APIKey: "test-tavily-key"}, searchOptions{TimeRange: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Snippet == "" {
		t.Fatalf("Tavily results = %#v", results)
	}
	if requestBody["search_depth"] != "basic" || requestBody["time_range"] != "day" {
		t.Fatalf("Tavily request = %#v", requestBody)
	}
}

func TestWebSearchToolRunUsesConfiguredProviderPipeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.Header.Get("Authorization") != "Bearer test-tavily-key" {
			t.Fatalf("unexpected Tavily request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write(readWebSearchFixture(t, "tavily_search_captured.json"))
	}))
	defer server.Close()
	clearHostForTest(t, server.URL)

	tavilyEnabled := true
	tool := WebSearchTool{Restrictions: &config.WebSearchConfig{
		Provider: "tavily",
		Providers: map[string]config.WebSearchProviderConfig{
			"tavily": {Enabled: &tavilyEnabled, BaseURL: server.URL, APIKey: "test-tavily-key"},
		},
	}}
	result, err := tool.Run(context.Background(), Context{SessionID: "configured-provider-test"}, json.RawMessage(`{"query":"golang","max_results":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "The Go Programming Language") || result.Meta["parallel"] != false {
		t.Fatalf("tool result = %#v", result)
	}
	if strings.Contains(result.Output, "test-tavily-key") {
		t.Fatal("provider credential leaked in search output")
	}
}

func clearHostForTest(t *testing.T, rawURL string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	hostMu.Lock()
	delete(hostLastCall, request.URL.Hostname())
	hostMu.Unlock()
}

func TestMergeSearchResultsConsensusAndDeduplication(t *testing.T) {
	batches := []providerBatch{
		{name: "duckduckgo", weight: 1, results: []searchResult{
			{Title: "Shared", URL: "https://example.com/page?utm_source=test", Snippet: "short"},
			{Title: "Only DDG", URL: "https://ddg.example/"},
		}},
		{name: "exa", weight: 1.2, results: []searchResult{
			{Title: "Shared from Exa", URL: "https://example.com/page", Snippet: "more complete snippet"},
			{Title: "Only Exa", URL: "https://exa.example/"},
		}},
	}
	results := mergeSearchResults(batches, 3)
	if len(results) != 3 {
		t.Fatalf("got %d merged results: %#v", len(results), results)
	}
	if results[0].URL != "https://example.com/page" || results[0].Snippet != "more complete snippet" {
		t.Fatalf("consensus result = %#v", results[0])
	}
}

func TestParallelProvidersRespectBound(t *testing.T) {
	var active int32
	var peak int32
	providers := make([]configuredSearchProvider, 0, 5)
	for i := 0; i < 5; i++ {
		name := "test-provider-" + string(rune('a'+i))
		providers = append(providers, configuredSearchProvider{name: name, weight: 1, fn: func(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
			current := atomic.AddInt32(&active, 1)
			for {
				old := atomic.LoadInt32(&peak)
				if current <= old || atomic.CompareAndSwapInt32(&peak, old, current) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return []searchResult{{Title: query, URL: "https://" + query + ".example/"}}, nil
		}})
	}
	batches := (WebSearchTool{}).runParallelProviders(context.Background(), providers, "parallel-test", 5, 2, "unique-parallel-test")
	if len(batches) != 5 || atomic.LoadInt32(&peak) > 2 {
		t.Fatalf("batches=%d peak=%d", len(batches), peak)
	}
}
