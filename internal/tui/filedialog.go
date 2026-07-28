package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// openFileDialog shows a native file dialog and returns the selected path.
// It blocks the TUI briefly, which is acceptable for a one-shot picker.
func openFileDialog(filter string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		return windowsFileDialog(filter)
	case "darwin":
		return macFileDialog(filter)
	default:
		return linuxFileDialog(filter)
	}
}

func windowsFileDialog(filter string) (string, error) {
	// Build a PowerShell filter string from a space-separated glob list.
	// e.g. "*.rick *.json" → "Theme files (*.rick;*.json)|*.rick;*.json"
	parts := strings.Fields(filter)
	semis := strings.Join(parts, ";")
	psFilter := fmt.Sprintf("Files (%s)|%s", semis, semis)
	script := fmt.Sprintf(
		`Add-Type -AssemblyName System.Windows.Forms; `+
			`$f = New-Object System.Windows.Forms.OpenFileDialog; `+
			`$f.Filter = '%s'; `+
			`if($f.ShowDialog() -eq 'OK'){$f.FileName}`,
		psFilter,
	)
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return "", fmt.Errorf("file dialog: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("no file selected")
	}
	return path, nil
}

func macFileDialog(filter string) (string, error) {
	// Build an AppleScript type list from globs like "*.md" → "md".
	var types []string
	for _, g := range strings.Fields(filter) {
		ext := strings.TrimPrefix(g, "*.")
		types = append(types, fmt.Sprintf("%q", ext))
	}
	typeList := "{" + strings.Join(types, ",") + "}"
	script := fmt.Sprintf("POSIX path of (choose file with prompt \"Select file\" of type %s)", typeList)
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", fmt.Errorf("file dialog: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("no file selected")
	}
	return path, nil
}

func linuxFileDialog(filter string) (string, error) {
	// Try zenity first, fall back to kdialog.
	if _, err := exec.LookPath("zenity"); err == nil {
		args := []string{"--file-selection"}
		for _, g := range strings.Fields(filter) {
			args = append(args, "--file-filter="+g)
		}
		out, err := exec.Command("zenity", args...).Output()
		if err != nil {
			return "", fmt.Errorf("file dialog: %w", err)
		}
		path := strings.TrimSpace(string(out))
		if path == "" {
			return "", fmt.Errorf("no file selected")
		}
		return path, nil
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		out, err := exec.Command("kdialog", "--getopenfilename", ".", filter).Output()
		if err != nil {
			return "", fmt.Errorf("file dialog: %w", err)
		}
		path := strings.TrimSpace(string(out))
		if path == "" {
			return "", fmt.Errorf("no file selected")
		}
		return path, nil
	}
	return "", fmt.Errorf("no file dialog available (install zenity or kdialog)")
}

// openInExplorer opens the containing folder of path in the OS file explorer.
func openInExplorer(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", "/select,"+path).Run()
	case "darwin":
		return exec.Command("open", "-R", path).Run()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Run()
	}
}

// openURL opens a URL in the default browser.
func openURL(rawURL string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Run()
	case "darwin":
		return exec.Command("open", rawURL).Run()
	default:
		return exec.Command("xdg-open", rawURL).Run()
	}
}
