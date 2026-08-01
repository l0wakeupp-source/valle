package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveWebSearchConfigPreservesUnrelatedGlobalSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RICK_HOME", home)

	path := filepath.Join(home, "rick.json")
	initial := []byte(`{
  "model": "anthropic/claude-sonnet",
  "unknown_setting": {"keep": true},
  "web_search": {"max_results": 2}
}
`)
	if err := os.WriteFile(path, initial, 0o644); err != nil {
		t.Fatal(err)
	}

	enabled := true
	err := SaveWebSearchConfig(WebSearchConfig{
		Provider:   "parallel",
		MaxResults: 7,
		Providers: map[string]WebSearchProviderConfig{
			"exa": {Enabled: &enabled, MaxAgeHours: intPtr(24)},
		},
	})
	if err != nil {
		t.Fatalf("SaveWebSearchConfig() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	var model string
	if err := json.Unmarshal(doc["model"], &model); err != nil {
		t.Fatal(err)
	}
	if model != "anthropic/claude-sonnet" {
		t.Fatalf("model was not preserved: %q", model)
	}
	if _, ok := doc["unknown_setting"]; !ok {
		t.Fatal("unknown_setting was removed")
	}

	var saved WebSearchConfig
	if err := json.Unmarshal(doc["web_search"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Provider != "parallel" || saved.MaxResults != 7 {
		t.Fatalf("saved web search config = %#v", saved)
	}
	if saved.Providers["exa"].MaxAgeHours == nil || *saved.Providers["exa"].MaxAgeHours != 24 {
		t.Fatalf("saved Exa freshness setting = %#v", saved.Providers["exa"])
	}
}

func intPtr(value int) *int { return &value }
