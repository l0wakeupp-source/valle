package session

import (
	"os"
	"path/filepath"
	"testing"
)

func newSnap(t *testing.T) (*Snapshotter, string) {
	t.Helper()
	work := t.TempDir()
	data := t.TempDir()
	s, err := NewSnapshotter(work, data)
	if err != nil {
		t.Fatalf("snapshotter: %v", err)
	}
	if !s.Enabled() {
		t.Skip("git unavailable")
	}
	return s, work
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A read-only tool batch takes a checkpoint that commits nothing. Does it
// destroy the redo tail?
func TestScratchNoopSnapshotDropsRedo(t *testing.T) {
	s, work := newSnap(t)
	write(t, work, "a.txt", "v1")
	s.Snapshot("first")
	write(t, work, "a.txt", "v2")
	s.Snapshot("second")
	write(t, work, "a.txt", "v3")

	if _, err := s.Undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	t.Logf("after undo: history=%d cursor=%d canRedo=%v", len(s.history), s.cursor, s.CanRedo())
	body, _ := os.ReadFile(filepath.Join(work, "a.txt"))
	t.Logf("file after undo = %q", body)

	// read-only tool batch -> snapshot with nothing to commit
	if _, err := s.Snapshot("read-only bash"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Logf("after no-op snapshot: history=%d cursor=%d canRedo=%v", len(s.history), s.cursor, s.CanRedo())
	if _, err := s.Redo(); err != nil {
		t.Logf("REDO LOST: %v", err)
	}
	body, _ = os.ReadFile(filepath.Join(work, "a.txt"))
	t.Logf("file after redo attempt = %q (v3 unreachable => data loss)", body)
}

func TestScratchRestoreUnknownHashWipesHistory(t *testing.T) {
	s, work := newSnap(t)
	write(t, work, "a.txt", "v1")
	h1, _ := s.Snapshot("first")
	write(t, work, "a.txt", "v2")
	s.Snapshot("second")
	t.Logf("history=%d cursor=%d", len(s.history), s.cursor)
	// Restore an *existing* commit that is in history: fine.
	if err := s.Restore(h1); err != nil {
		t.Fatalf("restore: %v", err)
	}
	t.Logf("after restore(h1): history=%d cursor=%d canUndo=%v canRedo=%v", len(s.history), s.cursor, s.CanUndo(), s.CanRedo())

	// Now restore a valid-but-untracked commit hash (e.g. one trimmed by the
	// 100-entry cap, or loaded from another session).
	s2, work2 := newSnap(t)
	write(t, work2, "b.txt", "x")
	foreign, _ := s2.Snapshot("other project")
	err := s.Restore(foreign)
	t.Logf("restore(foreign)=%v history=%d cursor=%d", err, len(s.history), s.cursor)
}

func TestScratchLoadHistoryForeignHashes(t *testing.T) {
	s, work := newSnap(t)
	write(t, work, "a.txt", "v1")
	s.Snapshot("first")
	// Resume a session recorded in a different project.
	s.LoadHistory([]Snapshot{{ID: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", Label: "other"}})
	snap, err := s.Undo()
	t.Logf("undo after foreign LoadHistory: snap=%q err=%v history=%d cursor=%d", snap.ID, err, len(s.history), s.cursor)
}
