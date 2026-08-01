package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadDir scans a directory for plugin manifest files (.json, .rick-plugin)
// and returns every valid manifest found. Files that fail to parse or
// validate are skipped silently so one broken plugin cannot block the rest.
func LoadDir(dir string) ([]Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []Manifest
	for _, e := range entries {
		if e.IsDir() {
			// A plugin folder may contain a manifest named plugin.json or
			// <name>.rick-plugin at its root.
			for _, candidate := range []string{"plugin.json", "manifest.json"} {
				p := filepath.Join(dir, e.Name(), candidate)
				if m, ok := loadFile(p); ok {
					out = append(out, m)
					break
				}
			}
			// Also pick up any *.rick-plugin directly inside the subfolder.
			sub, _ := os.ReadDir(filepath.Join(dir, e.Name()))
			for _, se := range sub {
				if se.IsDir() {
					continue
				}
				if strings.HasSuffix(se.Name(), ".rick-plugin") {
					if m, ok := loadFile(filepath.Join(dir, e.Name(), se.Name())); ok {
						out = append(out, m)
					}
				}
			}
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".rick-plugin") {
			if m, ok := loadFile(filepath.Join(dir, name)); ok {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// loadFile reads and validates a single manifest file.
func loadFile(path string) (Manifest, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, false
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, false
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, false
	}
	m.Source = path
	return m, true
}

// LoadFile reads and validates a single manifest file (exported wrapper).
func LoadFile(path string) (Manifest, bool) {
	return loadFile(path)
}

// LoadURL fetches a plugin manifest from a remote URL.
func LoadURL(url string) (Manifest, error) {
	parsed, err := urlpkg.Parse(url)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return Manifest{}, fmt.Errorf("plugin fetch: only absolute HTTPS URLs are allowed")
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.Host == "" {
				return fmt.Errorf("plugin fetch: redirect left HTTPS")
			}
			return nil
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return Manifest{}, fmt.Errorf("plugin fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("plugin fetch: %s returned %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Manifest{}, fmt.Errorf("plugin fetch: read body: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("plugin fetch: parse: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	m.Source = url
	return m, nil
}

// LoadAll gathers manifests from the global dir, the project dir, and any
// explicit URLs. URL entries that fail to load are skipped with their error
// collected, so one unreachable host does not abort startup.
func LoadAll(globalDir, projectDir string, urls []string) ([]Manifest, []error) {
	var out []Manifest
	var errs []error

	dirs := []string{globalDir}
	if trustedProjectPlugins() {
		dirs = append(dirs, projectDir)
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		ms, err := LoadDir(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, ms...)
	}

	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		m, err := LoadURL(u)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, m)
	}

	return out, errs
}

func trustedProjectPlugins() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RICK_TRUST_PROJECT_PLUGINS"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
