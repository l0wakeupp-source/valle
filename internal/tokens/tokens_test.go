package tokens

import "testing"

func TestCountCl100kBaseUsesExactEncoding(t *testing.T) {
	result := Count("hello world", EncodingCl100kBase)
	if result.Count != 2 {
		t.Fatalf("Count() = %d, want 2", result.Count)
	}
	if !result.Exact {
		t.Fatal("Count() reported fallback for cl100k_base")
	}
}

func TestCountO200kBaseUsesExactEncoding(t *testing.T) {
	result := Count("hello world", EncodingO200kBase)
	if result.Count != 2 {
		t.Fatalf("Count() = %d, want 2", result.Count)
	}
	if !result.Exact {
		t.Fatal("Count() reported fallback for o200k_base")
	}
}

func TestCountFallbackIsConservativeAndUnicodeSafe(t *testing.T) {
	result := Count("こんにちは世界", Encoding("unknown"))
	if result.Count < 7 {
		t.Fatalf("Count() = %d, want at least one token per rune", result.Count)
	}
	if result.Exact {
		t.Fatal("unknown encoding used exact mode")
	}
}

func TestEncodingForModel(t *testing.T) {
	cases := map[string]Encoding{
		"gpt-4o":        EncodingO200kBase,
		"gpt-5":         EncodingO200kBase,
		"o4-mini":       EncodingO200kBase,
		"codex-mini":    EncodingO200kBase,
		"claude-sonnet": EncodingCl100kBase,
		"deepseek-v4":   EncodingCl100kBase,
		"":              EncodingCl100kBase,
	}
	for model, want := range cases {
		if got := EncodingForModel(model); got != want {
			t.Errorf("EncodingForModel(%q) = %s, want %s", model, got, want)
		}
	}
}
