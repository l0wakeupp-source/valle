package agent

import (
	"testing"

	"rick/internal/config"
	"rick/internal/permission"
)

func TestSubagentPermissionsInheritsYoloMode(t *testing.T) {
	base := permission.New(&config.Permission{
		Default: config.PermDeny,
		Bash:    map[string]string{"*": config.PermDeny},
		Write:   config.PermDeny,
	}, t.TempDir())
	base.SetYolo(true)

	child := SubagentPermissions(SubagentSpec{ReadOnly: true}, base, t.TempDir())
	if child != base {
		t.Fatal("yolo child should reuse the parent's permission engine")
	}
	if got := child.Check(permission.Request{Tool: "bash", Command: "go test ./..."}); got != permission.Allow {
		t.Fatalf("yolo permission did not propagate to child: %s", got)
	}
}

func TestSubagentToolFilterExposesReadOnlyToolsOnlyOutsideYolo(t *testing.T) {
	spec := SubagentSpec{ReadOnly: true}
	base := func(string) bool { return true }
	filtered := SubagentToolFilter(spec, base)
	for _, name := range []string{"write", "edit", "apply_patch", "bash"} {
		if filtered(name) {
			t.Fatalf("read-only subagent exposed %q", name)
		}
	}

	spec.ReadOnly = false
	unfiltered := SubagentToolFilter(spec, base)
	for _, name := range []string{"write", "edit", "apply_patch", "bash"} {
		if !unfiltered(name) {
			t.Fatalf("yolo-effective subagent hid %q", name)
		}
	}
}
