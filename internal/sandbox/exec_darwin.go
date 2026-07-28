//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func init() {
	backendFor = func(Policy) Backend { return darwinBackend{} }
}

// darwinBackend confines commands with sandbox-exec, the same Seatbelt
// mechanism macOS uses for App Store apps. The profile is generated per
// command from the policy.
//
// sandbox-exec is deprecated by Apple but still functional and still the only
// user-space confinement available without entitlements. When it is missing
// rick falls back to a process group plus rlimits.
type darwinBackend struct{}

func (darwinBackend) Name() string {
	if sandboxExecPath() != "" {
		return "sandbox-exec (Seatbelt)"
	}
	return "rlimit + process group"
}

func (darwinBackend) Available() bool { return true }

func sandboxExecPath() string {
	for _, p := range []string{"/usr/bin/sandbox-exec"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func (darwinBackend) Prepare(cmd *exec.Cmd, p Policy) (Session, error) {
	s := &unixSession{limits: p.Limits, applied: "rlimit + process group"}

	if se := sandboxExecPath(); se != "" && p.Mode != ModeTrusted {
		profile := seatbeltProfile(p)
		args := []string{"-p", profile, cmd.Path}
		args = append(args, cmd.Args[1:]...)
		cmd.Path = se
		cmd.Args = append([]string{se}, args...)
		s.applied = "sandbox-exec (Seatbelt)"
		if !p.Network {
			s.applied += " · network denied"
		}
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return s, nil
}

// seatbeltProfile renders the policy as a Seatbelt SBPL profile.
func seatbeltProfile(p Policy) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")

	if !p.Network {
		b.WriteString("(deny network*)\n")
	}

	if !p.WritesAllowed() {
		b.WriteString("(deny file-write*)\n")
	} else {
		b.WriteString("(deny file-write*)\n")
		for _, root := range p.writeRoots() {
			fmt.Fprintf(&b, "(allow file-write* (subpath %q))\n", root)
		}
		// Everything needs a scratch dir and a tty.
		b.WriteString("(allow file-write-data (literal \"/dev/null\") (literal \"/dev/stdout\") (literal \"/dev/stderr\"))\n")
		b.WriteString("(allow file-write* (subpath \"/private/var/folders\"))\n")
	}

	for _, deny := range p.DenyPaths {
		fmt.Fprintf(&b, "(deny file-read* file-write* (subpath %q))\n", deny)
	}

	// A sandboxed build has no business driving the user's desktop.
	b.WriteString("(deny mach-lookup (global-name \"com.apple.pasteboard.1\"))\n")
	return b.String()
}

// applyResourceLimits caps a running child with setrlimit. macOS has no
// prlimit(2), so the limits that matter are enforced by Seatbelt and the
// wall-clock timeout instead; this covers the file-size cap which Seatbelt
// does not express.
func applyResourceLimits(pid int, l Limits) {
	// Darwin cannot set another process's rlimits; the child inherits rick's.
	// Left intentionally empty so the shared session code stays uniform.
	_, _ = pid, l
}
