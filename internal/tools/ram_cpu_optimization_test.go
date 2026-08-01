package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadToolPreservesPagedCRLFLineSemantics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "paged.txt")
	if err := os.WriteFile(path, []byte("one\r\ntwo\r\nthree"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (ReadTool{}).Run(context.Background(), Context{Cwd: dir}, jsonArgs(map[string]any{
		"path":   "paged.txt",
		"offset": 2,
		"limit":  1,
	}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Output != "2|two\n\n<showing lines 2-2 of 3; continue with offset=3>" {
		t.Fatalf("output = %q", result.Output)
	}
	if strings.Contains(result.Output, "\r") {
		t.Fatalf("output retained CRLF bytes: %q", result.Output)
	}
}

func TestReadToolRejectsInputBeforeBuildingOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(path, []byte("123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (ReadTool{MaxInputBytes: 4}).Run(context.Background(), Context{Cwd: dir}, jsonArgs(map[string]string{
		"path": "large.txt",
	}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(result.Output, "exceeds the read input limit") {
		t.Fatalf("output = %q", result.Output)
	}
}
