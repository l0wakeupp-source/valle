package modelsdev

import (
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		id      string
		want    int
		wantOK  bool
	}{
		{"claude-sonnet-4-5-20250929", 200000, true},
		{"gpt-5", 400000, true},
		{"gemini-2.5-pro", 1048576, true},
		{"longcat-2.0", 1000000, true},
		{"deepseek-chat", 128000, true},
		{"qwen3-coder", 256000, true},
		{"nonexistent-model", 0, false},
		{"", 0, false},
	}

	for _, tt := range tests {
		got, ok := Lookup(tt.id)
		if ok != tt.wantOK {
			t.Errorf("Lookup(%q): got ok=%v, want ok=%v", tt.id, ok, tt.wantOK)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("Lookup(%q): got %d, want %d", tt.id, got, tt.want)
		}
	}
}

func TestLookupWithProvider(t *testing.T) {
	// Models with provider prefix should still resolve
	got, ok := Lookup("anthropic/claude-sonnet-4-5-20250929")
	if !ok || got != 200000 {
		t.Errorf("Lookup with provider prefix: got %d, ok=%v; want 200000, true", got, ok)
	}
}

func TestLookupCaseInsensitive(t *testing.T) {
	got, ok := Lookup("GPT-5")
	if !ok || got != 400000 {
		t.Errorf("Lookup case insensitive: got %d, ok=%v; want 400000, true", got, ok)
	}
}

func TestLoadExport(t *testing.T) {
	original := Export()

	// Load new data
	err := Load([]byte(`{"test-model": 50000}`))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got, ok := Lookup("test-model")
	if !ok || got != 50000 {
		t.Errorf("Lookup after Load: got %d, ok=%v; want 50000, true", got, ok)
	}

	// Restore
	if err := Load(original); err != nil {
		t.Fatalf("Load restore failed: %v", err)
	}
}

func TestLen(t *testing.T) {
	if Len() == 0 {
		t.Error("Len() returned 0, expected non-zero")
	}
}
