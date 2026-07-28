// Package session persists conversations to disk and provides snapshot-backed
// undo/redo of file changes.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"rick/internal/provider"
)

// Session is one conversation.
type Session struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Cwd       string             `json:"cwd"`
	Model     string             `json:"model"`
	Agent     string             `json:"agent"`
	Parent    string             `json:"parent,omitempty"`
	Favorite  bool               `json:"favorite,omitempty"`
	Created   time.Time          `json:"created"`
	Updated   time.Time          `json:"updated"`
	Messages  []provider.Message `json:"messages"`
	Snapshots []Snapshot         `json:"snapshots,omitempty"`
	Usage     Usage              `json:"usage"`
}

// Usage is the cumulative token count for a session.
type Usage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

// Snapshot references a point-in-time file state.
type Snapshot struct {
	ID      string    `json:"id"`      // git commit hash in the shadow repo
	Label   string    `json:"label"`   // what triggered it
	MsgIdx  int       `json:"msg_idx"` // message count when taken
	Created time.Time `json:"created"`
}

// Meta is the lightweight listing entry.
type Meta struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Cwd      string    `json:"cwd"`
	Model    string    `json:"model"`
	Parent   string    `json:"parent,omitempty"`
	Favorite bool      `json:"favorite,omitempty"`
	Messages int       `json:"messages"`
	Updated  time.Time `json:"updated"`
}

// Store is a directory of session files.
type Store struct {
	mu  sync.Mutex
	dir string
}

// NewStore opens (and creates) a session directory.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir returns the backing directory.
func (s *Store) Dir() string { return s.dir }

// NewID mints a sortable, filesystem-safe id.
func NewID() string {
	now := time.Now()
	return fmt.Sprintf("%s_%04x", now.Format("2006-01-02T15-04-05"), now.Nanosecond()&0xFFFF)
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// Save atomically writes a session.
func (s *Store) Save(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.ID == "" {
		sess.ID = NewID()
	}
	sess.Updated = time.Now()
	if sess.Created.IsZero() {
		sess.Created = sess.Updated
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	final := s.path(sess.ID)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// Load reads a session by id.
func (s *Store) Load(id string) (*Session, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Delete removes a session file.
func (s *Store) Delete(id string) error { return os.Remove(s.path(id)) }

// List returns session metadata, newest first, optionally filtered by cwd.
func (s *Store) List(cwd string) ([]Meta, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if json.Unmarshal(data, &sess) != nil {
			continue
		}
		if cwd != "" && sess.Cwd != cwd {
			continue
		}
		out = append(out, Meta{
			ID: sess.ID, Title: sess.Title, Cwd: sess.Cwd, Model: sess.Model,
			Parent: sess.Parent, Messages: len(sess.Messages), Updated: sess.Updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// SetCurrent records the last-active session for a working directory.
func (s *Store) SetCurrent(cwd, id string) error {
	m, _ := s.currentMap()
	if m == nil {
		m = map[string]string{}
	}
	m[cwd] = id
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(s.dir, "current.json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// GetCurrent returns the last-active session id for a working directory.
func (s *Store) GetCurrent(cwd string) string {
	m, err := s.currentMap()
	if err != nil {
		return ""
	}
	return m[cwd]
}

func (s *Store) currentMap() (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "current.json"))
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Title derives a fallback title from the first user message.
func Title(msgs []provider.Message) string {
	for _, m := range msgs {
		if m.Role != provider.RoleUser {
			continue
		}
		t := strings.TrimSpace(m.Text())
		if t == "" {
			continue
		}
		t = strings.ReplaceAll(t, "\n", " ")
		if len(t) > 48 {
			t = t[:48] + "…"
		}
		return t
	}
	return "untitled"
}

// Fork deep-copies a session with a new ID, sets Parent to the original, and
// appends "(fork)" to the title.
func (s *Store) Fork(id string) (*Session, error) {
	orig, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	fork := *orig
	fork.ID = NewID()
	fork.Parent = orig.ID
	fork.Title = orig.Title + " (fork)"
	fork.Created = time.Now()
	fork.Updated = fork.Created
	// Deep-copy messages so mutations don't leak back.
	fork.Messages = make([]provider.Message, len(orig.Messages))
	for i, m := range orig.Messages {
		cp := m
		cp.Content = make([]provider.ContentBlock, len(m.Content))
		copy(cp.Content, m.Content)
		fork.Messages[i] = cp
	}
	fork.Snapshots = nil
	if err := s.Save(&fork); err != nil {
		return nil, err
	}
	return &fork, nil
}

// Search returns sessions whose title or message text contains query
// (case-insensitive substring match), newest first.
func (s *Store) Search(query string) ([]Meta, error) {
	q := strings.ToLower(query)
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "current.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if json.Unmarshal(data, &sess) != nil {
			continue
		}
		if strings.Contains(strings.ToLower(sess.Title), q) {
			out = append(out, metaFrom(&sess))
			continue
		}
		for _, m := range sess.Messages {
			if strings.Contains(strings.ToLower(m.Text()), q) {
				out = append(out, metaFrom(&sess))
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Rename updates a session's title.
func (s *Store) Rename(id, title string) error {
	sess, err := s.Load(id)
	if err != nil {
		return err
	}
	sess.Title = title
	return s.Save(sess)
}

// SetFavorite toggles the favorite flag on a session.
func (s *Store) SetFavorite(id string, fav bool) error {
	sess, err := s.Load(id)
	if err != nil {
		return err
	}
	sess.Favorite = fav
	return s.Save(sess)
}
func metaFrom(s *Session) Meta {
	return Meta{
		ID: s.ID, Title: s.Title, Cwd: s.Cwd, Model: s.Model,
		Parent: s.Parent, Messages: len(s.Messages), Updated: s.Updated,
		Favorite: s.Favorite,
	}
}
