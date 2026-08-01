package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/config"
	"rick/internal/tools"
)

func TestRunSlashOpensWebProviderSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RICK_HOME", home)

	m := newModelChoiceTestModel()
	m.deps.Loaded = &config.Loaded{}
	m.deps.Registry = tools.NewRegistry()

	m.runSlash("/webproviders")

	if !m.web.active {
		t.Fatal("/webproviders did not open the web-provider settings flow")
	}
	tool, ok := m.deps.Registry.Get("websearch")
	if !ok {
		t.Fatal("opening settings did not refresh the websearch tool")
	}
	if got := tool.(tools.WebSearchTool).Restrictions; got != m.webConfig() {
		t.Fatal("websearch tool does not reference the live web-search config")
	}
	if m.web.stage != webProviderList {
		t.Fatalf("web-provider stage = %d, want list", m.web.stage)
	}

	m.handleWebKey(tea.KeyMsg{Type: tea.KeyDown}, "down")
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyDown}, "down")
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
	if m.web.selected != "ollama" || m.web.stage != webProviderMenu {
		t.Fatalf("selected web provider = %q at stage %d, want ollama menu", m.web.selected, m.web.stage)
	}

	m.handleWebKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
	enabled := m.webConfig().Providers["ollama"].Enabled
	if enabled == nil || !*enabled {
		t.Fatal("toggling Ollama did not enable it")
	}

	m.handleWebKey(tea.KeyMsg{Type: tea.KeyDown}, "down")
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyDown}, "down")
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
	m.handleWebKey(tea.KeyMsg{Runes: []rune("http://localhost:11434/api/experimental")}, "")
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")

	if got := m.webConfig().Providers["ollama"].BaseURL; got != "http://localhost:11434/api/experimental" {
		t.Fatalf("live Ollama endpoint = %q", got)
	}
	saved, err := os.ReadFile(filepath.Join(home, "rick.json"))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(saved), "http://localhost:11434/api/experimental") {
		t.Fatalf("saved config did not contain the Ollama endpoint: %s", saved)
	}
}

func TestWebProviderEditorRedactsExistingAPIKey(t *testing.T) {
	m := newModelChoiceTestModel()
	m.deps.Loaded = &config.Loaded{}
	m.deps.Loaded.Config.WebSearch = &config.WebSearchConfig{
		Providers: map[string]config.WebSearchProviderConfig{
			"exa": {APIKey: "super-secret-test-key"},
		},
	}
	m.web = webSearchState{
		active:   true,
		stage:    webProviderEdit,
		selected: "exa",
		fieldID:  "api_key",
		fields:   m.webFields(),
	}

	view := m.webView()
	if strings.Contains(view, "super-secret-test-key") {
		t.Fatal("web-provider editor exposed the configured API key")
	}
	if !strings.Contains(view, "[REDACTED]") {
		t.Fatal("web-provider editor did not show a redacted key status")
	}
}
