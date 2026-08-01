package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrUnavailable is returned when Enforcement is EnforceOS but the platform
// cannot provide kernel-level confinement.
var ErrUnavailable = errors.New("os-level sandbox is unavailable on this system")

// Spec describes one sandboxed command execution.
type Spec struct {
	Command string        // shell command line
	Shell   string        // shell binary
	Prefix  []string      // shell argument prefix, e.g. ["-lc"]
	Dir     string        // working directory
	Env     []string      // fully resolved environment (see Environ)
	Timeout time.Duration // hard wall clock cap
	Stdout  io.Writer
	Stderr  io.Writer
}

var homeCacheRelativePaths = []string{
	".cache",
	".cargo/registry",
	".cargo/git",
	".npm",
	"go/pkg/mod",
	".gradle/caches",
	".m2/repository",
	".nuget/packages",
}

func homeCacheRoots(home string) []string {
	roots := make([]string, 0, len(homeCacheRelativePaths))
	for _, relativePath := range homeCacheRelativePaths {
		roots = append(roots, filepath.Join(home, filepath.FromSlash(relativePath)))
	}
	return roots
}

// Outcome reports how a sandboxed command finished.
type Outcome struct {
	ExitCode int
	Elapsed  time.Duration
	TimedOut bool
	// Applied names the confinement rick actually obtained, for example
	// "job object + write-restricted token" or "none (static analysis only)".
	Applied string
	Err     error
}

// Backend is the platform-specific confinement implementation.
//
// The two-phase shape exists because Windows can only assign a process to a
// job object after CreateProcess returns, which means the child must be
// started suspended and resumed once the fence is in place.
type Backend interface {
	// Name identifies the backend in status output.
	Name() string
	// Available reports whether this backend can confine on this machine.
	Available() bool
	// Prepare mutates cmd before it starts and returns the live session.
	Prepare(cmd *exec.Cmd, p Policy) (Session, error)
}

// Session is one backend's per-command state.
type Session interface {
	// AfterStart runs once the child exists but before it is allowed to make
	// progress. Backends that start the child suspended resume it here.
	AfterStart(cmd *exec.Cmd) error
	// Applied describes the confinement actually in force.
	Applied() string
	// Close releases the session, killing any survivors in the process tree.
	Close()
}

// backendFor returns the strongest backend available for this platform.
// Assigned in exec_windows.go / exec_linux.go / exec_darwin.go.
var backendFor = func(p Policy) Backend { return nopBackend{} }

// nopBackend applies nothing, so unsupported platforms degrade to static
// analysis instead of failing.
type nopBackend struct{}

func (nopBackend) Name() string                               { return "none" }
func (nopBackend) Available() bool                            { return false }
func (nopBackend) Prepare(*exec.Cmd, Policy) (Session, error) { return nopSession{}, nil }

type nopSession struct{}

func (nopSession) AfterStart(*exec.Cmd) error { return nil }
func (nopSession) Applied() string            { return "none (static analysis only)" }
func (nopSession) Close()                     {}
func (nopSession) Reaped()                    {}

// Available reports whether OS-level confinement can be applied here.
func Available(p Policy) bool { return backendFor(p).Available() }

// BackendName names the confinement backend for this platform.
func BackendName(p Policy) string { return backendFor(p).Name() }

// Run executes a command under the policy.
//
// Static analysis runs first and short-circuits with a violation error. Then
// the platform backend fences the process. When no backend is available the
// behaviour depends on Enforcement: EnforceOS refuses to run, EnforceAuto
// proceeds and records the downgrade in Outcome.Applied.
func Run(ctx context.Context, p Policy, spec Spec) Outcome {
	analysisPolicy := p
	if spec.Dir != "" {
		absDir, err := filepath.Abs(spec.Dir)
		if err != nil {
			return Outcome{ExitCode: -1, Applied: "blocked by policy", Err: err}
		}
		if p.Confined() && p.Workspace != "" && !under(p.Workspace, absDir) {
			return Outcome{ExitCode: -1, Applied: "blocked by policy", Err: fmt.Errorf("sandbox: working directory %s is outside the workspace", absDir)}
		}
		analysisPolicy.Workspace = absDir
	}
	if violations := Analyze(analysisPolicy, spec.Command); len(violations) > 0 {
		return Outcome{ExitCode: -1, Applied: "blocked by policy", Err: violationError(violations)}
	}

	backend := backendFor(p)
	confine := p.Confined() && p.Enforcement != EnforceStatic
	if confine && p.Enforcement == EnforceOS && !backend.Available() {
		return Outcome{ExitCode: -1, Applied: "none",
			Err: fmt.Errorf("%w: sandbox.enforcement is %q but %s offers no usable backend",
				ErrUnavailable, EnforceOS, runtime.GOOS)}
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, spec.Shell, append(spec.Prefix, spec.Command)...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr

	var session Session = nopSession{}
	if confine && backend.Available() {
		s, err := backend.Prepare(cmd, p)
		switch {
		case err != nil && p.Enforcement == EnforceOS:
			return Outcome{ExitCode: -1, Applied: "none",
				Err: fmt.Errorf("could not apply the %s sandbox: %w", backend.Name(), err)}
		case err == nil:
			session = s
		}
	}
	defer session.Close()
	// Snapshot the description before Close can race with the reaper below.
	applied := session.Applied()

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Outcome{ExitCode: -1, Applied: applied, Elapsed: time.Since(start), Err: err}
	}
	if err := session.AfterStart(cmd); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return Outcome{ExitCode: -1, Applied: applied, Elapsed: time.Since(start),
			Err: fmt.Errorf("could not confine the running command: %w", err)}
	}

	// Tear the sandbox down the moment the deadline fires rather than waiting
	// for Wait to return. A command that backgrounds a child (`start /b`, `&`)
	// hands it the stdout pipe, and Wait blocks until every holder of that
	// handle exits — so killing only the direct child would hang here forever.
	// Closing the job object / process group kills the whole tree at once,
	// which releases the pipe and lets Wait return.
	reaped := make(chan struct{})
	go func() {
		select {
		case <-runCtx.Done():
			session.Close()
		case <-reaped:
		}
	}()

	err := cmd.Wait()
	if reaper, ok := session.(interface{ Reaped() }); ok {
		reaper.Reaped()
	}
	close(reaped)
	elapsed := time.Since(start)

	out := Outcome{Elapsed: elapsed, Applied: applied}
	switch {
	case runCtx.Err() == context.DeadlineExceeded:
		out.TimedOut = true
		out.ExitCode = -1
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out.ExitCode = ee.ExitCode()
		} else {
			out.ExitCode = -1
			out.Err = err
		}
	}
	return out
}

// violationError folds multiple violations into one readable error.
func violationError(vs []Violation) error {
	if len(vs) == 1 {
		return fmt.Errorf("sandbox: %s", vs[0].Error())
	}
	var b strings.Builder
	b.WriteString("sandbox: the command violates the active policy:")
	for _, v := range vs {
		b.WriteString("\n  - " + v.Error())
	}
	return errors.New(b.String())
}

// Violations re-exports analysis so callers can inspect a command without
// running it; the permission prompt uses this to warn before approval.
func Violations(p Policy, command string) []Violation { return Analyze(p, command) }
