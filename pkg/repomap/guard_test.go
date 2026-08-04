package repomap

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildSkipsNonProjectDirectories(t *testing.T) {
	root := t.TempDir()
	// A lone source file with no manifest is not a project.
	writeFile(t, root, "solo.go", "package x\n")
	if block, err := Build(Options{Root: root, MaxTokens: 1024}); err != nil || block != "" {
		t.Fatalf("expected empty build for non-project: %q err=%v", block, err)
	}

	// With a go.mod it becomes a project.
	writeFile(t, root, "go.mod", "module test\n")
	writeFile(t, root, "main.go", "package main\nfunc main() {}\n")
	if block, err := Build(Options{Root: root, MaxTokens: 1024}); err != nil || block == "" {
		t.Fatalf("expected non-empty build for project: %q err=%v", block, err)
	}
}

func TestWalkerSkipsDotDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module test\n")
	writeFile(t, root, "main.go", "package main\nfunc main() {}\n")
	// A hidden dir full of source files must be skipped entirely.
	hidden := filepath.Join(root, ".cache")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, hidden, "a.go", "package cache\nfunc A() {}\n")
	writeFile(t, hidden, "b.go", "package cache\nfunc B() {}\n")
	writeFile(t, hidden, "c.go", "package cache\nfunc C() {}\n")

	index, err := Parse(Options{Root: root, MaxTokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range index.Symbols {
		if len(symbol.File) > 0 && symbol.File[0] == '.' {
			t.Fatalf("symbol from hidden dir leaked: %+v", symbol)
		}
	}
}

func TestWalkerAbortsOnScanBudget(t *testing.T) {
	root := t.TempDir()
	// Too many files for a 1ms budget.
	for i := 0; i < 200; i++ {
		writeFile(t, root, filepath.Join("sub", "f"+string(rune('a'+i%26))+string(rune('0'+i/26))+".go"),
			"package x\nfunc F() {}\n")
	}
	start := time.Now()
	_, err := Parse(Options{Root: root, MaxTokens: 1024, MaxScan: time.Millisecond})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a scan timeout error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("scan budget not enforced: took %v", elapsed)
	}
}
