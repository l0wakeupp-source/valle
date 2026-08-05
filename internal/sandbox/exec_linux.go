//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func init() {
	backendFor = func(Policy) Backend { return linuxBackend{} }
}

// linuxBackend confines commands with bubblewrap when it is installed and
// falls back to rlimits plus a dedicated process group otherwise.
//
// bubblewrap gives real isolation: a fresh mount namespace where the workspace
// is the only writable path, a network namespace with no interfaces when the
// policy denies the network, and PID namespace cleanup so orphans cannot
// survive the tool call. Without it rick still caps resources and kills the
// whole tree, which matches what the Windows job object guarantees.
type linuxBackend struct{}

func (linuxBackend) Name() string {
	if bwrapPath() != "" {
		return "bubblewrap"
	}
	return "rlimit + process group"
}

func (linuxBackend) Available() bool { return true }

func bwrapPath() string {
	bwrapPathOnce.Do(func() {
		p, err := exec.LookPath("bwrap")
		if err != nil {
			cachedBwrapPath = ""
		} else {
			cachedBwrapPath = p
		}
	})
	return cachedBwrapPath
}

var (
	bwrapPathOnce   sync.Once
	cachedBwrapPath string
)

func (linuxBackend) Prepare(cmd *exec.Cmd, p Policy) (Session, error) {
	s := &unixSession{limits: p.Limits, applied: "rlimit + process group"}

	if bw := bwrapPath(); bw != "" && p.Mode != ModeTrusted {
		args := bwrapArgs(p, cmd.Dir)
		args = append(args, cmd.Path)
		args = append(args, cmd.Args[1:]...)
		cmd.Path = bw
		cmd.Args = append([]string{bw}, args...)
		s.applied = "bubblewrap namespaces"
		if !p.Network {
			s.applied += " · network namespace isolated"
		}
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Own process group so Close can signal the whole tree; Pdeathsig makes
	// the child die if rick is killed rather than leaking.
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL

	return s, nil
}

// bwrapArgs renders the policy as a bubblewrap command line.
func bwrapArgs(p Policy, dir string) []string {
	args := []string{
		"--die-with-parent",
		"--unshare-uts", "--unshare-ipc", "--unshare-pid", "--unshare-cgroup-try",
		"--new-session",
		"--proc", "/proc",
		"--dev", "/dev",
	}

	if !p.Network {
		args = append(args, "--unshare-net")
	}

	for _, ro := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt"} {
		if _, err := os.Stat(ro); err == nil {
			args = append(args, "--ro-bind", ro, ro)
		}
	}
	// Toolchain caches live in a small allowlist under $HOME; do not expose
	// credentials and unrelated application data from the whole home directory.
	if home, err := os.UserHomeDir(); err == nil {
		for _, cacheRoot := range homeCacheRoots(home) {
			if _, err := os.Stat(cacheRoot); err == nil {
				args = append(args, "--ro-bind-try", cacheRoot, cacheRoot)
			}
		}
	}
	for _, r := range p.ReadableRoots {
		args = append(args, "--ro-bind-try", r, r)
	}

	if p.WritesAllowed() {
		if p.Workspace != "" {
			args = append(args, "--bind", p.Workspace, p.Workspace)
		}
		for _, w := range p.WritableRoots {
			args = append(args, "--bind-try", w, w)
		}
		args = append(args, "--tmpfs", "/tmp")
	}

	if p.Workspace != "" {
		if dir == "" {
			dir = p.Workspace
		}
		args = append(args, "--chdir", dir)
	}
	for _, denied := range p.DenyPaths {
		if denied != "" {
			args = append(args, "--tmpfs", denied)
		}
	}
	return args
}

// applyResourceLimits sets caps on a running child via prlimit(2).
func applyResourceLimits(pid int, l Limits) {
	set := func(resource int, value uint64) {
		if value == 0 {
			return
		}
		rlim := syscall.Rlimit{Cur: value, Max: value}
		_, _, _ = syscall.RawSyscall6(syscall.SYS_PRLIMIT64,
			uintptr(pid), uintptr(resource), uintptr(unsafe.Pointer(&rlim)), 0, 0, 0)
	}
	if l.MemoryMB > 0 {
		set(syscall.RLIMIT_AS, uint64(l.MemoryMB)<<20)
	}
	if l.CPUSeconds > 0 {
		set(syscall.RLIMIT_CPU, uint64(l.CPUSeconds))
	}
	if l.FileSizeMB > 0 {
		set(syscall.RLIMIT_FSIZE, uint64(l.FileSizeMB)<<20)
	}
	if l.Processes > 0 {
		set(unix.RLIMIT_NPROC, uint64(l.Processes))
	}
}
