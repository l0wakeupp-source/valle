package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ScriptTimeout is the maximum wall time a hook script may run.
const ScriptTimeout = 5 * time.Second

// ScriptResult is the outcome of running a hook script.
type ScriptResult struct {
	// Action is "allow", "deny", or "modify".
	Action string
	// Output carries the modified value when Action == "modify", or the
	// deny reason when Action == "deny".
	Output string
}

// RunScript executes a hook script with the standard rick environment
// variables and interprets the exit code:
//
//	0 = allow (pass through)
//	1 = deny / block
//	2 = modify (stdout carries the replacement value)
//
// The script receives: RICK_HOOK, RICK_TOOL, RICK_SESSION, RICK_INPUT,
// RICK_OUTPUT.
func RunScript(ctx context.Context, script string, env map[string]string) (ScriptResult, error) {
	ctx, cancel := context.WithTimeout(ctx, ScriptTimeout)
	defer cancel()

	shell, prefix := shellCommand()
	args := append(prefix, script)
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Env = buildEnv(env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := strings.TrimRight(stdout.String(), "\r\n")

	if ctx.Err() == context.DeadlineExceeded {
		return ScriptResult{Action: "allow"}, fmt.Errorf("hook script timed out after %s", ScriptTimeout)
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 1:
				reason := out
				if reason == "" {
					reason = strings.TrimRight(stderr.String(), "\r\n")
				}
				if reason == "" {
					reason = "blocked by plugin script"
				}
				return ScriptResult{Action: "deny", Output: reason}, nil
			case 2:
				return ScriptResult{Action: "modify", Output: out}, nil
			default:
				return ScriptResult{Action: "allow"}, fmt.Errorf("hook script exit %d: %s",
					exitErr.ExitCode(), strings.TrimRight(stderr.String(), "\r\n"))
			}
		}
		return ScriptResult{Action: "allow"}, err
	}

	return ScriptResult{Action: "allow", Output: out}, nil
}

// buildEnv merges the hook variables with the current process environment.
func buildEnv(hookVars map[string]string) []string {
	env := os.Environ()
	for k, v := range hookVars {
		env = append(env, k+"="+v)
	}
	return env
}

// shellCommand returns the shell binary and any required prefix flags.
func shellCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		if s := os.Getenv("COMSPEC"); s != "" {
			return s, []string{"/c"}
		}
		return "cmd.exe", []string{"/c"}
	}
	return "sh", []string{"-c"}
}

// ManifestToHooks converts a file-based Manifest into a Hooks struct by
// wiring each declared hook to its script or inline action.
func ManifestToHooks(m Manifest) Hooks {
	h := Hooks{Name: m.Name}

	for hookName, target := range m.Hooks {
		switch hookName {
		case "tool_before":
			h.ToolExecuteBefore = makeToolBeforeHook(target)
		case "tool_after":
			h.ToolExecuteAfter = makeToolAfterHook(target)
		case "session_start":
			h.SessionStart = makeSessionStartHook(target)
		case "session_end":
			h.SessionEnd = makeSessionEndHook(target)
		case "session_idle":
			h.SessionIdle = makeSessionIdleHook(target)
		case "session_error":
			h.SessionError = makeSessionErrorHook(target)
		case "turn_start":
			h.TurnStart = makeTurnStartHook(target)
		case "turn_end":
			h.TurnEnd = makeTurnEndHook(target)
		case "subagent_start":
			h.SubagentStart = makeSubagentStartHook(target)
		case "subagent_end":
			h.SubagentEnd = makeSubagentEndHook(target)
		}
	}
	return h
}

// isInlineAction reports whether target is a built-in keyword rather than a
// script path or URL.
func isInlineAction(target string) bool {
	switch target {
	case "allow", "deny", "log":
		return true
	}
	return false
}

func makeToolBeforeHook(target string) func(ctx context.Context, ev *ToolBeforeEvent) error {
	if isInlineAction(target) {
		return func(_ context.Context, ev *ToolBeforeEvent) error {
			if target == "deny" {
				ev.Skip = true
				ev.Reason = "denied by plugin policy"
			}
			return nil
		}
	}
	return func(ctx context.Context, ev *ToolBeforeEvent) error {
		res, err := RunScript(ctx, target, map[string]string{
			"RICK_HOOK":    "tool_before",
			"RICK_TOOL":    ev.Tool,
			"RICK_SESSION": ev.SessionID,
			"RICK_INPUT":   string(ev.Input),
		})
		if err != nil {
			return nil // non-fatal: log and pass through
		}
		switch res.Action {
		case "deny":
			ev.Skip = true
			ev.Reason = res.Output
		case "modify":
			if res.Output != "" {
				var object map[string]any
				if err := json.Unmarshal([]byte(res.Output), &object); err == nil && object != nil {
					ev.Input = []byte(res.Output)
				}
			}
		}
		return nil
	}
}

func makeToolAfterHook(target string) func(ctx context.Context, ev *ToolAfterEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *ToolAfterEvent) error {
		res, err := RunScript(ctx, target, map[string]string{
			"RICK_HOOK":    "tool_after",
			"RICK_TOOL":    ev.Tool,
			"RICK_SESSION": ev.SessionID,
			"RICK_INPUT":   string(ev.Input),
			"RICK_OUTPUT":  ev.Output,
		})
		if err != nil {
			return nil
		}
		if res.Action == "modify" && res.Output != "" {
			ev.Output = res.Output
		}
		return nil
	}
}

func makeSessionStartHook(target string) func(ctx context.Context, ev *SessionStartEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *SessionStartEvent) error {
		_, _ = RunScript(ctx, target, map[string]string{
			"RICK_HOOK":    "session_start",
			"RICK_SESSION": ev.SessionID,
		})
		return nil
	}
}

func makeSessionEndHook(target string) func(ctx context.Context, ev *SessionEndEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *SessionEndEvent) error {
		_, _ = RunScript(ctx, target, map[string]string{
			"RICK_HOOK":    "session_end",
			"RICK_SESSION": ev.SessionID,
		})
		return nil
	}
}

func makeSessionIdleHook(target string) func(ctx context.Context, ev *SessionEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *SessionEvent) error {
		_, _ = RunScript(ctx, target, map[string]string{
			"RICK_HOOK": "session_idle", "RICK_SESSION": ev.SessionID,
		})
		return nil
	}
}

func makeSessionErrorHook(target string) func(ctx context.Context, ev *SessionEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *SessionEvent) error {
		vars := map[string]string{"RICK_HOOK": "session_error", "RICK_SESSION": ev.SessionID}
		if ev.Err != nil {
			vars["RICK_ERROR"] = ev.Err.Error()
		}
		_, _ = RunScript(ctx, target, vars)
		return nil
	}
}

func makeTurnStartHook(target string) func(ctx context.Context, ev *TurnStartEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *TurnStartEvent) error {
		_, _ = RunScript(ctx, target, map[string]string{
			"RICK_HOOK":    "turn_start",
			"RICK_SESSION": ev.SessionID,
		})
		return nil
	}
}

func makeTurnEndHook(target string) func(ctx context.Context, ev *TurnEndEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *TurnEndEvent) error {
		_, _ = RunScript(ctx, target, map[string]string{
			"RICK_HOOK":    "turn_end",
			"RICK_SESSION": ev.SessionID,
		})
		return nil
	}
}

func makeSubagentStartHook(target string) func(ctx context.Context, ev *SubagentStartEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *SubagentStartEvent) error {
		_, _ = RunScript(ctx, target, map[string]string{
			"RICK_HOOK":    "subagent_start",
			"RICK_SESSION": ev.SessionID,
		})
		return nil
	}
}

func makeSubagentEndHook(target string) func(ctx context.Context, ev *SubagentEndEvent) error {
	if isInlineAction(target) {
		return nil
	}
	return func(ctx context.Context, ev *SubagentEndEvent) error {
		_, _ = RunScript(ctx, target, map[string]string{
			"RICK_HOOK":    "subagent_end",
			"RICK_SESSION": ev.SessionID,
		})
		return nil
	}
}
