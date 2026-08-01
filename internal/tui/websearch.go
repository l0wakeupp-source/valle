package tui

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"rick/internal/config"
	"rick/internal/tools"
)

// webProviderStage is the position in the /webproviders settings flow.
type webProviderStage int

const (
	webProviderList webProviderStage = iota
	webProviderRouting
	webProviderMenu
	webProviderEdit
)

type webProviderSpec struct {
	id          string
	label       string
	description string
}

type webProviderRow struct {
	id     string
	label  string
	detail string
}

type webOption struct {
	value  string
	label  string
	detail string
}

type webField struct {
	id      string
	label   string
	value   string
	detail  string
	options []webOption
	secret  bool
	toggle  bool
}

type webSearchState struct {
	active bool
	stage  webProviderStage

	rows   []webProviderRow
	cursor int
	scroll int

	selected     string
	fields       []webField
	fieldID      string
	inputBuf     string
	choiceCursor int
	customInput  bool
	returnTo     webProviderStage

	statusLine string
}

const (
	webButtonBack    = "web-back"
	webButtonPrimary = "web-primary"
)

var webProviderSpecs = []webProviderSpec{
	{id: "duckduckgo", label: "DuckDuckGo", description: "free HTML/instant search; no API key"},
	{id: "ollama", label: "Ollama", description: "Ollama Cloud or local experimental proxy"},
	{id: "ddginstant", label: "DDG Instant", description: "DuckDuckGo instant-answer JSON; opt-in"},
	{id: "searxng", label: "SearXNG", description: "self-hosted or explicitly configured instance"},
	{id: "brave", label: "Brave Search API", description: "API-key provider; not HTML scraping"},
	{id: "exa", label: "Exa", description: "neural search with live crawl and freshness"},
	{id: "tavily", label: "Tavily", description: "AI search API with topic and freshness"},
	{id: "serper", label: "Serper", description: "Google-results API; explicit key required"},
	{id: "you", label: "You.com", description: "You.com search API; explicit key required"},
	{id: "firecrawl", label: "Firecrawl", description: "search plus crawl API; explicit key required"},
	{id: "serpapi", label: "SerpAPI", description: "multi-engine search API; explicit key required"},
	{id: "google_cse", label: "Google CSE", description: "Google Programmable Search; key and engine ID"},
	{id: "jina", label: "Jina", description: "explicitly configured Jina search endpoint"},
	{id: "gdelt", label: "GDELT", description: "public global news search"},
	{id: "mediawiki", label: "MediaWiki", description: "Wikipedia or another MediaWiki API"},
	{id: "arxiv", label: "arXiv", description: "open research-paper search"},
	{id: "crossref", label: "Crossref", description: "open scholarly metadata search"},
	{id: "openalex", label: "OpenAlex", description: "open scholarly works search"},
	{id: "github", label: "GitHub", description: "repository search API"},
	{id: "stackexchange", label: "Stack Exchange", description: "Stack Overflow network search"},
	{id: "hackernews", label: "Hacker News", description: "Algolia-powered HN search"},
	{id: "archive", label: "Internet Archive", description: "open archive metadata search"},
	{id: "bing", label: "Bing (retired)", description: "official Bing Search API retired; not routed automatically"},
}

func (m *Model) openWebProviders() (tea.Model, tea.Cmd) {
	m.web = webSearchState{active: true, stage: webProviderList}
	m.rebuildWebProviderRows()
	m.syncWebSearchTool()
	m.refresh()
	return m, nil
}

func (m *Model) webConfig() *config.WebSearchConfig {
	if m.deps.Loaded == nil {
		m.deps.Loaded = &config.Loaded{}
	}
	if m.deps.Loaded.Config.WebSearch == nil {
		m.deps.Loaded.Config.WebSearch = &config.WebSearchConfig{}
	}
	if m.deps.Loaded.Config.WebSearch.Providers == nil {
		m.deps.Loaded.Config.WebSearch.Providers = map[string]config.WebSearchProviderConfig{}
	}
	return m.deps.Loaded.Config.WebSearch
}

func (m *Model) syncWebSearchTool() {
	if m.deps.Registry == nil {
		return
	}
	m.deps.Registry.Register(tools.WebSearchTool{Restrictions: m.webConfig()})
}

func (m *Model) rebuildWebProviderRows() {
	cfg := m.webConfig()
	rows := []webProviderRow{{
		id:     "routing",
		label:  "Routing and limits",
		detail: webRoutingSummary(cfg),
	}}
	for _, spec := range webProviderSpecs {
		rows = append(rows, webProviderRow{
			id:     spec.id,
			label:  spec.label,
			detail: webProviderSummary(cfg, spec),
		})
	}
	m.web.rows = rows
	if m.web.cursor >= len(rows) {
		m.web.cursor = len(rows) - 1
	}
	if m.web.cursor < 0 {
		m.web.cursor = 0
	}
	m.webRevealRow(m.web.cursor)
}

func webRoutingSummary(cfg *config.WebSearchConfig) string {
	mode := "auto"
	if cfg != nil && strings.TrimSpace(cfg.Provider) != "" {
		mode = cfg.Provider
	}
	parallel := "serial"
	if cfg != nil && cfg.Parallel != nil && *cfg.Parallel {
		parallel = "parallel"
	}
	return fmt.Sprintf("mode %s · %s · max %d results", mode, parallel, effectiveMaxResults(cfg))
}

func effectiveMaxResults(cfg *config.WebSearchConfig) int {
	if cfg != nil && cfg.MaxResults > 0 {
		return cfg.MaxResults
	}
	return 5
}

func webProviderSummary(cfg *config.WebSearchConfig, spec webProviderSpec) string {
	if cfg == nil {
		return "not configured"
	}
	provider, configured := cfg.Providers[webProviderConfigID(spec.id)]
	if provider.Enabled != nil && !*provider.Enabled {
		return "disabled"
	}
	if spec.id == "bing" {
		return "retired · not used by auto"
	}
	if spec.id == "duckduckgo" && !configured {
		return "enabled by default · lite backend"
	}
	if !configured {
		return "not configured"
	}
	details := []string{"enabled"}
	if provider.APIKey != "" || provider.APIKeyEnv != "" {
		details = append(details, "key configured")
	}
	if provider.BaseURL != "" {
		details = append(details, webHostOf(provider.BaseURL))
	}
	for _, status := range tools.ProviderStatuses(cfg) {
		if status.ID != spec.id || status.Attempts == 0 {
			continue
		}
		if status.State == "cooldown" {
			details = append(details, "cooldown")
		} else if status.LastResults > 0 {
			details = append(details, fmt.Sprintf("healthy · %d results", status.LastResults))
		} else if status.LastClass != "" {
			details = append(details, string(status.LastClass))
		}
		break
	}
	return strings.Join(details, " · ")
}

func webHostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "[configured]"
	}
	return parsed.Hostname()
}

func (m *Model) webProviderSpec(id string) webProviderSpec {
	for _, spec := range webProviderSpecs {
		if spec.id == id {
			return spec
		}
	}
	return webProviderSpec{id: id, label: id}
}

func webProviderConfigID(id string) string {
	if id == "ddginstant" {
		return "duckduckgo"
	}
	return id
}

func makeWebOption(value, label, detail string) webOption {
	return webOption{value: value, label: label, detail: detail}
}

func webChoiceLabel(value string) string {
	labels := map[string]string{
		"":                "Default",
		"default":         "Default",
		"auto":            "Automatic",
		"parallel":        "Parallel",
		"lite":            "Lite HTML",
		"instant":         "Instant answers",
		"off":             "Off",
		"on":              "On",
		"moderate":        "Moderate",
		"strict":          "Strict",
		"general":         "General",
		"news":            "News",
		"finance":         "Finance",
		"fallback":        "Fallback",
		"preferred":       "Preferred",
		"always":          "Always",
		"never":           "Never",
		"api":             "API",
		"local":           "Local",
		"public_instance": "Public/self-hosted instance",
		"domain":          "Domain-specific",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

func webChoiceOptions(values ...string) []webOption {
	options := make([]webOption, 0, len(values))
	for _, value := range values {
		options = append(options, makeWebOption(value, webChoiceLabel(value), ""))
	}
	return options
}

func webNumericChoices(values ...string) []webOption {
	options := []webOption{makeWebOption("default", "Default", "Use the safe provider/runtime default")}
	for _, value := range values {
		options = append(options, makeWebOption(value, value, ""))
	}
	options = append(options, makeWebOption("__custom__", "Custom value…", "Type an exact value after selecting this option"))
	return options
}

func webDuckDuckGoRegionChoices() []webOption {
	options := webChoiceOptions("wt-wt", "de-de", "at-de", "us-en", "uk-en", "fr-fr", "es-es", "it-it", "ja-jp", "nl-nl", "pl-pl")
	return append(options, makeWebOption("__custom__", "Custom region…", "Type another DuckDuckGo region code"))
}

func webStackExchangeSiteChoices() []webOption {
	options := webChoiceOptions("stackoverflow", "superuser", "serverfault", "askubuntu", "mathoverflow", "unix", "apple", "codegolf")
	return append(options, makeWebOption("__custom__", "Custom site…", "Type another Stack Exchange site key"))
}

func webProviderModeChoices() []webOption {
	return webChoiceOptions("auto", "parallel", "duckduckgo", "ddginstant", "searxng", "brave", "ollama", "exa", "tavily", "serper", "you", "firecrawl", "serpapi", "google_cse", "jina", "gdelt", "mediawiki", "arxiv", "crossref", "openalex", "github", "stackexchange", "hackernews", "archive")
}

func intOrDefault(value *int) string {
	if value == nil {
		return "default"
	}
	return strconv.Itoa(*value)
}

func (m *Model) webFields() []webField {
	cfg := m.webConfig()
	provider := cfg.Providers[webProviderConfigID(m.web.selected)]
	enabled := providerEnabledForUI(m.web.selected, provider, cfg)
	fields := []webField{{
		id:     "enabled",
		label:  map[bool]string{true: "disable provider", false: "enable provider"}[enabled],
		value:  map[bool]string{true: "on", false: "off"}[enabled],
		detail: "switch whether auto/parallel may use this provider",
		toggle: true,
	}}

	if m.web.selected != "duckduckgo" && m.web.selected != "ddginstant" {
		fields = append(fields, webField{
			id: "api_key", label: "API key", value: "", secret: true,
			detail: "leave blank to keep the current key; type - to clear it",
		})
	}
	fields = append(fields,
		webField{id: "base_url", label: "base URL", value: provider.BaseURL,
			detail: "endpoint override; leave blank for the provider default"},
		webField{id: "api_key_env", label: "API key environment variable", value: provider.APIKeyEnv, detail: "environment variable name; the secret is never displayed"},
		webField{id: "kind", label: "provider kind", value: firstWebValue(provider.Kind, "api"), options: webChoiceOptions("api", "local", "public_instance", "domain"), detail: "provider classification used in routing and diagnostics"},
		webField{id: "instances", label: "instances", value: strings.Join(provider.Instances, ", "), detail: "comma-separated explicit endpoints; no public list is embedded"},
		webField{id: "priority", label: "fallback priority", value: intValueDefault(provider.Priority, 0), options: webNumericChoices("10", "20", "30", "40", "50", "75", "100", "150", "200", "300"), detail: "lower values are tried first in automatic fallback"},
		webField{id: "max_rpm", label: "max requests/minute", value: intValueDefault(provider.MaxRPM, 0), options: webNumericChoices("1", "5", "10", "30", "60", "120", "300", "600", "1000"), detail: "provider rate gate; Default uses the safe runtime setting"},
		webField{id: "max_concurrency", label: "max concurrency", value: intValueDefault(provider.MaxConcurrency, 0), options: webNumericChoices("1", "2", "3", "4", "8", "16", "32"), detail: "per-provider in-flight limit"},
		webField{id: "timeout_seconds", label: "provider timeout", value: intValueDefault(provider.TimeoutSeconds, 0), options: webNumericChoices("5", "10", "15", "30", "45", "60", "90", "120"), detail: "request deadline; the overall search uses the tightest configured timeout"},
		webField{id: "cache_ttl_seconds", label: "cache lifetime", value: intValueDefault(provider.CacheTTLSeconds, 0), options: webNumericChoices("60", "300", "900", "1800", "3600", "21600", "86400"), detail: "positive-result cache lifetime"},
	)

	switch m.web.selected {
	case "duckduckgo", "ddginstant":
		fields = append(fields,
			webField{id: "backend", label: "backend", value: firstWebValue(provider.Backend, "lite"), options: webChoiceOptions("lite", "instant", "auto"), detail: "Lite HTML, Instant-answer JSON, or automatic backend"},
			webField{id: "region", label: "region", value: firstWebValue(provider.Region, "wt-wt"), options: webDuckDuckGoRegionChoices(), detail: "DuckDuckGo regional index"},
			webField{id: "safe_search", label: "safe search", value: firstWebValue(provider.SafeSearch, "moderate"), options: webChoiceOptions("off", "moderate", "strict"), detail: "content filtering level"},
			webField{id: "time_range", label: "time range", value: firstWebValue(provider.TimeRange, "default"), options: webChoiceOptions("default", "day", "week", "month", "year"), detail: "optional freshness filter"},
		)
	case "exa":
		fields = append(fields,
			webField{id: "type", label: "search type", value: firstWebValue(provider.Type, "auto"), options: webChoiceOptions("auto", "fast", "deep"), detail: "Exa retrieval mode"},
			webField{id: "livecrawl", label: "live crawl", value: firstWebValue(provider.Livecrawl, "fallback"), options: webChoiceOptions("fallback", "preferred", "always", "never"), detail: "freshness strategy"},
			webField{id: "max_age_hours", label: "max age", value: intOrDefault(provider.MaxAgeHours), options: webNumericChoices("1", "6", "12", "24", "72", "168", "720"), detail: "maximum cached content age in hours"},
			webField{id: "include_domains", label: "include domains", value: strings.Join(provider.IncludeDomains, ", "), detail: "comma-separated domain allowlist"},
			webField{id: "exclude_domains", label: "exclude domains", value: strings.Join(provider.ExcludeDomains, ", "), detail: "comma-separated domain denylist"},
		)
	case "tavily":
		fields = append(fields,
			webField{id: "type", label: "topic", value: firstWebValue(provider.Type, "general"), options: webChoiceOptions("general", "news", "finance"), detail: "Tavily topic"},
			webField{id: "time_range", label: "time range", value: firstWebValue(provider.TimeRange, "default"), options: webChoiceOptions("default", "day", "week", "month", "year"), detail: "Tavily freshness filter"},
			webField{id: "include_domains", label: "include domains", value: strings.Join(provider.IncludeDomains, ", "), detail: "comma-separated domain allowlist"},
			webField{id: "exclude_domains", label: "exclude domains", value: strings.Join(provider.ExcludeDomains, ", "), detail: "comma-separated domain denylist"},
		)
	case "google_cse":
		fields = append(fields, webField{id: "backend", label: "search engine ID", value: provider.Backend, detail: "Google Programmable Search engine identifier"})
	case "stackexchange":
		fields = append(fields, webField{id: "region", label: "Stack Exchange site", value: firstWebValue(provider.Region, "stackoverflow"), options: webStackExchangeSiteChoices(), detail: "site key sent to the Stack Exchange API"})
	}
	fields = append(fields,
		webField{id: "weight", label: "provider weight", value: floatValue(provider.Weight), options: webNumericChoices("0.25", "0.5", "0.75", "1", "1.25", "1.5", "2", "3", "5"), detail: "higher weight gives this provider more influence when results are merged"},
		webField{id: "health_reset", label: "reset provider health", value: "press enter", detail: "clear this provider's in-memory cooldown and circuit state"},
	)
	return fields
}

func providerEnabledForUI(id string, provider config.WebSearchProviderConfig, cfg *config.WebSearchConfig) bool {
	if provider.Enabled != nil {
		return *provider.Enabled
	}
	if id == "duckduckgo" {
		return true
	}
	_, configured := cfg.Providers[webProviderConfigID(id)]
	return configured
}

func (m *Model) webRoutingFields() []webField {
	cfg := m.webConfig()
	provider := firstWebValue(cfg.Provider, "auto")
	parallel := "off"
	if cfg.Parallel != nil && *cfg.Parallel {
		parallel = "on"
	}
	return []webField{
		{id: "provider", label: "provider mode", value: provider, options: webProviderModeChoices(), detail: "Automatic uses healthy configured providers; Parallel is bounded"},
		{id: "max_results", label: "max results", value: intValueDefault(cfg.MaxResults, 0), options: webNumericChoices("1", "3", "5", "8", "10"), detail: "number of results returned; Default is 5"},
		{id: "max_searches_per_session", label: "search budget", value: intValueDefault(searchBudgetConfigValue(cfg), 0), options: webNumericChoices("1", "3", "5", "10", "20", "50"), detail: "logical searches in one session, not upstream attempts"},
		{id: "parallel", label: "parallel execution", value: parallel, options: webChoiceOptions("off", "on"), toggle: true, detail: "switch whether selected providers run concurrently within global gates"},
		{id: "max_parallel", label: "max parallel", value: intValueDefault(cfg.MaxParallel, 0), options: webNumericChoices("1", "2", "3", "4"), detail: "bounded provider execution limit; Default is 4"},
		{id: "max_concurrent", label: "global concurrency", value: intValueDefault(cfg.MaxConcurrent, 0), options: webNumericChoices("1", "2", "3", "4"), detail: "additional logical provider cap; Default uses the safe runtime limit"},
		{id: "cache_ttl_seconds", label: "cache lifetime", value: intValueDefault(cfg.CacheTTLSeconds, 0), options: webNumericChoices("60", "300", "900", "1800", "3600", "21600", "86400"), detail: "positive-result cache lifetime"},
		{id: "allow_domains", label: "global allow domains", value: strings.Join(cfg.AllowDomains, ", "), detail: "comma-separated; empty means unrestricted"},
		{id: "deny_domains", label: "global deny domains", value: strings.Join(cfg.DenyDomains, ", "), detail: "comma-separated blocked domains"},
	}
}

func searchBudgetConfigValue(cfg *config.WebSearchConfig) int {
	if cfg == nil {
		return 0
	}
	if cfg.LogicalBudget > 0 {
		return cfg.LogicalBudget
	}
	return cfg.MaxSearchesPerSession
}

func effectiveSearchBudget(cfg *config.WebSearchConfig) int {
	if cfg != nil && cfg.LogicalBudget > 0 {
		return cfg.LogicalBudget
	}
	if cfg != nil && cfg.MaxSearchesPerSession > 0 {
		return cfg.MaxSearchesPerSession
	}
	return 10
}

func effectiveMaxParallel(cfg *config.WebSearchConfig) int {
	if cfg != nil && cfg.MaxParallel > 0 {
		return cfg.MaxParallel
	}
	return 4
}

func firstWebValue(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func intValue(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func intValueDefault(value, fallback int) string {
	if value == 0 {
		if fallback == 0 {
			return "default"
		}
		return strconv.Itoa(fallback)
	}
	return strconv.Itoa(value)
}

func floatValue(value float64) string {
	if value <= 0 {
		return "default"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func (m *Model) webView() string {
	s := m.styles
	width := m.width - 4
	if width > 96 {
		width = 96
	}
	if width < 52 {
		width = 52
	}

	title := "web providers"
	switch m.web.stage {
	case webProviderRouting:
		title = "web providers · routing"
	case webProviderMenu:
		title = "web providers · " + m.webProviderSpec(m.web.selected).label
	case webProviderEdit:
		title = "web providers · edit setting"
	}

	var b strings.Builder
	b.WriteString(s.Primary.Render(title) + "\n\n")
	switch m.web.stage {
	case webProviderList:
		b.WriteString(m.webProviderListBody(width))
	case webProviderRouting:
		b.WriteString(m.webFieldListBody(m.webRoutingFields(), width))
	case webProviderMenu:
		b.WriteString(m.webFieldListBody(m.webFields(), width))
	case webProviderEdit:
		b.WriteString(m.webFieldBody(width))
	}
	if m.web.statusLine != "" {
		b.WriteString("\n" + m.web.statusLine + "\n")
	}
	b.WriteString("\n  " + m.webButtonLabel(false) + " " + m.webButtonLabel(true) + "\n")
	b.WriteString(s.Faint.Render(m.webHint()))

	if m.height < 14 {
		return padHeight(trimHeight(b.String(), m.height-1), m.height-1)
	}
	return s.Overlay.Width(width).Render(b.String())
}

func (m *Model) webProviderListBody(width int) string {
	s := m.styles
	var b strings.Builder
	rows, from, to := m.webVisibleRows()
	if from > 0 {
		b.WriteString(s.Faint.Render(fmt.Sprintf("  ↑ %d more above", from)) + "\n")
	}
	for i, row := range rows {
		index := from + i
		marker := s.Faint.Render(fmt.Sprintf("%2d ", index+1))
		label := s.Muted.Render(row.label)
		if index == m.web.cursor {
			marker = s.Primary.Render("❯ ")
			label = s.Base.Render(row.label)
		}
		line := marker + padRight(truncate(label, 28), 28) + s.Faint.Render(truncate(row.detail, width-34))
		b.WriteString(line + "\n")
	}
	if to < len(m.web.rows) {
		b.WriteString(s.Faint.Render(fmt.Sprintf("  ↓ %d more below", len(m.web.rows)-to)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(s.Muted.Render("  Select a provider to change its endpoint, key, defaults, and routing weight.") + "\n")
	return b.String()
}

func (m *Model) webVisibleRows() ([]webProviderRow, int, int) {
	per := m.webPageSize()
	if per >= len(m.web.rows) {
		return m.web.rows, 0, len(m.web.rows)
	}
	from := m.web.scroll
	if from > len(m.web.rows)-per {
		from = len(m.web.rows) - per
	}
	if from < 0 {
		from = 0
	}
	return m.web.rows[from : from+per], from, from + per
}

func (m *Model) webPageSize() int {
	size := m.height - 13
	if size < 3 {
		size = 3
	}
	return size
}

func (m *Model) webFieldListBody(fields []webField, width int) string {
	s := m.styles
	var b strings.Builder
	for i, field := range fields {
		marker := s.Faint.Render(fmt.Sprintf("%2d ", i+1))
		label := s.Muted.Render(field.label)
		if i == m.web.cursor {
			marker = s.Primary.Render("❯ ")
			label = s.Base.Render(field.label)
		}
		value := field.value
		if field.id == "api_key" {
			value = "configured"
			if m.webConfig().Providers[webProviderConfigID(m.web.selected)].APIKey == "" {
				value = "not set"
			}
		}
		if len(field.options) > 0 {
			for _, option := range field.options {
				if option.value == field.value {
					value = option.label
					break
				}
			}
			value += " ▾"
		}
		if field.toggle {
			value = onOff(field.value == "on")
		}
		line := marker + padRight(truncate(label, 25), 25) + s.Faint.Render(truncate(value, width-31))
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + s.Faint.Render("  Enter edit/toggle · ↑↓ select · esc/backspace back") + "\n")
	return b.String()
}

func (m *Model) webFieldBody(width int) string {
	s := m.styles
	field := m.currentWebField()
	if field == nil {
		return s.Error.Render("  setting unavailable")
	}
	b := strings.Builder{}
	b.WriteString(s.Muted.Render(field.label) + "\n")
	if field.detail != "" {
		b.WriteString(s.Faint.Render("  "+field.detail) + "\n")
	}
	if field.secret {
		keyStatus := "not set"
		if m.webConfig().Providers[webProviderConfigID(m.web.selected)].APIKey != "" {
			keyStatus = "[REDACTED]"
		}
		b.WriteString(s.Faint.Render("  current key: "+keyStatus) + "\n")
	}
	if len(field.options) > 0 && !m.web.customInput {
		choiceCursor := m.web.choiceCursor
		if choiceCursor < 0 || choiceCursor >= len(field.options) {
			choiceCursor = 0
		}
		option := field.options[choiceCursor]
		b.WriteString("\n  " + s.Base.Render("‹ "+option.label+" ›") + "\n")
		if option.detail != "" {
			b.WriteString(s.Faint.Render("  "+option.detail) + "\n")
		}
		b.WriteString(s.Faint.Render("  ↑↓ choose · Enter save · Esc cancel"))
		return b.String()
	}
	value := m.web.inputBuf
	if field.secret {
		value = maskKey(value)
	}
	b.WriteString("\n  " + s.Base.Render(truncate(value, width-6)+"█") + "\n")
	if len(field.options) > 0 && m.web.customInput {
		b.WriteString(s.Faint.Render("  custom value · Ctrl+U clear · Enter save · Esc cancel"))
	} else {
		b.WriteString(s.Faint.Render("  Ctrl+U clear · Enter save · Esc cancel"))
	}
	return b.String()
}

func (m *Model) currentWebField() *webField {
	fields := m.web.fields
	for i := range fields {
		if fields[i].id == m.web.fieldID {
			return &fields[i]
		}
	}
	return nil
}

func (m *Model) webHint() string {
	switch m.web.stage {
	case webProviderList:
		return "↑↓ select · enter configure · numbers/provider mode · esc close"
	case webProviderRouting, webProviderMenu:
		return "↑↓ select · enter edit/toggle · esc/backspace back"
	case webProviderEdit:
		return "enter save · ctrl+u clear · esc cancel"
	default:
		return "esc close"
	}
}

func (m *Model) webButtonLabel(primary bool) string {
	if primary {
		switch m.web.stage {
		case webProviderEdit:
			return m.choiceButtonStyle(true).Render("↵ Save")
		case webProviderList:
			return m.choiceButtonStyle(true).Render("↵ Configure")
		default:
			return m.choiceButtonStyle(true).Render("↵ Edit")
		}
	}
	return m.choiceButtonStyle(false).Render("← Back")
}

func (m *Model) webButtonZones() []authButtonZone {
	if !m.web.active {
		return nil
	}
	panel := m.webView()
	panelWidth := lipgloss.Width(panel)
	panelLeft := (m.width - panelWidth) / 2
	panelTop := (m.height - lipgloss.Height(panel)) / 2
	buttonRow := -1
	panelLine := ""
	primary := m.webButtonLabel(true)
	for row, line := range strings.Split(panel, "\n") {
		if strings.Contains(line, "← Back") && strings.Contains(line, primary) {
			buttonRow = row
			panelLine = line
			break
		}
	}
	if buttonRow < 0 {
		// Styled output may not contain the plain label; use the first button row.
		for row, line := range strings.Split(panel, "\n") {
			if strings.Contains(line, "← Back") {
				buttonRow = row
				panelLine = line
				break
			}
		}
	}
	if buttonRow < 0 {
		return nil
	}
	backLabel := "← Back"
	primaryLabel := map[bool]string{true: "↵ Save", false: "↵ Edit"}[m.web.stage == webProviderEdit]
	if m.web.stage == webProviderList {
		primaryLabel = "↵ Configure"
	}
	backIndex := strings.Index(panelLine, backLabel)
	primaryIndex := strings.Index(panelLine, primaryLabel)
	if backIndex < 0 || primaryIndex < 0 {
		return nil
	}
	backRendered := m.choiceButtonStyle(false).Render(backLabel)
	primaryRendered := m.choiceButtonStyle(true).Render(primaryLabel)
	backWidth := lipgloss.Width(backRendered)
	primaryWidth := lipgloss.Width(primaryRendered)
	backPadding := (backWidth - lipgloss.Width(backLabel)) / 2
	primaryPadding := (primaryWidth - lipgloss.Width(primaryLabel)) / 2
	return []authButtonZone{
		{id: webButtonBack, x: panelLeft + lipgloss.Width(panelLine[:backIndex]) - backPadding, y: panelTop + buttonRow, width: backWidth},
		{id: webButtonPrimary, x: panelLeft + lipgloss.Width(panelLine[:primaryIndex]) - primaryPadding, y: panelTop + buttonRow, width: primaryWidth},
	}
}

func (m *Model) webButtonAt(x, y int) (authButtonZone, bool) {
	for _, zone := range m.webButtonZones() {
		if x >= zone.x && x < zone.x+zone.width && y == zone.y {
			return zone, true
		}
	}
	return authButtonZone{}, false
}

func (m *Model) handleWebButton(zone authButtonZone) (tea.Model, tea.Cmd) {
	if zone.id == webButtonBack {
		return m.webBack()
	}
	return m.handleWebKey(tea.KeyMsg{Type: tea.KeyEnter}, "enter")
}

func (m *Model) handleWebMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button == tea.MouseButtonWheelUp {
		m.webScrollBy(-m.scrollStep())
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		m.webScrollBy(m.scrollStep())
		return m, nil
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if button, ok := m.webButtonAt(msg.X, msg.Y); ok {
			return m.handleWebButton(button)
		}
	}
	return m, nil
}

func (m *Model) webScrollBy(delta int) {
	per := m.webPageSize()
	maxScroll := len(m.web.rows) - per
	if maxScroll < 0 {
		maxScroll = 0
	}
	m.web.scroll += delta
	if m.web.scroll < 0 {
		m.web.scroll = 0
	}
	if m.web.scroll > maxScroll {
		m.web.scroll = maxScroll
	}
}

func (m *Model) webRevealRow(index int) {
	per := m.webPageSize()
	if index < m.web.scroll {
		m.web.scroll = index
	}
	if index >= m.web.scroll+per {
		m.web.scroll = index - per + 1
	}
	m.webScrollBy(0)
}

func (m *Model) handleWebKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	if key == "esc" || (key == "backspace" && m.web.stage != webProviderEdit && m.web.inputBuf == "") {
		return m.webBack()
	}
	switch m.web.stage {
	case webProviderList:
		return m.webListKey(msg, key)
	case webProviderRouting, webProviderMenu:
		return m.webMenuKey(key)
	case webProviderEdit:
		return m.webFieldKey(msg, key)
	default:
		return m, nil
	}
}

func (m *Model) webListKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		input := strings.TrimSpace(strings.ToLower(m.web.inputBuf))
		m.web.inputBuf = ""
		if input == "" {
			return m.selectWebRow(m.web.rows[m.web.cursor])
		}
		if input == "r" || input == "routing" {
			return m.selectWebRow(m.web.rows[0])
		}
		if index, err := strconv.Atoi(input); err == nil && index >= 1 && index <= len(m.web.rows) {
			return m.selectWebRow(m.web.rows[index-1])
		}
		for _, row := range m.web.rows {
			if strings.EqualFold(row.id, input) || strings.EqualFold(row.label, input) {
				return m.selectWebRow(row)
			}
		}
		m.web.statusLine = m.styles.Error.Render("  no web-provider setting matches " + strconv.Quote(input))
		return m, nil
	case "up", "ctrl+p":
		if m.web.cursor > 0 {
			m.web.cursor--
		}
		m.webRevealRow(m.web.cursor)
		return m, nil
	case "down", "ctrl+n":
		if m.web.cursor < len(m.web.rows)-1 {
			m.web.cursor++
		}
		m.webRevealRow(m.web.cursor)
		return m, nil
	case "pgup":
		m.webScrollBy(-m.webPageSize())
		return m, nil
	case "pgdown":
		m.webScrollBy(m.webPageSize())
		return m, nil
	case "home":
		m.web.cursor = 0
		m.webRevealRow(0)
		return m, nil
	case "end":
		m.web.cursor = len(m.web.rows) - 1
		m.webRevealRow(m.web.cursor)
		return m, nil
	case "backspace":
		if len(m.web.inputBuf) > 0 {
			m.web.inputBuf = m.web.inputBuf[:len(m.web.inputBuf)-1]
		}
		return m, nil
	}
	if len(msg.Runes) > 0 {
		m.web.inputBuf += string(msg.Runes)
		m.web.statusLine = ""
	}
	return m, nil
}

func (m *Model) selectWebRow(row webProviderRow) (tea.Model, tea.Cmd) {
	m.web.statusLine = ""
	m.web.cursor = 0
	if row.id == "routing" {
		m.web.stage = webProviderRouting
		m.web.selected = ""
		m.web.fields = m.webRoutingFields()
		return m, nil
	}
	m.web.stage = webProviderMenu
	m.web.selected = row.id
	m.web.fields = m.webFields()
	return m, nil
}

func (m *Model) webMenuKey(key string) (tea.Model, tea.Cmd) {
	fields := m.web.fields
	if m.web.stage == webProviderRouting {
		fields = m.webRoutingFields()
	} else if m.web.stage == webProviderMenu {
		fields = m.webFields()
	}
	m.web.fields = fields
	if len(fields) == 0 {
		return m, nil
	}
	switch key {
	case "up", "ctrl+p":
		if m.web.cursor > 0 {
			m.web.cursor--
		}
	case "down", "ctrl+n":
		if m.web.cursor < len(fields)-1 {
			m.web.cursor++
		}
	case "home":
		m.web.cursor = 0
	case "end":
		m.web.cursor = len(fields) - 1
	case "enter":
		return m.openWebField(fields[m.web.cursor])
	default:
		if n, err := strconv.Atoi(key); err == nil && n >= 1 && n <= len(fields) {
			m.web.cursor = n - 1
			return m.openWebField(fields[m.web.cursor])
		}
	}
	return m, nil
}

func (m *Model) openWebField(field webField) (tea.Model, tea.Cmd) {
	if field.id == "enabled" {
		return m.toggleWebProvider()
	}
	if field.toggle {
		next := "on"
		if field.value == "on" {
			next = "off"
		}
		m.web.returnTo = m.web.stage
		return m.applyWebField(&field, next)
	}
	if field.id == "health_reset" {
		tools.ResetProviderHealth(m.web.selected)
		m.web.statusLine = m.styles.Success.Render("  provider health reset")
		m.web.fields = m.webFields()
		m.rebuildWebProviderRows()
		return m, nil
	}
	m.web.returnTo = m.web.stage
	m.web.stage = webProviderEdit
	m.web.fieldID = field.id
	m.web.inputBuf = field.value
	m.web.customInput = false
	m.web.choiceCursor = webOptionIndex(field.options, field.value)
	if m.web.choiceCursor < 0 {
		m.web.choiceCursor = webOptionIndex(field.options, "__custom__")
		m.web.customInput = m.web.choiceCursor >= 0
		if !m.web.customInput {
			m.web.choiceCursor = 0
		}
	}
	if field.secret {
		m.web.inputBuf = ""
	}
	m.web.statusLine = ""
	return m, nil
}

func (m *Model) webFieldKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	field := m.currentWebField()
	if field == nil {
		return m.webBack()
	}
	if len(field.options) > 0 && !m.web.customInput {
		switch key {
		case "up", "left", "ctrl+p":
			m.web.choiceCursor = (m.web.choiceCursor - 1 + len(field.options)) % len(field.options)
			m.web.inputBuf = field.options[m.web.choiceCursor].value
		case "down", "right", "ctrl+n":
			m.web.choiceCursor = (m.web.choiceCursor + 1) % len(field.options)
			m.web.inputBuf = field.options[m.web.choiceCursor].value
		case "enter":
			if field.options[m.web.choiceCursor].value == "__custom__" {
				m.web.customInput = true
				m.web.inputBuf = ""
				return m, nil
			}
			return m.applyWebField(field, field.options[m.web.choiceCursor].value)
		}
		return m, nil
	}
	switch key {
	case "enter":
		return m.applyWebField(field, strings.TrimSpace(m.web.inputBuf))
	case "backspace":
		if len(m.web.inputBuf) > 0 {
			m.web.inputBuf = m.web.inputBuf[:len(m.web.inputBuf)-1]
		}
	case "ctrl+u":
		m.web.inputBuf = ""
	case "ctrl+v":
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			for _, r := range msg.Runes {
				if r != '\r' && r != '\n' && r != 0 {
					m.web.inputBuf += string(r)
				}
			}
		}
	}
	return m, nil
}

func webOptionIndex(options []webOption, value string) int {
	for index, option := range options {
		if option.value == value {
			return index
		}
	}
	return -1
}

func (m *Model) toggleWebProvider() (tea.Model, tea.Cmd) {
	cfg := m.webConfig()
	providerID := webProviderConfigID(m.web.selected)
	provider := cfg.Providers[providerID]
	enabled := providerEnabledForUI(m.web.selected, provider, cfg)
	next := !enabled
	provider.Enabled = &next
	cfg.Providers[providerID] = provider
	m.syncWebSearchTool()
	if err := config.SaveWebSearchConfig(*cfg); err != nil {
		m.web.statusLine = m.styles.Error.Render("  save failed: " + err.Error())
		return m, nil
	}
	m.web.statusLine = m.styles.Success.Render(fmt.Sprintf("  %s %s", m.webProviderSpec(m.web.selected).label, onOff(next)))
	m.web.fields = m.webFields()
	m.rebuildWebProviderRows()
	return m, nil
}

func (m *Model) applyWebField(field *webField, value string) (tea.Model, tea.Cmd) {
	cfg := m.webConfig()
	var err error
	if m.web.stage != webProviderEdit && !field.toggle {
		return m, nil
	}
	if m.web.returnTo == webProviderMenu && field.id == "health_reset" {
		if strings.ToLower(strings.TrimSpace(value)) != "reset" {
			m.web.statusLine = m.styles.Error.Render("  type reset to clear provider health")
			return m, nil
		}
		tools.ResetProviderHealth(m.web.selected)
		m.web.stage = m.web.returnTo
		m.web.inputBuf = ""
		m.web.statusLine = m.styles.Success.Render("  provider health reset")
		m.web.cursor = 0
		m.web.fields = m.webFields()
		m.rebuildWebProviderRows()
		return m, nil
	}
	if m.web.returnTo == webProviderRouting {
		err = applyWebRoutingField(cfg, field.id, value)
	} else {
		providerID := webProviderConfigID(m.web.selected)
		provider := cfg.Providers[providerID]
		err = applyWebProviderField(&provider, field.id, value, m.web.selected)
		if err == nil {
			cfg.Providers[providerID] = provider
		}
	}
	if err != nil {
		m.web.statusLine = m.styles.Error.Render("  " + err.Error())
		return m, nil
	}
	m.syncWebSearchTool()
	if err := config.SaveWebSearchConfig(*cfg); err != nil {
		m.web.statusLine = m.styles.Error.Render("  save failed: " + err.Error())
		return m, nil
	}
	m.web.stage = m.web.returnTo
	m.web.inputBuf = ""
	m.web.statusLine = m.styles.Success.Render("  saved")
	m.web.cursor = 0
	if m.web.stage == webProviderRouting {
		m.web.fields = m.webRoutingFields()
	} else {
		m.web.fields = m.webFields()
	}
	m.rebuildWebProviderRows()
	return m, nil
}

func applyWebRoutingField(cfg *config.WebSearchConfig, id, value string) error {
	switch id {
	case "provider":
		allowed := []string{"auto", "parallel", "duckduckgo", "ddginstant", "searxng", "brave", "ollama", "exa", "tavily", "serper", "you", "firecrawl", "serpapi", "google_cse", "jina", "gdelt", "mediawiki", "arxiv", "crossref", "openalex", "github", "stackexchange", "hackernews", "archive"}
		if !containsWeb(allowed, strings.ToLower(value)) {
			return fmt.Errorf("provider mode is not supported")
		}
		cfg.Provider = strings.ToLower(value)
	case "max_results":
		if isWebDefault(value) {
			cfg.MaxResults = 0
			return nil
		}
		cfg.MaxResults = 0
		return assignPositiveInt(&cfg.MaxResults, value, "max results")
	case "max_searches_per_session":
		if isWebDefault(value) {
			cfg.MaxSearchesPerSession = 0
			return nil
		}
		cfg.MaxSearchesPerSession = 0
		return assignPositiveInt(&cfg.MaxSearchesPerSession, value, "search budget")
	case "parallel":
		enabled, err := parseWebBool(value)
		if err != nil {
			return err
		}
		cfg.Parallel = &enabled
	case "max_parallel":
		if isWebDefault(value) {
			cfg.MaxParallel = 0
			return nil
		}
		cfg.MaxParallel = 0
		return assignPositiveInt(&cfg.MaxParallel, value, "max parallel")
	case "max_concurrent":
		if isWebDefault(value) {
			cfg.MaxConcurrent = 0
			return nil
		}
		cfg.MaxConcurrent = 0
		return assignPositiveInt(&cfg.MaxConcurrent, value, "global concurrency")
	case "cache_ttl_seconds":
		if isWebDefault(value) {
			cfg.CacheTTLSeconds = 0
			return nil
		}
		cfg.CacheTTLSeconds = 0
		return assignPositiveInt(&cfg.CacheTTLSeconds, value, "cache TTL")
	case "allow_domains":
		cfg.AllowDomains = parseWebDomains(value)
	case "deny_domains":
		cfg.DenyDomains = parseWebDomains(value)
	}
	return nil
}

func isWebDefault(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || trimmed == "-" || strings.EqualFold(trimmed, "default")
}

func applyWebProviderField(provider *config.WebSearchProviderConfig, id, value, providerID string) error {
	switch id {
	case "api_key":
		if value == "" {
			return nil
		}
		if value == "-" {
			provider.APIKey = ""
		} else {
			provider.APIKey = value
		}
	case "base_url":
		if value == "-" {
			value = ""
			provider.ClearBaseURL = true
		} else {
			provider.ClearBaseURL = false
		}
		provider.BaseURL = value
	case "api_key_env":
		provider.APIKeyEnv = strings.TrimSpace(value)
	case "kind":
		if !containsWeb([]string{"api", "local", "public_instance", "domain"}, strings.ToLower(value)) {
			return fmt.Errorf("provider kind must be api, local, public_instance, or domain")
		}
		provider.Kind = strings.ToLower(value)
	case "instances":
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) == "-" {
			provider.Instances = nil
		} else {
			provider.Instances = parseWebDomains(value)
		}
	case "priority":
		if isWebDefault(value) {
			provider.Priority = 0
			return nil
		}
		provider.Priority = 0
		return assignPositiveInt(&provider.Priority, value, "priority")
	case "max_rpm":
		if isWebDefault(value) {
			provider.MaxRPM = 0
			return nil
		}
		provider.MaxRPM = 0
		return assignPositiveInt(&provider.MaxRPM, value, "max requests/minute")
	case "max_concurrency":
		if isWebDefault(value) {
			provider.MaxConcurrency = 0
			return nil
		}
		provider.MaxConcurrency = 0
		return assignPositiveInt(&provider.MaxConcurrency, value, "max concurrency")
	case "timeout_seconds":
		if isWebDefault(value) {
			provider.TimeoutSeconds = 0
			return nil
		}
		provider.TimeoutSeconds = 0
		return assignPositiveInt(&provider.TimeoutSeconds, value, "provider timeout")
	case "cache_ttl_seconds":
		if isWebDefault(value) {
			provider.CacheTTLSeconds = 0
			return nil
		}
		provider.CacheTTLSeconds = 0
		return assignPositiveInt(&provider.CacheTTLSeconds, value, "cache TTL")
	case "backend":
		if providerID != "google_cse" && !containsWeb([]string{"lite", "instant", "auto"}, strings.ToLower(value)) {
			return fmt.Errorf("DuckDuckGo backend must be lite, instant, or auto")
		}
		if providerID == "google_cse" && strings.TrimSpace(value) == "" {
			return fmt.Errorf("Google CSE search engine ID is required")
		}
		provider.Backend = strings.TrimSpace(value)
	case "region":
		provider.Region = strings.ToLower(value)
	case "safe_search":
		if !containsWeb([]string{"off", "moderate", "strict"}, strings.ToLower(value)) {
			return fmt.Errorf("safe search must be off, moderate, or strict")
		}
		provider.SafeSearch = strings.ToLower(value)
	case "time_range":
		if !isWebDefault(value) && !containsWeb([]string{"day", "week", "month", "year"}, strings.ToLower(value)) {
			return fmt.Errorf("time range must be day, week, month, or year")
		}
		if isWebDefault(value) {
			provider.TimeRange = ""
		} else {
			provider.TimeRange = strings.ToLower(value)
		}
	case "type":
		if value == "" {
			return fmt.Errorf("a search type is required")
		}
		provider.Type = strings.ToLower(value)
	case "livecrawl":
		if !containsWeb([]string{"fallback", "preferred", "always", "never"}, strings.ToLower(value)) {
			return fmt.Errorf("live crawl must be fallback, preferred, always, or never")
		}
		provider.Livecrawl = strings.ToLower(value)
	case "max_age_hours":
		if isWebDefault(value) {
			provider.MaxAgeHours = nil
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("max age hours must be a non-negative integer")
		}
		provider.MaxAgeHours = &n
	case "include_domains":
		provider.IncludeDomains = parseWebDomains(value)
	case "exclude_domains":
		provider.ExcludeDomains = parseWebDomains(value)
	case "weight":
		if value == "" || value == "default" {
			provider.Weight = 0
			return nil
		}
		weight, err := strconv.ParseFloat(value, 64)
		if err != nil || weight <= 0 {
			return fmt.Errorf("weight must be a positive number")
		}
		provider.Weight = weight
	}
	return nil
}

func assignPositiveInt(target *int, value, label string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fmt.Errorf("%s must be a positive integer", label)
	}
	*target = n
	return nil
}

func parseWebBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes", "1":
		return true, nil
	case "off", "false", "no", "0":
		return false, nil
	default:
		return false, fmt.Errorf("use on or off")
	}
}

func parseWebDomains(value string) []string {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) == "-" {
		return nil
	}
	parts := strings.Split(value, ",")
	domains := make([]string, 0, len(parts))
	for _, part := range parts {
		if domain := strings.TrimSpace(part); domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains
}

func containsWeb(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (m *Model) webBack() (tea.Model, tea.Cmd) {
	switch m.web.stage {
	case webProviderEdit:
		m.web.stage = m.web.returnTo
		m.web.inputBuf = ""
		m.web.statusLine = ""
		if m.web.stage == webProviderRouting {
			m.web.fields = m.webRoutingFields()
		} else {
			m.web.fields = m.webFields()
		}
	case webProviderRouting, webProviderMenu:
		m.web.stage = webProviderList
		m.web.selected = ""
		m.web.inputBuf = ""
		m.web.statusLine = ""
		m.rebuildWebProviderRows()
	case webProviderList:
		m.web.active = false
		m.input.Focus()
		m.refresh()
	}
	return m, nil
}

// WebSearchActive reports whether the /webproviders flow is open.
func (m *Model) WebSearchActive() bool { return m.web.active }
