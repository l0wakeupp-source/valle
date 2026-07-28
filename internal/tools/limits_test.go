package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedBufferKeepsOnlyConfiguredBytes(t *testing.T) {
	var buffer boundedBuffer
	buffer.limit = 4

	if _, err := buffer.Write([]byte("abcdef")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := buffer.String(); got != "abcd" {
		t.Fatalf("String() = %q, want %q", got, "abcd")
	}
	if !buffer.Truncated() || buffer.Total() != 6 {
		t.Fatalf("buffer state = truncated:%v total:%d, want truncated:true total:6", buffer.Truncated(), buffer.Total())
	}
}

func TestReadToolRejectsOversizedInputBeforeFormatting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 32)), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (ReadTool{MaxInputBytes: 16}).Run(context.Background(), Context{Cwd: dir}, jsonArgs(map[string]string{"path": "large.txt"}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "input limit") {
		t.Fatalf("result = %#v, want input-limit error", result)
	}
}

func TestDecodeArgsRejectsUnknownFields(t *testing.T) {
	var args struct {
		Command string `json:"command"`
	}
	if err := decodeArgs(jsonArgs(map[string]string{"command": "echo ok", "commmand": "typo"}), &args); err == nil {
		t.Fatal("decodeArgs accepted an unknown field")
	}
}

func TestMemoryToolRejectsOversizedValue(t *testing.T) {
	dir := t.TempDir()
	result, err := (MemoryTool{}).Run(context.Background(), Context{Cwd: dir}, jsonArgs(map[string]string{
		"action": "store", "key": "large", "value": strings.Repeat("x", maxMemoryValueBytes+1),
	}))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "memory limit") {
		t.Fatalf("result = %#v, want memory-limit error", result)
	}
}

func jsonArgs(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
