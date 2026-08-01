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

type webField struct {
	id      string
	label   string
	value   string
	detail  string
	options []string
	secret  bool
}

type webSearchState struct {
	active bool
	stage  webProviderStage

	rows   []webProviderRow
	cursor int
	scroll int

	selected string
	fields   []webField
	fieldID  string
	inputBuf string
	returnTo webProviderStage

	statusLine string
}

const (
	webButtonBack    = "web-back"
	webButtonPrimary = "web-primary"
)

var webProviderSpecs = []webProviderSpec{
	{id: "duckduckgo", label: "DuckDuckGo", description: "free HTML/instant search; no API key"},
	{id: "ollama", label: "Ollama", description: "Ollama Cloud or local experimental proxy"},
	{id: "exa", label: "Exa", description: "neural search with live crawl and freshness"},
	{id: "tavily", label: "Tavily", description: "AI search API with topic and freshness"},
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
	provider, configured := cfg.Providers[spec.id]
	if provider.Enabled != nil && !*provider.Enabled {
		return "disabled"
	}
	if spec.id == "duckduckgo" && !configured {
		return "enabled by default · lite backend"
	}
	if !configured {
		return "not configured"
	}
	details := []string{"enabled"}
	if provider.APIKey != "" {
		details = append(details, "key set")
	}
	if provider.BaseURL != "" {
		details = append(details, webHostOf(provider.BaseURL))
	}
	return strings.Join(details, " · ")
}

func webHostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Host
}

func (m *Model) webProviderSpec(id string) webProviderSpec {
	for _, spec := range webProviderSpecs {
		if spec.id == id {
			return spec
		}
	}
	return webProviderSpec{id: id, label: id}
}

func (m *Model) webFields() []webField {
	cfg := m.webConfig()
	provider := cfg.Providers[m.web.selected]
	enabled := providerEnabledForUI(m.web.selected, provider, cfg)
	fields := []webField{{
		id:     "enabled",
		label:  map[bool]string{true: "disable provider", false: "enable provider"}[enabled],
		value:  map[bool]string{true: "on", false: "off"}[enabled],
		detail: "toggle whether auto/parallel may use this provider",
	}}

	if m.web.selected != "duckduckgo" {
		fields = append(fields, webField{
			id: "api_key", label: "API key", value: "", secret: true,
			detail: "leave blank to keep the current key; type - to clear it",
		})
	}
	fields = append(fields, webField{
		id: "base_url", label: "base URL", value: provider.BaseURL,
		detail: "leave blank to use the built-in endpoint",
	})

	switch m.web.selected {
	case "duckduckgo":
		fields = append(fields,
			webField{id: "backend", label: "backend", value: firstWebValue(provider.Backend, "lite"), options: []string{"lite", "instant", "auto"}, detail: "lite HTML or instant JSON"},
			webField{id: "region", label: "region", value: firstWebValue(provider.Region, "wt-wt"), detail: "DuckDuckGo region, e.g. de-de or us-en"},
			webField{id: "safe_search", label: "safe search", value: firstWebValue(provider.SafeSearch, "moderate"), options: []string{"off", "moderate", "strict"}, detail: "content filtering"},
			webField{id: "time_range", label: "time range", value: provider.TimeRange, options: []string{"", "day", "week", "month", "year"}, detail: "optional freshness filter"},
		)
	case "exa":
		fields = append(fields,
			webField{id: "type", label: "search type", value: firstWebValue(provider.Type, "auto"), options: []string{"auto", "fast", "deep"}, detail: "Exa search mode"},
			webField{id: "livecrawl", label: "live crawl", value: firstWebValue(provider.Livecrawl, "fallback"), options: []string{"fallback", "preferred", "always", "never"}, detail: "freshness strategy"},
			webField{id: "max_age_hours", label: "max age hours", value: intValue(provider.MaxAgeHours), detail: "cached content age; blank clears the override"},
			webField{id: "include_domains", label: "include domains", value: strings.Join(provider.IncludeDomains, ", "), detail: "comma-separated domain allowlist"},
			webField{id: "exclude_domains", label: "exclude domains", value: strings.Join(provider.ExcludeDomains, ", "), detail: "comma-separated domain denylist"},
		)
	case "tavily":
		fields = append(fields,
			webField{id: "type", label: "topic", value: firstWebValue(provider.Type, "general"), options: []string{"general", "news", "finance"}, detail: "Tavily topic"},
			webField{id: "include_domains", label: "include domains", value: strings.Join(provider.IncludeDomains, ", "), detail: "comma-separated domain allowlist"},
			webField{id: "exclude_domains", label: "exclude domains", value: strings.Join(provider.ExcludeDomains, ", "), detail: "comma-separated domain denylist"},
		)
	}
	fields = append(fields, webField{
		id: "weight", label: "provider weight", value: floatValue(provider.Weight),
		detail: "higher weight wins when results are merged",
	})
	return fields
}

func providerEnabledForUI(id string, provider config.WebSearchProviderConfig, cfg *config.WebSearchConfig) bool {
	if provider.Enabled != nil {
		return *provider.Enabled
	}
	if id == "duckduckgo" {
		return true
	}
	_, configured := cfg.Providers[id]
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
		{id: "provider", label: "provider mode", value: provider, options: []string{"auto", "parallel", "duckduckgo", "ollama", "exa", "tavily"}, detail: "auto chooses configured providers; parallel merges them"},
		{id: "max_results", label: "max results", value: strconv.Itoa(effectiveMaxResults(cfg)), detail: "default 5; maximum 10"},
		{id: "max_searches_per_session", label: "search budget", value: strconv.Itoa(effectiveSearchBudget(cfg)), detail: "maximum searches in one session"},
		{id: "parallel", label: "parallel execution", value: parallel, options: []string{"off", "on"}, detail: "run selected providers concurrently"},
		{id: "max_parallel", label: "max parallel", value: strconv.Itoa(effectiveMaxParallel(cfg)), detail: "concurrent provider limit"},
		{id: "allow_domains", label: "global allow domains", value: strings.Join(cfg.AllowDomains, ", "), detail: "comma-separated; empty means unrestricted"},
		{id: "deny_domains", label: "global deny domains", value: strings.Join(cfg.DenyDomains, ", "), detail: "comma-separated blocked domains"},
	}
}

func effectiveSearchBudget(cfg *config.WebSearchConfig) int {
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
			if m.webConfig().Providers[m.web.selected].APIKey == "" {
				value = "not set"
			}
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
		if m.webConfig().Providers[m.web.selected].APIKey != "" {
			keyStatus = "[REDACTED]"
		}
		b.WriteString(s.Faint.Render("  current key: "+keyStatus) + "\n")
	}
	if len(field.options) > 0 {
		b.WriteString(s.Faint.Render("  choices: "+strings.Join(displayWebOptions(field.options), " · ")) + "\n")
	}
	value := m.web.inputBuf
	if field.secret {
		value = maskKey(value)
	}
	b.WriteString("\n  " + s.Base.Render(truncate(value, width-6)+"█") + "\n")
	b.WriteString(s.Faint.Render("  Ctrl+U clear · Enter save · Esc cancel"))
	return b.String()
}

func displayWebOptions(options []string) []string {
	out := make([]string, len(options))
	for i, option := range options {
		if option == "" {
			out[i] = "(default)"
		} else {
			out[i] = option
		}
	}
	return out
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
	m.web.returnTo = m.web.stage
	m.web.stage = webProviderEdit
	m.web.fieldID = field.id
	m.web.inputBuf = field.value
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

func (m *Model) toggleWebProvider() (tea.Model, tea.Cmd) {
	cfg := m.webConfig()
	provider := cfg.Providers[m.web.selected]
	enabled := providerEnabledForUI(m.web.selected, provider, cfg)
	next := !enabled
	provider.Enabled = &next
	cfg.Providers[m.web.selected] = provider
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
	if m.web.stage != webProviderEdit {
		return m, nil
	}
	var err error
	if m.web.returnTo == webProviderRouting {
		err = applyWebRoutingField(cfg, field.id, value)
	} else {
		provider := cfg.Providers[m.web.selected]
		err = applyWebProviderField(&provider, field.id, value)
		if err == nil {
			cfg.Providers[m.web.selected] = provider
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
		if !containsWeb([]string{"auto", "parallel", "duckduckgo", "ollama", "exa", "tavily"}, strings.ToLower(value)) {
			return fmt.Errorf("provider mode must be auto, parallel, duckduckgo, ollama, exa, or tavily")
		}
		cfg.Provider = strings.ToLower(value)
	case "max_results":
		cfg.MaxResults = 0
		return assignPositiveInt(&cfg.MaxResults, value, "max results")
	case "max_searches_per_session":
		cfg.MaxSearchesPerSession = 0
		return assignPositiveInt(&cfg.MaxSearchesPerSession, value, "search budget")
	case "parallel":
		enabled, err := parseWebBool(value)
		if err != nil {
			return err
		}
		cfg.Parallel = &enabled
	case "max_parallel":
		cfg.MaxParallel = 0
		return assignPositiveInt(&cfg.MaxParallel, value, "max parallel")
	case "allow_domains":
		cfg.AllowDomains = parseWebDomains(value)
	case "deny_domains":
		cfg.DenyDomains = parseWebDomains(value)
	}
	return nil
}

func applyWebProviderField(provider *config.WebSearchProviderConfig, id, value string) error {
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
		}
		provider.BaseURL = value
	case "backend":
		if !containsWeb([]string{"lite", "instant", "auto"}, strings.ToLower(value)) {
			return fmt.Errorf("backend must be lite, instant, or auto")
		}
		provider.Backend = strings.ToLower(value)
	case "region":
		provider.Region = strings.ToLower(value)
	case "safe_search":
		if !containsWeb([]string{"off", "moderate", "strict"}, strings.ToLower(value)) {
			return fmt.Errorf("safe search must be off, moderate, or strict")
		}
		provider.SafeSearch = strings.ToLower(value)
	case "time_range":
		if value != "" && !containsWeb([]string{"day", "week", "month", "year"}, strings.ToLower(value)) {
			return fmt.Errorf("time range must be day, week, month, or year")
		}
		provider.TimeRange = strings.ToLower(value)
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
		if value == "" || value == "-" {
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
