package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/config"
	"rick/internal/sandbox"
)

// ---------- /yolo ----------

// cmdYolo toggles permission prompting off (or back on).
//
// When yolo is turned on the sandbox is automatically turned off so the agent
// can work unhindered; turning yolo off restores the sandbox to workspace-write.
func (m *Model) cmdYolo(args string) (tea.Model, tea.Cmd) {
	perms := m.deps.Perms
	if perms == nil {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "no permission engine is active", Time: time.Now()})
		return m, nil
	}

	want := !perms.Yolo()
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "on", "yes", "true", "1":
		want = true
	case "off", "no", "false", "0":
		want = false
	case "", "toggle":
	default:
		m.appendMsg(ChatMsg{Kind: MsgError,
			Text: "usage: /yolo [on|off]", Time: time.Now()})
		return m, nil
	}

	perms.SetYolo(want)

	// Link sandbox state to yolo: yolo on → sandbox off, yolo off → sandbox on.
	if m.deps.Sandbox != nil {
		if want {
			m.deps.Sandbox.SetMode(sandbox.ModeOff)
		} else {
			m.deps.Sandbox.SetMode(sandbox.ModeWorkspace)
		}
	}

	var b strings.Builder
	if want {
		b.WriteString("YOLO MODE ON — every tool call is auto-approved.\n")
		b.WriteString("No prompts for edits, writes, or shell commands.\n")
		b.WriteString("Sandbox turned OFF — commands run unconfined on the host.\n")
	} else {
		b.WriteString("yolo mode off — the permission policy applies again.\n")
		b.WriteString("Sandbox restored to workspace-write.")
		if p := perms.Profile(); p != "" {
			b.WriteString(" (profile: " + p + ")")
		}
	}

	kind := MsgSystem
	if want {
		kind = MsgError // render loud: this is a safety-relevant state change
	}
	m.appendMsg(ChatMsg{Kind: kind, Text: b.String(), Time: time.Now()})
	m.setStatus("yolo " + onOff(want))
	return m, nil
}

// ---------- /sandbox ----------

// cmdSandbox shows or changes the command sandbox.
func (m *Model) cmdSandbox(args string) (tea.Model, tea.Cmd) {
	holder := m.deps.Sandbox
	if holder == nil {
		m.appendMsg(ChatMsg{Kind: MsgError,
			Text: "no sandbox is wired up in this session", Time: time.Now()})
		return m, nil
	}

	arg := strings.ToLower(strings.TrimSpace(args))
	switch arg {
	case "":
		policy := holder.Policy()
		m.appendMsg(ChatMsg{Kind: MsgSystem,
			Text: policy.Detail(sandbox.BackendName(policy)), Time: time.Now()})
		return m, nil

	case "network on", "network":
		p := holder.SetNetwork(true)
		m.appendMsg(ChatMsg{Kind: MsgSystem,
			Text: "sandbox network enabled · " + p.Describe(), Time: time.Now()})
		m.setStatus("sandbox: network on")
		return m, nil

	case "network off":
		p := holder.SetNetwork(false)
		m.appendMsg(ChatMsg{Kind: MsgSystem,
			Text: "sandbox network denied · " + p.Describe(), Time: time.Now()})
		m.setStatus("sandbox: network off")
		return m, nil
	}

	mode, ok := sandbox.ParseMode(arg)
	if !ok {
		var b strings.Builder
		b.WriteString("usage: /sandbox [mode]\n\nmodes:\n")
		for _, md := range sandbox.Modes() {
			fmt.Fprintf(&b, "  %-16s %s\n", md, modeHelp(md))
		}
		b.WriteString("\nalso: /sandbox network on|off")
		m.appendMsg(ChatMsg{Kind: MsgError, Text: b.String(), Time: time.Now()})
		return m, nil
	}

	policy := holder.SetMode(mode)
	text := "sandbox: " + policy.Describe()
	if mode == sandbox.ModeOff {
		text = "SANDBOX OFF — commands now run directly on the host with no confinement."
	}
	kind := MsgSystem
	if mode == sandbox.ModeOff {
		kind = MsgError
	}
	m.appendMsg(ChatMsg{Kind: kind, Text: text, Time: time.Now()})
	m.setStatus("sandbox: " + string(mode))
	return m, nil
}

func modeHelp(m sandbox.Mode) string {
	switch m {
	case sandbox.ModeReadOnly:
		return "no writes, no network — inspection only"
	case sandbox.ModeWorkspace:
		return "writes confined to the project directory (default)"
	case sandbox.ModeTrusted:
		return "resource limits and tree cleanup only"
	case sandbox.ModeOff:
		return "no confinement at all"
	}
	return ""
}

// sandboxPolicy returns the active sandbox policy, or the unconfined one when
// no sandbox is wired up.
func (m *Model) sandboxPolicy() sandbox.Policy {
	if m.deps.Sandbox == nil {
		return sandbox.Off()
	}
	return m.deps.Sandbox.Policy()
}

// ---------- /permission ----------

// cmdPermission opens an interactive menu for approval modes and profiles.
func (m *Model) cmdPermission(args string) (tea.Model, tea.Cmd) {
	perms := m.deps.Perms
	if perms == nil {
		m.appendMsg(ChatMsg{Kind: MsgError, Text: "no permission engine is active", Time: time.Now()})
		return m, nil
	}

	// Quick toggle with args (backward compat).
	arg := strings.ToLower(strings.TrimSpace(args))
	switch arg {
	case "manual":
		m.setPermissionMode("manual")
		return m, nil
	case "smart":
		m.setPermissionMode("smart")
		return m, nil
	case "off", "bypass":
		m.setPermissionMode("off")
		return m, nil
	case "list", "profiles":
		return m.cmdPermissionProfiles()
	case "reset", "clear":
		perms.ClearSessionGrants()
		m.appendMsg(ChatMsg{Kind: MsgSystem,
			Text: "session grants cleared — previously \"always allowed\" actions will ask again",
			Time: time.Now()})
		return m, nil
	case "":
		// Fall through to interactive menu.
	default:
		// Try to resolve as a named profile.
		cfg := m.deps.Loaded.Config
		resolved, err := config.ResolveProfileByName(cfg, arg)
		if err == nil {
			perms.SetPermission(resolved)
			perms.SetProfile(arg)
			perms.SetYolo(false)
			m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "profile: " + arg, Time: time.Now()})
			m.setStatus("profile: " + arg)
			return m, nil
		}
		// Fall through to interactive menu.
	}

	// Interactive menu.
	mode := "manual"
	if perms.Yolo() {
		mode = "off"
	}

	opts := []choiceOption{
		{value: "1", label: "Manual — prompt for unapproved actions", active: mode == "manual"},
		{value: "2", label: "Smart  — allow safe, prompt for risky", active: mode == "smart"},
		{value: "3", label: "Off    — allow everything (bypass)", active: mode == "off"},
		{value: "4", label: "Profiles →", active: false},
		{value: "5", label: "Show current policy", active: false},
	}
	m.armChoice("permissions (" + mode + ")", pendingPermission, "", opts)
	return m, nil
}

// applyPermissionMenu routes the interactive permission choice.
func (m *Model) applyPermissionMenu(value string) (tea.Model, tea.Cmd) {
	switch value {
	case "1":
		return m.setPermissionMode("manual")
	case "2":
		return m.setPermissionMode("smart")
	case "3":
		return m.setPermissionMode("off")
	case "4":
		return m.cmdPermissionProfiles()
	case "5":
		m.appendMsg(ChatMsg{Kind: MsgSystem, Text: m.permissionSummary(), Time: time.Now()})
		return m, nil
	}
	// Otherwise, treat as a named profile.
	cfg := m.deps.Loaded.Config
	perms := m.deps.Perms
	resolved, err := config.ResolveProfileByName(cfg, value)
	if err == nil {
		perms.SetPermission(resolved)
		perms.SetProfile(value)
		perms.SetYolo(false)
		if resolved.Sandbox != nil && m.deps.Sandbox != nil {
			merged := config.MergeSandbox(m.deps.Loaded.Config.Sandbox, resolved.Sandbox)
			m.deps.Sandbox.Set(sandbox.FromConfig(merged, m.deps.Loaded.ProjectRoot))
		}
		m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "profile: " + value, Time: time.Now()})
		m.setStatus("profile: " + value)
	}
	return m, nil
}

// setPermissionMode sets the approval mode and reports it.
func (m *Model) setPermissionMode(mode string) (tea.Model, tea.Cmd) {
	perms := m.deps.Perms
	switch mode {
	case "manual":
		perms.SetYolo(false)
	case "smart":
		perms.SetYolo(false)
	case "off":
		perms.SetYolo(true)
	}
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "approval mode: " + mode, Time: time.Now()})
	m.setStatus("permissions: " + mode)
	return m, nil
}

// cmdPermissionProfiles lists profiles as an interactive toggle.
func (m *Model) cmdPermissionProfiles() (tea.Model, tea.Cmd) {
	cfg := m.deps.Loaded.Config
	perms := m.deps.Perms
	names := config.ProfileNames(cfg)
	var opts []choiceOption
	for _, name := range names {
		active := name == perms.Profile()
		opts = append(opts, choiceOption{
			value:  name,
			label:  padRight(name, 12) + " " + profileHelp(name),
			active: active,
		})
	}
	m.armChoice("profiles (pick to switch)", pendingPermission, "", opts)
	return m, nil
}

func profileHelp(name string) string {
	switch name {
	case config.ProfileReadonly:
		return "inspect only; no writes, no network"
	case config.ProfileStandard:
		return "workspace writes ask; destructive commands denied"
	case config.ProfileTrusted:
		return "most things allowed; still blocks shutdown/mkfs"
	case config.ProfileCI:
		return "unattended: never prompts, OS sandbox required"
	}
	return "user-defined"
}

// permissionSummary renders the active policy for /permissions.
func (m *Model) permissionSummary() string {
	perms := m.deps.Perms
	p := perms.Permission()

	var b strings.Builder
	fmt.Fprintf(&b, "agent: %s", m.agentName)
	if prof := perms.Profile(); prof != "" {
		fmt.Fprintf(&b, " · profile: %s", prof)
	}
	if perms.Yolo() {
		b.WriteString(" · YOLO (all prompts bypassed)")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "default: %s · edit: %s · write: %s · read: %s · webfetch: %s\n",
		orString(p.Default, "ask"), orString(p.Edit, "-"), orString(p.Write, "-"),
		orString(p.Read, "-"), orString(p.WebF, "-"))

	writeRules(&b, "bash patterns", p.Bash)
	writeRules(&b, "path rules", p.Paths)
	writeRules(&b, "host rules", p.Hosts)
	writeRules(&b, "tool rules", p.Tools)

	if grants := perms.SessionGrants(); len(grants) > 0 {
		fmt.Fprintf(&b, "session grants: %s\n", strings.Join(grants, ", "))
	}

	if m.deps.Sandbox != nil {
		policy := m.deps.Sandbox.Policy()
		fmt.Fprintf(&b, "sandbox: %s", policy.Describe())
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeRules renders one rule map, grouped by level so a long policy stays
// readable instead of scrolling as an unsorted dump.
func writeRules(b *strings.Builder, label string, rules map[string]string) {
	if len(rules) == 0 {
		return
	}
	byLevel := map[string][]string{}
	for pat, lvl := range rules {
		byLevel[lvl] = append(byLevel[lvl], pat)
	}
	fmt.Fprintf(b, "%s:\n", label)
	for _, lvl := range []string{config.PermDeny, config.PermAsk, config.PermAllow} {
		pats := byLevel[lvl]
		if len(pats) == 0 {
			continue
		}
		sort.Strings(pats)
		fmt.Fprintf(b, "  %-5s %s\n", lvl, strings.Join(pats, " "))
	}
}
