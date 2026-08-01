package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"rick/internal/config"
	"rick/internal/glob"
	"rick/internal/tools"
)

// Manager owns every connected MCP server and exposes their tools.
type Manager struct {
	mu            sync.RWMutex
	clients       map[string]*Client
	errs          map[string]error
	connectCancel context.CancelFunc
	connectWG     sync.WaitGroup
}

// NewManager builds an empty manager.
func NewManager() *Manager {
	return &Manager{clients: map[string]*Client{}, errs: map[string]error{}}
}

// ConnectAsync connects servers without delaying the TUI startup. Close
// cancels and joins this work before disconnecting established clients.
func (m *Manager) ConnectAsync(parent context.Context, servers map[string]config.MCPServer, reg *tools.Registry, enabled map[string]bool) {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.connectCancel = cancel
	m.connectWG.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.connectWG.Done()
		m.Connect(ctx, servers)
		if ctx.Err() == nil {
			m.Register(reg, enabled)
		}
	}()
}

// Connect dials every enabled server in the config. Failures are recorded but
// never fatal — a broken MCP server must not stop rick from starting.
func (m *Manager) Connect(ctx context.Context, servers map[string]config.MCPServer) {
	if len(servers) == 0 {
		return
	}
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)

	var wg sync.WaitGroup
	for _, name := range names {
		s := servers[name]
		if s.Enabled != nil && !*s.Enabled {
			continue
		}
		wg.Add(1)
		go func(name string, s config.MCPServer) {
			defer wg.Done()
			dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			var (
				c   *Client
				err error
			)
			kind := s.Type
			if kind == "" {
				if s.URL != "" {
					kind = "remote"
				} else {
					kind = "local"
				}
			}
			switch kind {
			case "remote":
				c, err = DialRemote(dialCtx, name, s.URL, s.Headers)
			default:
				c, err = DialLocal(dialCtx, name, s.Command, s.Environment)
			}

			m.mu.Lock()
			if err != nil {
				m.errs[name] = err
				m.mu.Unlock()
				return
			}
			old := m.clients[name]
			m.clients[name] = c
			delete(m.errs, name)
			m.mu.Unlock()
			if old != nil {
				_ = old.Close()
			}
		}(name, s)
	}
	wg.Wait()
}

// Errors returns per-server connection failures.
func (m *Manager) Errors() map[string]error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]error{}
	for k, v := range m.errs {
		out[k] = v
	}
	return out
}

// ServerNames lists connected servers.
func (m *Manager) ServerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.clients))
	for k := range m.clients {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Register adds every MCP tool to a rick tool registry using the
// <server>_<tool> naming convention, honouring enable/disable globs.
func (m *Manager) Register(reg *tools.Registry, enabled map[string]bool) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for server, c := range m.clients {
		for _, td := range c.Tools() {
			name := server + "_" + sanitizeToolName(td.Name)
			if enabled != nil {
				if v, ok := glob.Lookup(enabled, name); ok && !v {
					continue
				}
			}
			reg.Register(&mcpTool{
				client: c, remoteName: td.Name, localName: name,
				desc: td.Description, schema: td.InputSchema,
			})
			count++
		}
	}
	return count
}

func sanitizeToolName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Close disconnects every server.
func (m *Manager) Close() {
	m.mu.RLock()
	cancel := m.connectCancel
	m.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	m.connectWG.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		_ = c.Close()
	}
	m.clients = map[string]*Client{}
}

// mcpTool adapts an MCP tool to the rick tool interface.
type mcpTool struct {
	client     *Client
	remoteName string
	localName  string
	desc       string
	schema     map[string]any
}

func (t *mcpTool) Name() string { return t.localName }

func (t *mcpTool) Description() string {
	if t.desc == "" {
		return fmt.Sprintf("Tool %q provided by the %q MCP server.", t.remoteName, t.client.Name)
	}
	return t.desc
}

func (t *mcpTool) Schema() map[string]any {
	if t.schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return t.schema
}

// ReadOnly is conservative: MCP tools may have side effects.
func (t *mcpTool) ReadOnly() bool { return false }

func (t *mcpTool) Run(ctx context.Context, _ tools.Context, in json.RawMessage) (tools.Result, error) {
	var args map[string]any
	if len(in) > 0 {
		if err := json.Unmarshal(in, &args); err != nil {
			return tools.Errf("invalid arguments: %v", err), nil
		}
	}
	res, err := t.client.Call(ctx, t.remoteName, args)
	if err != nil {
		return tools.Errf("%v", err), nil
	}
	text := res.Text()
	if text == "" {
		text = "<no output>"
	}
	return tools.Result{
		Output:  text,
		Title:   t.localName,
		IsError: res.IsError,
	}, nil
}
