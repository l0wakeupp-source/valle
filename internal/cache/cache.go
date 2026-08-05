// Package cache provides a small, stdlib-only content-addressed disk cache
// for expensive derived artifacts such as the RepoMap structural skeleton.
// Values are stored as files under a keyed path so a later process can reuse
// work that is still valid (the key carries the inputs' content hash).
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Dir is a content-addressed on-disk cache with bounded entry count.
type Dir struct {
	path     string
	maxFiles int
}

// New opens (and creates) a cache directory. maxFiles bounds the entry count;
// when exceeded the oldest entries are evicted. maxFiles <= 0 keeps every
// entry.
func New(path string, maxFiles int) (*Dir, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	if maxFiles <= 0 {
		maxFiles = 128
	}
	return &Dir{path: path, maxFiles: maxFiles}, nil
}

// Key returns the file-safe content address for a cache key.
func Key(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

// Get returns the cached value for key, or ok=false when absent, expired or
// unreadable.
func (d *Dir) Get(key string) ([]byte, bool) {
	if d == nil {
		return nil, false
	}
	path := d.filePath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Put stores value under key and evicts the oldest entries when the entry
// count exceeds the bound.
func (d *Dir) Put(key string, value []byte) {
	if d == nil || len(value) == 0 {
		return
	}
	path := d.filePath(key)
	if err := os.WriteFile(path, value, 0o644); err != nil {
		return
	}
	d.evict()
}

func (d *Dir) filePath(key string) string {
	return filepath.Join(d.path, Key(key)+".json")
}

// evict removes the oldest files (by modification time) over the entry bound.
func (d *Dir) evict() {
	entries, err := os.ReadDir(d.path)
	if err != nil || len(entries) <= d.maxFiles {
		return
	}
	type entry struct {
		path string
		mod  time.Time
	}
	var files []entry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, entry{filepath.Join(d.path, e.Name()), info.ModTime()})
	}
	if len(files) <= d.maxFiles {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files[:len(files)-d.maxFiles] {
		_ = os.Remove(f.path)
	}
}
