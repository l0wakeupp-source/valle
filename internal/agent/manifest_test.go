package agent

import (
	"strings"
	"testing"

	"rick/internal/provider"
)

func TestToolManifestRendersTypeScriptSignatures(t *testing.T) {
	schemas := []provider.ToolSchema{
		{
			Name: "read",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"path"},
				"properties": map[string]any{
					"path":   map[string]any{"type": "string"},
					"offset": map[string]any{"type": "number"},
					"full":   map[string]any{"type": "boolean"},
				},
			},
		},
		{
			Name: "edit",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"path", "old_string", "new_string"},
				"properties": map[string]any{
					"path":        map[string]any{"type": "string"},
					"old_string":  map[string]any{"type": "string"},
					"new_string":  map[string]any{"type": "string"},
					"replace_all": map[string]any{"type": "boolean"},
				},
			},
		},
	}

	manifest := toolManifest(schemas)
	if !strings.Contains(manifest, "read(full?: boolean, offset?: number, path: string): void") {
		t.Fatalf("unexpected read signature:\n%s", manifest)
	}
	if !strings.Contains(manifest, "edit(new_string: string, old_string: string, path: string, replace_all?: boolean): void") {
		t.Fatalf("unexpected edit signature:\n%s", manifest)
	}
}

func TestToolManifestEmptyWithoutTools(t *testing.T) {
	if toolManifest(nil) != "" {
		t.Fatal("expected an empty manifest for no tools")
	}
}

func TestToolManifestMapsEnumToUnion(t *testing.T) {
	schemas := []provider.ToolSchema{{
		Name: "set_mode",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"mode"},
			"properties": map[string]any{
				"mode": map[string]any{"type": "string", "enum": []any{"read-only", "trusted", "off"}},
			},
		},
	}}
	manifest := toolManifest(schemas)
	if !strings.Contains(manifest, `set_mode(mode: "read-only" | "trusted" | "off"): void`) {
		t.Fatalf("enum not rendered as a union:\n%s", manifest)
	}
}
