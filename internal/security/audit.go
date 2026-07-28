// Package security provides supply-chain vulnerability auditing against OSV.dev.
package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// osvRateLimiter paces OSV.dev queries to 10 requests per second.
// It is a channel-based token bucket: callers block on receive before sending
// a query, and a background goroutine refills one token every 100ms.
var osvRateLimiter = func() chan struct{} {
	ch := make(chan struct{}, 10)
	// Prime the bucket.
	for i := 0; i < 10; i++ {
		ch <- struct{}{}
	}
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}()
	return ch
}()

// Finding is one vulnerability discovered in a dependency.
type Finding struct {
	Package  string `json:"package"`
	Version  string `json:"version"`
	Severity string `json:"severity"`
	CVE      string `json:"cve"`
	OSVID    string `json:"osv_id"`
	URL      string `json:"url"`
}

// auditCacheEntry is one cached OSV response, keyed by package+version+ecosystem.
type auditCacheEntry struct {
	Fingerprint   string          `json:"fingerprint"`
	ResponseBody  []byte          `json:"response_body"`
	Timestamp     time.Time       `json:"timestamp"`
	SourceMtime   time.Time       `json:"source_mtime"`
}

// auditCache is the on-disk cache of OSV query responses.
type auditCache struct {
	Entries []auditCacheEntry `json:"entries"`
}

// dependency is one extracted package reference.
type dependency struct {
	Name       string
	Version    string
	Ecosystem  string
	SourceMtime time.Time
}

// Audit scans dir for dependency manifests and queries OSV.dev for known
// vulnerabilities in each dependency. Results are cached per
// (package, version, ecosystem) tuple so repeated runs stay fast.
func Audit(dir string) ([]Finding, error) {
	deps, err := collect(dir)
	if err != nil {
		return nil, err
	}

	cache, err := loadCache(dir)
	if err != nil {
		return nil, err
	}

	var (
		mu      sync.Mutex
		findings []Finding
	)

	for _, dep := range deps {
		hit := cache.lookup(dep, dir)
		if hit != nil {
			fs, err := parseFindings(hit, dep)
			if err == nil {
				mu.Lock()
				findings = append(findings, fs...)
				mu.Unlock()
				continue
			}
		}

		fs, body, err := queryOSV(dep.Name, dep.Version, dep.Ecosystem)
		if err != nil {
			return nil, fmt.Errorf("query %s %s: %w", dep.Name, dep.Version, err)
		}
		cache.store(dep, body, dir)
		mu.Lock()
		findings = append(findings, fs...)
		mu.Unlock()
	}

	if err := saveCache(dir, cache); err != nil {
		return nil, err
	}

	sort.Slice(findings, func(i, j int) bool {
		sev := map[string]int{"critical": 4, "high": 3, "moderate": 2, "low": 1, "": 0}
		if sev[findings[i].Severity] != sev[findings[j].Severity] {
			return sev[findings[i].Severity] > sev[findings[j].Severity]
		}
		return findings[i].Package < findings[j].Package
	})

	return findings, nil
}

// collect walks dir for known manifest files and extracts dependencies from each.
func collect(dir string) ([]dependency, error) {
	var deps []dependency

	if entries, err := parseGoMod(dir); err == nil {
		deps = append(deps, entries...)
	}
	if entries, err := parsePackageJSON(dir); err == nil {
		deps = append(deps, entries...)
	}
	if entries, err := parseCargoTOML(dir); err == nil {
		deps = append(deps, entries...)
	}
	if entries, err := parseRequirementsTXT(dir); err == nil {
		deps = append(deps, entries...)
	}
	if entries, err := parsePyprojectTOML(dir); err == nil {
		deps = append(deps, entries...)
	}

	return deps, nil
}

// osvQuery is the request body sent to OSV.dev.
type osvQuery struct {
	Version  string `json:"version"`
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
}

// osvResponse is the response body from OSV.dev.
type osvResponse struct {
	Vulns []osvVuln `json:"vulns"`
}

type osvVuln struct {
	ID       string          `json:"id"`
	Summary  string          `json:"summary"`
	Modified string          `json:"modified"`
	Severity []osvSeverity   `json:"severity"`
	Aliases  []string        `json:"aliases"`
	Database json.RawMessage `json:"database_specific"`
	Details  string          `json:"details"`
}

type osvSeverity struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}

const osvEndpoint = "https://api.osv.dev/v1/query"

// queryOSV sends a single lookup to OSV.dev and returns any findings.
func queryOSV(pkg, version, ecosystem string) ([]Finding, []byte, error) {
	// Rate limit: wait for a token before sending the query.
	<-osvRateLimiter

	body := osvQuery{Version: version}
	body.Package.Name = pkg
	body.Package.Ecosystem = ecosystem

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}

	resp, err := http.Post(osvEndpoint, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("osv: HTTP %d: %s", resp.StatusCode, string(data))
	}

	var parsed osvResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, nil, err
	}

	var findings []Finding
	for _, v := range parsed.Vulns {
		f := Finding{
			Package: pkg,
			Version: version,
			OSVID:   v.ID,
			URL:     "https://osv.dev/vulnerability/" + v.ID,
		}
		f.Severity = extractSeverity(v)
		f.CVE = extractCVE(v)
		findings = append(findings, f)
	}

	return findings, data, nil
}

func extractSeverity(v osvVuln) string {
	for _, s := range v.Severity {
		if s.Type == "CVSS_V3" || s.Type == "CVSS_V2" {
			return classifyCVSS(s.Score)
		}
	}
	// Fallback to database_specific severity.
	if v.Database != nil {
		var ds struct {
			Severity string `json:"severity"`
		}
		if err := json.Unmarshal(v.Database, &ds); err == nil && ds.Severity != "" {
			return strings.ToLower(ds.Severity)
		}
	}
	return ""
}

func classifyCVSS(score string) string {
	switch {
	case strings.HasPrefix(score, "9."), strings.HasPrefix(score, "10"):
		return "critical"
	case strings.HasPrefix(score, "7."), strings.HasPrefix(score, "8."):
		return "high"
	case strings.HasPrefix(score, "4."), strings.HasPrefix(score, "5."), strings.HasPrefix(score, "6."):
		return "moderate"
	default:
		return "low"
	}
}

func extractCVE(v osvVuln) string {
	for _, a := range v.Aliases {
		if strings.HasPrefix(a, "CVE-") {
			return a
		}
	}
	// database_specific may carry the CVE.
	if v.Database != nil {
		var ds struct {
			CVE []string `json:"cve"`
		}
		if err := json.Unmarshal(v.Database, &ds); err == nil && len(ds.CVE) > 0 {
			return ds.CVE[0]
		}
	}
	return ""
}

func parseFindings(data []byte, dep dependency) ([]Finding, error) {
	var resp osvResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	var findings []Finding
	for _, v := range resp.Vulns {
		f := Finding{
			Package: dep.Name,
			Version: dep.Version,
			OSVID:   v.ID,
			URL:     "https://osv.dev/vulnerability/" + v.ID,
		}
		f.Severity = extractSeverity(v)
		f.CVE = extractCVE(v)
		findings = append(findings, f)
	}
	return findings, nil
}

// cacheFile returns the path to the on-disk cache.
func cacheFile(dir string) string {
	return filepath.Join(dir, ".rick", "security-cache.json")
}

// loadCache reads the cache file. A missing cache returns an empty one.
func loadCache(dir string) (*auditCache, error) {
	cache := &auditCache{}
	data, err := os.ReadFile(cacheFile(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return cache, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cache); err != nil {
		// Corrupt cache: start fresh.
		return &auditCache{}, nil
	}
	return cache, nil
}

// saveCache writes the cache atomically via a temp file + rename.
func saveCache(dir string, cache *auditCache) error {
	path := cacheFile(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// lookup returns the cached response for dep if present and not stale.
func (c *auditCache) lookup(dep dependency, dir string) []byte {
	fp := cacheFingerprint(dep)
	for i := range c.Entries {
		e := &c.Entries[i]
		if e.Fingerprint != fp {
			continue
		}
		// Invalidate when the source manifest's mtime has changed since caching.
		if !dep.SourceMtime.IsZero() && !dep.SourceMtime.Equal(e.SourceMtime) {
			c.remove(i)
			return nil
		}
		return e.ResponseBody
	}
	return nil
}

// store records dep's response body in the cache.
func (c *auditCache) store(dep dependency, body []byte, dir string) {
	if dep.SourceMtime.IsZero() {
		fi, err := os.Stat(filepath.Join(dir, manifestFile(dep)))
		if err == nil {
			dep.SourceMtime = fi.ModTime()
		}
	}
	fp := cacheFingerprint(dep)
	for i := range c.Entries {
		if c.Entries[i].Fingerprint == fp {
			c.Entries[i] = auditCacheEntry{
				Fingerprint:  fp,
				ResponseBody: body,
				Timestamp:    time.Now(),
				SourceMtime:  dep.SourceMtime,
			}
			return
		}
	}
	c.Entries = append(c.Entries, auditCacheEntry{
		Fingerprint:  fp,
		ResponseBody: body,
		Timestamp:    time.Now(),
		SourceMtime:  dep.SourceMtime,
	})
}

func (c *auditCache) remove(i int) {
	c.Entries = append(c.Entries[:i], c.Entries[i+1:]...)
}

func cacheFingerprint(dep dependency) string {
	return dep.Ecosystem + "|" + dep.Name + "|" + dep.Version
}

// manifestFile returns the manifest filename associated with dep's ecosystem.
func manifestFile(dep dependency) string {
	switch dep.Ecosystem {
	case "Go":
		return "go.mod"
	case "npm":
		return "package.json"
	case "crates.io":
		return "Cargo.toml"
	case "PyPI":
		// requirements.txt and pyproject.toml both map to PyPI.
		return "requirements.txt"
	default:
		return "go.mod"
	}
}

// ---------- manifest parsers ----------

// parseGoMod extracts direct dependencies from a go.mod file.
func parseGoMod(dir string) ([]dependency, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	mt := fi.ModTime()

	lines := strings.Split(string(data), "\n")
	var deps []dependency
	inRequire := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "require (") {
			inRequire = true
			continue
		}
		if inRequire {
			if trimmed == ")" {
				inRequire = false
				continue
			}
			// Line like: github.com/foo/bar v1.2.3
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 {
				deps = append(deps, dependency{
					Name:        fields[0],
					Version:     fields[1],
					Ecosystem:   "Go",
					SourceMtime: mt,
				})
			}
		}
	}
	return deps, nil
}

// parsePackageJSON extracts dependencies and devDependencies from package.json.
func parsePackageJSON(dir string) ([]dependency, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, err
	}
	mt := fi.ModTime()

	var doc struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var deps []dependency
	for name, ver := range doc.Dependencies {
		if seen[name] {
			continue
		}
		seen[name] = true
		deps = append(deps, dependency{
			Name:        name,
			Version:     strings.TrimLeft(ver, "^~>=<"),
			Ecosystem:   "npm",
			SourceMtime: mt,
		})
	}
	for name, ver := range doc.DevDependencies {
		if seen[name] {
			continue
		}
		seen[name] = true
		deps = append(deps, dependency{
			Name:        name,
			Version:     strings.TrimLeft(ver, "^~>=<"),
			Ecosystem:   "npm",
			SourceMtime: mt,
		})
	}
	return deps, nil
}

// parseCargoTOML extracts dependencies from the [dependencies] section.
func parseCargoTOML(dir string) ([]dependency, error) {
	data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		return nil, err
	}
	mt := fi.ModTime()

	lines := strings.Split(string(data), "\n")
	var deps []dependency
	inDeps := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		if trimmed == "[dependencies]" {
			inDeps = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inDeps = false
			continue
		}
		if !inDeps {
			continue
		}
		// Format: name = "version" or name = {version = "..."}
		eq := strings.Index(trimmed, "=")
		if eq < 0 {
			continue
		}
		name := strings.TrimSpace(trimmed[:eq])
		val := strings.TrimSpace(trimmed[eq+1:])

		// Inline table form: {version = "1.2.3", ...}
		if strings.HasPrefix(val, "{") {
			val = strings.TrimPrefix(val, "{")
			val = strings.TrimSuffix(val, "}")
			for _, part := range strings.Split(val, ",") {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "version") {
					v := strings.SplitN(part, "=", 2)
					if len(v) == 2 {
						ver := strings.TrimSpace(v[1])
						ver = strings.Trim(ver, `"`)
						deps = append(deps, dependency{
							Name:        name,
							Version:     ver,
							Ecosystem:   "crates.io",
							SourceMtime: mt,
						})
					}
					break
				}
			}
			continue
		}
		// Simple string form: name = "1.2.3"
		val = strings.Trim(val, `"`)
		if name != "" && val != "" {
			deps = append(deps, dependency{
				Name:        name,
				Version:     val,
				Ecosystem:   "crates.io",
				SourceMtime: mt,
			})
		}
	}
	return deps, nil
}

// parseRequirementsTXT extracts package==version entries from requirements.txt.
func parseRequirementsTXT(dir string) ([]dependency, error) {
	data, err := os.ReadFile(filepath.Join(dir, "requirements.txt"))
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(filepath.Join(dir, "requirements.txt"))
	if err != nil {
		return nil, err
	}
	mt := fi.ModTime()

	lines := strings.Split(string(data), "\n")
	var deps []dependency
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		idx := strings.Index(trimmed, "==")
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(trimmed[:idx])
		ver := strings.TrimSpace(trimmed[idx+2:])
		deps = append(deps, dependency{
			Name:        name,
			Version:     ver,
			Ecosystem:   "PyPI",
			SourceMtime: mt,
		})
	}
	return deps, nil
}

// parsePyprojectTOML extracts dependencies from [project].dependencies or
// [tool.poetry].dependencies sections.
func parsePyprojectTOML(dir string) ([]dependency, error) {
	data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(filepath.Join(dir, "pyproject.toml"))
	if err != nil {
		return nil, err
	}
	mt := fi.ModTime()

	lines := strings.Split(string(data), "\n")
	var deps []dependency
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimPrefix(strings.TrimSuffix(trimmed, "]"), "[")
			continue
		}
		// Only look at [tool.poetry] and [tool.poetry.*] sections.
		// [project] uses an array format ("pkg>=1.0") that needs different
		// parsing; skip it to avoid false positives.
		if section != "tool.poetry" && !strings.HasPrefix(section, "tool.poetry.") {
			continue
		}
		// Skip section headers and array-form dependency lists; keep only
		// simple name = "version" lines.
		if strings.HasPrefix(trimmed, "[") || !strings.Contains(trimmed, "=") {
			continue
		}
		eq := strings.Index(trimmed, "=")
		if eq <= 0 {
			continue
		}
		name := strings.TrimSpace(trimmed[:eq])
		val := strings.TrimSpace(trimmed[eq+1:])
		// Skip the "dependencies = [...]" array header line.
		if strings.HasPrefix(val, "[") {
			continue
		}
		val = strings.Trim(val, `"`)
		// Extract version from spec like ">=1.0" or "^1.2.3".
		ver := strings.TrimLeft(val, "^>=<~! ")
		if name != "" && ver != "" {
			deps = append(deps, dependency{
				Name:        name,
				Version:     ver,
				Ecosystem:   "PyPI",
				SourceMtime: mt,
			})
		}
	}
	return deps, nil
}
