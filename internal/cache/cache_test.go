package cache

import (
	"path/filepath"
	"testing"
)

func TestDirGetPutRoundTrip(t *testing.T) {
	dir, err := New(filepath.Join(t.TempDir(), "c"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dir.Get("k1"); ok {
		t.Fatal("empty cache served a value")
	}
	dir.Put("k1", []byte("v1"))
	got, ok := dir.Get("k1")
	if !ok || string(got) != "v1" {
		t.Fatalf("Get after Put = %q, %v; want v1, true", got, ok)
	}
	// Same key bytes, different file path: content-addressed lookup works.
	if Key("k1") != Key("k1") || len(Key("k1")) != 32 {
		t.Fatal("Key is not a stable content address")
	}
}

func TestDirEvictsOldestOverBound(t *testing.T) {
	dir, err := New(filepath.Join(t.TempDir(), "c"), 2)
	if err != nil {
		t.Fatal(err)
	}
	dir.Put("a", []byte("a"))
	dir.Put("b", []byte("b"))
	dir.Put("c", []byte("c"))
	if _, ok := dir.Get("a"); ok {
		t.Fatal("oldest entry survived eviction")
	}
	if _, ok := dir.Get("b"); !ok {
		t.Fatal("newer entry was evicted")
	}
	if _, ok := dir.Get("c"); !ok {
		t.Fatal("newest entry missing")
	}
}
