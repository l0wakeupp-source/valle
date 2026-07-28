// Command ricke2e runs the full agent loop against a scripted local HTTP
// server that speaks the real OpenAI wire format. This exercises the actual
// network path, SSE parsing, tool execution and file mutation end to end.
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
	"sync"

	"rick/internal/agent"
	"rick/internal/config"
	"rick/internal/permission"
	"rick/internal/provider"
	"rick/internal/provider/openai"
	"rick/internal/session"
	"rick/internal/tools"
)

var failures []string

func check(name string, cond bool, detail ...string) {
	if cond {
		fmt.Printf("  PASS  %s\n", name)
		return
	}
	msg := name
	if len(detail) > 0 {
		msg += " — " + strings.Join(detail, " ")
	}
	failures = append(failures, msg)
	fmt.Printf("  FAIL  %s\n", msg)
}

// scriptedServer replays turns keyed by how many requests it has seen, and
// records the exact request bodies so we can assert the conversation shape.
type scriptedServer struct {
	mu       sync.Mutex
	turn     int
	requests []map[string]any
	script   []func(w http.ResponseWriter, req map[string]any)
}

func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
		if fl != nil {
			fl.Flush()
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
}

func (s *scriptedServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	s.mu.Lock()
	s.requests = append(s.requests, body)
	i := s.turn
	s.turn++
	s.mu.Unlock()

	if i >= len(s.script) {
		http.Error(w, "script exhausted", 500)
		return
	}
	s.script[i](w, body)
}

func main() {
	fmt.Println("== end-to-end: real HTTP + SSE + agent loop + file mutation ==")

	work, err := os.MkdirTemp("", "rick-e2e-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(work)

	// A tiny project the agent will actually modify.
	src := filepath.Join(work, "calc.go")
	original := `package calc

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a - b
}
`
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		panic(err)
	}
	_ = os.WriteFile(filepath.Join(work, "README.md"), []byte("# calc\n"), 0o644)

	srv := &scriptedServer{script: []func(http.ResponseWriter, map[string]any){
		// Turn 1: plan with todowrite + search for the bug.
		func(w http.ResponseWriter, _ map[string]any) {
			sse(w,
				`{"choices":[{"delta":{"content":"Let me find the bug."}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"todowrite","arguments":"{\"todos\":[{\"id\":\"1\",\"content\":\"find the bug\",\"status\":\"in_progress\"},{\"id\":\"2\",\"content\":\"fix it\",\"status\":\"pending\"}]}"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"t2","function":{"name":"grep","arguments":"{\"pattern\":\"func Add\"}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":500,"completion_tokens":40}}`)
		},
		// Turn 2: read the file.
		func(w http.ResponseWriter, _ map[string]any) {
			sse(w,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t3","function":{"name":"read","arguments":"{\"path\":\"calc.go\"}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		},
		// Turn 3: fix it with edit.
		func(w http.ResponseWriter, _ map[string]any) {
			sse(w,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t4","function":{"name":"edit","arguments":"{\"path\":\"calc.go\",\"old_string\":\"return a - b\",\"new_string\":\"return a + b\"}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		},
		// Turn 4: verify with bash, close out the todo list.
		func(w http.ResponseWriter, _ map[string]any) {
			sse(w,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t5","function":{"name":"bash","arguments":"{\"command\":\"grep -c 'a + b' calc.go\",\"description\":\"verify the fix\"}"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"t6","function":{"name":"todowrite","arguments":"{\"todos\":[{\"id\":\"1\",\"content\":\"find the bug\",\"status\":\"completed\"},{\"id\":\"2\",\"content\":\"fix it\",\"status\":\"completed\"}]}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		},
		// Turn 5: final answer.
		func(w http.ResponseWriter, _ map[string]any) {
			sse(w,
				`{"choices":[{"delta":{"content":"Fixed: Add used subtraction. calc.go:5 now returns a + b."}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1200,"completion_tokens":25}}`)
		},
	}}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	prov := openai.New("local", "test-key", ts.URL)

	todos := tools.NewTodoStore()
	reg := tools.NewRegistry()
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

	cfg, _ := config.Defaults()
	perm := *cfg.Permission
	perm.Edit = "ask" // force the approval path to be exercised
	perms := permission.New(&perm, work)

	snapData, _ := os.MkdirTemp("", "rick-e2e-snap-")
	defer os.RemoveAll(snapData)
	snaps, snapErr := session.NewSnapshotter(work, snapData)
	if snapErr != nil {
		fmt.Println("  note: snapshotter:", snapErr)
	}

	askCount := 0
	var askedTitles []string

	runner := agent.New(agent.Config{
		Provider: prov, Model: "test-model", System: agent.BuildPrompt,
		MaxTokens: 4096, Tools: reg, Perms: perms, Cwd: work,
		SessionID: "e2e", AgentName: "build", Parallel: true,
		Snapshotter: func() agent.Snapshotter {
			if snapErr == nil && snaps.Enabled() {
				return snaps
			}
			return nil
		}(),
		Ask: func(ctx context.Context, r permission.Request) agent.PermissionDecision {
			askCount++
			askedTitles = append(askedTitles, r.Title)
			return agent.DecideAccept
		},
	})

	ch := make(chan agent.Event, 512)
	var (
		finalText strings.Builder
		toolNames []string
		toolErrs  []string
		usage     provider.Usage
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			switch ev.Kind {
			case agent.EvText:
				finalText.WriteString(ev.Text)
			case agent.EvToolEnd:
				toolNames = append(toolNames, ev.Tool.Name)
				if ev.Tool.IsError {
					toolErrs = append(toolErrs, ev.Tool.Name+": "+ev.Tool.Output)
				}
			case agent.EvUsage:
				usage.InputTokens += ev.Usage.InputTokens
				usage.OutputTokens += ev.Usage.OutputTokens
			}
		}
	}()

	appended, runErr := runner.Run(context.Background(),
		[]provider.Message{provider.UserText("Add() is returning the wrong result. Find and fix it.")}, ch)
	<-done

	// --- assertions ---
	check("agent run completed without error", runErr == nil, fmt.Sprint(runErr))
	check("server saw all 5 scripted turns", srv.turn == 5, fmt.Sprint(srv.turn))
	check("no tool returned an unexpected error", len(toolErrs) == 0, strings.Join(toolErrs, "; "))

	expected := []string{"todowrite", "grep", "read", "edit", "bash", "todowrite"}
	check("tools executed in the expected order",
		fmt.Sprint(toolNames) == fmt.Sprint(expected),
		fmt.Sprintf("got %v want %v", toolNames, expected))

	final, _ := os.ReadFile(src)
	check("THE BUG WAS ACTUALLY FIXED ON DISK",
		strings.Contains(string(final), "return a + b") && !strings.Contains(string(final), "return a - b"),
		string(final))

	// Two gated actions: the edit (policy "ask") and the unmatched grep
	// command (bash "*" defaults to ask). read/grep/glob/todowrite are allowed
	// outright and must NOT prompt.
	check("exactly the two gated actions prompted", askCount == 2, fmt.Sprint(askCount))
	check("the edit prompt named the file",
		len(askedTitles) > 0 && strings.Contains(askedTitles[0], "calc.go"), fmt.Sprint(askedTitles))
	check("the bash prompt used the model's description",
		len(askedTitles) > 1 && askedTitles[1] == "verify the fix", fmt.Sprint(askedTitles))

	check("final answer was streamed",
		strings.Contains(finalText.String(), "a + b"), finalText.String())
	check("usage was accumulated across turns",
		usage.InputTokens == 1700 && usage.OutputTokens == 65, fmt.Sprintf("%+v", usage))

	check("todo store reflects the completed plan",
		todos.Pending() == 0 && len(todos.Items()) == 2,
		fmt.Sprintf("pending=%d items=%d", todos.Pending(), len(todos.Items())))

	// The conversation sent back to the server must be well formed.
	last := srv.requests[len(srv.requests)-1]
	msgs, _ := last["messages"].([]any)
	roles := []string{}
	toolMsgCount := 0
	for _, mm := range msgs {
		m, _ := mm.(map[string]any)
		role, _ := m["role"].(string)
		roles = append(roles, role)
		if role == "tool" {
			toolMsgCount++
			if _, hasID := m["tool_call_id"]; !hasID {
				check("every tool message carries tool_call_id", false, fmt.Sprint(m))
			}
		}
	}
	check("system prompt is the first message", len(roles) > 0 && roles[0] == "system", fmt.Sprint(roles))
	check("all 6 tool results were sent back", toolMsgCount == 6, fmt.Sprint(toolMsgCount))
	check("tool schemas were sent on every request",
		last["tools"] != nil && len(last["tools"].([]any)) == 10,
		fmt.Sprint(last["tools"] != nil))

	// Appended history must round-trip through a real session file.
	sessDir, _ := os.MkdirTemp("", "rick-e2e-sess-")
	defer os.RemoveAll(sessDir)
	store, err := session.NewStore(sessDir)
	if err != nil {
		check("session store opens", false, err.Error())
	} else {
		s := &session.Session{Cwd: work, Model: "test-model",
			Messages: append([]provider.Message{provider.UserText("fix Add")}, appended...)}
		saveErr := store.Save(s)
		loaded, loadErr := store.Load(s.ID)
		check("full conversation persists and reloads",
			saveErr == nil && loadErr == nil && len(loaded.Messages) == len(s.Messages),
			fmt.Sprintf("save=%v load=%v", saveErr, loadErr))

		uses, results := 0, 0
		for _, m := range loaded.Messages {
			for _, b := range m.Content {
				switch b.Type {
				case "tool_use":
					uses++
				case "tool_result":
					results++
				}
			}
		}
		check("persisted history keeps tool_use/tool_result paired",
			uses == 6 && results == 6, fmt.Sprintf("uses=%d results=%d", uses, results))
	}

	// Snapshot-based undo must restore the buggy file.
	if snapErr == nil && snaps.Enabled() {
		hist := snaps.History()
		check("snapshots were taken during the run", len(hist) >= 1, fmt.Sprint(len(hist)))

		if _, err := snaps.Undo(); err != nil {
			check("undo executes", false, err.Error())
		} else {
			reverted, _ := os.ReadFile(src)
			check("UNDO RESTORED THE ORIGINAL BUGGY FILE",
				strings.Contains(string(reverted), "return a - b"), string(reverted))

			if _, err := snaps.Redo(); err != nil {
				check("redo executes", false, err.Error())
			} else {
				redone, _ := os.ReadFile(src)
				check("REDO REAPPLIED THE FIX",
					strings.Contains(string(redone), "return a + b"), string(redone))
			}
		}
	} else {
		fmt.Println("  SKIP  snapshots (git unavailable)")
	}

	fmt.Println()
	if len(failures) > 0 {
		fmt.Printf("%d FAILURES:\n", len(failures))
		for _, f := range failures {
			fmt.Println("  - " + f)
		}
		os.Exit(1)
	}
	fmt.Println("end-to-end run clean")
}
