// Command rickserve runs rick as a headless daemon. It reads newline-delimited
// JSON requests from stdin (or a TCP socket) and writes newline-delimited JSON
// events back, so editors, desktop apps, CI runners and other agents can drive
// the agent loop without a TUI.
//
// Protocol v2 (one JSON object per line):
//
// Requests (→):
//
//	{"type":"run","session_id":"abc","prompt":"hi","model":"anthropic/...","yolo":false,"agent":"build","cwd":"/proj","resume":false}
//	{"type":"permission_response","request_id":"r1","decision":"accept"}
//	{"type":"interrupt","session_id":"abc"}
//	{"type":"sessions","cwd":"/proj"}
//	{"type":"models"}
//	{"type":"config","cwd":"/proj"}
//	{"type":"ping"}
//	{"type":"shutdown"}
//
// Events (←):
//
//	{"type":"event","session_id":"abc","event":"Content","data":{"text":"..."}}
//	{"type":"event","session_id":"abc","event":"Thinking","data":{"text":"..."}}
//	{"type":"event","session_id":"abc","event":"ToolUse","data":{"name":"bash","title":"...","input":{...}}}
//	{"type":"event","session_id":"abc","event":"ToolResult","data":{"name":"bash","output":"...","is_error":false,"elapsed":"1.2s"}}
//	{"type":"event","session_id":"abc","event":"Usage","data":{"input_tokens":100,"output_tokens":50}}
//	{"type":"event","session_id":"abc","event":"PermissionRequest","data":{"request_id":"r1","tool":"bash","command":"rm -rf /","title":"...","body":"..."}}
//	{"type":"done","session_id":"abc"}
//	{"type":"sessions","data":[...]}
//	{"type":"models","data":[...]}
//	{"type":"config","data":{...}}
//	{"type":"pong"}
//	{"type":"error","error":"..."}
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"rick/internal/agent"
	"rick/internal/config"
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
	"rick/internal/usage"
)

// Version is the daemon protocol version reported in the ready banner.
const Version = "2.0.0"

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
	Resume    bool   `json:"resume,omitempty"`
	// Permission response fields.
	RequestID string `json:"request_id,omitempty"`
	Decision  string `json:"decision,omitempty"` // accept | reject | always
}

// Response is one outbound ndjson line.
type Response struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Event     string          `json:"event,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     string          `json:"error,omitempty"`
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

// pendingPerm tracks an outstanding permission request awaiting a client reply.
type pendingPerm struct {
	ch chan agent.PermissionDecision
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
	usage   *usage.Tracker

	// Permission routing: request_id -> pending decision channel.
	permMu       sync.Mutex
	permPending  map[string]*pendingPerm
	permCounter  atomic.Int64

	// Active run cancellation: session_id -> cancel func.
	runMu     sync.Mutex
	runCancel map[string]context.CancelFunc
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
			"Protocol v2 adds interactive permission routing, interrupt, session\n" +
			"resume, and query endpoints (sessions, models, config).\n\n" +
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

			// Emit a ready banner so clients know the daemon is alive.
			out := newWriter(os.Stdout)
			out.emit(Response{
				Type: "ready",
				Data: mustJSON(map[string]string{
					"protocol": Version,
					"version":  "v" + rickVersion,
				}),
			})
			out.flush()

			if flagPort > 0 {
				return srv.serveTCP(ctx, flagPort)
			}
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

// rickVersion is injected at build time; fallback for dev builds.
var rickVersion = "0.1.6"

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

	usageTracker := usage.New(config.GlobalDir())

	return &server{
		loaded:      loaded,
		cwd:         abs,
		provs:       buildProviders(loaded.Config),
		tools:       reg,
		plugins:     plugin.NewRegistry(),
		store:       store,
		mcp:         mcp.NewManager(),
		sandbox:     holder,
		usage:       usageTracker,
		permPending: make(map[string]*pendingPerm),
		runCancel:   make(map[string]context.CancelFunc),
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
		case "permission_response":
			s.handlePermissionResponse(req)
		case "interrupt":
			s.handleInterrupt(req)
		case "sessions":
			s.handleSessions(req, out)
		case "models":
			s.handleModels(out)
		case "config":
			s.handleConfig(req, out)
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

// ---------- permission routing ----------

// registerPerm creates a pending permission slot and returns its request_id.
func (s *server) registerPerm() (string, *pendingPerm) {
	id := fmt.Sprintf("perm_%d", s.permCounter.Add(1))
	p := &pendingPerm{ch: make(chan agent.PermissionDecision, 1)}
	s.permMu.Lock()
	s.permPending[id] = p
	s.permMu.Unlock()
	return id, p
}

// handlePermissionResponse delivers a client's decision to the waiting agent.
func (s *server) handlePermissionResponse(req Request) {
	s.permMu.Lock()
	p, ok := s.permPending[req.RequestID]
	if ok {
		delete(s.permPending, req.RequestID)
	}
	s.permMu.Unlock()
	if !ok {
		return // stale or unknown — ignore
	}
	switch req.Decision {
	case "accept":
		p.ch <- agent.DecideAccept
	case "always":
		p.ch <- agent.DecideAlways
	default:
		p.ch <- agent.DecideReject
	}
}

// ---------- interrupt ----------

func (s *server) handleInterrupt(req Request) {
	s.runMu.Lock()
	cancel, ok := s.runCancel[req.SessionID]
	s.runMu.Unlock()
	if ok {
		cancel()
	}
}

// ---------- query handlers ----------

func (s *server) handleSessions(req Request, out *writer) {
	cwd := req.Cwd // empty = all
	metas, err := s.store.List(cwd)
	if err != nil {
		out.emit(Response{Type: "error", Error: err.Error()})
		return
	}
	out.emit(Response{Type: "sessions", Data: mustJSON(metas)})
}

func (s *server) handleModels(out *writer) {
	type modelEntry struct {
		Provider      string `json:"provider"`
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextWindow int    `json:"context_window"`
	}
	var entries []modelEntry
	for name, p := range s.provs {
		for _, mi := range provider.FilterChatModels(p.Models()) {
			entries = append(entries, modelEntry{
				Provider:      name,
				ID:            mi.ID,
				Name:          mi.Name,
				ContextWindow: mi.ContextWindow,
			})
		}
	}
	out.emit(Response{Type: "models", Data: mustJSON(entries)})
}

func (s *server) handleConfig(req Request, out *writer) {
	cwd := s.cwd
	if req.Cwd != "" {
		if abs, err := filepath.Abs(req.Cwd); err == nil {
			cwd = abs
		}
	}
	loaded, err := config.Load(cwd)
	if err != nil {
		out.emit(Response{Type: "error", Error: err.Error()})
		return
	}
	out.emit(Response{Type: "config", Data: mustJSON(map[string]any{
		"project_root": loaded.ProjectRoot,
		"global_dir":   config.GlobalDir(),
		"data_dir":     config.DataDir(),
		"sources":      loaded.Sources,
		"config":       loaded.Config,
		"tui":          loaded.TUI,
	})})
}

// ---------- agent run ----------

// handleRun executes one agent run using agent.Runner directly, streaming
// events back as ndjson and routing permission requests to the client.
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
	maxTurns := req.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50
	}

	// Build permission engine.
	permPolicy := config.ResolvePermission(s.loaded.Config, s.loaded.Config.Permission)
	perms := permission.New(permPolicy, s.loaded.ProjectRoot)
	perms.SetYolo(req.Yolo)

	// Build the permission asker that routes to the client via ndjson.
	var ask agent.PermissionAsker
	if req.Yolo {
		ask = func(_ context.Context, _ permission.Request) agent.PermissionDecision {
			return agent.DecideAlways
		}
	} else {
		ask = func(askCtx context.Context, permReq permission.Request) agent.PermissionDecision {
			reqID, pending := s.registerPerm()
			paths := permReq.Paths
			if len(paths) == 0 && permReq.Path != "" {
				paths = []string{permReq.Path}
			}
			out.emit(Response{
				Type:      "event",
				SessionID: sid,
				Event:     "PermissionRequest",
				Data: mustJSON(map[string]any{
					"request_id": reqID,
					"tool":       permReq.Tool,
					"command":    permReq.Command,
					"path":       permReq.Path,
					"paths":      paths,
					"host":       permReq.Host,
					"title":      permReq.Title,
					"body":       permReq.Body,
				}),
			})
			select {
			case decision := <-pending.ch:
				return decision
			case <-askCtx.Done():
				return agent.DecideReject
			}
		}
	}

	// Build system prompt.
	stableSystem := agent.BuildPrompt + agent.ProjectContext(s.loaded.ProjectRoot, nil)
	system := stableSystem + agent.Environment(cwd, model, agentName, "")

	// Snapshotter for undo support.
	snaps, _ := session.NewSnapshotter(s.loaded.ProjectRoot, config.DataDir())
	var snapshotter agent.Snapshotter
	if snaps != nil && snaps.Enabled() {
		snapshotter = snaps
	}

	// Build history: resume from existing session or start fresh.
	var history []provider.Message
	if req.Resume {
		if existing, lerr := s.store.Load(sid); lerr == nil {
			history = existing.Messages
		}
	}
	if len(history) == 0 {
		history = []provider.Message{provider.UserText(req.Prompt)}
	}

	// Create a cancellable context for this run.
	runCtx, cancel := context.WithCancel(ctx)
	s.runMu.Lock()
	s.runCancel[sid] = cancel
	s.runMu.Unlock()
	defer func() {
		s.runMu.Lock()
		delete(s.runCancel, sid)
		s.runMu.Unlock()
		cancel()
	}()

	runner := agent.New(agent.Config{
		Provider:     prov,
		Model:        modelID,
		System:       system,
		SystemStable: stableSystem,
		MaxTokens:    s.loaded.Config.MaxTokens,
		Tools:        s.tools,
		Perms:        perms,
		Ask:          ask,
		Cwd:          cwd,
		SessionID:    sid,
		AgentName:    agentName,
		MaxTurns:     maxTurns,
		Plugins:      s.plugins,
		Parallel:     true,
		Snapshotter:  snapshotter,
	})

	ch := make(chan agent.Event, 256)
	var (
		appended []provider.Message
		runErr   error
		wg       sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		appended, runErr = runner.Run(runCtx, history, ch)
	}()

	// Track usage for the session.
	var totalUsage session.Usage

	for ev := range ch {
		switch ev.Kind {
		case agent.EvText:
			out.emit(Response{Type: "event", SessionID: sid, Event: "Content",
				Data: mustJSON(map[string]string{"text": ev.Text})})

		case agent.EvThinking:
			out.emit(Response{Type: "event", SessionID: sid, Event: "Thinking",
				Data: mustJSON(map[string]string{"text": ev.Text})})

		case agent.EvToolStart:
			if ev.Tool == nil {
				break
			}
			out.emit(Response{Type: "event", SessionID: sid, Event: "ToolUse",
				Data: mustJSON(map[string]any{
					"name":  ev.Tool.Name,
					"title": ev.Tool.Title,
					"input": json.RawMessage(orNull(ev.Tool.Input)),
				})})

		case agent.EvToolEnd:
			if ev.Tool == nil {
				break
			}
			out.emit(Response{Type: "event", SessionID: sid, Event: "ToolResult",
				Data: mustJSON(map[string]any{
					"name":     ev.Tool.Name,
					"title":    ev.Tool.Title,
					"output":   truncate(ev.Tool.Output, 4000),
					"is_error": ev.Tool.IsError,
					"elapsed":  ev.Tool.Elapsed.Round(time.Millisecond).String(),
				})})

		case agent.EvUsage:
			if ev.Usage == nil {
				break
			}
			totalUsage.Input += ev.Usage.InputTokens
			totalUsage.Output += ev.Usage.OutputTokens
			totalUsage.CacheRead += ev.Usage.CacheReadTokens
			totalUsage.CacheWrite += ev.Usage.CacheWriteTokens
			// Record in the usage tracker.
			if s.usage != nil {
				_ = s.usage.Record(model, ev.Usage.InputTokens, ev.Usage.OutputTokens,
					ev.Usage.CacheReadTokens, ev.Usage.CacheWriteTokens)
			}
			out.emit(Response{Type: "event", SessionID: sid, Event: "Usage",
				Data: mustJSON(map[string]int{
					"input_tokens":       ev.Usage.InputTokens,
					"output_tokens":      ev.Usage.OutputTokens,
					"cache_read_tokens":  ev.Usage.CacheReadTokens,
					"cache_write_tokens": ev.Usage.CacheWriteTokens,
				})})

		case agent.EvPermissionAsk:
			// Handled by the ask callback — no separate event needed.

		case agent.EvError:
			if ev.Err != nil {
				out.emit(Response{Type: "event", SessionID: sid, Event: "Error",
					Data: mustJSON(map[string]string{"error": ev.Err.Error()})})
			}

		case agent.EvDone:
			// Loop will end when ch closes.
		}
	}

	wg.Wait()

	// Persist the session.
	allMsgs := history
	if len(appended) > 0 {
		allMsgs = append(append([]provider.Message{}, history...), appended...)
	}
	sess := &session.Session{
		ID:       sid,
		Title:    session.Title(allMsgs),
		Cwd:      cwd,
		Model:    model,
		Agent:    agentName,
		Messages: allMsgs,
		Usage:    totalUsage,
	}
	// Preserve existing metadata on resume.
	if req.Resume {
		if prior, perr := s.store.Load(sid); perr == nil {
			if prior.Title != "" && prior.Title != "untitled" {
				sess.Title = prior.Title
			}
			sess.Created = prior.Created
			sess.Category = prior.Category
			sess.Favorite = prior.Favorite
		}
	}
	if err := s.store.Save(sess); err != nil {
		fmt.Fprintf(os.Stderr, "rickserve: warning: failed to save session: %v\n", err)
	}
	_ = s.store.SetCurrent(cwd, sid)

	if runErr != nil {
		out.emit(Response{Type: "error", SessionID: sid, Error: runErr.Error()})
	}
	out.emit(Response{Type: "done", SessionID: sid})
}

// ---------- helpers ----------

// mustJSON marshals v, returning null on the (impossible) error path.
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

func orNull(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
