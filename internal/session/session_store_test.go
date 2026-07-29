package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"rick/internal/provider"
)

func TestListDoesNotReloadMetadataBackedSessionsAsLegacy(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	modern := &Session{
		ID:       "modern",
		Title:    "modern session",
		Cwd:      t.TempDir(),
		Messages: []provider.Message{provider.UserText("modern")},
	}
	if err := store.Save(modern); err != nil {
		t.Fatal(err)
	}

	legacy := &Session{
		ID:       "legacy",
		Title:    "legacy session",
		Cwd:      modern.Cwd,
		Messages: []provider.Message{provider.UserText("legacy")},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "legacy.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	metas, err := store.List(modern.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("got %d sessions, want modern plus legacy: %+v", len(metas), metas)
	}
	seen := make(map[string]int)
	for _, meta := range metas {
		seen[meta.ID]++
	}
	if seen["modern"] != 1 || seen["legacy"] != 1 {
		t.Fatalf("unexpected session counts: %+v", seen)
	}
}
