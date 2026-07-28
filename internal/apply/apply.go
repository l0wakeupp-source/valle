// Package apply extracts and applies the latest agent-produced diff.
package apply

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"rick/internal/config"
	"rick/internal/provider"
	"rick/internal/sandbox"
	"rick/internal/session"
)

// patchStats summarises what a unified diff touches.
type patchStats struct {
	Modified int
	Added    int
	Deleted  int
}

// Options configures an apply run.
type Options struct {
	SessionID string
	DryRun    bool
}

// Run locates and applies the latest diff.
func Run(dir string, opts Options) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	loaded, err := config.Load(abs)
	if err != nil {
		return err
	}
	if err := ensureWritable(loaded); err != nil {
		return err
	}

	store, err := session.NewStore(filepath.Join(config.DataDir(), "sessions"))
	if err != nil {
		return err
	}

	id := opts.SessionID
	if id == "" {
		id = store.GetCurrent(abs)
		if id == "" {
			return fmt.Errorf("no current session for %s (pass --session <id>)", abs)
		}
	}
	sess, err := store.Load(id)
	if err != nil {
		return fmt.Errorf("load session %s: %w", id, err)
	}

	patch := findLatestPatch(sess.Messages)
	if patch == "" {
		return fmt.Errorf("no unified diff found in session %s", id)
	}

	tmp, err := os.CreateTemp("", "rick-apply-*.patch")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(patch); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if out, err := gitApply(abs, tmpPath, true); err != nil {
		return fmt.Errorf("git apply --check failed: %v\n%s", err, out)
	}

	stats := statsFor(patch)
	if opts.DryRun {
		fmt.Printf("dry run OK — patch from session %s applies cleanly\n", id)
		reportStats(stats)
		return nil
	}

	if out, err := gitApply(abs, tmpPath, false); err != nil {
		return fmt.Errorf("git apply failed: %v\n%s", err, out)
	}
	fmt.Printf("applied patch from session %s\n", id)
	reportStats(stats)
	return nil
}

func ensureWritable(loaded *config.Loaded) error {
	cfg := loaded.Config
	perm := config.ResolvePermission(cfg, cfg.Permission)
	sbCfg := cfg.Sandbox
	if perm != nil && perm.Sandbox != nil {
		sbCfg = config.MergeSandbox(perm.Sandbox, cfg.Sandbox)
	}
	policy := sandbox.FromConfig(sbCfg, loaded.ProjectRoot)
	if policy.Mode == sandbox.ModeReadOnly {
		return fmt.Errorf("sandbox is read-only; rick apply would write to the working tree")
	}
	return nil
}

func gitApply(dir, patchPath string, check bool) (string, error) {
	args := []string{"apply"}
	if check {
		args = append(args, "--check")
	}
	args = append(args, patchPath)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func findLatestPatch(msgs []provider.Message) string {
	// Pass 1: apply_patch tool calls
	for i := len(msgs) - 1; i >= 0; i-- {
		for j := len(msgs[i].Content) - 1; j >= 0; j-- {
			b := msgs[i].Content[j]
			if b.Type != "tool_use" || b.Name != "apply_patch" {
				continue
			}
			if p := extractPatch(string(b.Input)); p != "" {
				return p
			}
		}
	}
	// Pass 2: any block whose text looks like a unified diff
	for i := len(msgs) - 1; i >= 0; i-- {
		for j := len(msgs[i].Content) - 1; j >= 0; j-- {
			b := msgs[i].Content[j]
			var candidate string
			switch b.Type {
			case "tool_result":
				candidate = b.Content
			case "text":
				candidate = b.Text
			case "tool_use":
				candidate = string(b.Input)
			}
			if p := extractPatch(candidate); p != "" {
				return p
			}
		}
	}
	return ""
}

func extractPatch(s string) string {
	if s == "" {
		return ""
	}
	s = unescapeJSONish(s)
	if !looksLikePatch(s) {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "diff --git ") || strings.HasPrefix(l, "--- a/") ||
			strings.HasPrefix(l, "--- /dev/null") || strings.HasPrefix(l, "Index: ") {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "```") {
			end = i
			break
		}
	}
	patch := strings.Join(lines[start:end], "\n")
	patch = strings.TrimRight(patch, "\n")
	if patch == "" {
		return ""
	}
	return patch + "\n"
}

func unescapeJSONish(s string) string {
	if !strings.Contains(s, `\n`) {
		return s
	}
	r := strings.NewReplacer(`\r\n`, "\n", `\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`)
	return r.Replace(s)
}

func looksLikePatch(s string) bool {
	if strings.Contains(s, "diff --git ") {
		return true
	}
	hasOld := strings.Contains(s, "\n--- a/") || strings.HasPrefix(s, "--- a/") ||
		strings.Contains(s, "\n--- /dev/null")
	hasNew := strings.Contains(s, "\n+++ b/") || strings.Contains(s, "\n+++ /dev/null")
	return hasOld && hasNew
}

func statsFor(patch string) patchStats {
	var st patchStats
	lines := strings.Split(patch, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "--- ") {
			continue
		}
		oldPath := strings.TrimSpace(strings.TrimPrefix(lines[i], "--- "))
		newPath := ""
		if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") {
			newPath = strings.TrimSpace(strings.TrimPrefix(lines[i+1], "+++ "))
		}
		switch {
		case oldPath == "/dev/null":
			st.Added++
		case newPath == "/dev/null":
			st.Deleted++
		default:
			st.Modified++
		}
	}
	return st
}

func reportStats(st patchStats) {
	fmt.Printf("%d modified, %d added, %d deleted\n", st.Modified, st.Added, st.Deleted)
}
