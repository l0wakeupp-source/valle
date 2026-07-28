// Package theme loads JSON theme files (built-in via go:embed, then user and
// project overrides) and exposes them as lipgloss styles.
package theme

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

//go:embed builtin/*.json builtin/*.rick
var builtinFS embed.FS

// File is the on-disk theme format.
//
//	{
//	  "$schema": "...",
//	  "defs": { "emerald": "#00d992" },
//	  "theme": { "primary": { "dark": "emerald", "light": "#047857" } }
//	}
type File struct {
	Schema string            `json:"$schema,omitempty"`
	Name   string            `json:"name,omitempty"`
	Defs   map[string]string `json:"defs,omitempty"`
	Theme  map[string]Entry  `json:"theme"`
}

// Entry is one colour role; it can be a bare string or {dark,light}.
type Entry struct {
	Dark  string
	Light string
}

// UnmarshalJSON accepts both "#fff" and {"dark":"#fff","light":"#000"}.
func (e *Entry) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		e.Dark, e.Light = s, s
		return nil
	}
	var o struct{ Dark, Light string }
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	e.Dark, e.Light = o.Dark, o.Light
	if e.Light == "" {
		e.Light = e.Dark
	}
	if e.Dark == "" {
		e.Dark = e.Light
	}
	return nil
}

// Theme is a resolved palette.
type Theme struct {
	Name   string
	colors map[string]lipgloss.AdaptiveColor
}

// Color returns the colour for a role, falling back to text/none.
func (t *Theme) Color(role string) lipgloss.AdaptiveColor {
	if c, ok := t.colors[role]; ok {
		return c
	}
	if c, ok := t.colors["text"]; ok {
		return c
	}
	return lipgloss.AdaptiveColor{Dark: "#cdd6f4", Light: "#1e1e2e"}
}

// Style returns a foreground style for a role.
func (t *Theme) Style(role string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.Color(role))
}

// Roles lists every defined role, sorted.
func (t *Theme) Roles() []string {
	out := make([]string, 0, len(t.colors))
	for k := range t.colors {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// resolve expands defs references (a value that names a def key).
func (f *File) resolve() *Theme {
	t := &Theme{Name: f.Name, colors: map[string]lipgloss.AdaptiveColor{}}
	lookup := func(v string) string {
		seen := 0
		for !strings.HasPrefix(v, "#") && seen < 8 {
			nv, ok := f.Defs[v]
			if !ok {
				break
			}
			v = nv
			seen++
		}
		return v
	}
	for role, e := range f.Theme {
		t.colors[role] = lipgloss.AdaptiveColor{Dark: lookup(e.Dark), Light: lookup(e.Light)}
	}
	return t
}

// Registry holds all discovered themes.
type Registry struct {
	themes map[string]*Theme
	order  []string
}

// Names returns theme names in load order.
func (r *Registry) Names() []string { return append([]string(nil), r.order...) }

// Get returns a theme by name (nil if absent).
func (r *Registry) Get(name string) *Theme { return r.themes[name] }

// Load discovers built-in themes then overrides from dirs (later wins).
func Load(dirs ...string) *Registry {
	r := &Registry{themes: map[string]*Theme{}}

	entries, _ := builtinFS.ReadDir("builtin")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := builtinFS.ReadFile("builtin/" + n)
		if err != nil {
			continue
		}
		r.add(themeBaseName(n), b)
	}

	for _, d := range dirs {
		if d == "" {
			continue
		}
		files, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		fnames := make([]string, 0, len(files))
		for _, f := range files {
			if !f.IsDir() && isThemeFile(f.Name()) {
				fnames = append(fnames, f.Name())
			}
		}
		sort.Strings(fnames)
		for _, n := range fnames {
			b, err := os.ReadFile(filepath.Join(d, n))
			if err != nil {
				continue
			}
			r.add(themeBaseName(n), b)
		}
	}
	return r
}

// isThemeFile reports whether a filename has a supported theme extension.
func isThemeFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".rick")
}

// themeBaseName strips a theme file extension to get the registry key.
func themeBaseName(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".rick") {
		return strings.TrimSuffix(name, name[len(name)-5:])
	}
	return strings.TrimSuffix(name, ".json")
}

func (r *Registry) add(name string, raw []byte) {
	var f File
	if err := json.Unmarshal(stripComments(raw), &f); err != nil {
		return
	}
	if f.Name == "" {
		f.Name = name
	}
	if _, exists := r.themes[name]; !exists {
		r.order = append(r.order, name)
	}
	r.themes[name] = f.resolve()
}

// Add parses raw theme JSON and registers it under name at runtime.
// It returns an error if the JSON is invalid.
func (r *Registry) Add(name string, raw []byte) error {
	var f File
	if err := json.Unmarshal(stripComments(raw), &f); err != nil {
		return fmt.Errorf("invalid theme JSON: %w", err)
	}
	if f.Name == "" {
		f.Name = name
	}
	if _, exists := r.themes[name]; !exists {
		r.order = append(r.order, name)
	}
	r.themes[name] = f.resolve()
	return nil
}

// AddFromFile reads a .rick or .json theme file from disk and registers it.
func (r *Registry) AddFromFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read theme file: %w", err)
	}
	name := themeBaseName(filepath.Base(path))
	return r.Add(name, raw)
}

// AddFromURL fetches a theme JSON from a URL and registers it.
func (r *Registry) AddFromURL(rawURL string) error {
	raw, err := fetchURL(rawURL)
	if err != nil {
		return err
	}
	// Derive a name from the URL path.
	name := "url-theme"
	if u, err := url.Parse(rawURL); err == nil {
		base := filepath.Base(u.Path)
		if base != "." && base != "/" && base != "" {
			name = themeBaseName(base)
		}
	}
	return r.Add(name, raw)
}

// LoadFromFile reads a single .rick or .json theme file and returns the
// resolved Theme without registering it in any registry.
func LoadFromFile(path string) (*Theme, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read theme file: %w", err)
	}
	var f File
	if err := json.Unmarshal(stripComments(raw), &f); err != nil {
		return nil, fmt.Errorf("invalid theme JSON: %w", err)
	}
	if f.Name == "" {
		f.Name = themeBaseName(filepath.Base(path))
	}
	return f.resolve(), nil
}

// LoadFromURL fetches a theme JSON from a URL and returns the resolved Theme.
func LoadFromURL(rawURL string) (*Theme, error) {
	raw, err := fetchURL(rawURL)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(stripComments(raw), &f); err != nil {
		return nil, fmt.Errorf("invalid theme JSON from URL: %w", err)
	}
	if f.Name == "" {
		f.Name = "url-theme"
	}
	return f.resolve(), nil
}

// fetchURL downloads content from a URL with a timeout.
func fetchURL(rawURL string) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch theme URL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch theme URL: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read theme URL body: %w", err)
	}
	return raw, nil
}

func stripComments(b []byte) []byte {
	var out []byte
	inStr, esc, inLine := false, false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inLine {
			if c == '\n' {
				inLine = false
				out = append(out, c)
			}
			continue
		}
		if inStr {
			out = append(out, c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '/' {
			inLine = true
			i++
			continue
		}
		out = append(out, c)
	}
	return out
}
