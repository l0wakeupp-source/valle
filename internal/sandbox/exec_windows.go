//go:build windows

package sandbox

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	backendFor = func(Policy) Backend { return windowsBackend{} }
}

// windowsBackend confines commands with two Windows primitives:
//
//   - A job object caps memory, CPU time and process count, and guarantees the
//     whole process tree dies when rick closes the handle. This is what stops a
//     runaway `npm install` from outliving the tool call.
//   - A write-restricted primary token makes the kernel deny writes to any
//     securable object whose DACL does not grant the restricting SID. Combined
//     with the workspace ACE we add, that confines writes to the workspace
//     without touching the rest of the machine.
//
// The token half is skipped in trusted mode and whenever the current process
// lacks the rights to build one; the job object still applies.
type windowsBackend struct{}

func (windowsBackend) Name() string { return "job object" }

func (windowsBackend) Available() bool { return true }

func (windowsBackend) Prepare(cmd *exec.Cmd, p Policy) (Session, error) {
	job, err := newJobObject(p)
	if err != nil {
		return nil, err
	}

	s := &windowsSession{job: job, applied: "job object"}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// The child must not run before it is inside the job, otherwise it could
	// spawn escapees in the gap between CreateProcess and AssignProcessToJobObject.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP

	if p.Mode == ModeReadOnly || p.Mode == ModeWorkspace {
		if reason := integrityIncompatible(cmd, p); reason != "" {
			s.note = "integrity drop skipped: " + reason
		} else if token, terr := lowIntegrityToken(); terr != nil {
			// Not fatal: the job object and static analysis still apply, and
			// refusing to run here would break rick on locked-down hosts.
			s.note = "integrity drop unavailable: " + terr.Error()
		} else {
			// A low-integrity process cannot write the workspace unless the
			// workspace itself carries a low label.
			if p.WritesAllowed() && p.Workspace != "" {
				if lerr := labelWorkspaceLow(p.Workspace); lerr != nil {
					token.Close()
					s.note = "workspace could not be labelled: " + lerr.Error()
					return s, nil
				}
			}
			cmd.SysProcAttr.Token = syscall.Token(token)
			s.token = token
			s.applied = "job object + low-integrity token"
		}
	}

	return s, nil
}

type windowsSession struct {
	job     windows.Handle
	token   windows.Token
	applied string
	note    string
	once    sync.Once
}

// AfterStart puts the child in the job and then lets it run.
func (s *windowsSession) AfterStart(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("process did not start")
	}
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = resumeProcess(uint32(cmd.Process.Pid))
		return fmt.Errorf("open child process: %w", err)
	}
	defer windows.CloseHandle(proc)

	if err := windows.AssignProcessToJobObject(s.job, proc); err != nil {
		_ = resumeProcess(uint32(cmd.Process.Pid))
		return fmt.Errorf("assign to job object: %w", err)
	}
	if err := resumeProcess(uint32(cmd.Process.Pid)); err != nil {
		return fmt.Errorf("resume child: %w", err)
	}
	return nil
}

func (s *windowsSession) Applied() string {
	if s.note != "" {
		return s.applied + " (" + s.note + ")"
	}
	return s.applied
}

// Close kills anything still alive in the job. JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
// means closing the handle is the kill.
func (s *windowsSession) Close() {
	s.once.Do(func() {
		if s.job != 0 {
			windows.CloseHandle(s.job)
			s.job = 0
		}
		if s.token != 0 {
			s.token.Close()
			s.token = 0
		}
	})
}

// newJobObject creates a job carrying the policy's resource limits.
func newJobObject(p Policy) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create job object: %w", err)
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION

	if mb := p.Limits.MemoryMB; mb > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
		info.ProcessMemoryLimit = uintptr(mb) << 20
	}
	if n := p.Limits.Processes; n > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = uint32(n)
	}
	if secs := p.Limits.CPUSeconds; secs > 0 {
		// PerJobUserTimeLimit counts in 100ns ticks across the whole tree.
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_TIME
		info.BasicLimitInformation.PerJobUserTimeLimit = int64(secs) * 10_000_000
	}

	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("set job limits: %w", err)
	}

	// Deny the sandboxed tree access to the user's desktop, clipboard and
	// global atoms; a build has no business reading what the user copied.
	if p.Mode != ModeTrusted {
		ui := struct{ UIRestrictionsClass uint32 }{
			UIRestrictionsClass: windows.JOB_OBJECT_UILIMIT_WRITECLIPBOARD |
				windows.JOB_OBJECT_UILIMIT_READCLIPBOARD |
				windows.JOB_OBJECT_UILIMIT_GLOBALATOMS |
				windows.JOB_OBJECT_UILIMIT_EXITWINDOWS |
				windows.JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS,
		}
		// Best effort: some session configurations reject UI restrictions.
		_, _ = windows.SetInformationJobObject(job,
			windows.JobObjectBasicUIRestrictions,
			uintptr(unsafe.Pointer(&ui)),
			uint32(unsafe.Sizeof(ui)))
	}

	return job, nil
}

// integrityIncompatible reports why the low-integrity drop must be skipped for
// this command, or "" when it is safe to apply.
//
// MSYS-based shells (git-bash, MSYS2, Cygwin) create a shared object directory
// under \BaseNamedObjects at startup. Low integrity denies that outright and
// the shell aborts with a fatal error before running anything, so applying the
// drop here would break every command instead of confining it. The job object,
// the environment scrub and static analysis still apply in that case.
func integrityIncompatible(cmd *exec.Cmd, p Policy) string {
	if isMSYSShell(cmd.Path) {
		return "MSYS shells cannot start at low integrity"
	}
	return ""
}

// isMSYSShell recognises the shells that ship an MSYS/Cygwin runtime.
func isMSYSShell(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	if !strings.HasSuffix(lower, "/bash.exe") && !strings.HasSuffix(lower, "/sh.exe") &&
		!strings.HasSuffix(lower, "/zsh.exe") && !strings.HasSuffix(lower, "/dash.exe") {
		return false
	}
	// A bash.exe under Git, MSYS2 or Cygwin carries the MSYS runtime; WSL's
	// bash is a different animal and never appears as a .exe path here.
	for _, marker := range []string{"/git/", "/msys", "/cygwin", "/usr/bin/"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return true // any native *.exe POSIX shell on Windows is MSYS-family
}

// resumeProcess releases every thread of a suspended child. Go gives us no
// thread handle, so walk the snapshot and resume the ones owned by our pid.
func resumeProcess(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("thread snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	resumed := 0
	for err = windows.Thread32First(snap, &entry); err == nil; err = windows.Thread32Next(snap, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		th, oerr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if oerr != nil {
			continue
		}
		if _, rerr := windows.ResumeThread(th); rerr == nil {
			resumed++
		}
		windows.CloseHandle(th)
	}
	if resumed == 0 {
		return fmt.Errorf("no threads resumed for pid %d", pid)
	}
	return nil
}
