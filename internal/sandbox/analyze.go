package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"rick/internal/glob"
)

// Violation is a policy breach found by static analysis.
type Violation struct {
	Rule    string // short machine-readable id, e.g. "network.denied"
	Command string // the offending sub-command
	Detail  string // human explanation
}

// Error renders the violation for the model and the user.
func (v Violation) Error() string {
	if v.Command == "" {
		return v.Detail
	}
	return v.Detail + " (in: " + strings.TrimSpace(v.Command) + ")"
}

// networkClients are programs whose entire purpose is to talk to the network.
var networkClients = map[string]bool{
	"curl": true, "wget": true, "nc": true, "netcat": true, "ncat": true,
	"telnet": true, "ssh": true, "scp": true, "sftp": true, "rsync": true,
	"ftp": true, "aria2c": true, "httpie": true, "http": true,
}

// packageManagers reach the network as a side effect of their normal job.
// They are only flagged for subcommands that actually fetch.
var packageManagers = map[string][]string{
	"npm":     {"install", "i", "ci", "update", "publish", "audit"},
	"pnpm":    {"install", "i", "add", "update", "publish"},
	"yarn":    {"install", "add", "upgrade", "publish"},
	"pip":     {"install", "download", "wheel"},
	"pip3":    {"install", "download", "wheel"},
	"uv":      {"pip", "add", "sync", "tool"},
	"go":      {"get", "mod", "install", "download"},
	"cargo":   {"install", "fetch", "publish", "update"},
	"gem":     {"install", "update", "push"},
	"apt":     {"install", "update", "upgrade"},
	"apt-get": {"install", "update", "upgrade"},
	"brew":    {"install", "update", "upgrade", "fetch"},
	"git":     {"clone", "fetch", "pull", "push", "remote", "submodule"},
	"docker":  {"pull", "push", "build", "run"},
}

// privilegeEscalators must never run inside a sandbox.
var privilegeEscalators = map[string]bool{
	"sudo": true, "su": true, "doas": true, "pkexec": true,
	"runas": true, "gsudo": true,
}

// sensitiveDevices are write targets that can brick a machine.
var sensitiveDevices = []string{
	"/dev/sd", "/dev/nvme", "/dev/hd", "/dev/disk", "/dev/mem", "/dev/kmem",
	`\\.\PhysicalDrive`, `\\.\C:`,
}

// systemRoots are never writable regardless of policy.
func systemRoots() []string {
	if runtime.GOOS == "windows" {
		sysRoot := os.Getenv("SystemRoot")
		if sysRoot == "" {
			sysRoot = `C:\Windows`
		}
		return []string{
			sysRoot,
			os.Getenv("ProgramFiles"),
			os.Getenv("ProgramFiles(x86)"),
		}
	}
	return []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/sys", "/proc"}
}

// Analyze checks a shell command line against the policy and returns every
// violation it can prove statically.
//
// This is deliberately conservative: it only reports things that are
// unambiguous from the command text. Real enforcement is the OS layer; this
// exists to give a fast, readable refusal instead of a confusing EPERM deep
// inside a build, and to cover platforms with no kernel confinement.
func Analyze(p Policy, command string) []Violation {
	if p.Mode == ModeOff {
		return nil
	}
	var out []Violation
	for _, sub := range splitPipeline(command) {
		argv := tokenize(sub)
		if len(argv) == 0 {
			continue
		}
		out = append(out, analyzeOne(p, sub, argv)...)
	}
	return out
}

func analyzeOne(p Policy, raw string, argv []string) []Violation {
	var out []Violation
	prog := baseName(argv[0])
	args := argv[1:]

	if privilegeEscalators[prog] {
		out = append(out, Violation{
			Rule: "privilege.escalation", Command: raw,
			Detail: prog + " is not permitted inside the sandbox",
		})
		return out
	}

	if !p.Network && usesNetwork(prog, args) {
		out = append(out, Violation{
			Rule: "network.denied", Command: raw,
			Detail: "the sandbox has no network access and " + prog + " needs it",
		})
	}

	if p.Network && (len(p.AllowHosts) > 0 || len(p.DenyHosts) > 0) {
		for _, host := range hostsIn(args) {
			if !hostAllowed(p, host) {
				out = append(out, Violation{
					Rule: "network.host", Command: raw,
					Detail: "host " + host + " is outside the sandbox network allowlist",
				})
			}
		}
	}

	if !p.WritesAllowed() && mutatesFilesystem(prog, args) && len(writeTargets(prog, args)) > 0 {
		out = append(out, Violation{
			Rule: "write.readonly", Command: raw,
			Detail: "the sandbox is read-only and " + prog + " writes to disk",
		})
		return out
	}

	if p.WritesAllowed() && p.Mode != ModeTrusted {
		for _, target := range writeTargets(prog, args) {
			if strings.ContainsAny(target, "$`") {
				out = append(out, Violation{
					Rule: "write.dynamic_path", Command: raw,
					Detail: "dynamic shell path expansion cannot be verified safely",
				})
				continue
			}
			target = expandWriteTarget(target)
			if dev := matchDevice(target); dev != "" {
				out = append(out, Violation{
					Rule: "write.device", Command: raw,
					Detail: "writing to the raw device " + dev + " is never permitted",
				})
				continue
			}
			if sysRoot := matchSystemRoot(target); sysRoot != "" {
				out = append(out, Violation{
					Rule: "write.system", Command: raw,
					Detail: "writing under the system directory " + sysRoot + " is never permitted",
				})
				continue
			}
			if !p.Writable(resolveAgainst(p.Workspace, target)) {
				out = append(out, Violation{
					Rule: "write.outside", Command: raw,
					Detail: "path " + target + " is outside the sandbox writable roots",
				})
			}
		}
	}

	return out
}

// usesNetwork reports whether the program will open a socket.
func usesNetwork(prog string, args []string) bool {
	if networkClients[prog] {
		return true
	}
	subs, ok := packageManagers[prog]
	if !ok {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		for _, s := range subs {
			if a == s {
				return true
			}
		}
		return false // first positional decides
	}
	return false
}

// writeCommands map a program to the argument positions it writes to.
// A nil slice means "every non-flag argument", which is the common case.
var writeCommands = map[string]bool{
	"rm": true, "rmdir": true, "mv": true, "cp": true, "touch": true,
	"mkdir": true, "truncate": true, "dd": true, "tee": true, "chmod": true,
	"chown": true, "ln": true, "install": true, "shred": true,
}

// mutatesFilesystem reports whether the program writes at all.
func mutatesFilesystem(prog string, args []string) bool {
	if writeCommands[prog] {
		return true
	}
	// Redirections are caught by the caller scanning the raw text.
	for _, a := range args {
		if a == ">" || a == ">>" {
			return true
		}
	}
	return false
}

// nullSinks are the discard devices. Writing to them is not a filesystem
// write and must never be flagged, or every `2> /dev/null` trips the analyzer.
var nullSinks = map[string]bool{
	"/dev/null": true, "nul": true, "nul:": true, "/dev/stdout": true,
	"/dev/stderr": true, "/dev/tty": true, "con": true, "con:": true,
}

func isNullSink(target string) bool {
	t := strings.ToLower(strings.Trim(target, `"'`))
	t = strings.TrimPrefix(filepath.ToSlash(t), "./")
	return nullSinks[t]
}

// writeTargets extracts the paths a command will write to.
func writeTargets(prog string, args []string) []string {
	var out []string
	add := func(t string) {
		t = strings.Trim(t, `"'`)
		if t == "" || isNullSink(t) {
			return
		}
		out = append(out, t)
	}
	if writeCommands[prog] {
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			// dd takes `of=PATH` rather than a positional target; without
			// stripping the prefix the device check never fires.
			if v, ok := strings.CutPrefix(a, "of="); ok {
				add(v)
				continue
			}
			if strings.HasPrefix(a, "if=") {
				continue // input file, not a write
			}
			add(a)
		}
	}
	// Shell redirection targets, whatever the program is.
	for i, a := range args {
		if (a == ">" || a == ">>") && i+1 < len(args) {
			add(args[i+1])
			continue
		}
		if strings.HasPrefix(a, ">>") && len(a) > 2 {
			add(a[2:])
		} else if strings.HasPrefix(a, ">") && len(a) > 1 {
			add(a[1:])
		}
	}
	return out
}

func expandWriteTarget(target string) string {
	if !strings.HasPrefix(target, "~") {
		return target
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return target
	}
	if target == "~" {
		return home
	}
	if strings.HasPrefix(target, "~/") || strings.HasPrefix(target, `~\\`) {
		return filepath.Join(home, target[2:])
	}
	return target
}

// hostsIn pulls hostnames out of URL-looking arguments.
func hostsIn(args []string) []string {
	var out []string
	for _, a := range args {
		h := hostOf(a)
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}

func hostOf(arg string) string {
	s := arg
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else if !strings.Contains(s, ".") || strings.HasPrefix(s, "-") {
		return ""
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	if s == "" || !strings.Contains(s, ".") {
		return ""
	}
	return strings.ToLower(s)
}

// hostAllowed applies the deny list first, then the allow list.
func hostAllowed(p Policy, host string) bool {
	for _, d := range p.DenyHosts {
		if glob.Match(strings.ToLower(d), host) {
			return false
		}
	}
	if len(p.AllowHosts) == 0 {
		return true
	}
	for _, a := range p.AllowHosts {
		if glob.Match(strings.ToLower(a), host) {
			return true
		}
	}
	return false
}

func matchDevice(target string) string {
	lower := strings.ToLower(target)
	for _, d := range sensitiveDevices {
		if strings.HasPrefix(lower, strings.ToLower(d)) {
			return d
		}
	}
	return ""
}

func matchSystemRoot(target string) string {
	abs, err := filepath.Abs(target)
	if err != nil {
		return ""
	}
	for _, root := range systemRoots() {
		if root == "" {
			continue
		}
		if under(filepath.Clean(root), filepath.Clean(abs)) {
			return root
		}
	}
	return ""
}

func pathDenied(patterns []string, abs string) bool {
	if len(patterns) == 0 {
		return false
	}
	slashed := filepath.ToSlash(abs)
	for _, pat := range patterns {
		pat = filepath.ToSlash(expandUser(pat))
		if glob.MatchPath(pat, slashed) {
			return true
		}
	}
	return false
}

func resolveAgainst(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}

func baseName(prog string) string {
	prog = filepath.Base(prog)
	return strings.TrimSuffix(strings.ToLower(prog), ".exe")
}

func expandUser(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p[1:], string(filepath.Separator)))
		}
	}
	return p
}

func tempRoots() []string {
	roots := []string{filepath.Clean(os.TempDir())}
	if runtime.GOOS != "windows" {
		roots = append(roots, "/tmp", "/var/tmp")
	}
	return roots
}

// splitPipeline splits a shell line on &&, ||, ;, | and newlines, ignoring
// separators inside quotes.
func splitPipeline(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		if strings.TrimSpace(cur.String()) != "" {
			out = append(out, cur.String())
		}
		cur.Reset()
	}
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
		case ';', '\n', '|', '&':
			if (c == '|' && i+1 < len(cmd) && cmd[i+1] == '|') ||
				(c == '&' && i+1 < len(cmd) && cmd[i+1] == '&') {
				i++
			}
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

// tokenize splits a sub-command into argv, honouring quotes and keeping
// redirection operators as their own tokens so writeTargets can see them.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	push := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote && (i == 0 || s[i-1] != '\\') {
				quote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case ' ', '\t', '\r', '\n':
			push()
		case '>':
			push()
			if i+1 < len(s) && s[i+1] == '>' {
				out = append(out, ">>")
				i++
			} else {
				out = append(out, ">")
			}
		default:
			cur.WriteByte(c)
		}
	}
	push()
	return out
}
