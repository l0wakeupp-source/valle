package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadToolUsesCompactLineNumbers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("first\nsecond"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (ReadTool{}).Run(context.Background(), Context{Cwd: dir}, jsonArgs(map[string]string{
		"path": "sample.txt",
	}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := "1|first\n2|second\n"
	if result.Output != want {
		t.Fatalf("output = %q, want %q", result.Output, want)
	}
}

func TestTreeToolCapsOutput(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2000; i++ {
		name := filepath.Join(dir, fmt.Sprintf("file-%04d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := (TreeTool{}).Run(context.Background(), Context{Cwd: dir}, jsonArgs(map[string]any{
		"path":  dir,
		"depth": 1,
	}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(result.Output) > defaultToolOutputLimit+128 {
		t.Fatalf("output length = %d, exceeds capped output with marker", len(result.Output))
	}
	if !strings.Contains(result.Output, "bytes omitted") {
		t.Fatalf("output lacks truncation marker: %q", result.Output[len(result.Output)-100:])
	}
}
