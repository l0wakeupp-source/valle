package tui

import (
	"testing"
	"time"
)

func TestDetectImageSupportDoesNotReadTerminalInput(t *testing.T) {
	for _, name := range []string{
		"KITTY_WINDOW_ID",
		"GHOSTTY_RESOURCES_DIR",
		"WEZTERM_PANE",
		"TERM",
		"TERM_PROGRAM",
		"KONSOLE_VERSION",
		"VSCODE_INJECTION",
		"WT_SESSION",
		"TMUX",
		"RICK_SIXEL",
		"RICK_IMAGE_PROTO",
	} {
		t.Setenv(name, "")
	}

	start := time.Now()
	if got := DetectImageSupport(); got != "none" {
		t.Fatalf("unknown terminal: got %q, want none", got)
	}
	if elapsed := time.Since(start); elapsed >= 50*time.Millisecond {
		t.Fatalf("terminal detection took %s; it must not wait on stdin", elapsed)
	}
}

func TestDetectImageSupportHonorsOverride(t *testing.T) {
	t.Setenv("RICK_IMAGE_PROTO", "sixel")
	t.Setenv("TMUX", "ignored because the override is explicit")

	if got := DetectImageSupport(); got != "sixel" {
		t.Fatalf("override: got %q, want sixel", got)
	}
}
