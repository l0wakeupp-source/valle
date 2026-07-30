package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "rick-tui-tests-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tempDir)

	originalHome, hadHome := os.LookupEnv("RICK_HOME")
	originalData, hadData := os.LookupEnv("RICK_DATA")
	if err := os.Setenv("RICK_HOME", filepath.Join(tempDir, "config")); err != nil {
		panic(err)
	}
	if err := os.Setenv("RICK_DATA", filepath.Join(tempDir, "data")); err != nil {
		panic(err)
	}
	defer func() {
		if hadHome {
			_ = os.Setenv("RICK_HOME", originalHome)
		} else {
			_ = os.Unsetenv("RICK_HOME")
		}
		if hadData {
			_ = os.Setenv("RICK_DATA", originalData)
		} else {
			_ = os.Unsetenv("RICK_DATA")
		}
	}()

	os.Exit(m.Run())
}
