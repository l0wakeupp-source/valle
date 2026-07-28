// Package permission implements the allow / ask / deny engine that gates every
// tool call, including glob matching for bash command patterns, file paths,
// hostnames and tool names.
package permission

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"rick/internal/config"
	"rick/internal/glob"
)

// Level is the resolved decision for a tool call.
type Level string

// Levels.
const (
	Allow Level = "allow"
	Ask   Level = "ask"
	Deny  Level = "deny"
)

// Request describes a pending tool invocation to be checked.
type Request struct {
	Tool    string // "bash", "edit", "write", "read", "webfetch", or an MCP tool name
	Command string // bash command line (Tool == "bash")
	Path    string // target path for file tools
	Host    string // target host for webfetch
	Title   string // human summary for the prompt
	Body    string // preview (diff, command, ...) rendered in the prompt
}

// Decision is a resolved level plus the rule that produced it, so the UI can
// explain *why* something was blocked instead of just refusing.
type Decision struct {
	Level  Level
	Rule   string // the matching pattern, e.g. "bash:git push*" or "path:**/.env"
	Source string // "session", "yolo", "policy" or "default"
}

// Engine resolves permissions from config plus session-scoped grants.
type Engine struct {
	mu        sync.RWMutex
	perm      *config.Permission
	root      string
	sessionOK map[string]bool // pattern -> always allow this session
	yolo      bool
	profile   string // name of the active profile, for display
}

// New builds an engine for a project root.
func New(perm *config.Permission, projectRoot string) *Engine {
	if perm == nil {
		perm = &config.Permission{Default: config.PermAsk}
	}
	return &Engine{perm: perm, root: projectRoot, sessionOK: map[string]bool{}}
}

// SetPermission swaps the policy (used when switching agents or profiles).
func (e *Engine) SetPermission(p *config.Permission) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p != nil {
		e.perm = p
	}
}

// Permission returns the active policy.
func (e *Engine) Permission() *config.Permission {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.perm
}

// SetProfile records the name of the active profile.
func (e *Engine) SetProfile(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.profile = name
}

// Profile returns the active profile name, or "" when the policy is inline.
func (e *Engine) Profile() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.profile
}

// SetYolo disables all prompting (dangerous; opt-in via --yolo or /yolo).
func (e *Engine) SetYolo(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.yolo = v
}

// Yolo reports whether prompting is disabled.
func (e *Engine) Yolo() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.yolo
}

// GrantSession records an "always allow this session" decision.
func (e *Engine) GrantSession(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionOK[key] = true
}

// SessionGrants lists the grants made this session, for /permissions.
func (e *Engine) SessionGrants() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.sessionOK))
	for k := range e.sessionOK {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ClearSessionGrants forgets every "always allow" made this session.
func (e *Engine) ClearSessionGrants() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionOK = map[string]bool{}
}

// SessionKey is the stable key used for session grants.
func SessionKey(r Request) string {
	if r.Tool == "bash" {
		return "bash:" + firstWords(r.Command, 2)
	}
	return r.Tool
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// Check resolves the level for a request.
func (e *Engine) Check(r Request) Level { return e.Resolve(r).Level }

// Resolve is Check with the reasoning attached.
//
// Precedence, strongest first:
//  1. yolo
//  2. session grants
//  3. an explicit deny anywhere (path, host, tool or bash rule)
//  4. the most specific matching rule for the tool's own dimension
//  5. the coarse per-tool level
//  6. the policy default
//
// Rule 3 is what makes a deny on "**/.ssh/**" meaningful: without it, a
// coarse `"edit": "allow"` would silently outrank the narrower path rule.
func (e *Engine) Resolve(r Request) Decision {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.yolo {
		return Decision{Level: Allow, Rule: "yolo", Source: "yolo"}
	}
	if e.sessionOK[SessionKey(r)] {
		return Decision{Level: Allow, Rule: SessionKey(r), Source: "session"}
	}

	p := e.perm
	def := Level(orDefault(p.Default, config.PermAsk))

	// A deny from any dimension wins outright.
	if d, ok := e.denyScan(p, r); ok {
		return d
	}

	switch r.Tool {
	case "bash":
		if lvl, pat, ok := matchBash(p.Bash, r.Command); ok {
			return Decision{Level: lvl, Rule: "bash:" + pat, Source: "policy"}
		}
		return Decision{Level: def, Rule: "default", Source: "default"}

	case "edit", "patch", "apply_patch", "write":
		if d, ok := matchPathRule(p.Paths, r.Path); ok {
			return e.guardDecision(r, d)
		}
		coarse := p.Edit
		if r.Tool == "write" {
			coarse = p.Write
		}
		if l := lvlOf(coarse); l != "" {
			return e.guardDecision(r, Decision{Level: l, Rule: r.Tool, Source: "policy"})
		}
		if d, ok := matchToolRule(p.Tools, r.Tool); ok {
			return e.guardDecision(r, d)
		}
		return e.guardDecision(r, Decision{Level: def, Rule: "default", Source: "default"})

	case "read", "grep", "glob", "list", "tree", "code_symbols":
		if d, ok := matchPathRule(p.Paths, r.Path); ok {
			return d
		}
		if l := lvlOf(p.Read); l != "" {
			return Decision{Level: l, Rule: "read", Source: "policy"}
		}
		if d, ok := matchToolRule(p.Tools, r.Tool); ok {
			return d
		}
		return Decision{Level: Allow, Rule: "read", Source: "default"}

	case "webfetch", "fetch", "websearch":
		if d, ok := matchHostRule(p.Hosts, r.Host); ok {
			return d
		}
		if l := lvlOf(p.WebF); l != "" {
			return Decision{Level: l, Rule: "webfetch", Source: "policy"}
		}
		if d, ok := matchToolRule(p.Tools, r.Tool); ok {
			return d
		}
		return Decision{Level: def, Rule: "default", Source: "default"}

	case "todowrite", "todoread", "task":
		if d, ok := matchToolRule(p.Tools, r.Tool); ok {
			return d
		}
		return Decision{Level: Allow, Rule: r.Tool, Source: "default"}
	}

	// Anything else, including every MCP tool, resolves through Tools.
	if d, ok := matchToolRule(p.Tools, r.Tool); ok {
		return d
	}
	return Decision{Level: def, Rule: "default", Source: "default"}
}

// denyScan looks for an explicit deny in any dimension relevant to r.
func (e *Engine) denyScan(p *config.Permission, r Request) (Decision, bool) {
	if r.Path != "" {
		if d, ok := matchPathRule(p.Paths, r.Path); ok && d.Level == Deny {
			return d, true
		}
	}
	if r.Host != "" {
		if d, ok := matchHostRule(p.Hosts, r.Host); ok && d.Level == Deny {
			return d, true
		}
	}
	if d, ok := matchToolRule(p.Tools, r.Tool); ok && d.Level == Deny {
		return d, true
	}
	return Decision{}, false
}

// guardDecision upgrades allow->ask when the target escapes the project root.
func (e *Engine) guardDecision(r Request, d Decision) Decision {
	if d.Level == Deny || r.Path == "" || e.root == "" {
		return d
	}
	abs, err := filepath.Abs(r.Path)
	if err != nil {
		return Decision{Level: Ask, Rule: "path unresolvable", Source: "guard"}
	}
	rel, err := filepath.Rel(e.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// Never silently auto-approve writes outside the project.
		return Decision{Level: Ask, Rule: "outside project root", Source: "guard"}
	}
	return d
}

func lvlOf(s string) Level {
	switch s {
	case config.PermAllow:
		return Allow
	case config.PermAsk:
		return Ask
	case config.PermDeny:
		return Deny
	}
	return ""
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// mostSpecific picks the winning pattern from a rule map.
//
// Specificity is: exact (no wildcard) beats wildcard, then longer beats
// shorter, then lexical order for stability. That makes "git push*" outrank
// "git*" outrank "*".
func mostSpecific(rules map[string]string, subject string, match func(pat, s string) bool) (Level, string, bool) {
	if len(rules) == 0 || subject == "" {
		return "", "", false
	}
	keys := make([]string, 0, len(rules))
	for k := range rules {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		wi, wj := strings.ContainsAny(keys[i], "*?"), strings.ContainsAny(keys[j], "*?")
		if wi != wj {
			return !wi
		}
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		if match(k, subject) {
			if l := lvlOf(rules[k]); l != "" {
				return l, k, true
			}
		}
	}
	return "", "", false
}

// matchPathRule resolves a path against the Paths rules.
func matchPathRule(rules map[string]string, path string) (Decision, bool) {
	if path == "" {
		return Decision{}, false
	}
	subject := filepath.ToSlash(path)
	lvl, pat, ok := mostSpecific(rules, subject, func(pat, s string) bool {
		if glob.MatchPath(pat, s) {
			return true
		}
		// Also try the absolute form so "**/.ssh/**" matches a relative arg.
		if abs, err := filepath.Abs(s); err == nil {
			return glob.MatchPath(pat, filepath.ToSlash(abs))
		}
		return false
	})
	if !ok {
		return Decision{}, false
	}
	return Decision{Level: lvl, Rule: "path:" + pat, Source: "policy"}, true
}

// matchHostRule resolves a hostname against the Hosts rules.
func matchHostRule(rules map[string]string, host string) (Decision, bool) {
	if host == "" {
		return Decision{}, false
	}
	lvl, pat, ok := mostSpecific(rules, strings.ToLower(host), func(pat, s string) bool {
		return glob.Match(strings.ToLower(pat), s)
	})
	if !ok {
		return Decision{}, false
	}
	return Decision{Level: lvl, Rule: "host:" + pat, Source: "policy"}, true
}

// matchToolRule resolves a tool name against the Tools rules.
func matchToolRule(rules map[string]string, tool string) (Decision, bool) {
	lvl, pat, ok := mostSpecific(rules, tool, glob.Match)
	if !ok {
		return Decision{}, false
	}
	return Decision{Level: lvl, Rule: "tool:" + pat, Source: "policy"}, true
}

// matchBash finds the most specific matching pattern for a command line.
//
// Every sub-command of a compound line must be permitted; the strictest level
// wins, so `git status && sudo reboot` resolves to deny.
func matchBash(patterns map[string]string, cmd string) (Level, string, bool) {
	if len(patterns) == 0 {
		return "", "", false
	}
	cmd = strings.TrimSpace(cmd)

	strictest := Level("")
	winner := ""
	found := false
	for _, sub := range SplitCommands(cmd) {
		sub = strings.TrimSpace(sub)
		if sub == "" {
			continue
		}
		lvl, pat, ok := mostSpecific(patterns, sub, glob.Match)
		if !ok {
			continue
		}
		found = true
		if s := stricter(strictest, lvl); s != strictest {
			strictest, winner = s, pat
		} else if winner == "" {
			winner = pat
		}
	}
	if !found {
		return "", "", false
	}
	return strictest, winner, true
}

func stricter(a, b Level) Level {
	rank := map[Level]int{Allow: 0, Ask: 1, Deny: 2, "": -1}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// SplitCommands splits a shell line on &&, ||, ; and | (outside quotes).
func SplitCommands(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote && (i == 0 || cmd[i-1] != '\\') {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case ';', '|', '&', '\n':
			if c == '|' && i+1 < len(cmd) && cmd[i+1] == '|' {
				i++
			} else if c == '&' && i+1 < len(cmd) && cmd[i+1] == '&' {
				i++
			}
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

// Match reports whether s matches a glob pattern. See package glob.
func Match(pattern, s string) bool { return glob.Match(pattern, s) }

// MatchAny reports whether s matches any pattern.
func MatchAny(patterns []string, s string) bool { return glob.MatchAny(patterns, s) }
