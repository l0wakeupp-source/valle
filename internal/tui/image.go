package tui

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// rickPNG is the mascot photo, shown when the terminal supports graphics.
//
//go:embed assets/rick.png
var rickPNG []byte

// Real images in the terminal.
//
// Three incompatible protocols exist and none is universal:
//
//	Kitty graphics  — kitty, ghostty, WezTerm, Konsole
//	iTerm2 inline   — iTerm2, WezTerm, VSCode, Tabby, Rio
//	Sixel           — Windows Terminal 1.22+, mlterm, foot, xterm -ti 340
//
// Everything else — including cmd.exe, most SSH sessions, tmux without
// passthrough, and CI — gets nothing. So a real image can only ever be an
// enhancement layered over the half-block art, never a replacement.
//
// Detection is environment-based rather than by querying the terminal: a
// query means writing an escape and waiting for a reply on stdin, which
// fights bubbletea for the input stream and stalls startup on terminals
// that never answer.

// imageProto is a terminal graphics protocol.
type imageProto int

const (
	imageNone imageProto = iota
	imageKitty
	imageITerm
	imageSixel
)

// parseImageProto is the inverse of imageProto.String.
func parseImageProto(s string) imageProto {
	switch s {
	case "kitty":
		return imageKitty
	case "iterm2":
		return imageITerm
	case "sixel":
		return imageSixel
	}
	return imageNone
}

func (p imageProto) String() string {
	switch p {
	case imageKitty:
		return "kitty"
	case imageITerm:
		return "iterm2"
	case imageSixel:
		return "sixel"
	}
	return "none"
}

// DetectImageSupport resolves the terminal's graphics protocol without
// touching stdin. A terminal query would compete with Bubble Tea for input
// and can block startup when a wrapper or terminal does not answer.
func DetectImageSupport() string { return detectImageSupport().String() }

func detectImageSupport() imageProto {
	if override := parseImageProto(strings.ToLower(strings.TrimSpace(os.Getenv("RICK_IMAGE_PROTO")))); override != imageNone {
		return override
	}
	return detectImageProtoEnv()
}

// detectImageProtoEnv reports the best protocol the current terminal supports
// based solely on environment variables.
func detectImageProtoEnv() imageProto {
	// tmux and screen swallow graphics escapes unless passthrough is on, and
	// getting that wrong corrupts the display. Not worth the risk.
	if os.Getenv("TMUX") != "" || strings.HasPrefix(os.Getenv("TERM"), "screen") {
		return imageNone
	}

	term := strings.ToLower(os.Getenv("TERM"))
	prog := strings.ToLower(os.Getenv("TERM_PROGRAM"))

	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "", strings.Contains(term, "kitty"):
		return imageKitty
	case os.Getenv("GHOSTTY_RESOURCES_DIR") != "", strings.Contains(term, "ghostty"):
		return imageKitty
	case os.Getenv("WEZTERM_PANE") != "", prog == "wezterm":
		// WezTerm speaks both; kitty transfers in chunks and gives exact
		// cell placement, so prefer it.
		return imageKitty
	case prog == "iterm.app", prog == "tabby", prog == "rio":
		return imageITerm
	case os.Getenv("KONSOLE_VERSION") != "":
		return imageKitty
	case os.Getenv("VSCODE_INJECTION") != "" && prog == "vscode":
		return imageITerm
	case os.Getenv("WT_SESSION") != "":
		// Windows Terminal gained Sixel in 1.22. Older builds print the
		// escape as garbage, so require an explicit opt-in.
		if os.Getenv("RICK_SIXEL") != "" {
			return imageSixel
		}
		return imageNone
	case strings.Contains(term, "sixel"), strings.Contains(term, "foot"),
		strings.Contains(term, "mlterm"):
		return imageSixel
	}
	return imageNone
}

// DetectImageProto names the terminal's graphics protocol (test helper).
func DetectImageProto() string { return detectImageProtoEnv().String() }

// RickPNG is the embedded mascot photo (test helper).
func RickPNG() []byte { return rickPNG }

// KittyImage builds a kitty graphics escape (test helper).
func KittyImage(png []byte, cols, rows int) string { return kittyImage(png, cols, rows) }

// ItermImage builds an iTerm2 inline-image escape (test helper).
func ItermImage(png []byte, cols, rows int) string { return itermImage(png, cols, rows) }

// kittyImage encodes PNG bytes as a Kitty graphics escape sized to a cell box.
//
// The image is transmitted in 4KB base64 chunks (the protocol's limit) with
// c/r giving the target size in cells, so the terminal does the scaling.
func kittyImage(png []byte, cols, rows int) string {
	b64 := base64.StdEncoding.EncodeToString(png)
	const chunk = 4096

	var b strings.Builder
	for i := 0; i < len(b64); i += chunk {
		end := i + chunk
		if end > len(b64) {
			end = len(b64)
		}
		more := 0
		if end < len(b64) {
			more = 1
		}
		// Every chunk carries m explicitly: m=1 means "more follows", and the
		// final m=0 is what tells the terminal the transmission is complete.
		// Omitting it on the last chunk leaves kitty waiting for data that
		// never arrives, and nothing is ever drawn.
		if i == 0 {
			// a=T transmit+display, f=100 PNG, C=1 don't move the cursor.
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,C=1,c=%d,r=%d,m=%d;%s\x1b\\",
				cols, rows, more, b64[i:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, b64[i:end])
		}
	}
	return b.String()
}

// itermImage encodes PNG bytes as an iTerm2 inline-image escape.
func itermImage(png []byte, cols, rows int) string {
	return fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1;size=%d:%s\a",
		cols, rows, len(png), base64.StdEncoding.EncodeToString(png))
}
