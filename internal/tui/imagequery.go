package tui

import (
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// Querying the terminal for graphics support.
//
// Environment detection alone is not enough: launching rick through cmd.exe,
// a wrapper script, ssh or a task runner drops WEZTERM_PANE and friends, so a
// perfectly capable terminal looks like a dumb one. That is exactly what
// happened in WezTerm on Windows, where the shortcut runs cmd.exe.
//
// So when the environment is silent we ask the terminal itself. Both protocols
// have a probe that a supporting terminal answers and others ignore:
//
//	kitty   \e_Gi=31,s=1,v=1,a=q,t=d,f=24;<pixel>\e\\   ->  \e_Gi=31;OK\e\\
//	iTerm2  \e[>q  (XTVERSION)                          ->  \eP>|WezTerm ...
//
// Both are sent together with a Primary Device Attributes request appended.
// Every terminal answers DA1, so it acts as a fence: once DA1 arrives we know
// any graphics reply that was coming has already been received, and we can
// stop waiting instead of blocking on a timeout.
//
// This runs BEFORE bubbletea starts, so nothing is competing for stdin.

// ProbeSequence is the escape sent to the terminal (test helper).
func ProbeSequence() string { return probeSeq }

// QueryImageProto is the query-only verdict (test helper).
func QueryImageProto() string { return queryImageProto().String() }

// QueryRaw returns the terminal's raw reply to the probe (test helper).
func QueryRaw() string { return probeTerminal() }

// probeSeq asks about kitty graphics, then the terminal's name, then DA1 as
// a fence that every terminal answers.
const probeSeq = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\" + "\x1b[>q" + "\x1b[c"

// probeBudget caps how long startup may wait for a terminal that never
// answers. Terminals that do reply are far quicker, because DA1 ends the wait.
const probeBudget = 400 * time.Millisecond

// probeTerminal sends the probe and returns whatever the terminal said, or ""
// when stdin is not a terminal.
func probeTerminal() string {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return ""
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return ""
	}
	defer func() { _ = term.Restore(fd, state) }()

	if _, err := os.Stdout.WriteString(probeSeq); err != nil {
		return ""
	}
	return readReply(probeBudget)
}

// queryImageProto asks the terminal what it supports, returning imageNone if
// it says nothing useful or is not a terminal at all.
func queryImageProto() imageProto {
	reply := probeTerminal()
	switch {
	case strings.Contains(reply, "_Gi=31;OK"):
		// Answering the kitty query is proof; WezTerm and Ghostty land here.
		return imageKitty
	case containsAny(reply, "iTerm2", "mintty"):
		// XTVERSION named a terminal that does inline images but not kitty.
		return imageITerm
	}
	return imageNone
}

// readReply collects bytes until the DA1 answer arrives or time runs out.
//
// DA1 ends with 'c' and is emitted last, so its arrival proves every earlier
// reply has already been read — no fixed sleep needed.
//
// os.Stdin has no read deadline on Windows, so the read runs on its own
// goroutine and is simply abandoned if the terminal never answers. It is
// blocked on a pipe that will be restored to cooked mode; the next keystroke
// releases it. Leaking one goroutine in that case is preferable to hanging
// the whole startup.
func readReply(budget time.Duration) string {
	type chunk struct {
		data []byte
		err  error
	}
	ch := make(chan chunk, 8)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			cp := make([]byte, n)
			copy(cp, buf[:n])
			ch <- chunk{cp, err}
			if err != nil {
				return
			}
		}
	}()

	var b strings.Builder
	timeout := time.After(budget)
	for {
		select {
		case c := <-ch:
			b.Write(c.data)
			// DA1 looks like \e[?1;2c or \e[?62;…c — a final 'c' after "\e[?".
			if s := b.String(); strings.Contains(s, "\x1b[?") && strings.HasSuffix(s, "c") {
				return s
			}
			if c.err != nil {
				return b.String()
			}
		case <-timeout:
			return b.String()
		}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
