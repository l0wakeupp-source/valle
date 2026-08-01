package config

import "testing"

func TestMergeWebSearchConfigPreservesProviderDefaults(t *testing.T) {
	enabled := true
	base := &WebSearchConfig{
		MaxResults: 8,
		Provider:   "parallel",
		Providers: map[string]WebSearchProviderConfig{
			"exa": {APIKey: "base-secret", BaseURL: "https://base.example", Type: "auto", Enabled: &enabled},
		},
	}
	override := &WebSearchConfig{
		MaxParallel: 2,
		Providers: map[string]WebSearchProviderConfig{
			"exa": {BaseURL: "https://override.example", IncludeDomains: []string{"go.dev"}},
		},
	}
	merged := mergeWebSearchConfig(base, override)
	if merged.MaxResults != 8 || merged.MaxParallel != 2 || merged.Provider != "parallel" {
		t.Fatalf("merged config lost defaults: %#v", merged)
	}
	exa := merged.Providers["exa"]
	if exa.APIKey != "base-secret" || exa.BaseURL != "https://override.example" || exa.Type != "auto" || !*exa.Enabled {
		t.Fatalf("merged Exa config = %#v", exa)
	}
	if len(exa.IncludeDomains) != 1 || exa.IncludeDomains[0] != "go.dev" {
		t.Fatalf("merged domains = %#v", exa.IncludeDomains)
	}
}
