package session

import (
	"os"
	"path/filepath"
	"testing"
)

func newSnapshotterForTest(t *testing.T) (*Snapshotter, string) {
	t.Helper()
	workTree := t.TempDir()
	dataDir := t.TempDir()
	snapshotter, err := NewSnapshotter(workTree, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotter.Enabled() {
		t.Fatal("snapshotter is disabled")
	}
	return snapshotter, workTree
}

func writeSnapshotFile(t *testing.T, workTree, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workTree, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSameAsWorkTreeDoesNotStageChangesInShadowIndex(t *testing.T) {
	snapshotter, workTree := newSnapshotterForTest(t)
	writeSnapshotFile(t, workTree, "state.txt", "initial")
	hash, err := snapshotter.Snapshot("initial")
	if err != nil {
		t.Fatal(err)
	}

	writeSnapshotFile(t, workTree, "state.txt", "changed")
	if snapshotter.sameAsWorkTree(hash) {
		t.Fatal("changed work tree matched the snapshot")
	}
	if _, err := snapshotter.git("diff", "--cached", "--quiet"); err != nil {
		t.Fatalf("sameAsWorkTree staged the changed file: %v", err)
	}
}

func TestRestoreRemovesFilesAbsentFromTargetSnapshot(t *testing.T) {
	snapshotter, workTree := newSnapshotterForTest(t)
	writeSnapshotFile(t, workTree, "state.txt", "initial")
	initialHash, err := snapshotter.Snapshot("initial")
	if err != nil {
		t.Fatal(err)
	}

	writeSnapshotFile(t, workTree, "new.txt", "new")
	if _, err := snapshotter.Snapshot("with new file"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workTree, "new.txt")); err != nil {
		t.Fatalf("setup file missing before restore: %v", err)
	}

	if err := snapshotter.Restore(initialHash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workTree, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt still exists after restore, stat error = %v", err)
	}
}

func TestSnapshotBranchPreservesUndoBase(t *testing.T) {
	snapshotter, workTree := newSnapshotterForTest(t)
	writeSnapshotFile(t, workTree, "state.txt", "A")
	if _, err := snapshotter.Snapshot("A"); err != nil {
		t.Fatal(err)
	}

	writeSnapshotFile(t, workTree, "state.txt", "B")
	hashB, err := snapshotter.Snapshot("B")
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshotFile(t, workTree, "state.txt", "C")
	if _, err := snapshotter.Snapshot("C"); err != nil {
		t.Fatal(err)
	}

	if _, err := snapshotter.Undo(); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFile(t, workTree, "state.txt", "D")
	if _, err := snapshotter.Snapshot("D"); err != nil {
		t.Fatal(err)
	}

	target, err := snapshotter.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != hashB {
		t.Fatalf("undo target = %s, want branch base %s", target.ID, hashB)
	}
}

func TestRestoreUpdatesUndoCursor(t *testing.T) {
	snapshotter, workTree := newSnapshotterForTest(t)
	writeSnapshotFile(t, workTree, "state.txt", "A")
	hashA, err := snapshotter.Snapshot("A")
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshotFile(t, workTree, "state.txt", "B")
	if _, err := snapshotter.Snapshot("B"); err != nil {
		t.Fatal(err)
	}

	if err := snapshotter.Restore(hashA); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotter.Undo(); err == nil {
		t.Fatal("Undo succeeded after restoring the oldest snapshot")
	}
}
