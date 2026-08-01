package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultToolOutputLimit = 15 << 10

func runBoundedCommand(ctx context.Context, cwd, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd
	out := boundedBuffer{limit: defaultToolOutputLimit}
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.Output(), err
}

// GitTool provides structured access to git operations: status, diff, log, branches.
type GitTool struct{}

func (GitTool) Name() string { return "git" }

func (GitTool) ReadOnly() bool { return true }

func (GitTool) Description() string {
	return "Structured git operations: status, diff, log, branches, or changed_files."
}

func (GitTool) Schema() map[string]any {
	return obj(map[string]any{
		"action": enumProp("What to do.", "status", "diff", "log", "branches", "changed_files"),
		"path":   strProp("Specific file path for diff (optional)."),
		"staged": boolProp("Show staged diff instead of unstaged."),
		"count":  strProp("Number of log entries (default 10)."),
		"since":  strProp("Ref to compare against for changed_files (default HEAD~1)."),
	}, "action")
}

type gitArgs struct {
	Action string `json:"action"`
	Path   string `json:"path"`
	Staged bool   `json:"staged"`
	Count  string `json:"count"`
	Since  string `json:"since"`
}

func (GitTool) Run(ctx context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a gitArgs
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Count == "" {
		a.Count = "10"
	}
	if a.Since == "" {
		a.Since = "HEAD~1"
	}

	switch a.Action {
	case "status":
		return gitRun(ctx, tc.Cwd, "status", "--short", "--branch")
	case "diff":
		args := []string{"diff"}
		if a.Staged {
			args = append(args, "--staged")
		}
		args = append(args, "--")
		if a.Path != "" {
			args = append(args, a.Path)
		} else {
			args = append(args, ".")
		}
		return gitRun(ctx, tc.Cwd, args...)
	case "log":
		return gitRun(ctx, tc.Cwd, "log", "--oneline", "-n", a.Count)
	case "branches":
		return gitRun(ctx, tc.Cwd, "branch", "--list", "--no-color")
	case "changed_files":
		return gitRun(ctx, tc.Cwd, "diff", "--name-only", a.Since, "--", ".")
	default:
		return Errf("unknown action %q", a.Action), nil
	}
}

func gitRun(ctx context.Context, cwd string, args ...string) (Result, error) {
	out, err := runBoundedCommand(ctx, cwd, "git", args...)
	if err != nil {
		return Errf("git %s: %s\n%s", strings.Join(args, " "), err, strings.TrimSpace(out)), nil
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return Result{Output: "(no output)", Title: "git " + strings.Join(args[:min(2, len(args))], " ")}, nil
	}
	return Result{Output: s, Title: "git " + strings.Join(args[:min(2, len(args))], " ")}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// DiagnosticsTool captures compiler/type errors for the current package.
type DiagnosticsTool struct{}

func (DiagnosticsTool) Name() string { return "diagnostics" }

func (DiagnosticsTool) ReadOnly() bool { return true }

func (DiagnosticsTool) Description() string {
	return "Capture compiler and type errors for the current Go package. Runs 'go build ./...' and returns failures in a compact format."
}

func (DiagnosticsTool) Schema() map[string]any {
	return obj(map[string]any{
		"scope": strProp("Package path to check (default '.')."),
	})
}

func (DiagnosticsTool) Run(ctx context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a struct {
		Scope string `json:"scope"`
	}
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Scope == "" {
		a.Scope = "."
	}

	out, err := runBoundedCommand(ctx, tc.Cwd, "go", "build", a.Scope)
	if err == nil {
		return Result{Output: fmt.Sprintf("no errors in %s", a.Scope), Title: "diagnostics"}, nil
	}
	return Result{Output: fmt.Sprintf("go build %s:\n%s", a.Scope, strings.TrimSpace(out)), Title: "build errors"}, nil
}

// TestTool runs scoped Go tests with failures-only output.
type TestTool struct{}

func (TestTool) Name() string { return "test" }

func (TestTool) ReadOnly() bool { return false }

func (TestTool) Description() string {
	return "Run Go tests for a package. Returns compact pass/fail summary."
}

func (TestTool) Schema() map[string]any {
	return obj(map[string]any{
		"scope":   strProp("Package path to test (default '.')."),
		"verbose": boolProp("Verbose output (default false)."),
		"related": boolProp("Run only tests related to current changes (default false)."),
	})
}

func (TestTool) Run(ctx context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a struct {
		Scope   string `json:"scope"`
		Verbose bool   `json:"verbose"`
		Related bool   `json:"related"`
	}
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Scope == "" {
		a.Scope = "."
	}

	args := []string{"test"}
	if a.Verbose {
		args = append(args, "-v")
	}
	if a.Related {
		args = append(args, "-run", "TestRelated")
	}
	args = append(args, a.Scope)

	out, err := runBoundedCommand(ctx, tc.Cwd, "go", args...)

	if err == nil {
		return Result{Output: fmt.Sprintf("PASS: go test %s", a.Scope), Title: "test " + a.Scope}, nil
	}
	return Result{Output: fmt.Sprintf("FAIL: go test %s\n%s", a.Scope, strings.TrimSpace(out)), Title: "test " + a.Scope}, nil
}

// TreeTool provides a token-capped directory listing.
type TreeTool struct{}

func (TreeTool) Name() string { return "tree" }

func (TreeTool) ReadOnly() bool { return true }

func (TreeTool) Description() string {
	return "List directory structure as a tree. Token-capped to avoid context overflow."
}

func (TreeTool) Schema() map[string]any {
	return obj(map[string]any{
		"path":    strProp("Directory to list (default project root)."),
		"depth":   strProp("Max depth (default 3)."),
		"pattern": strProp("Glob pattern to filter (e.g. '*.go', '*.md')."),
	}, "path")
}

func (TreeTool) Run(_ context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a struct {
		Path    string `json:"path"`
		Depth   int    `json:"depth"`
		Pattern string `json:"pattern"`
	}
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Path == "" {
		a.Path = tc.Cwd
	}
	if a.Depth == 0 {
		a.Depth = 3
	}

	tree, err := buildTree(a.Path, a.Depth, a.Pattern, "")
	if err != nil {
		return Errf("tree: %v", err), nil
	}
	return Result{Output: tree, Title: "tree " + a.Path}, nil
}

func buildTree(root string, depth int, pattern string, indent string) (string, error) {
	var out boundedBuffer
	out.limit = defaultToolOutputLimit
	if err := buildTreeInto(root, depth, pattern, indent, &out); err != nil {
		return "", err
	}
	return out.Output(), nil
}

func buildTreeInto(root string, depth int, pattern string, indent string, out *boundedBuffer) error {
	if depth <= 0 || out.Truncated() {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if out.Truncated() {
			return nil
		}
		if strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" || e.Name() == "vendor" {
			continue
		}
		full := filepath.Join(root, e.Name())
		if e.IsDir() {
			fmt.Fprintf(out, "%s%s/\n", indent, e.Name())
			if err := buildTreeInto(full, depth-1, pattern, indent+"  ", out); err != nil {
				continue
			}
		} else {
			if pattern != "" && !matchGlob(e.Name(), pattern) {
				continue
			}
			fmt.Fprintf(out, "%s%s\n", indent, e.Name())
		}
	}
	return nil
}

func matchGlob(name, pattern string) bool {
	matched, _ := matchGlobHelper(name, pattern)
	return matched
}

func matchGlobHelper(name, pattern string) (bool, error) {
	if pattern == "*" {
		return true, nil
	}
	if strings.HasPrefix(pattern, "*.") {
		ext := pattern[1:]
		return strings.HasSuffix(name, ext), nil
	}
	return name == pattern, nil
}

// FetchTool does safe HTTP GET and returns compact Markdown.
type FetchTool struct{}

func (FetchTool) Name() string { return "fetch" }

func (FetchTool) ReadOnly() bool { return true }

func (FetchTool) Description() string {
	return "Fetch a URL and return compact text. Only HTTP/HTTPS GET. Max 8KB returned."
}

func (FetchTool) Schema() map[string]any {
	return obj(map[string]any{
		"url":     strProp("URL to fetch."),
		"extract": strProp("Optional: 'text' for plain text, 'links' for links only."),
	}, "url")
}

func (FetchTool) Run(ctx context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a struct {
		URL     string `json:"url"`
		Extract string `json:"extract"`
	}
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if !strings.HasPrefix(a.URL, "http://") && !strings.HasPrefix(a.URL, "https://") {
		return Errf("only HTTP/HTTPS URLs allowed"), nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if err != nil {
		return Errf("request: %v", err), nil
	}
	req.Header.Set("User-Agent", "rick-agent/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return Errf("fetch: %v", err), nil
	}
	defer resp.Body.Close()

	const maxFetchBytes = 8 << 10
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return Errf("read response: %v", err), nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Errf("fetch: HTTP %s", resp.Status), nil
	}
	truncated := len(body) > maxFetchBytes
	if truncated {
		body = body[:maxFetchBytes]
	}
	content := strings.TrimSpace(string(body))
	if truncated {
		content += "\n… <response capped at 8 KiB>"
	}

	if a.Extract == "links" {
		var links []string
		for _, line := range strings.Split(content, "\n") {
			if idx := strings.Index(line, "http"); idx >= 0 {
				end := strings.IndexAny(line[idx:], " \t\"'>")
				if end > 0 {
					links = append(links, line[idx:idx+end])
				}
			}
		}
		return Result{Output: strings.Join(links, "\n"), Title: "links: " + a.URL}, nil
	}

	return Result{Output: content, Title: a.URL}, nil
}

// MemoryTool persists and retrieves project facts (decisions, conventions).
type MemoryTool struct{}

const (
	maxMemoryValueBytes = 64 << 10
	maxMemoryFileBytes  = maxMemoryValueBytes + 4096
	maxMemoryEntries    = 100
)

func (MemoryTool) Name() string { return "memory" }

func (MemoryTool) ReadOnly() bool { return false }

func (MemoryTool) Description() string {
	return "Persistent project memory in .rick/memory/: store, get, list, or delete facts."
}

func (MemoryTool) Schema() map[string]any {
	return obj(map[string]any{
		"action": enumProp("What to do.", "store", "get", "list", "delete"),
		"key":    strProp("Key for the fact (e.g. 'auth_strategy')."),
		"value":  strProp("Value to store (required for store action)."),
	}, "action")
}

func (MemoryTool) Run(_ context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Value  string `json:"value"`
	}
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Action == "" {
		return Errf("action is required"), nil
	}

	memDir := filepath.Join(tc.Cwd, ".rick", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		return Errf("memory directory: %v", err), nil
	}

	switch a.Action {
	case "store":
		if a.Key == "" || a.Value == "" {
			return Errf("key and value required"), nil
		}
		if len(a.Value) > maxMemoryValueBytes {
			return Errf("value exceeds the memory limit of %d bytes", maxMemoryValueBytes), nil
		}
		path := filepath.Join(memDir, sanitizeKey(a.Key)+".json")
		data, err := json.Marshal(map[string]string{"key": a.Key, "value": a.Value})
		if err != nil {
			return Errf("store: %v", err), nil
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return Errf("store: %v", err), nil
		}
		return Result{Output: fmt.Sprintf("stored %s", a.Key), Title: "memory"}, nil
	case "get":
		if a.Key == "" {
			return Errf("key required"), nil
		}
		path := filepath.Join(memDir, sanitizeKey(a.Key)+".json")
		file, err := os.Open(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return Errf("get: %v", err), nil
			}
			return Result{Output: fmt.Sprintf("no memory for %s", a.Key), Title: "memory"}, nil
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxMemoryFileBytes+1))
		if err != nil {
			return Errf("get: %v", err), nil
		}
		if len(data) > maxMemoryFileBytes {
			return Errf("get: memory file exceeds the limit of %d bytes", maxMemoryFileBytes), nil
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return Errf("get: invalid memory file: %v", err), nil
		}
		return Result{Output: m["value"], Title: "memory " + a.Key}, nil
	case "list":
		entries, err := os.ReadDir(memDir)
		if err != nil && !os.IsNotExist(err) {
			return Errf("list: %v", err), nil
		}
		var keys []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				keys = append(keys, strings.TrimSuffix(e.Name(), ".json"))
				if len(keys) == maxMemoryEntries {
					break
				}
			}
		}
		return Result{Output: strings.Join(keys, "\n"), Title: "memory"}, nil
	case "delete":
		if a.Key == "" {
			return Errf("key required"), nil
		}
		if err := os.Remove(filepath.Join(memDir, sanitizeKey(a.Key)+".json")); err != nil && !os.IsNotExist(err) {
			return Errf("delete: %v", err), nil
		}
		return Result{Output: fmt.Sprintf("deleted %s", a.Key), Title: "memory"}, nil
	default:
		return Errf("unknown action %q", a.Action), nil
	}
}

func sanitizeKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	var b strings.Builder
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\':
			b.WriteByte('_')
		}
	}
	k = strings.Trim(b.String(), ".")
	if k == "" {
		return "memory"
	}
	if len(k) > 50 {
		k = k[:50]
	}
	return k
}
