package main

import (
	"fmt"
	"os"
	"strings"

	"rick/internal/provider/catalog"
)

var failed int

func check(label string, ok bool, detail string) {
	if ok {
		fmt.Printf("  PASS  %s\n", label)
		return
	}
	failed++
	fmt.Printf("  FAIL  %s — %s\n", label, detail)
}

func idx(id string) int {
	for i, e := range catalog.Registry {
		if e.ID == id {
			return i
		}
	}
	return -1
}

func main() {
	fmt.Println("== curated entries survive the merge ==")
	check("anthropic still first", catalog.Registry[0].ID == "anthropic", catalog.Registry[0].ID)
	check("openai still second", catalog.Registry[1].ID == "openai", catalog.Registry[1].ID)
	for _, id := range []string{"nous", "chatgpt", "copilot"} {
		e, _ := catalog.Get(id)
		check(id+" keeps OAuth flow", e.OAuth != nil || e.Auth == catalog.AuthDeviceCode, e.Auth)
	}
	e, _ := catalog.Get("copilot")
	check("copilot keeps token exchange", e.CopilotExchange, "lost")
	e, _ = catalog.Get("anthropic")
	check("anthropic keeps anthropic flavor", e.Flavor == catalog.FlavorAnthropic, e.Flavor)

	fmt.Println("\n== this turn's three providers ==")
	for _, w := range [][3]string{
		{"fireworks", "Fireworks AI", "https://api.fireworks.ai/inference/v1"},
		{"opencode-go", "OpenCode Go", "https://opencode.ai/zen/go/v1"},
		{"tokenrouter", "TokenRouter", "https://api.tokenrouter.com/v1"},
	} {
		g, ok := catalog.Get(w[0])
		check(w[0]+" present", ok, "missing")
		check(w[0]+" name", g.Name == w[1], g.Name)
		check(w[0]+" URL verbatim", g.BaseURL == w[2], g.BaseURL)
	}
	z, g := idx("opencode-zen"), idx("opencode-go")
	check("OpenCode Go directly below Zen", g == z+1, fmt.Sprintf("zen=%d go=%d", z, g))

	fmt.Println("\n== merged registry integrity ==")
	seen, dupID := map[string]bool{}, []string{}
	dupURL, urls := []string{}, map[string]string{}
	bad := []string{}
	for _, e := range catalog.Registry {
		if seen[e.ID] {
			dupID = append(dupID, e.ID)
		}
		seen[e.ID] = true
		if e.ID == "" || e.Name == "" {
			bad = append(bad, "blank:"+e.ID)
		}
		if e.BaseURL != "" {
			u := strings.TrimRight(e.BaseURL, "/")
			if prev, ok := urls[u]; ok && !(prev == "openai" && e.ID == "chatgpt") {
				dupURL = append(dupURL, prev+"~"+e.ID)
			}
			urls[u] = e.ID
		}
		if e.NeedsKey() && len(e.KeyEnv) == 0 {
			bad = append(bad, "nokeyenv:"+e.ID)
		}
		if e.BaseURL != "" && !strings.HasPrefix(e.BaseURL, "http") {
			bad = append(bad, "badurl:"+e.ID)
		}
	}
	check("no duplicate ids", len(dupID) == 0, fmt.Sprint(dupID))
	check("no duplicate base URLs", len(dupURL) == 0, fmt.Sprint(dupURL))
	check("no malformed entries", len(bad) == 0, fmt.Sprint(bad))
	check("registry is 161", len(catalog.Registry) == 161, fmt.Sprint(len(catalog.Registry)))
	check("generated is 120", len(catalog.Generated) == 120, fmt.Sprint(len(catalog.Generated)))
	check("IDs() covers all", len(catalog.IDs()) == len(catalog.Registry), fmt.Sprint(len(catalog.IDs())))

	fmt.Println("\n== sample of newly available providers ==")
	for _, id := range []string{"deepinfra", "hyperbolt", "sambanova", "chutes", "vercel-ai-gateway"} {
		if e, ok := catalog.Get(id); ok {
			fmt.Printf("  + %-20s %-24s %s\n", e.ID, e.Name, e.BaseURL)
		}
	}

	fmt.Printf("\n%d failed\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("merged catalog verification OK")
}
