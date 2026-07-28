// Package goal implements persistent goals with progress tracking and token
// budget enforcement for rick's agent loop.
package goal

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "time"
)

// Goal is a tracked objective with optional steps and a token budget.
type Goal struct {
    ID          string    `json:"id"`
    Title       string    `json:"title"`
    Description string    `json:"description,omitempty"`
    Status      string    `json:"status"` // active | completed | aborted
    TokenBudget int       `json:"token_budget,omitempty"`
    TokensUsed  int       `json:"tokens_used"`
    Steps       []Step    `json:"steps,omitempty"`
    Created     time.Time `json:"created"`
    Updated     time.Time `json:"updated"`
}

// Step is one unit of work within a goal.
type Step struct {
    ID      string `json:"id"`
    Content string `json:"content"`
    Status  string `json:"status"` // pending | in_progress | done | skipped
}

// Store persists goals as JSON files in a directory.
type Store struct {
    mu  sync.Mutex
    dir string
}

// NewStore opens (and creates) a goal directory.
func NewStore(dir string) (*Store, error) {
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return nil, err
    }
    return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

// NewID mints a sortable, filesystem-safe goal id.
func NewID() string {
    now := time.Now()
    return fmt.Sprintf("goal_%s_%04x", now.Format("2006-01-02T15-04-05"), now.Nanosecond()&0xFFFF)
}

// Save atomically writes a goal to disk.
func (s *Store) Save(g *Goal) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if g.ID == "" {
        g.ID = NewID()
    }
    g.Updated = time.Now()
    if g.Created.IsZero() {
        g.Created = g.Updated
    }
    data, err := json.MarshalIndent(g, "", "  ")
    if err != nil {
        return err
    }
    final := s.path(g.ID)
    tmp := final + ".tmp"
    if err := os.WriteFile(tmp, data, 0o644); err != nil {
        return err
    }
    return os.Rename(tmp, final)
}

// Load reads a goal by id.
func (s *Store) Load(id string) (*Goal, error) {
    data, err := os.ReadFile(s.path(id))
    if err != nil {
        return nil, err
    }
    var g Goal
    if err := json.Unmarshal(data, &g); err != nil {
        return nil, err
    }
    return &g, nil
}

// List returns all goals, newest first.
func (s *Store) List() ([]Goal, error) {
    entries, err := os.ReadDir(s.dir)
    if err != nil {
        return nil, err
    }
    var out []Goal
    for _, e := range entries {
        if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
            continue
        }
        data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
        if err != nil {
            continue
        }
        var g Goal
        if json.Unmarshal(data, &g) != nil {
            continue
        }
        out = append(out, g)
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
    return out, nil
}

// Delete removes a goal file.
func (s *Store) Delete(id string) error { return os.Remove(s.path(id)) }

// SetActive marks a goal as the active one by writing an active.json pointer.
func (s *Store) SetActive(id string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    p := filepath.Join(s.dir, "active.json")
    data, err := json.Marshal(map[string]string{"id": id})
    if err != nil {
        return err
    }
    tmp := p + ".tmp"
    if err := os.WriteFile(tmp, data, 0o644); err != nil {
        return err
    }
    return os.Rename(tmp, p)
}

// ClearActive removes the active goal pointer.
func (s *Store) ClearActive() error {
    p := filepath.Join(s.dir, "active.json")
    if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
        return err
    }
    return nil
}

// GetActive returns the currently active goal, or nil if none is set.
func (s *Store) GetActive() (*Goal, error) {
    data, err := os.ReadFile(filepath.Join(s.dir, "active.json"))
    if err != nil {
        return nil, nil //nolint:nilerr — no active goal is not an error
    }
    var m map[string]string
    if json.Unmarshal(data, &m) != nil {
        return nil, nil
    }
    id := m["id"]
    if id == "" {
        return nil, nil
    }
    return s.Load(id)
}

// CheckBudget reports whether a goal still has token budget remaining.
// It returns false when TokensUsed >= TokenBudget (and TokenBudget > 0).
func CheckBudget(g *Goal) (ok bool, remaining int) {
    if g.TokenBudget <= 0 {
        return true, -1 // unlimited
    }
    remaining = g.TokenBudget - g.TokensUsed
    return remaining > 0, remaining
}

// AddTokens accumulates token usage on a goal and persists the change.
func (s *Store) AddTokens(id string, n int) error {
    g, err := s.Load(id)
    if err != nil {
        return err
    }
    g.TokensUsed += n
    return s.Save(g)
}

// UpdateStep changes a step's status within a goal.
func (s *Store) UpdateStep(goalID, stepID, status string) error {
    g, err := s.Load(goalID)
    if err != nil {
        return err
    }
    for i := range g.Steps {
        if g.Steps[i].ID == stepID {
            g.Steps[i].Status = status
            return s.Save(g)
        }
    }
    return fmt.Errorf("step %q not found in goal %q", stepID, goalID)
}

// Progress renders a human-readable progress string for a goal.
func Progress(g *Goal) string {
    done := 0
    total := len(g.Steps)
    for _, st := range g.Steps {
        if st.Status == "done" || st.Status == "skipped" {
            done++
        }
    }
    var b strings.Builder
    if total > 0 {
        fmt.Fprintf(&b, "%d/%d steps", done, total)
    } else {
        b.WriteString("no steps")
    }
    if g.TokenBudget > 0 {
        fmt.Fprintf(&b, " · %dk/%dk tokens", g.TokensUsed/1000, g.TokenBudget/1000)
    } else if g.TokensUsed > 0 {
        fmt.Fprintf(&b, " · %dk tokens used", g.TokensUsed/1000)
    }
    return b.String()
}
