package sandbox

import (
	"path/filepath"
	"testing"

	"rick/internal/config"
)

func TestFromConfigUsesConfiguredSandboxRoot(t *testing.T) {
	project := t.TempDir()
	policy := FromConfig(&config.SandboxConfig{
		Root: "fence",
		Mode: "workspace-write",
	}, project)
	want := filepath.Join(project, "fence")
	if policy.Workspace != want {
		t.Fatalf("policy workspace = %q, want %q", policy.Workspace, want)
	}
}
