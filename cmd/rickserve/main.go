// Command rickserve runs rick as a headless daemon. It reads newline-delimited
// JSON requests from stdin (or a TCP socket) and writes newline-delimited JSON
// events back, so editors, CI runners and other agents can drive the agent loop
// without a TUI.
//
// Protocol (one JSON object per line):
//
//	→ {"type":"run","session_id":"abc","prompt":"hi","model":"anthropic/...","yolo":false}
//	← {"type":"event","session_id":"abc","event":"ToolUse","data":{...}}
//	← {"type":"event","session_id":"abc","event":"Content","data":{"text":"..."}}
//	← {"type":"done","session_id":"abc"}
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"rick/internal/config"
	"rick/internal/headless"
	"rick/internal/mcp"
	"rick/internal/permission"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/provider/anthropic"
	"rick/internal/provider/catalog"
	"rick/internal/provider/openai"
	"rick/internal/sandbox"
	"rick/internal/session"
	"rick/internal/tools"
)

// Version is the daemon protocol version reported in the ready banner.
const Version = "0.1.0"

// Request is one inbound ndjson line.
type Request struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	Model     string `json:"model,omitempty"`
	Yolo      bool   `json:"yolo,omitempty"`
	MaxTurns  int    `json:"max_turns,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
}

// Response is one outbound ndjson line.
type Response struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Event     string          `json:"event,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// streamLine mirrors the shape headless.Run emits for FormatStreamJSON.
type streamLine struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	Tool    json.RawMessage `json:"tool,omitempty"`
	Usage   json.RawMessage `json:"usage,omitempty"`
	Error   string          `json:"error,omitempty"`
	Session string          `json:"session_id,omitempty"`
}

// writer serialises ndjson output so concurrent runs cannot interleave lines.
type writer struct {
	mu  sync.Mutex
	bw  *bufio.Writer
	enc *json.Encoder
}

func newWriter(w io.Writer) *writer {
	bw := bufio.NewWriter(w)
	return &writer{bw: bw, enc: json.NewEncoder(bw)}
}

// emit writes one response line and flushes it immediately.
func (w *writer) emit(r Response) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(r)
	_ = w.bw.Flush()
}

// flush drains any buffered output.
func (w *writer) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.bw.Flush()
}

// server holds the shared infrastructure reused across requests.
type server struct {
	loaded  *config.Loaded
	cwd     string
	provs   map[string]provider.Provider
	tools   *tools.Registry
	plugins *plugin.Registry
	store   *session.Store
	mcp     *mcp.Manager
	sandbox *sandbox.Holder
}

func main() {
	var (
		flagPort    int
		flagCwd     string
		flagSandbox string
		flagProfile string
	)

	root := &cobra.Command{
		Use:   "rickserve",
		Short: "Run rick as a headless ndjson daemon",
		Long: "rickserve accepts newline-delimited JSON run requests on stdin (or a TCP\n" +
			"port) and streams newline-delimited JSON events back.\n\n" +
			"Examples:\n" +
			"  echo '{\"type\":\"run\",\"prompt\":\"hello\"}' | rickserve\n" +
			"  rickserve --port 7333",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, err := newServer(flagCwd, flagSandbox, flagProfile)
			if err != nil {
				return err
			}
			defer srv.mcp.Close()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if len(srv.loaded.Config.MCP) > 0 {
				srv.mcp.Connect(ctx, srv.loaded.Config.MCP)
			}

			if flagPort > 0 {
				return srv.serveTCP(ctx, flagPort)
			}
			out := newWriter(os.Stdout)
			defer out.flush()
			srv.serveConn(ctx, os.Stdin, out)
			return nil
		},
	}

	root.Flags().IntVar(&flagPort, "port", 0, "listen on a TCP port instead of stdin/stdout")
	root.Flags().StringVar(&flagCwd, "cwd", ".", "working directory for agent runs")
	root.Flags().StringVar(&flagSandbox, "sandbox", "",
		"command sandbox: read-only | workspace-write | trusted | off")
	root.Flags().StringVar(&flagProfile, "permission-profile", "",
		"permission profile: readonly | standard | trusted | ci")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rickserve: "+err.Error())
		os.Exit(1)
	}
}

// newServer assembles the shared dependencies once at startup.
func newServer(dir, sandboxMode, profile string) (*server, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", abs)
	}

	loaded, err := config.Load(abs)
	if err != nil {
		return nil, err
	}
	if creds, cerr := config.LoadCredentials(); cerr == nil {
		config.MergeCredentials(&loaded.Config, creds)
		if loaded.Config.Model == "" {
			for _, id := range creds.IDs() {
				if mdl := config.FirstConfiguredModel(creds, id); mdl != "" {
					loaded.Config.Model = mdl
					break
				}
			}
		}
	}

	policy, err := resolveSandbox(loaded, sandboxMode, profile)
	if err != nil {
		return nil, err
	}
	holder := sandbox.NewHolder(policy)

	todos := tools.NewTodoStore()
	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	reg.Register(tools.WriteTool{})
	reg.Register(tools.EditTool{})
	reg.Register(tools.BashTool{Sandbox: holder})
	reg.Register(tools.GrepTool{})
	reg.Register(tools.GlobTool{})
	reg.Register(tools.ListTool{})
	reg.Register(tools.ApplyPatchTool{})
	reg.Register(tools.TodoWriteTool{Store: todos})
	reg.Register(tools.TodoReadTool{Store: todos})
	reg.Register(tools.CodeSymbolsTool{})
	reg.Register(tools.GitTool{})
	reg.Register(tools.DiagnosticsTool{})
	reg.Register(tools.TestTool{})
	reg.Register(tools.TreeTool{})
	reg.Register(tools.FetchTool{})
	reg.Register(tools.MemoryTool{})
	reg.Register(tools.WebSearchTool{Restrictions: loaded.Config.WebSearch})

	store, err := session.NewStore(filepath.Join(config.DataDir(), "sessions"))
	if err != nil {
		return nil, err
	}

	return &server{
		loaded:  loaded,
		cwd:     abs,
		provs:   buildProviders(loaded.Config),
		tools:   reg,
		plugins: plugin.NewRegistry(),
		store:   store,
		mcp:     mcp.NewManager(),
		sandbox: holder,
	}, nil
}

// resolveSandbox mirrors the sandbox half of the main binary's security
// resolution: config block, optional profile override, optional flag override.
func resolveSandbox(loaded *config.Loaded, mode, profile string) (sandbox.Policy, error) {
	cfg := loaded.Config
	perm := cfg.Permission
	if profile != "" {
		resolved, err := config.ResolveProfileByName(cfg, profile)
		if err != nil {
			return sandbox.Policy{}, err
		}
		perm = resolved
	} else {
		perm = config.ResolvePermission(cfg, perm)
	}

	sbCfg := cfg.Sandbox
	if perm != nil && perm.Sandbox != nil {
		if profile != "" {
			sbCfg = config.MergeSandbox(cfg.Sandbox, perm.Sandbox)
		} else {
			sbCfg = config.MergeSandbox(perm.Sandbox, cfg.Sandbox)
		}
	}
	policy := sandbox.FromConfig(sbCfg, loaded.ProjectRoot)
	if mode != "" {
		m, ok := sandbox.ParseMode(mode)
		if !ok {
			return sandbox.Policy{}, fmt.Errorf("unknown sandbox mode %q (want read-only, workspace-write, trusted or off)", mode)
		}
		policy.Mode = m
	}
	return policy.Normalize(loaded.ProjectRoot), nil
}

// buildProviders instantiates every configured provider that has credentials.
func buildProviders(cfg config.Config) map[string]provider.Provider {
	out := map[string]provider.Provider{}
	for name, p := range cfg.Providers {
		if p.Enabled != nil && !*p.Enabled {
			continue
		}
		kind := p.Type
		if kind == "" {
			if e, ok := catalog.Get(name); ok {
				kind = e.Flavor
			} else {
				kind = name
			}
		}
		switch kind {
		case "anthropic":
			if p.APIKey == "" && p.BaseURL == "" {
				continue
			}
			out[name] = anthropic.New(p.APIKey, p.BaseURL)
		case "openai", "openrouter", "groq", "deepseek", "together", "openai-compatible":
			if p.APIKey == "" && p.BaseURL == "" {
				continue
			}
			out[name] = openai.New(name, p.APIKey, p.BaseURL)
		default:
			if p.BaseURL != "" {
				out[name] = openai.New(name, p.APIKey, p.BaseURL)
			}
		}
	}
	return out
}

// resolveProvider picks the provider for a fully-qualified model string,
// tolerating multi-segment model ids such as "openrouter/meta/llama-3".
func (s *server) resolveProvider(model string) (provider.Provider, string, error) {
	provID, modelID := config.SplitModel(model)
	if p, ok := s.provs[provID]; ok {
		return p, modelID, nil
	}
	idx := strings.Index(model, "/")
	for idx >= 0 && idx < len(model)-1 {
		if p, found := s.provs[model[:idx]]; found {
			return p, model[idx+1:], nil
		}
		next := strings.Index(model[idx+1:], "/")
		if next < 0 {
			break
		}
		idx = idx + 1 + next
	}
	return nil, "", fmt.Errorf("no provider configured for model %q", model)
}

// serveTCP listens on a port and serves each client connection concurrently.
func (s *server) serveTCP(ctx context.Context, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "rickserve v%s listening on %s\n", Version, ln.Addr())

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			fmt.Fprintf(os.Stderr, "rickserve: accept: %v\n", err)
			continue
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer c.Close()
			out := newWriter(c)
			s.serveConn(ctx, c, out)
			out.flush()
		}(conn)
	}
	wg.Wait()
	return nil
}

// serveConn reads ndjson requests from r until EOF or shutdown.
func (s *server) serveConn(ctx context.Context, r io.Reader, out *writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			out.emit(Response{Type: "error", Error: "malformed request: " + err.Error()})
			continue
		}
		switch req.Type {
		case "", "run":
			s.handleRun(ctx, req, out)
		case "ping":
			out.emit(Response{Type: "pong", SessionID: req.SessionID})
		case "shutdown":
			out.emit(Response{Type: "done", SessionID: req.SessionID})
			return
		default:
			out.emit(Response{Type: "error", SessionID: req.SessionID,
				Error: fmt.Sprintf("unknown request type %q", req.Type)})
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		out.emit(Response{Type: "error", Error: "read: " + err.Error()})
	}
}

// handleRun executes one agent run and streams its events back as ndjson.
func (s *server) handleRun(ctx context.Context, req Request, out *writer) {
	sid := req.SessionID
	if sid == "" {
		sid = session.NewID()
	}
	if req.Prompt == "" {
		out.emit(Response{Type: "error", SessionID: sid, Error: "prompt is required"})
		return
	}

	model := s.loaded.Config.Model
	if req.Model != "" {
		model = req.Model
	}
	prov, modelID, err := s.resolveProvider(model)
	if err != nil {
		out.emit(Response{Type: "error", SessionID: sid, Error: err.Error()})
		return
	}

	cwd := s.cwd
	if req.Cwd != "" {
		if abs, aerr := filepath.Abs(req.Cwd); aerr == nil {
			cwd = abs
		}
	}
	agentName := req.Agent
	if agentName == "" {
		agentName = "build"
	}

	perms := permission.New(config.ResolvePermission(s.loaded.Config, s.loaded.Config.Permission), s.loaded.ProjectRoot)
	perms.SetYolo(req.Yolo)

	deps := headless.Deps{
		Provider: prov,
		ModelID:  modelID,
		Config:   s.loaded.Config,
		Tools:    s.tools,
		Perms:    perms,
		Plugins:  s.plugins,
		Store:    s.store,
	}
	opts := headless.Options{
		Prompt:      req.Prompt,
		Model:       model,
		Yolo:        req.Yolo,
		MaxTurns:    req.MaxTurns,
		Format:      headless.FormatStreamJSON,
		Cwd:         cwd,
		ProjectRoot: s.loaded.ProjectRoot,
		AgentName:   agentName,
	}

	// headless.Run writes stream-json to an io.Writer; pipe it through a
	// translator so each line becomes a protocol event carrying the client's
	// session id.
	pr, pw := io.Pipe()
	var (
		runErr     error
		internalID string
		wg         sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = headless.Run(ctx, opts, deps, pw, io.Discard)
		_ = pw.Close()
	}()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev streamLine
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Session != "" {
			internalID = ev.Session
		}
		name, data := translate(ev)
		if name == "" {
			continue
		}
		out.emit(Response{Type: "event", SessionID: sid, Event: name, Data: data})
	}
	_, _ = io.Copy(io.Discard, pr)
	wg.Wait()

	s.persist(sid, internalID, cwd)

	if runErr != nil {
		out.emit(Response{Type: "error", SessionID: sid, Error: runErr.Error()})
	}
	out.emit(Response{Type: "done", SessionID: sid})
}

// translate maps one headless stream line onto a protocol event name plus its
// JSON payload. An empty name means the line carries no client-visible event.
func translate(ev streamLine) (string, json.RawMessage) {
	switch ev.Type {
	case "text":
		return "Content", mustJSON(map[string]string{"text": ev.Text})
	case "thinking":
		return "Thinking", mustJSON(map[string]string{"text": ev.Text})
	case "tool_start":
		return "ToolUse", ev.Tool
	case "tool_end":
		return "ToolResult", ev.Tool
	case "usage":
		return "Usage", ev.Usage
	case "error":
		return "Error", mustJSON(map[string]string{"error": ev.Error})
	default:
		return "", nil
	}
}

// mustJSON marshals v, returning null on the (impossible) error path.
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

// persist re-keys the session headless.Run saved under its own generated id so
// the client's session id stays stable and resumable across requests.
func (s *server) persist(clientID, internalID, cwd string) {
	if s.store == nil || internalID == "" {
		return
	}
	defer func() { _ = s.store.SetCurrent(cwd, clientID) }()
	if clientID == internalID {
		return
	}
	fresh, err := s.store.Load(internalID)
	if err != nil {
		return
	}
	msgs := fresh.Messages
	if prior, perr := s.store.Load(clientID); perr == nil {
		msgs = append(append([]provider.Message{}, prior.Messages...), msgs...)
		fresh.Created = prior.Created
		if prior.Title != "" {
			fresh.Title = prior.Title
		}
		fresh.Usage.Input += prior.Usage.Input
		fresh.Usage.Output += prior.Usage.Output
		fresh.Usage.CacheRead += prior.Usage.CacheRead
		fresh.Usage.CacheWrite += prior.Usage.CacheWrite
	}
	fresh.ID = clientID
	fresh.Messages = msgs
	if err := s.store.Save(fresh); err == nil {
		_ = s.store.Delete(internalID)
	}
}
