package tui

import (
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"
)

// cmdPlugins handles the /plugins slash command.
// With no args, shows the interactive plugin menu.
// With args, handles backward-compat subcommands.
func (m *Model) cmdPlugins(args string) (tea.Model, tea.Cmd) {
    fields := strings.Fields(args)
    if len(fields) == 0 {
        return m.cmdPluginsMenu()
    }

    switch strings.ToLower(fields[0]) {
    case "on":
        if len(fields) < 2 {
            m.appendMsg(ChatMsg{Kind: MsgError, Text: "usage: /plugins on <name>", Time: time.Now()})
            return m, nil
        }
        name := fields[1]
        if !m.pluginExists(name) {
            m.appendMsg(ChatMsg{Kind: MsgError, Text: "plugin not found: " + name, Time: time.Now()})
            return m, nil
        }
        m.deps.Plugins.SetEnabled(name, true)
        m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "enabled: " + name, Time: time.Now()})
        return m, nil

    case "off":
        if len(fields) < 2 {
            m.appendMsg(ChatMsg{Kind: MsgError, Text: "usage: /plugins off <name>", Time: time.Now()})
            return m, nil
        }
        name := fields[1]
        if !m.pluginExists(name) {
            m.appendMsg(ChatMsg{Kind: MsgError, Text: "plugin not found: " + name, Time: time.Now()})
            return m, nil
        }
        m.deps.Plugins.SetEnabled(name, false)
        m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "disabled: " + name, Time: time.Now()})
        return m, nil

    case "add":
        if len(fields) < 2 {
            return m.cmdPluginSource()
        }
        src := strings.Join(fields[1:], " ")
        return m.addPluginFromSource(src)

    case "remove", "rm":
        if len(fields) < 2 {
            m.appendMsg(ChatMsg{Kind: MsgError, Text: "usage: /plugins remove <name>", Time: time.Now()})
            return m, nil
        }
        name := fields[1]
        if m.deps.Plugins.Remove(name) {
            m.appendMsg(ChatMsg{Kind: MsgSystem, Text: "removed: " + name, Time: time.Now()})
        } else {
            m.appendMsg(ChatMsg{Kind: MsgError, Text: "plugin not found: " + name, Time: time.Now()})
        }
        return m, nil

    default:
        return m.cmdPluginsMenu()
    }
}

// pluginExists checks if a plugin is registered by name.
func (m *Model) pluginExists(name string) bool {
    for _, n := range m.deps.Plugins.Names() {
        if n == name {
            return true
        }
    }
    return false
}

// cmdSkills handles the /skills slash command.
// With no args, shows the interactive skill menu.
// With args, handles backward-compat subcommands.
func (m *Model) cmdSkills(args string) (tea.Model, tea.Cmd) {
    fields := strings.Fields(args)
    if len(fields) == 0 {
        return m.cmdSkillsMenu()
    }

    switch strings.ToLower(fields[0]) {
    case "show":
        if len(fields) < 2 {
            m.appendMsg(ChatMsg{Kind: MsgError, Text: "usage: /skills show <name>", Time: time.Now()})
            return m, nil
        }
        return m.showSkillContent(fields[1])

    default:
        return m.cmdSkillsMenu()
    }
}

// cmdSkillsMenu shows all skills as numbered options with "Add skill" last.
func (m *Model) cmdSkillsMenu() (tea.Model, tea.Cmd) {
    if len(m.deps.Skills) == 0 {
        m.armChoice("no skills loaded", pendingSkillOpen, "", []choiceOption{
            {value: "__add__", label: "＋ Add skill"},
        })
        return m, nil
    }
    var opts []choiceOption
    for _, s := range m.deps.Skills {
        desc := s.Description
        if desc == "" {
            desc = "(no description)"
        }
        opts = append(opts, choiceOption{
            value: s.Name, label: s.Name, detail: desc,
        })
    }
    opts = append(opts, choiceOption{value: "__add__", label: "＋ Add skill"})
    m.armChoice("skills (pick to open source)", pendingSkillOpen, "", opts)
    return m, nil
}
