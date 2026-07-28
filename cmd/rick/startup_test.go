package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildDepsKeepsStartupResponsiveWithMCPConfigured(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	project := t.TempDir()
	t.Setenv("RICK_HOME", home)
	t.Setenv("RICK_DATA", data)

	configJSON := `{"mcp":{"slow":{"type":"remote","url":"http://127.0.0.1:1"}}}`
	if err := os.WriteFile(filepath.Join(project, "rick.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	deps, err := buildDeps(project, opts{})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	deps.MCP.Close()
	if elapsed >= time.Second {
		t.Fatalf("buildDeps took %s; startup work must not wait on terminal probes or MCP", elapsed)
	}
}
