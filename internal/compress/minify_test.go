package compress

import (
	"strings"
	"testing"
)

func TestMinifyJSONCompactsWhitespace(t *testing.T) {
	pretty := `{
  "name": "store",
  "items": [
    1,
    2
  ]
}`
	compact, ok := Minify(pretty)
	if !ok {
		t.Fatal("expected JSON to minify")
	}
	if compact != `{"name":"store","items":[1,2]}` {
		t.Fatalf("unexpected compact form: %q", compact)
	}
	if len(compact) >= len(pretty) {
		t.Fatal("minified JSON was not smaller")
	}
}

func TestMinifyJSONRejectsNonDocuments(t *testing.T) {
	for _, text := range []string{
		"just a sentence, not structured",
		"{ this is not valid json }",
		"",
	} {
		if _, ok := Minify(text); ok {
			t.Fatalf("Minify accepted %q", text)
		}
	}
}

func TestMinifyYAMLStripsComments(t *testing.T) {
	commented := `# store configuration
name: store   # display name
enabled: true
`
	block, ok := Minify(commented)
	if !ok {
		t.Fatal("expected commented YAML to minify")
	}
	if strings.Contains(block, "#") {
		t.Fatalf("yaml round-trip kept comments: %q", block)
	}
	if len(block) >= len(commented) {
		t.Fatal("yaml round-trip was not smaller")
	}
	for _, want := range []string{"name: store", "enabled: true"} {
		if !strings.Contains(block, want) {
			t.Fatalf("yaml round-trip lost %q: %q", want, block)
		}
	}
}

func TestMinifyYAMLPreservesValues(t *testing.T) {
	verbose := `---
"name": "store"
"nested":
    "enabled": "true"
`
	block, ok := Minify(verbose)
	if !ok {
		t.Fatal("expected YAML with a document marker to minify")
	}
	for _, want := range []string{"name", "store", "nested", "enabled"} {
		if !strings.Contains(block, want) {
			t.Fatalf("yaml round-trip lost %q: %q", want, block)
		}
	}
	if strings.Contains(block, "---") {
		t.Fatalf("yaml round-trip kept the document marker: %q", block)
	}
}

func TestMinifyYAMLRejectsExpansion(t *testing.T) {
	// A compact, uncommented block-style document must be left untouched.
	minimal := "name: store\ncount: 3\n"
	if _, ok := Minify(minimal); ok {
		t.Fatal("Minify expanded an already-minimal document")
	}
}

func TestForToolMinifiesStructuredOutput(t *testing.T) {
	result := ForTool(Input{
		Command:  "cat",
		Text:     "{\n  \"key\": \"value\"\n}\n",
		MaxBytes: 4096,
	})
	if result.Stage != "minify" {
		t.Fatalf("stage = %q, want minify", result.Stage)
	}
	if result.Fallback {
		t.Fatal("minified output should not be flagged as a fallback")
	}
	if result.Text != `{"key":"value"}` {
		t.Fatalf("unexpected minified output: %q", result.Text)
	}
}
