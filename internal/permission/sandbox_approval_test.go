package permission

import (
	"os"
	"path/filepath"
	"testing"

	"rick/internal/config"
)

func TestWorkspaceSandboxAutoApprovesWriteInsideFence(t *testing.T) {
	root := t.TempDir()
	engine := New(&config.Permission{
		Default: config.PermAsk,
		Edit:    config.PermAsk,
		Write:   config.PermAsk,
	}, root)
	engine.SetSandboxRoot(root, true)

	inside := filepath.Join(root, "src", "new.go")
	if got := engine.Check(Request{Tool: "write", Path: inside}); got != Allow {
		t.Fatalf("inside-fence write decision = %s, want allow", got)
	}

	outside := filepath.Join(filepath.Dir(root), "outside.go")
	if got := engine.Check(Request{Tool: "write", Path: outside}); got != Ask {
		t.Fatalf("outside-fence write decision = %s, want ask", got)
	}
	prefixSibling := root + "-evil.go"
	if got := engine.Check(Request{Tool: "write", Path: prefixSibling}); got != Ask {
		t.Fatalf("prefix-confusion write decision = %s, want ask", got)
	}
	traversal := filepath.Join(root, "..", filepath.Base(outside))
	if got := engine.Check(Request{Tool: "write", Path: traversal}); got != Ask {
		t.Fatalf("traversal write decision = %s, want ask", got)
	}
}

func TestWorkspaceSandboxBlocklistedPathStaysDenied(t *testing.T) {
	root := t.TempDir()
	engine := New(&config.Permission{
		Default: config.PermAsk,
		Write:   config.PermAsk,
		Paths: map[string]string{
			"**/.env": config.PermDeny,
		},
	}, root)
	engine.SetSandboxRoot(root, true)
	engine.SetProtectedPaths([]string{"**/credentials.json"})
	engine.SetYolo(true)

	blocked := filepath.Join(root, ".env")
	if got := engine.Check(Request{Tool: "write", Path: blocked}); got != Deny {
		t.Fatalf("blocklisted inside-fence write decision = %s, want deny", got)
	}
	if got := engine.Check(Request{Tool: "write", Path: ".env"}); got != Deny {
		t.Fatalf("relative blocklisted write decision = %s, want deny", got)
	}

	ordinary := filepath.Join(root, "config.json")
	if got := engine.Check(Request{Tool: "write", Path: ordinary}); got != Allow {
		t.Fatalf("ordinary yolo write decision = %s, want allow", got)
	}

	protected := filepath.Join(root, "credentials.json")
	if got := engine.Check(Request{Tool: "write", Path: protected}); got != Deny {
		t.Fatalf("protected inside-fence write decision = %s, want deny", got)
	}
	if got := engine.Check(Request{Tool: "write", Path: "credentials.json"}); got != Deny {
		t.Fatalf("relative protected write decision = %s, want deny", got)
	}
}

func TestWorkspaceSandboxRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	engine := New(&config.Permission{
		Default: config.PermAllow,
		Write:   config.PermAllow,
	}, root)
	engine.SetSandboxRoot(root, true)

	escaped := filepath.Join(link, "outside.go")
	if got := engine.Check(Request{Tool: "write", Path: escaped}); got != Ask {
		t.Fatalf("symlink escape decision = %s, want ask", got)
	}
}

func TestWorkspaceSandboxChecksEveryPatchTarget(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	engine := New(&config.Permission{
		Default: config.PermAsk,
		Edit:    config.PermAsk,
	}, root)
	engine.SetSandboxRoot(root, true)

	insideA := filepath.Join(root, "a.go")
	insideB := filepath.Join(root, "b.go")
	if got := engine.Check(Request{Tool: "apply_patch", Paths: []string{insideA, insideB}}); got != Allow {
		t.Fatalf("all-inside patch decision = %s, want allow", got)
	}

	out := filepath.Join(outside, "outside.go")
	if got := engine.Check(Request{Tool: "apply_patch", Paths: []string{insideA, out}}); got != Ask {
		t.Fatalf("mixed-fence patch decision = %s, want ask", got)
	}
}
