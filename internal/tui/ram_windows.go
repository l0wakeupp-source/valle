//go:build windows

package tui

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

func currentProcessRAM() (uint64, error) {
	counters := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	proc := windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	r1, _, callErr := proc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&counters)), uintptr(counters.cb))
	if r1 == 0 {
		return 0, fmt.Errorf("GetProcessMemoryInfo: %w", callErr)
	}
	return uint64(counters.workingSetSize), nil
}
