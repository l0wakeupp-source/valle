package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetchExtractsHTMLAndReusesShortLivedCache(t *testing.T) {
	resetFetchCache()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><style>.x{}</style><script>alert(1)</script></head><body><h1>Hello</h1><p>World &amp; friends</p></body></html>`))
	}))
	defer server.Close()

	input, _ := json.Marshal(map[string]string{"url": server.URL, "extract": "text"})
	first, err := (FetchTool{}).Run(context.Background(), Context{}, input)
	if err != nil {
		t.Fatalf("first fetch returned error: %v", err)
	}
	if strings.Contains(first.Output, "<h1>") || strings.Contains(first.Output, "alert(1)") {
		t.Fatalf("HTML was not compacted: %q", first.Output)
	}
	if !strings.Contains(first.Output, "Hello World & friends") {
		t.Fatalf("clean text = %q, want readable page text", first.Output)
	}

	second, err := (FetchTool{}).Run(context.Background(), Context{}, input)
	if err != nil {
		t.Fatalf("cached fetch returned error: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d, want one cached request", requests.Load())
	}
	if second.Meta["cached"] != true {
		t.Fatalf("cached metadata = %#v, want cached=true", second.Meta)
	}
}
