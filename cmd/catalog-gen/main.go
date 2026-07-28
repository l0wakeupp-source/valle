// Command catalog-gen regenerates internal/provider/catalog/generated.go from
// the models.dev provider index.
//
// Usage: go run ./cmd/catalog-gen
//
// Curated entries in catalog.go always win. A models.dev provider is skipped
// when its id, display name or base URL already appears in Registry, so
// hand-tuned OAuth flows, wire protocols and notes are never clobbered.
package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"rick/internal/provider/catalog"
)

const (
	sourceURL = "https://models.dev/api.json"
	outPath   = "internal/provider/catalog/generated.go"
)

// anthropicSDKs marks the models.dev npm packages that speak the Anthropic wire
// protocol; everything else is OpenAI-compatible.
var anthropicSDKs = map[string]bool{
	"@ai-sdk/anthropic":               true,
	"@ai-sdk/google-vertex/anthropic": true,
}

type mdProvider struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	API  string   `json:"api"`
	Doc  string   `json:"doc"`
	NPM  string   `json:"npm"`
	Env  []string `json:"env"`
}

var (
	envRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	nonAlnum = regexp.MustCompile(`[^a-z0-9]`)
	schemeRe = regexp.MustCompile(`^https?://`)
)

func slug(s string) string { return nonAlnum.ReplaceAllString(strings.ToLower(s), "") }

func fetch() (map[string]mdProvider, error) {
	req, err := http.NewRequest(http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	// models.dev 403s requests without a User-Agent.
	req.Header.Set("User-Agent", "rick-catalog-gen/1.0")

	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", sourceURL, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := map[string]mdProvider{}
	return out, json.Unmarshal(body, &out)
}

func main() {
	providers, err := fetch()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch:", err)
		os.Exit(1)
	}

	// Index the curated registry so we never emit a duplicate. Registry
	// already contains the previous generation (appended by init), so exclude
	// it — otherwise a rerun would dedup against its own output and emit none.
	prev := map[string]bool{}
	for _, e := range catalog.Generated {
		prev[e.ID] = true
	}
	ids, names, urls := map[string]bool{}, map[string]bool{}, map[string]bool{}
	curated := 0
	for _, e := range catalog.Registry {
		if prev[e.ID] {
			continue
		}
		curated++
		ids[e.ID] = true
		names[slug(e.Name)] = true
		if e.BaseURL != "" {
			urls[strings.TrimRight(e.BaseURL, "/")] = true
		}
	}

	keys := make([]string, 0, len(providers))
	for k := range providers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	kept := 0
	body := &strings.Builder{}
	for _, k := range keys {
		p := providers[k]
		api := strings.TrimRight(p.API, "/")
		// Some entries ship an unexpanded ${VAR} template as their api value
		// (e.g. neon); those are not usable base URLs, so drop them.
		if api == "" || !strings.HasPrefix(api, "http") || strings.Contains(api, "${") {
			continue
		}
		if ids[k] || names[slug(p.Name)] || urls[api] {
			continue
		}
		ids[k], urls[api] = true, true // guard against intra-batch duplicates
		kept++

		env := make([]string, 0, len(p.Env))
		for _, e := range p.Env {
			if envRe.MatchString(e) {
				env = append(env, quote(e))
			}
		}
		auth, flavor := "AuthAPIKey", "FlavorOpenAI"
		if len(env) == 0 {
			auth = "AuthNone"
		}
		if anthropicSDKs[p.NPM] {
			flavor = "FlavorAnthropic"
		}
		name := p.Name
		if name == "" {
			name = k
		}

		fmt.Fprintf(body, "\t{ID: %s, Name: %s, Auth: %s, Flavor: %s,\n",
			quote(k), quote(name), auth, flavor)
		fmt.Fprintf(body, "\t\tBaseURL: %s,", quote(api))
		if len(env) > 0 {
			fmt.Fprintf(body, "\n\t\tKeyEnv:  []string{%s},", strings.Join(env, ", "))
		}
		if doc := strings.TrimRight(schemeRe.ReplaceAllString(p.Doc, ""), "/"); doc != "" {
			fmt.Fprintf(body, "\n\t\tKeyHint: %s,", quote(doc))
		}
		body.WriteString("},\n\n")
	}

	b.WriteString("// Code generated from https://models.dev/api.json — DO NOT EDIT.\n")
	b.WriteString("// Regenerate: go run ./cmd/catalog-gen\n")
	fmt.Fprintf(&b, "// Snapshot taken %s — %d providers.\n//\n", time.Now().Format("2006-01-02"), kept)
	b.WriteString("// Curated Registry entries always win: ids, base URLs and names already\n")
	b.WriteString("// present above are skipped here, so hand-tuned OAuth flows and wire\n")
	b.WriteString("// protocols are never clobbered by generated data.\n\n")
	b.WriteString("package catalog\n\nfunc init() { Registry = append(Registry, Generated...) }\n\n")
	b.WriteString("// Generated is the models.dev slice appended to Registry at init.\n")
	b.WriteString("var Generated = []Entry{\n")
	b.WriteString(body.String())
	b.WriteString("}\n")

	// gofmt the result so the committed file needs no follow-up formatting.
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "format:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s — %d generated, %d curated, %d total\n",
		outPath, kept, curated, curated+kept)
}

// quote renders a Go string literal.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
