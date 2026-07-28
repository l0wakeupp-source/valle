//go:build windows

package tui

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procDragQueryFileW             = shell32.NewProc("DragQueryFileW")
	procGetAsyncKeyState           = user32.NewProc("GetAsyncKeyState")
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	procGetConsoleWindow           = kernel32.NewProc("GetConsoleWindow")
	procCreateToolhelp32Snapshot   = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW            = kernel32.NewProc("Process32FirstW")
	procProcess32NextW             = kernel32.NewProc("Process32NextW")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
)

const (
	CF_DIB     = 8
	CF_HDROP   = 15
	VK_CONTROL = 0x11
	VK_V       = 0x56

	th32csSnapProcess = 0x00000002
	invalidHandle     = ^uintptr(0)
	maxAncestorWalk   = 16 // guards against PID-reuse cycles
)

type processEntry32 struct {
	size            uint32
	usage           uint32
	processID       uint32
	defaultHeapID   uintptr
	moduleID        uint32
	threads         uint32
	parentProcessID uint32
	priClassBase    int32
	flags           uint32
	exeFile         [260]uint16
}

// ancestorStops are processes the ancestor walk must never claim as "us".
// The chain from rick reaches the terminal host (WindowsTerminal.exe,
// OpenConsole.exe, conhost.exe) and then continues into the desktop shell that
// launched it. Including explorer.exe would make every File Explorer window —
// and the desktop itself — count as our terminal, reopening the bug.
var ancestorStops = map[string]bool{
	"explorer.exe":      true,
	"userinit.exe":      true,
	"winlogon.exe":      true,
	"wininit.exe":       true,
	"services.exe":      true,
	"svchost.exe":       true,
	"runtimebroker.exe": true,
}

// ownPIDs is the set of process ids whose window counts as "us": this process
// plus its ancestors up to (but excluding) the desktop shell, since the visible
// window belongs to the terminal host (WindowsTerminal.exe, conhost.exe, ...)
// rather than to rick itself.
//
// Process ancestry is fixed for our lifetime, so this is computed once —
// taking a toolhelp snapshot on every 20ms poll would be far too costly.
var ownPIDs = sync.OnceValue(func() map[uint32]bool {
	self := uint32(os.Getpid())
	pids := map[uint32]bool{self: true}

	snap, _, _ := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == 0 || snap == invalidHandle {
		return pids
	}
	defer procCloseHandle.Call(snap)

	// Build the child -> parent and pid -> name maps once, then walk up.
	parent := map[uint32]uint32{}
	name := map[uint32]string{}
	var e processEntry32
	e.size = uint32(unsafe.Sizeof(e))
	ok, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&e)))
	for ok != 0 {
		parent[e.processID] = e.parentProcessID
		name[e.processID] = strings.ToLower(syscall.UTF16ToString(e.exeFile[:]))
		ok, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&e)))
	}

	for pid, depth := self, 0; depth < maxAncestorWalk; depth++ {
		next, found := parent[pid]
		if !found || next == 0 || pids[next] || ancestorStops[name[next]] {
			break
		}
		pids[next] = true
		pid = next
	}
	return pids
})

// ownsWindow decides whether a foreground window belongs to us, given its
// handle, our console handle and its owning pid. Split out from
// terminalHasFocus so both outcomes are unit-testable without stealing focus.
func ownsWindow(hwnd, console uintptr, pid uint32) bool {
	if hwnd == 0 {
		return false
	}
	// Classic conhost: our console window is itself the foreground window.
	if console != 0 && console == hwnd {
		return true
	}
	return pid != 0 && ownPIDs()[pid]
}

// terminalHasFocus reports whether the foreground window belongs to our
// terminal. GetAsyncKeyState is global — without this check a Ctrl+V pressed
// in any other application would paste into the TUI.
func terminalHasFocus() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	console, _, _ := procGetConsoleWindow.Call()
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return ownsWindow(hwnd, console, pid)
}

func clipboardShortcutSupported() bool { return true }

// clipboardShortcutDown reports whether Ctrl+V is currently held *and* our
// terminal owns the foreground window.
//
// GetAsyncKeyState reads physical key state process-wide, so the focus check
// is mandatory: m.focused alone is not enough, because it depends on terminal
// focus reporting that many Windows terminals never emit (leaving it stuck at
// its initial true).
func clipboardShortcutDown() bool {
	if !terminalHasFocus() {
		return false
	}
	ctrl, _, _ := procGetAsyncKeyState.Call(VK_CONTROL)
	v, _, _ := procGetAsyncKeyState.Call(VK_V)
	return ctrl&0x8000 != 0 && v&0x8000 != 0
}

// readClipboardImage reads an image from the Windows clipboard and saves it as PNG.
// Returns the path to the saved file or an error.
func readClipboardImage() (string, error) {
	ret, _, _ := procOpenClipboard.Call(0)
	if ret == 0 {
		return "", fmt.Errorf("failed to open clipboard")
	}
	defer procCloseClipboard.Call()

	// Try CF_DIB first (most common for copied images)
	ret, _, _ = procIsClipboardFormatAvailable.Call(uintptr(CF_DIB))
	if ret == 0 {
		return "", fmt.Errorf("no image in clipboard")
	}

	h, _, _ := procGetClipboardData.Call(uintptr(CF_DIB))
	if h == 0 {
		return "", fmt.Errorf("failed to get clipboard data")
	}

	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", fmt.Errorf("failed to lock memory")
	}
	defer procGlobalUnlock.Call(h)

	size, _, _ := procGlobalSize.Call(h)
	data := make([]byte, size)
	for i := uintptr(0); i < size; i++ {
		data[i] = *(*byte)(unsafe.Pointer(p + i))
	}

	return dibToPNG(data)
}

// readClipboardFiles reads file paths from the Windows clipboard (CF_HDROP).
// This happens when you copy files in Explorer and paste.
func readClipboardFiles() ([]string, error) {
	ret, _, _ := procOpenClipboard.Call(0)
	if ret == 0 {
		return nil, fmt.Errorf("failed to open clipboard")
	}
	defer procCloseClipboard.Call()

	ret, _, _ = procIsClipboardFormatAvailable.Call(uintptr(CF_HDROP))
	if ret == 0 {
		return nil, fmt.Errorf("no files in clipboard")
	}

	h, _, _ := procGetClipboardData.Call(uintptr(CF_HDROP))
	if h == 0 {
		return nil, fmt.Errorf("failed to get clipboard data")
	}

	// DragQueryFileW with 0xFFFFFFFF returns the count
	count, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil, fmt.Errorf("no files in clipboard")
	}

	var files []string
	buf := make([]uint16, 32768)
	for i := uint32(0); i < uint32(count); i++ {
		procDragQueryFileW.Call(h, uintptr(i), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		path := syscall.UTF16ToString(buf)
		if path != "" {
			files = append(files, path)
		}
	}

	return files, nil
}

// dibToPNG converts a DIB (Device-Independent Bitmap) to PNG and saves it.
func dibToPNG(dibData []byte) (string, error) {
	if len(dibData) < 40 {
		return "", fmt.Errorf("DIB too small")
	}

	// Parse BITMAPINFOHEADER
	biSize := binary.LittleEndian.Uint32(dibData[0:4])
	biWidth := int32(binary.LittleEndian.Uint32(dibData[4:8]))
	biHeight := int32(binary.LittleEndian.Uint32(dibData[8:12]))
	biPlanes := binary.LittleEndian.Uint16(dibData[12:14])
	biBitCount := binary.LittleEndian.Uint16(dibData[14:16])
	biCompression := binary.LittleEndian.Uint32(dibData[16:20])

	if biPlanes != 1 {
		return "", fmt.Errorf("unsupported planes: %d", biPlanes)
	}

	// Handle top-down DIB (negative height)
	topDown := false
	if biHeight < 0 {
		topDown = true
		biHeight = -biHeight
	}

	if biBitCount != 24 && biBitCount != 32 {
		return "", fmt.Errorf("unsupported bit depth: %d", biBitCount)
	}

	if biCompression != 0 {
		// BI_BITFIELDS = 3, try to handle it
		if biCompression != 3 {
			return "", fmt.Errorf("compressed DIB not supported: %d", biCompression)
		}
	}

	// Create image
	img := image.NewRGBA(image.Rect(0, 0, int(biWidth), int(biHeight)))

	// Pixel data offset = header size + optional color table
	pixelOffset := int(biSize)
	if biBitCount <= 8 {
		// Color table
		numColors := binary.LittleEndian.Uint32(dibData[32:36])
		if numColors == 0 {
			numColors = 1 << biBitCount
		}
		pixelOffset += int(numColors) * 4
	}

	// Row size is padded to 4 bytes
	rowSize := ((int(biWidth) * int(biBitCount)) + 31) / 32 * 4

	for y := 0; y < int(biHeight); y++ {
		srcY := y
		if !topDown {
			srcY = int(biHeight) - 1 - y
		}
		srcRow := dibData[pixelOffset+srcY*rowSize:]
		for x := 0; x < int(biWidth); x++ {
			var c color.RGBA
			if biBitCount == 24 {
				c.B = srcRow[x*3]
				c.G = srcRow[x*3+1]
				c.R = srcRow[x*3+2]
				c.A = 255
			} else { // 32
				c.B = srcRow[x*4]
				c.G = srcRow[x*4+1]
				c.R = srcRow[x*4+2]
				c.A = srcRow[x*4+3]
			}
			img.Set(x, y, c)
		}
	}

	// Save as PNG
	tmpDir := os.TempDir()
	filename := filepath.Join(tmpDir, fmt.Sprintf("rick-clip-%d.png", time.Now().UnixNano()))
	f, err := os.Create(filename)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		return "", fmt.Errorf("encode PNG: %w", err)
	}

	return filename, nil
}

// ensureOpaque ensures the image is fully opaque (for formats without alpha).
func ensureOpaque(img image.Image) image.Image {
	bounds := img.Bounds()
	opaque := image.NewRGBA(bounds)
	draw.Draw(opaque, bounds, img, bounds.Min, draw.Src)
	return opaque
}
