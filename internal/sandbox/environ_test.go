package sandbox

import (
	"strings"
	"testing"
)

func TestEnvironSurfacesSandboxRoot(t *testing.T) {
	policy := Default().Normalize(t.TempDir())
	var found string
	for _, entry := range Environ(policy) {
		if strings.HasPrefix(entry, "RICK_SANDBOX_ROOT=") {
			found = strings.TrimPrefix(entry, "RICK_SANDBOX_ROOT=")
			break
		}
	}
	if found != policy.Workspace {
		t.Fatalf("RICK_SANDBOX_ROOT = %q, want %q", found, policy.Workspace)
	}
}
