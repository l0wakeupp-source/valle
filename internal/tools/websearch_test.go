package tools

import (
	"testing"

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
