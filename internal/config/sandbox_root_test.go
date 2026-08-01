package config

import (
	"path/filepath"
	"testing"
)

func TestSandboxRootResolvesConfiguredRelativeFence(t *testing.T) {
	project := t.TempDir()
	got := SandboxRoot(&SandboxConfig{Root: "workspace"}, project)
	want := filepath.Join(project, "workspace")
	if got != want {
		t.Fatalf("SandboxRoot() = %q, want %q", got, want)
	}
}
