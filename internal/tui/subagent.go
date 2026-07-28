package tui

import (
	"context"
	"fmt"
	"strings"

	"rick/internal/agent"
	"rick/internal/config"
	"rick/internal/plugin"
)

// registerTaskTool wires the subagent spawner into the tool registry.
//
// The spawn closure reuses the same registry, permission engine and provider
// set as the primary agent, but with a restricted tool filter, a tightened
// permission policy for read-only subagents, and an incremented depth so
// recursion is capped.
func (m *Model) registerTaskTool() {
	specs := agent.BuiltinSubagents()

	// Config-defined subagents override / extend the built-ins.
	for name, a := range m.deps.Loaded.Config.Agents {
		if a.Mode != "subagent" && a.Mode != "all" {
			continue
		}
		spec := agent.SubagentSpec{
			Name:        name,
			Description: a.Description,
			Prompt:      a.Prompt,
			Model:       a.Model,
		}
		if spec.Description == "" {
			spec.Description = "Custom subagent defined in config."
		}
		if spec.Prompt == "" {
			spec.Prompt = agent.GeneralSubagentPrompt
		}
		specs[name] = spec
	}

	maxDepth := 1
	if d := m.deps.Loaded.Config.SubagentDepth; d != nil && *d > 0 {
		maxDepth = *d
	}

	m.deps.Registry.Register(agent.TaskTool{
		Specs:    specs,
		MaxDepth: maxDepth,
		Spawn:    m.spawnSubagent(specs, maxDepth),
	})
	m.deps.Registry.Register(agent.ParallelTaskTool{
		Specs:    specs,
		MaxDepth: maxDepth,
		Spawn:    m.spawnSubagent(specs, maxDepth),
	})

	// Register the swarm tool if a swarm manager is available.
	if m.deps.SwarmManager != nil {
		m.deps.Registry.Register(agent.SwarmTool{
			Manager: m.spawnSwarm,
		})
	}
}

func (m *Model) spawnSubagent(specs map[string]agent.SubagentSpec, maxDepth int) func(context.Context, string, string, string, int) (string, error) {
	return func(ctx context.Context, kind, description, prompt string, depth int) (string, error) {
		spec, ok := specs[kind]
		if !ok {
			return "", fmt.Errorf("unknown subagent type %q", kind)
		}

		modelRef := m.modelID
		if spec.Model != "" {
			modelRef = spec.Model
		}
		provID, modelID := config.SplitModel(modelRef)
		prov, ok := m.deps.Providers[provID]
		if !ok {
			return "", fmt.Errorf("subagent: unknown provider %q", provID)
		}

		perms := agent.SubagentPermissions(spec, m.deps.Perms, m.deps.Loaded.ProjectRoot)

		sys := spec.Prompt +
			agent.Environment(m.deps.Cwd, modelID, kind, "") +
			agent.ProjectContext(m.deps.Loaded.ProjectRoot, m.deps.Loaded.Config.Instructions)

		// Report progress into the parent transcript.
		if p := m.program; p != nil {
			p.Send(subagentEventMsg{kind: kind, description: description, phase: "start"})
		}

		// Lifecycle hook: subagent start.
		if m.deps.Plugins != nil && m.deps.Plugins.Len() > 0 {
			m.deps.Plugins.DispatchSubagentStart(ctx, &plugin.SubagentStartEvent{
				SessionID: m.sessionID(), Agent: m.agentName,
				SubagentName: kind, Task: description,
			})
		}

		cfg := agent.Config{
			Provider:   prov,
			Model:      modelID,
			System:     sys,
			MaxTokens:  m.deps.Loaded.Config.MaxTokens,
			Tools:      m.deps.Registry,
			ToolFilter: agent.SubagentToolFilter(spec, m.toolFilter()),
			Perms:      perms,
			Ask:        m.makeAsker(),
			Cwd:        m.deps.Cwd,
			SessionID:  m.sessionID(),
			AgentName:  kind,
			Depth:      depth,
			MaxTurns:   30,
			Plugins:    m.deps.Plugins,
			Parallel:   true,
		}

		toolCount := 0
		out, err := agent.RunSubagent(ctx, cfg, prompt, func(ev agent.Event) {
			if ev.Kind == agent.EvToolEnd {
				toolCount++
				if p := m.program; p != nil && ev.Tool != nil {
					p.Send(subagentEventMsg{
						kind: kind, description: description, phase: "tool",
						detail: ev.Tool.Name + " " + ev.Tool.Title, count: toolCount,
					})
				}
			}
		})

		if p := m.program; p != nil {
			p.Send(subagentEventMsg{
				kind: kind, description: description, phase: "done", count: toolCount,
			})
		}
		return out, err
	}
}

// subagentEventMsg reports child-session progress to the parent UI.
type subagentEventMsg struct {
	kind        string
	description string
	phase       string // start | tool | done
	detail      string
	count       int
}

// applySubagentEvent renders child progress into the transcript.
func (m *Model) applySubagentEvent(msg subagentEventMsg) {
	label := msg.description
	if label == "" {
		label = msg.kind
	}
	switch msg.phase {
	case "start":
		m.childActive = append(m.childActive, label)
		m.setStatus(fmt.Sprintf("subagent %s: %s", msg.kind, label))
	case "tool":
		m.setStatus(fmt.Sprintf("subagent %s · %d tools · %s",
			msg.kind, msg.count, truncate(msg.detail, 40)))
	case "done":
		for i, c := range m.childActive {
			if c == label {
				m.childActive = append(m.childActive[:i], m.childActive[i+1:]...)
				break
			}
		}
		m.setStatus(fmt.Sprintf("subagent %s finished (%d tools)", msg.kind, msg.count))
	}
}

// mcpStatus summarises MCP connectivity for /mcp.
func (m *Model) mcpStatus() string {
	if m.deps.MCP == nil {
		return "MCP is not initialised"
	}
	names := m.deps.MCP.ServerNames()
	errs := m.deps.MCP.Errors()
	if len(names) == 0 && len(errs) == 0 {
		return "no MCP servers configured\n\nAdd one to rick.json:\n" +
			`  "mcp": { "myserver": { "type": "local", "command": ["npx","-y","@some/mcp-server"] } }`
	}
	var b strings.Builder
	for _, n := range names {
		count := 0
		for _, t := range m.deps.Registry.Names() {
			if strings.HasPrefix(t, n+"_") {
				count++
			}
		}
		fmt.Fprintf(&b, "● %s — connected, %d tool(s)\n", n, count)
	}
	for n, err := range errs {
		fmt.Fprintf(&b, "✗ %s — %v\n", n, err)
	}
	return strings.TrimRight(b.String(), "\n")
}

// pluginStatus summarises loaded plugins.
func (m *Model) pluginStatus() string {
	if m.deps.Plugins == nil || m.deps.Plugins.Len() == 0 {
		return "no plugins loaded"
	}
	return fmt.Sprintf("%d plugin(s): %s",
		m.deps.Plugins.Len(), strings.Join(m.deps.Plugins.Names(), ", "))
}

// expandAgentMentions rewrites a leading @subagent mention into a task
// delegation instruction so the model calls the task tool.
func (m *Model) expandAgentMentions(text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "@") {
		return text, false
	}
	name := strings.TrimPrefix(fields[0], "@")
	specs := agent.BuiltinSubagents()
	if _, ok := specs[name]; !ok {
		if a, ok2 := m.deps.Loaded.Config.Agents[name]; !ok2 || (a.Mode != "subagent" && a.Mode != "all") {
			return text, false
		}
	}
	rest := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	if rest == "" {
		return text, false
	}
	return fmt.Sprintf(
		"Use the task tool with subagent_type=%q to handle this, then report the result:\n\n%s",
		name, rest), true
}
