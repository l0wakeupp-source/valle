package session

import (
	"os"
	"path/filepath"
	"testing"

	"rick/internal/provider"
)

func TestSessionMetadataOperationsPersistAndDeleteCompanion(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess := &Session{
		ID:       "metadata-session",
		Title:    "original title",
		Cwd:      t.TempDir(),
		Messages: []provider.Message{provider.UserText("find this message")},
	}
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	if err := store.SetCategory(sess.ID, "Roblox"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFavorite(sess.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Rename(sess.ID, "renamed title"); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "renamed title" || loaded.Category != "Roblox" || !loaded.Favorite {
		t.Fatalf("metadata did not persist: %+v", loaded)
	}

	if err := store.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), sess.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("session file still exists, stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), sess.ID+".meta.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata companion still exists, stat error: %v", err)
	}
	metas, err := store.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("deleted session still listed: %+v", metas)
	}
}
