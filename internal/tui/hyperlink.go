package tui

import (
    "encoding/base64"
    "fmt"
    "os"
    "regexp"
    "strings"
)

// osc8Supported caches the terminal capability check.
var osc8Checked, osc8OK bool

// HasOSC8 reports whether the terminal likely supports OSC 8 hyperlinks.
func HasOSC8() bool {
    if osc8Checked {
        return osc8OK
    }
    osc8Checked = true
    for _, env := range []string{"WT_SESSION", "KITTY_WINDOW_ID", "ITERM_SESSION_ID", "WEZTERM_PANE", "VTE_VERSION"} {
        if os.Getenv(env) != "" {
            osc8OK = true
            return true
        }
    }
    switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
    case "vscode", "hyper", "contour", "foot":
        osc8OK = true
        return true
    }
    osc8OK = false
    return false
}

// linksEnabled checks the config override and terminal capability.
func (m *Model) linksEnabled() bool {
    if m.deps.Loaded.TUI.Links != nil {
        return *m.deps.Loaded.TUI.Links
    }
    return HasOSC8()
}

// OSC8 wraps text in an OSC 8 hyperlink escape sequence.
func OSC8(linkURL, text string) string {
    return "\x1b]8;;" + linkURL + "\x07" + text + "\x1b]8;;\x07"
}

// FileLink builds a file:// URL for a path with an optional line number.
func FileLink(path string, line int) string {
    p := strings.ReplaceAll(path, "\\", "/")
    if !strings.HasPrefix(p, "/") {
        p = "/" + p
    }
    u := "file://" + p
    if line > 0 {
        u += fmt.Sprintf("#L%d", line)
    }
    return u
}

// URLLink wraps a URL as a clickable OSC 8 link, displaying the URL itself.
func URLLink(url string) string {
    return OSC8(url, url)
}

// linkRe matches URLs and file-path-like tokens in text.
var linkRe = regexp.MustCompile(
    `https?://[^\s<>"')\]]+` +
        `|[A-Za-z]:[\\/](?:[\w.\-]+[\\/])*[\w.\-]+` +
        `|\.{1,2}/(?:[\w.\-]+/)*[\w.\-]+` +
        `|/(?:[\w.\-]+/)+[\w.\-]+`)

// linkifyLine wraps URLs and file paths in a line with OSC 8 sequences.
func linkifyLine(line string, enabled bool) string {
    if !enabled {
        return line
    }
    return linkRe.ReplaceAllStringFunc(line, func(match string) string {
        if strings.HasPrefix(match, "http") {
            return OSC8(match, match)
        }
        if len(match) < 4 {
            return match
        }
        return OSC8(FileLink(match, 0), match)
    })
}

// copyToClipboardOSC52 writes text to the system clipboard via OSC 52.
func copyToClipboardOSC52(text string) {
    encoded := base64.StdEncoding.EncodeToString([]byte(text))
    fmt.Printf("\x1b]52;c;%s\x07", encoded)
}
