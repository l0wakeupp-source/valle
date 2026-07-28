// Package sandbox confines commands rick executes on the user's machine.
//
// Three layers cooperate, weakest to strongest:
//
//  1. Static analysis (every platform) rejects a command line before it ever
//     reaches a shell when it plainly violates the policy — network clients
//     while the network is off, writes above the workspace, privilege
//     escalation, device writes.
//  2. Environment shaping strips credentials the command has no business
//     reading and points proxy variables at a black hole when the network is
//     denied, so well-behaved tooling fails closed.
//  3. OS enforcement is the real fence: a job object plus (optionally) a
//     write-restricted token on Windows, bubblewrap namespaces on Linux,
//     sandbox-exec on macOS, and rlimits wherever the kernel offers them.
//
// Layer 3 is best-effort by design: rick reports what it actually managed to
// apply through Applied() rather than pretending a fence exists.
package sandbox

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Mode is the coarse confinement level requested by the user.
type Mode string

// Modes, ordered from most to least confined.
const (
	// ModeReadOnly permits no filesystem writes and no network.
	ModeReadOnly Mode = "read-only"
	// ModeWorkspace permits writes inside the workspace and temp dirs only.
	ModeWorkspace Mode = "workspace-write"
	// ModeTrusted keeps resource limits and process-tree cleanup but does not
	// restrict paths or the network.
	ModeTrusted Mode = "trusted"
	// ModeOff runs commands directly on the host with no confinement at all.
	ModeOff Mode = "off"
)

// Enforcement selects how hard rick tries to obtain kernel-level confinement.
type Enforcement string

// Enforcement levels.
const (
	// EnforceAuto applies OS confinement when it is available and silently
	// falls back to static analysis when it is not.
	EnforceAuto Enforcement = "auto"
	// EnforceOS refuses to run a command when OS confinement is unavailable.
	EnforceOS Enforcement = "os"
	// EnforceStatic skips OS confinement and relies on analysis alone.
	EnforceStatic Enforcement = "static"
)

// Limits caps the resources a sandboxed command may consume. Zero means "no
// limit beyond whatever the OS already imposes".
type Limits struct {
	MemoryMB   int `json:"memory_mb,omitempty"`
	CPUSeconds int `json:"cpu_seconds,omitempty"`
	Processes  int `json:"processes,omitempty"`
	FileSizeMB int `json:"file_size_mb,omitempty"`
}

// Policy is a fully resolved sandbox configuration.
type Policy struct {
	Mode        Mode        `json:"mode,omitempty"`
	Enforcement Enforcement `json:"enforcement,omitempty"`

	// Network allows outbound connections. Ignored (forced false) in
	// ModeReadOnly.
	Network bool `json:"network,omitempty"`
	// AllowHosts narrows an enabled network to these host globs. Empty means
	// every host is reachable.
	AllowHosts []string `json:"allow_hosts,omitempty"`
	// DenyHosts blocks these host globs even when the network is enabled.
	DenyHosts []string `json:"deny_hosts,omitempty"`

	// WritableRoots lists absolute directories a command may write to, on top
	// of the workspace itself.
	WritableRoots []string `json:"writable_roots,omitempty"`
	// ReadableRoots lists absolute directories a command may read. Empty means
	// the whole filesystem is readable.
	ReadableRoots []string `json:"readable_roots,omitempty"`
	// DenyPaths blocks these path globs for both reads and writes.
	DenyPaths []string `json:"deny_paths,omitempty"`

	// AllowEnv lists environment variable globs that survive scrubbing. Empty
	// keeps the default allowlist.
	AllowEnv []string `json:"allow_env,omitempty"`
	// DenyEnv lists environment variable globs to strip on top of the
	// built-in credential patterns.
	DenyEnv []string `json:"deny_env,omitempty"`
	// KeepCredentials disables credential scrubbing (needed for commands that
	// legitimately push to a remote).
	KeepCredentials bool `json:"keep_credentials,omitempty"`

	Limits Limits `json:"limits,omitempty"`

	// Workspace is the project root; it is always writable outside
	// ModeReadOnly. Set by Resolve, not by the user.
	Workspace string `json:"-"`
}

// Default returns the policy rick uses when nothing is configured: writes are
// confined to the workspace, the network stays available for package managers,
// and a runaway command cannot take the machine down with it.
func Default() Policy {
	return Policy{
		Mode:        ModeWorkspace,
		Enforcement: EnforceAuto,
		Network:     true,
		Limits: Limits{
			MemoryMB:   4096,
			CPUSeconds: 900,
			Processes:  256,
			FileSizeMB: 2048,
		},
	}
}

// Off returns the unconfined policy.
func Off() Policy { return Policy{Mode: ModeOff, Enforcement: EnforceStatic, Network: true} }

// Normalize fills in defaults, resolves the workspace and enforces the
// invariants each mode implies. It never returns an unusable policy.
func (p Policy) Normalize(workspace string) Policy {
	out := p
	if out.Mode == "" {
		out.Mode = ModeWorkspace
	}
	if out.Enforcement == "" {
		out.Enforcement = EnforceAuto
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		out.Workspace = filepath.Clean(abs)
	} else {
		out.Workspace = workspace
	}

	switch out.Mode {
	case ModeReadOnly:
		out.Network = false
		out.WritableRoots = nil
	case ModeOff, ModeTrusted:
		out.Network = true
	}

	out.WritableRoots = normalizeRoots(out.WritableRoots)
	out.ReadableRoots = normalizeRoots(out.ReadableRoots)
	if out.Limits == (Limits{}) && out.Mode != ModeOff {
		out.Limits = Default().Limits
	}
	return out
}

func normalizeRoots(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, r := range in {
		r = strings.TrimSpace(expandUser(r))
		if r == "" {
			continue
		}
		if abs, err := filepath.Abs(r); err == nil {
			r = abs
		}
		r = filepath.Clean(r)
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// Confined reports whether the policy asks for any confinement at all.
func (p Policy) Confined() bool { return p.Mode != ModeOff }

// WritesAllowed reports whether the policy permits filesystem writes anywhere.
func (p Policy) WritesAllowed() bool { return p.Mode != ModeReadOnly }

// Writable reports whether path may be written under this policy.
func (p Policy) Writable(path string) bool {
	if p.Mode == ModeOff || p.Mode == ModeTrusted {
		return true
	}
	if p.Mode == ModeReadOnly {
		return false
	}
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	abs = filepath.Clean(abs)
	if pathDenied(p.DenyPaths, abs) {
		return false
	}
	for _, root := range p.writeRoots() {
		if under(root, abs) {
			return true
		}
	}
	return false
}

// Readable reports whether path may be read under this policy.
func (p Policy) Readable(path string) bool {
	if p.Mode == ModeOff {
		return true
	}
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	abs = filepath.Clean(abs)
	if pathDenied(p.DenyPaths, abs) {
		return false
	}
	if len(p.ReadableRoots) == 0 {
		return true
	}
	for _, root := range p.ReadableRoots {
		if under(root, abs) {
			return true
		}
	}
	return under(p.Workspace, abs)
}

// writeRoots is the full writable set: workspace, temp, and extra roots.
func (p Policy) writeRoots() []string {
	roots := make([]string, 0, len(p.WritableRoots)+3)
	if p.Workspace != "" {
		roots = append(roots, p.Workspace)
	}
	roots = append(roots, p.WritableRoots...)
	roots = append(roots, tempRoots()...)
	return roots
}

// under reports whether abs sits inside root (or is root itself).
func under(root, abs string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Describe renders a one-line human summary for the status bar.
func (p Policy) Describe() string {
	if p.Mode == ModeOff {
		return "sandbox off — commands run directly on the host"
	}
	net := "network off"
	if p.Network {
		net = "network on"
		if len(p.AllowHosts) > 0 {
			net = "network: " + strings.Join(p.AllowHosts, ",")
		}
	}
	return fmt.Sprintf("%s · %s · %s", p.Mode, net, p.Enforcement)
}

// Detail renders a multi-line summary for /sandbox.
func (p Policy) Detail(applied string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mode:        %s\n", p.Mode)
	fmt.Fprintf(&b, "enforcement: %s (%s)\n", p.Enforcement, applied)
	fmt.Fprintf(&b, "platform:    %s\n", runtime.GOOS)
	fmt.Fprintf(&b, "workspace:   %s\n", p.Workspace)
	if p.Mode != ModeReadOnly && p.Mode != ModeOff {
		fmt.Fprintf(&b, "writable:    %s\n", strings.Join(p.writeRoots(), "\n             "))
	}
	if len(p.ReadableRoots) > 0 {
		fmt.Fprintf(&b, "readable:    %s\n", strings.Join(p.ReadableRoots, "\n             "))
	}
	if len(p.DenyPaths) > 0 {
		fmt.Fprintf(&b, "deny paths:  %s\n", strings.Join(p.DenyPaths, ", "))
	}
	fmt.Fprintf(&b, "network:     %v", p.Network)
	if len(p.AllowHosts) > 0 {
		fmt.Fprintf(&b, " (allow %s)", strings.Join(p.AllowHosts, ", "))
	}
	if len(p.DenyHosts) > 0 {
		fmt.Fprintf(&b, " (deny %s)", strings.Join(p.DenyHosts, ", "))
	}
	b.WriteString("\n")
	l := p.Limits
	fmt.Fprintf(&b, "limits:      mem %s · cpu %s · procs %s · file %s",
		mb(l.MemoryMB), secs(l.CPUSeconds), count(l.Processes), mb(l.FileSizeMB))
	if !p.KeepCredentials {
		b.WriteString("\ncredentials: scrubbed from the command environment")
	}
	return b.String()
}

func mb(v int) string {
	if v <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%dMB", v)
}

func secs(v int) string {
	if v <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%ds", v)
}

func count(v int) string {
	if v <= 0 {
		return "unlimited"
	}
	return fmt.Sprint(v)
}

// ParseMode maps user input onto a Mode, accepting the aliases other agents
// use so muscle memory carries over.
func ParseMode(s string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read-only", "readonly", "ro", "read":
		return ModeReadOnly, true
	case "workspace-write", "workspace", "write", "ws":
		return ModeWorkspace, true
	case "trusted", "host", "full", "danger-full-access":
		return ModeTrusted, true
	case "off", "none", "disabled":
		return ModeOff, true
	}
	return "", false
}

// Modes lists the selectable modes in order of decreasing confinement.
func Modes() []Mode { return []Mode{ModeReadOnly, ModeWorkspace, ModeTrusted, ModeOff} }
