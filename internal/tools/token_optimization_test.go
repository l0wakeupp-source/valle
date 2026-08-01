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

func TestReadToolDoesNotExposeTrailingPhantomLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := (ReadTool{}).Run(context.Background(), Context{Cwd: dir}, jsonArgs(map[string]string{
		"path": "sample.txt",
	}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Meta["lines"] != 2 || strings.Contains(result.Output, "3|") {
		t.Fatalf("output = %q, metadata = %#v, want two lines", result.Output, result.Meta)
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

func TestUnifiedDiffLimitedCapsLargeOutput(t *testing.T) {
	oldText := strings.Repeat("old line\n", 4000)
	newText := strings.Repeat("new line\n", 4000)
	output := UnifiedDiffLimited("sample.txt", oldText, newText, 1, 1024)
	if len(output) > 1024 {
		t.Fatalf("diff length = %d, want <= 1024", len(output))
	}
	if !strings.Contains(output, "diff truncated") {
		t.Fatalf("diff lacks truncation marker: %q", output)
	}
}

func TestListToolCapsEntries(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 600; i++ {
		name := filepath.Join(dir, fmt.Sprintf("file-%04d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := (ListTool{}).Run(context.Background(), Context{Cwd: dir}, jsonArgs(map[string]any{"path": dir, "depth": 1}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(result.Output, "truncated at 500 entries") {
		t.Fatalf("output lacks entry cap marker: %q", result.Output[len(result.Output)-80:])
	}
}

func TestCodeSymbolsCapsLargeReferenceOutput(t *testing.T) {
	output := capCodeSymbolsOutput(strings.Repeat("reference\n", 5000))
	if len(output) > maxCodeSymbolsOutputBytes {
		t.Fatalf("output length = %d, want <= %d", len(output), maxCodeSymbolsOutputBytes)
	}
	if !strings.Contains(output, "symbols output truncated") {
		t.Fatalf("output lacks truncation marker")
	}
}

func TestGrepCapsOversizedLine(t *testing.T) {
	output := capGrepLine(strings.Repeat("界", maxGrepLineBytes))
	if len(output) > maxGrepLineBytes {
		t.Fatalf("line length = %d, want <= %d", len(output), maxGrepLineBytes)
	}
	if !strings.Contains(output, "line truncated") {
		t.Fatalf("line lacks truncation marker")
	}
}
