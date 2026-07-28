package maintenance

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const rawBase = "https://raw.githubusercontent.com/rick-cli/rick/main/scripts/"

// Update downloads and runs the platform-specific updater from the repository.
func Update(stdout, stderr io.Writer) error {
	return runScript("update.sh", "Update-Rick.ps1", nil, stdout, stderr)
}

// Uninstall asks for a removal scope, then runs the matching platform script.
func Uninstall(stdin io.Reader, stdout, stderr io.Writer) error {
	fmt.Fprintln(stdout, "Choose uninstall mode:")
	fmt.Fprintln(stdout, "1) FULL Removal - Rick plus credentials, sessions, config, and user data")
	fmt.Fprintln(stdout, "2) PART Removal - Rick executable only; keep credentials, sessions, config, and user data")
	fmt.Fprint(stdout, "Selection [1/2]: ")

	reader := bufio.NewReader(stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && len(answer) == 0 {
		return err
	}
	mode := strings.TrimSpace(strings.ToLower(answer))
	switch mode {
	case "1", "full":
		mode = "full"
	case "2", "part":
		mode = "part"
	default:
		return fmt.Errorf("invalid selection %q; choose 1 or 2", mode)
	}

	fmt.Fprintf(stdout, "Selected %s removal. Continue? [y/N]: ", strings.ToUpper(mode))
	confirm, err := reader.ReadString('\n')
	if err != nil && len(confirm) == 0 {
		return err
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(confirm)), "y") {
		fmt.Fprintln(stdout, "Uninstall cancelled.")
		return nil
	}
	return runScript("uninstall.sh", "Uninstall-Rick.ps1", []string{mode}, stdout, stderr)
}

// UninstallMode is the non-interactive path used by slash-command integrations.
func UninstallMode(mode string, stdout, stderr io.Writer) error {
	if mode != "full" && mode != "part" {
		return fmt.Errorf("invalid uninstall mode %q", mode)
	}
	return runScript("uninstall.sh", "Uninstall-Rick.ps1", []string{mode}, stdout, stderr)
}

func runScript(unixName, windowsName string, args []string, stdout, stderr io.Writer) error {
	name := unixName
	command := "sh"
	commandArgs := []string{}
	if runtime.GOOS == "windows" {
		name = windowsName
		command = "powershell.exe"
		commandArgs = []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}
	}

	response, err := http.Get(rawBase + name)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %s", name, response.Status)
	}

	tempDir := os.TempDir()
	tempFile, err := os.CreateTemp(tempDir, "rick-maintenance-*.ps1")
	if err != nil {
		return fmt.Errorf("create temporary maintenance script: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(tempFile, response.Body); err != nil {
		tempFile.Close()
		return fmt.Errorf("save %s: %w", name, err)
	}
	if err := tempFile.Close(); err != nil {
		return err
	}

	commandArgs = append(commandArgs, tempPath)
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command(command, commandArgs...)
	cmd.Stdout = stdout
	var scriptStderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(stderr, &scriptStderr)
	cmd.Env = append(os.Environ(), "RICK_TARGET="+currentExecutable())
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(scriptStderr.String()); message != "" {
			return fmt.Errorf("run %s: %w: %s", name, err, message)
		}
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}

func currentExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return filepath.Join(".", "rick")
	}
	return exe
}
