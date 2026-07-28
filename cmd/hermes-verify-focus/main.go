//go:build windows

// Live proof of the paste gate: synthesises a real Ctrl+V into ANOTHER
// application's window and asserts the TUI's predicate stays closed.
package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	getForegroundWindow      = user32.NewProc("GetForegroundWindow")
	getWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	getWindowTextW           = user32.NewProc("GetWindowTextW")
	getAsyncKeyState         = user32.NewProc("GetAsyncKeyState")
	keybdEvent               = user32.NewProc("keybd_event")
	getConsoleWindow         = kernel32.NewProc("GetConsoleWindow")
	createSnapshot           = kernel32.NewProc("CreateToolhelp32Snapshot")
	process32First           = kernel32.NewProc("Process32FirstW")
	process32Next            = kernel32.NewProc("Process32NextW")
	closeHandle              = kernel32.NewProc("CloseHandle")
)

type pe32 struct {
	size, usage, pid uint32
	heap             uintptr
	mod, threads     uint32
	ppid             uint32
	pri              int32
	flags            uint32
	exe              [260]uint16
}

var stops = map[string]bool{"explorer.exe": true, "userinit.exe": true, "winlogon.exe": true,
	"wininit.exe": true, "services.exe": true, "svchost.exe": true, "runtimebroker.exe": true}

func ownPIDs() (map[uint32]bool, map[uint32]string) {
	self := uint32(os.Getpid())
	pids := map[uint32]bool{self: true}
	snap, _, _ := createSnapshot.Call(0x2, 0)
	if snap == 0 || snap == ^uintptr(0) {
		return pids, nil
	}
	defer closeHandle.Call(snap)
	parent, name := map[uint32]uint32{}, map[uint32]string{}
	var e pe32
	e.size = uint32(unsafe.Sizeof(e))
	ok, _, _ := process32First.Call(snap, uintptr(unsafe.Pointer(&e)))
	for ok != 0 {
		parent[e.pid] = e.ppid
		name[e.pid] = strings.ToLower(syscall.UTF16ToString(e.exe[:]))
		ok, _, _ = process32Next.Call(snap, uintptr(unsafe.Pointer(&e)))
	}
	for pid, d := self, 0; d < 16; d++ {
		nx, found := parent[pid]
		if !found || nx == 0 || pids[nx] || stops[name[nx]] {
			break
		}
		pids[nx] = true
		pid = nx
	}
	return pids, name
}

// mirrors terminalHasFocus in internal/tui/clipboard_win.go
func hasFocus(own map[uint32]bool) bool {
	h, _, _ := getForegroundWindow.Call()
	if h == 0 {
		return false
	}
	if c, _, _ := getConsoleWindow.Call(); c != 0 && c == h {
		return true
	}
	var pid uint32
	getWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	return pid != 0 && own[pid]
}

func fg(names map[uint32]string) (string, string) {
	h, _, _ := getForegroundWindow.Call()
	buf := make([]uint16, 256)
	getWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	var pid uint32
	getWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	t := syscall.UTF16ToString(buf)
	if len(t) > 40 {
		t = t[:40] + "…"
	}
	return t, names[pid]
}

// wouldPaste is the exact composite condition guarding handleClipboardPaste.
func wouldPaste(own map[uint32]bool) bool {
	if !hasFocus(own) {
		return false
	}
	ctrl, _, _ := getAsyncKeyState.Call(0x11)
	v, _, _ := getAsyncKeyState.Call(0x56)
	return ctrl&0x8000 != 0 && v&0x8000 != 0
}

func pressCtrlV() {
	const keyUp = 0x0002
	keybdEvent.Call(0x11, 0, 0, 0) // ctrl down
	keybdEvent.Call(0x56, 0, 0, 0) // v down
	time.Sleep(120 * time.Millisecond)
	keybdEvent.Call(0x56, 0, keyUp, 0)
	keybdEvent.Call(0x11, 0, keyUp, 0)
}

func main() {
	own, names := ownPIDs()
	fail := 0

	title, proc := fg(names)
	fmt.Printf("foreground: %-16s %q\n", proc, title)
	fmt.Printf("hasFocus()  = %v\n\n", hasFocus(own))

	if hasFocus(own) {
		// Positive case: our own console owns the foreground window, so a
		// Ctrl+V here MUST be allowed through -- otherwise the fix would have
		// broken the feature it was guarding.
		fmt.Println("This console is focused -- checking the gate still ALLOWS pasting.")
		allowed := false
		fin := make(chan struct{})
		go func() {
			for i := 0; i < 40; i++ {
				if wouldPaste(own) {
					allowed = true
				}
				time.Sleep(20 * time.Millisecond)
			}
			close(fin)
		}()
		time.Sleep(100 * time.Millisecond)
		pressCtrlV()
		<-fin
		fmt.Printf("  gate would paste while focused : %v  (must be true)\n\n", allowed)
		if allowed {
			fmt.Println("PASS: paste still works when the terminal IS focused.")
			os.Exit(0)
		}
		fmt.Println("FAIL: gate blocks paste even when focused -- feature broken.")
		os.Exit(1)
	}

	// A real Ctrl+V, delivered to whatever app the user currently has focused.
	fmt.Println("Synthesising a REAL Ctrl+V into the focused (foreign) window...")
	sawKeys, pasted := false, false
	done := make(chan struct{})
	go func() { // poll like the TUI does, at 20ms
		for i := 0; i < 40; i++ {
			c, _, _ := getAsyncKeyState.Call(0x11)
			v, _, _ := getAsyncKeyState.Call(0x56)
			if c&0x8000 != 0 && v&0x8000 != 0 {
				sawKeys = true
			}
			if wouldPaste(own) {
				pasted = true
			}
			time.Sleep(20 * time.Millisecond)
		}
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	pressCtrlV()
	<-done

	fmt.Printf("  raw GetAsyncKeyState saw Ctrl+V : %v  (proves the keys really fired)\n", sawKeys)
	fmt.Printf("  gate would have pasted          : %v  (must be false)\n\n", pasted)

	if !sawKeys {
		fmt.Println("INCONCLUSIVE: synthetic keys were not observed at all.")
		fail++
	} else if pasted {
		fmt.Println("FAIL: Ctrl+V in a foreign window would still paste into the TUI.")
		fail++
	} else {
		fmt.Println("PASS: keys fired globally, but the focus gate suppressed the paste.")
	}
	os.Exit(fail)
}
