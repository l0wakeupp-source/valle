package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/config"
	"rick/internal/provider/catalog"
)

// ---------- /mcp management ----------

// cmdMcpMenu shows MCP server management (status, add, toggle, remove).
func (m *Model) cmdMcpMenu() (tea.Model, tea.Cmd) {
	if m.deps.MCP == nil {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "MCP is not initialised", Time: time.Now()})
		return m, nil
	}
	servers := m.deps.Loaded.Config.MCP
	if len(servers) == 0 {
		m.armChoice("no MCP servers configured", pendingMCPMenu, "", []choiceOption{
			{value: "__add__", label: "＋ Add MCP server"},
		})
		return m, nil
	}
	var opts []choiceOption
	for name, srv := range servers {
		status := "○"
		if m.deps.MCP != nil {
			for _, n := range m.deps.MCP.ServerNames() {
				if n == name {
					status = "●"
					break
				}
			}
		}
		if srv.Enabled != nil && !*srv.Enabled {
			status = "⊘"
		}
		detail := srv.Type
		if detail == "" {
			detail = "local"
		}
		if srv.URL != "" {
			detail += " · " + srv.URL
		}
		opts = append(opts, choiceOption{
			value:  name,
			label:  status + " " + name,
			detail: detail,
		})
	}
	opts = append(opts, choiceOption{value: "__add__", label: "＋ Add MCP server"})
	m.armChoice("MCP servers (pick to manage)", pendingMCPMenu, "", opts)
	return m, nil
}

// applyMcpMenu routes the MCP server list choice.
func (m *Model) cmdMcpApplyMenu(name string) (tea.Model, tea.Cmd) {
	if name == "__add__" {
		return m.cmdMcpAddName()
	}
	srv, ok := m.deps.Loaded.Config.MCP[name]
	if !ok {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "MCP server not found: " + name, Time: time.Now()})
		return m, nil
	}
	return m.cmdMcpServerMenu(name, srv)
}

// cmdMcpServerMenu shows actions for one MCP server.
func (m *Model) cmdMcpServerMenu(name string, srv config.MCPServer) (tea.Model, tea.Cmd) {
	isConnected := false
	for _, n := range m.deps.MCP.ServerNames() {
		if n == name {
			isConnected = true
			break
		}
	}
	statusStr := "disconnected"
	if isConnected {
		statusStr = "connected"
	}
	if srv.Enabled != nil && !*srv.Enabled {
		statusStr = "disabled"
	}
	title := fmt.Sprintf("%s · %s · %s", name, srv.Type, statusStr)
	m.armChoice(title, pendingMCPToggle, name, []choiceOption{
		{value: "toggle", label: "Toggle enabled"},
		{value: "remove", label: "Remove"},
	})
	return m, nil
}

// cmdMcpAddName prompts for the new server name.
func (m *Model) cmdMcpAddName() (tea.Model, tea.Cmd) {
	m.armInput("MCP server name:", pendingMCPAddName, "")
	return m, nil
}

// cmdMcpAddType asks local vs remote.
func (m *Model) cmdMcpAddType(name string) (tea.Model, tea.Cmd) {
	m.armChoice("server type for "+name, pendingMCPAddType, name, []choiceOption{
		{value: "local", label: "Local (stdio)"},
		{value: "remote", label: "Remote (HTTP)"},
	})
	return m, nil
}

// ---------- /refreshmodellist ----------

// cmdRefreshModelList re-probes every configured provider's /models endpoint
// and updates the in-memory model list.
func (m *Model) cmdRefreshModelList() (tea.Model, tea.Cmd) {
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "refreshing model lists…", Time: time.Now()})
	// Snapshot the credentials we need so the goroutine doesn't race the UI.
	creds := m.creds
	return m, func() tea.Msg {
		for id, cred := range creds.Providers {
			if cred.BaseURL == "" {
				continue
			}
			res := catalog.Probe(context.Background(), cred.BaseURL, cred.APIKey)
			if res.Err != nil || len(res.Models) == 0 {
				continue
			}
			cred.Models = nil
			cred.ContextWindows = map[string]int{}
			cred.VisionModels = nil
			for _, mm := range res.Models {
				cred.Models = append(cred.Models, mm.ID)
				if mm.Context > 0 {
					cred.ContextWindows[mm.ID] = mm.Context
				}
				if mm.SupportsImages {
					cred.VisionModels = append(cred.VisionModels, mm.ID)
				}
			}
			creds.Set(id, cred)
		}
		_ = creds.Save()
		return refreshDoneMsg{}
	}
}

type refreshDoneMsg struct{}

func (m *Model) applyRefreshDone() {
	m.reloadProviders()
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "model lists refreshed", Time: time.Now()})
}

// ---------- /stats ----------

// cmdStats shows a nicely formatted token usage summary.
func (m *Model) cmdStats() (tea.Model, tea.Cmd) {
	if m.deps.Usage == nil {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "usage tracking is not active", Time: time.Now()})
		return m, nil
	}
	models := m.deps.Usage.Models()
	if len(models) == 0 {
		m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "no usage recorded yet — run some prompts first", Time: time.Now()})
		return m, nil
	}
	s := m.styles
	var b strings.Builder

	// Build the table body in plain text (no ANSI codes inside columns).
	type row struct {
		provider, model             string
		input, output, cache, total int
	}
	var rows []row
	var grandIn, grandOut, grandCache int
	for _, id := range models {
		u := m.deps.Usage.ModelTotal(id)
		total := u.Input + u.Output + u.CacheRead + u.CacheWrite
		if total == 0 {
			continue
		}
		grandIn += u.Input
		grandOut += u.Output
		grandCache += u.CacheRead + u.CacheWrite
		// Split provider/model.
		prov := ""
		modelID := id
		if i := strings.Index(id, "/"); i >= 0 {
			prov = id[:i]
			modelID = id[i+1:]
		}
		rows = append(rows, row{prov, modelID, u.Input, u.Output, u.CacheRead + u.CacheWrite, total})
	}

	// Compute grand total from accumulated components.
	grandTotal := grandIn + grandOut + grandCache

	// Sort by total descending.
	sort.Slice(rows, func(i, j int) bool { return rows[i].total > rows[j].total })

	// Plain-text table.
	const (
		wProv   = 12
		wModel  = 32
		wInput  = 10
		wOutput = 10
		wCache  = 10
		wTotal  = 10
	)

	// Build the separator from the actual data format so it always matches.
	dataLine := fmt.Sprintf("  %s %s %s %s %s %s",
		padRight("", wProv), padRight("", wModel),
		padLeft("", wInput), padLeft("", wOutput),
		padLeft("", wCache), padLeft("", wTotal))
	line := strings.Repeat("-", len(dataLine))

	b.WriteString(s.Primary.Render("token usage") + "\n\n")

	// Build the whole table body in plain text, then style it once.
	var table strings.Builder
	table.WriteString(fmt.Sprintf("  %s %s %s %s %s %s\n",
		padRight("provider", wProv), padRight("model", wModel),
		padLeft("input", wInput), padLeft("output", wOutput),
		padLeft("cache", wCache), padLeft("total", wTotal)))
	table.WriteString(line + "\n")

	for _, r := range rows {
		table.WriteString(fmt.Sprintf("  %s %s %s %s %s %s\n",
			padRight(r.provider, wProv), padRight(truncate(r.model, wModel-1), wModel),
			padLeft(humanTokens(r.input), wInput), padLeft(humanTokens(r.output), wOutput),
			padLeft(humanTokens(r.cache), wCache), padLeft(humanTokens(r.total), wTotal)))
	}

	table.WriteString(line + "\n")
	table.WriteString(fmt.Sprintf("  %s %s %s %s %s %s\n",
		padRight("TOTAL", wProv), padRight("", wModel),
		padLeft(humanTokens(grandIn), wInput), padLeft(humanTokens(grandOut), wOutput),
		padLeft(humanTokens(grandCache), wCache), padLeft(humanTokens(grandTotal), wTotal)))

	b.WriteString(s.Faint.Render(table.String()))

	// Per-day breakdown.
	days := m.deps.Usage.Days()
	if len(days) > 0 {
		b.WriteString("\n" + s.Faint.Render("per day (newest first):\n"))
		for _, d := range days {
			dayTotal := m.deps.Usage.GrandTotal(d)
			grand := dayTotal.Total()
			if grand == 0 {
				continue
			}
			b.WriteString(s.Faint.Render(
				fmt.Sprintf("  %s  %s tokens  (in %s · out %s · cache %s)\n",
					d, humanTokens(grand),
					humanTokens(dayTotal.Input),
					humanTokens(dayTotal.Output),
					humanTokens(dayTotal.CacheRead+dayTotal.CacheWrite))))
		}
	}

	b.WriteString("\n" + s.Faint.Render("usage saved to: "+m.deps.Usage.Path()))
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: b.String(), Time: time.Now()})
	return m, nil
}

// padLeft right-aligns a string within width n.
func padLeft(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

// cmdMcpToggleServer toggles an MCP server's enabled state.
func (m *Model) cmdMcpToggleServer(name string) (tea.Model, tea.Cmd) {
	srv, ok := m.deps.Loaded.Config.MCP[name]
	if !ok {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "MCP server not found: " + name, Time: time.Now()})
		return m, nil
	}
	if srv.Enabled == nil {
		enabled := true
		srv.Enabled = &enabled
	}
	*srv.Enabled = !*srv.Enabled
	m.deps.Loaded.Config.MCP[name] = srv
	state := "enabled"
	if !*srv.Enabled {
		state = "disabled"
	}
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: fmt.Sprintf("MCP server %s: %s (restart to apply)", name, state), Time: time.Now()})
	return m, nil
}

// cmdMcpRemoveServer removes an MCP server from the config.
func (m *Model) cmdMcpRemoveServer(name string) (tea.Model, tea.Cmd) {
	delete(m.deps.Loaded.Config.MCP, name)
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: fmt.Sprintf("MCP server removed: %s", name), Time: time.Now()})
	return m, nil
}

// cmdMcpSaveAndConnect saves a new MCP server to config.
func (m *Model) cmdMcpSaveAndConnect(name string, srv config.MCPServer) (tea.Model, tea.Cmd) {
	if m.deps.Loaded.Config.MCP == nil {
		m.deps.Loaded.Config.MCP = map[string]config.MCPServer{}
	}
	m.deps.Loaded.Config.MCP[name] = srv
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: fmt.Sprintf("MCP server added: %s (restart to connect)", name), Time: time.Now()})
	return m, nil
}
