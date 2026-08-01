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
			"exa": {APIKey: "super...y"},
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

func TestWebProviderFieldsUseTypedProviderControls(t *testing.T) {
	m := newModelChoiceTestModel()
	m.deps.Loaded = &config.Loaded{Config: config.Config{WebSearch: &config.WebSearchConfig{}}}
	m.web.selected = "exa"

	fields := m.webFields()
	weight := findWebField(t, fields, "weight")
	if len(weight.options) == 0 {
		t.Fatal("provider weight has no dropdown options")
	}
	if webOptionIndex(weight.options, "1") < 0 || webOptionIndex(weight.options, "__custom__") < 0 {
		t.Fatal("provider weight dropdown is missing bounded or custom choices")
	}
	maxAge := findWebField(t, fields, "max_age_hours")
	if len(maxAge.options) == 0 {
		t.Fatal("Exa max age has no typed choices")
	}

	routing := m.webRoutingFields()
	parallel := findWebField(t, routing, "parallel")
	if !parallel.toggle || len(parallel.options) != 2 {
		t.Fatalf("parallel control = toggle:%v options:%d, want a two-state switch", parallel.toggle, len(parallel.options))
	}
	if len(findWebField(t, routing, "max_parallel").options) == 0 {
		t.Fatal("max parallel has no dropdown options")
	}
}

func TestWebProviderDropdownAndBooleanControlsApplyValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RICK_HOME", home)
	m := newModelChoiceTestModel()
	m.deps.Loaded = &config.Loaded{Config: config.Config{WebSearch: &config.WebSearchConfig{Providers: map[string]config.WebSearchProviderConfig{}}}}
	m.web.active = true
	m.web.stage = webProviderMenu
	m.web.selected = "exa"
	m.web.fields = m.webFields()

	weight := findWebField(t, m.web.fields, "weight")
	m.openWebField(weight)
	if m.web.stage != webProviderEdit {
		t.Fatalf("weight open stage = %d, want edit", m.web.stage)
	}
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyDown}, "down")
	if m.web.inputBuf != "0.25" {
		t.Fatalf("weight dropdown selection = %q, want 0.25", m.web.inputBuf)
	}
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
	if got := m.webConfig().Providers["exa"].Weight; got != 0.25 {
		t.Fatalf("saved provider weight = %v, want 0.25", got)
	}

	m.web.fields = m.webFields()
	weight = findWebField(t, m.web.fields, "weight")
	m.openWebField(weight)
	for index := 0; index < 9; index++ {
		m.handleWebKey(tea.KeyMsg{Type: tea.KeyDown}, "down")
	}
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
	if !m.web.customInput {
		t.Fatal("custom weight choice did not open exact-value input")
	}
	m.handleWebKey(tea.KeyMsg{Runes: []rune("2.75")}, "")
	m.handleWebKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
	if got := m.webConfig().Providers["exa"].Weight; got != 2.75 {
		t.Fatalf("saved custom provider weight = %v, want 2.75", got)
	}

	m.web.stage = webProviderRouting
	m.web.fields = m.webRoutingFields()
	parallel := findWebField(t, m.web.fields, "parallel")
	m.openWebField(parallel)
	if m.webConfig().Parallel == nil || !*m.webConfig().Parallel {
		t.Fatal("parallel switch did not enable parallel execution")
	}
}

func findWebField(t *testing.T, fields []webField, id string) webField {
	t.Helper()
	for _, field := range fields {
		if field.id == id {
			return field
		}
	}
	t.Fatalf("web field %q not found", id)
	return webField{}
}
