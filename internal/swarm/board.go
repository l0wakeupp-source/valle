package swarm

import (
	"fmt"
	"sync"
	"time"
)

// BoardEntry is a single item on the shared scratch board.
type BoardEntry struct {
	Key    string    `json:"key"`
	Value  string    `json:"value"`
	Author string    `json:"author"`
	Time   time.Time `json:"time"`
}

// Board is a thread-safe shared scratch space for swarm agents.
type Board struct {
	mu      sync.RWMutex
	entries map[string]BoardEntry
}

// NewBoard creates an empty board.
func NewBoard() *Board {
	return &Board{entries: map[string]BoardEntry{}}
}

// Put writes a value to the board, overwriting any previous entry with that key.
func (b *Board) Put(key, value, author string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[key] = BoardEntry{
		Key:    key,
		Value:  value,
		Author: author,
		Time:   time.Now(),
	}
}

// Get reads an entry by key. Returns an error if the key does not exist.
func (b *Board) Get(key string) (BoardEntry, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.entries[key]
	if !ok {
		return BoardEntry{}, fmt.Errorf("board: key %q not found", key)
	}
	return e, nil
}

// Has reports whether a key exists on the board.
func (b *Board) Has(key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.entries[key]
	return ok
}

// List returns a snapshot of all entries, sorted by key.
func (b *Board) List() []BoardEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BoardEntry, 0, len(b.entries))
	for _, e := range b.entries {
		out = append(out, e)
	}
	// sort by key for deterministic output
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].Key > out[j].Key {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Delete removes a key from the board.
func (b *Board) Delete(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, key)
}

// Len returns the number of entries on the board.
func (b *Board) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.entries)
}
