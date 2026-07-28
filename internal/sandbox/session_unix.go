//go:build linux || darwin

package sandbox

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// unixSession is the shared Linux/macOS session: it caps resources on the
// running child and guarantees the whole process group dies with the tool
// call, so a backgrounded server cannot outlive the command that spawned it.
type unixSession struct {
	applied string
	limits  Limits
	pgid    int
	once    sync.Once
}

func (s *unixSession) AfterStart(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("process did not start")
	}
	s.pgid = cmd.Process.Pid
	// Advisory: namespace or Seatbelt limits may already cover these, and a
	// hardened kernel can refuse prlimit on a child we do not yet own.
	applyResourceLimits(cmd.Process.Pid, s.limits)
	return nil
}

func (s *unixSession) Applied() string {
	if extra := limitSummary(s.limits); extra != "" {
		return s.applied + " · " + extra
	}
	return s.applied
}

func (s *unixSession) Close() {
	s.once.Do(func() {
		if s.pgid > 0 {
			// A negative pid signals the entire process group.
			_ = syscall.Kill(-s.pgid, syscall.SIGKILL)
		}
	})
}

func limitSummary(l Limits) string {
	var parts []string
	if l.MemoryMB > 0 {
		parts = append(parts, "mem "+strconv.Itoa(l.MemoryMB)+"MB")
	}
	if l.CPUSeconds > 0 {
		parts = append(parts, "cpu "+strconv.Itoa(l.CPUSeconds)+"s")
	}
	if l.Processes > 0 {
		parts = append(parts, "procs "+strconv.Itoa(l.Processes))
	}
	return strings.Join(parts, ", ")
}
