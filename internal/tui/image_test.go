package tui

import "testing"

func TestGraphicalImageSupportIsDisabled(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("RICK_IMAGE_PROTO", "kitty")

	if got := DetectImageSupport(); got != "none" {
		t.Fatalf("graphics detection: got %q, want none", got)
	}
	if got := DetectImageProto(); got != "none" {
		t.Fatalf("graphics protocol: got %q, want none", got)
	}
	if got := QueryImageProto(); got != "none" {
		t.Fatalf("graphics query: got %q, want none", got)
	}
}

func TestPixelArtDoesNotEmitTerminalGraphicsEscapes(t *testing.T) {
	model := newModelChoiceTestModel()
	model.width = 100
	model.height = 30
	model.viewport.Width = 100
	model.viewport.Height = 20
	model.ForceImageProto("kitty")

	for _, line := range model.SplashArtLines() {
		if line == "" {
			continue
		}
		for _, forbidden := range []string{"\x1b_G", "\x1b]1337", "sixel"} {
			if containsInsensitive(line, forbidden) {
				t.Fatalf("pixel art contains graphical protocol marker %q", forbidden)
			}
		}
	}
}

func containsInsensitive(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		match := true
		for j := range needle {
			left, right := value[i+j], needle[j]
			if left >= 'A' && left <= 'Z' {
				left += 'a' - 'A'
			}
			if right >= 'A' && right <= 'Z' {
				right += 'a' - 'A'
			}
			if left != right {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
