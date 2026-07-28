//go:build !windows

package tui

import "fmt"

func clipboardShortcutSupported() bool { return false }

func clipboardShortcutDown() bool { return false }

// readClipboardImage is a stub for non-Windows platforms.
func readClipboardImage() (string, error) {
	return "", fmt.Errorf("clipboard image paste not supported on this platform")
}

// readClipboardFiles is a stub for non-Windows platforms.
func readClipboardFiles() ([]string, error) {
	return nil, fmt.Errorf("clipboard file paste not supported on this platform")
}
