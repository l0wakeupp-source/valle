package usage

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestRecordBatchesUntilFlush(t *testing.T) {
	tracker := New(t.TempDir())
	if err := tracker.Record("test-model", 1, 2, 0, 0); err != nil {
		t.Fatalf("first Record returned error: %v", err)
	}

	path := tracker.Path()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read initial usage file: %v", err)
	}

	tracker.mu.Lock()
	tracker.lastPersist = time.Now()
	tracker.mu.Unlock()

	if err := tracker.Record("test-model", 3, 4, 0, 0); err != nil {
		t.Fatalf("batched Record returned error: %v", err)
	}
	afterRecord, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read batched usage file: %v", err)
	}
	if !bytes.Equal(afterRecord, before) {
		t.Fatal("usage file changed before the persistence interval elapsed")
	}

	if err := tracker.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	afterFlush, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read flushed usage file: %v", err)
	}
	if bytes.Equal(afterFlush, before) {
		t.Fatal("Flush did not persist the pending usage update")
	}
}
