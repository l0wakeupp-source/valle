package theme

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Watcher polls the theme directories and reports when a file changes, so a
// user editing a theme JSON sees the result without restarting rick.
//
// Polling rather than fsnotify: theme dirs hold a handful of small files, a
// 1s stat sweep costs nothing, and it avoids a dependency plus the usual
// cross-platform watcher edge cases (editors that write-rename, network
// shares, WSL paths).
type Watcher struct {
	dirs []string

	mu   sync.Mutex
	seen map[string]time.Time
}

// NewWatcher builds a watcher over the given directories.
func NewWatcher(dirs ...string) *Watcher {
	clean := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if strings.TrimSpace(d) != "" {
			clean = append(clean, d)
		}
	}
	w := &Watcher{dirs: clean, seen: map[string]time.Time{}}
	w.scan() // prime, so the first poll does not report every file as new
	return w
}

// scan returns the current modification map.
func (w *Watcher) scan() map[string]time.Time {
	out := map[string]time.Time{}
	for _, dir := range w.dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !isThemeFile(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out[filepath.Join(dir, e.Name())] = info.ModTime()
		}
	}
	w.mu.Lock()
	if len(w.seen) == 0 {
		w.seen = out
	}
	w.mu.Unlock()
	return out
}

// Changed reports whether any theme file was added, removed or modified since
// the last call.
func (w *Watcher) Changed() bool {
	current := map[string]time.Time{}
	for _, dir := range w.dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !isThemeFile(e.Name()) {
				continue
			}
			if info, err := e.Info(); err == nil {
				current[filepath.Join(dir, e.Name())] = info.ModTime()
			}
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	changed := len(current) != len(w.seen)
	if !changed {
		for p, t := range current {
			if prev, ok := w.seen[p]; !ok || !prev.Equal(t) {
				changed = true
				break
			}
		}
	}
	if changed {
		w.seen = current
	}
	return changed
}

// Dirs returns the watched directories.
func (w *Watcher) Dirs() []string { return append([]string(nil), w.dirs...) }

// Reload re-reads every theme from disk, returning a fresh registry.
func Reload(dirs ...string) *Registry { return Load(dirs...) }

// SortedNames returns theme names with the built-ins first, then custom ones
// alphabetically — a stable order for the picker.
func (r *Registry) SortedNames() []string {
	builtin := map[string]int{"pickle-rick": 0, "rick-black": 1, "evil-rick": 2, "rick-neon": 3, "synthwave": 4}
	names := r.Names()
	sort.SliceStable(names, func(i, j int) bool {
		bi, iok := builtin[names[i]]
		bj, jok := builtin[names[j]]
		switch {
		case iok && jok:
			return bi < bj
		case iok:
			return true
		case jok:
			return false
		}
		return names[i] < names[j]
	})
	return names
}
