package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rick/internal/tokens"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseExtractsSymbolsFromGoFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", `package main

type User struct {
	ID   int
	Name string
}

type Store interface {
	Get(id int) (*User, error)
}

func NewStore() Store { return nil }
`)
	writeFile(t, root, "lib/helper.go", `package lib

func Helper() string { return "x" }
`)
	writeFile(t, root, "util_test.go", `package lib

func TestHelper(t *testing.T) {}
`)

	index, err := Parse(Options{Root: root, MaxTokens: 1024, SkipTests: true})
	if err != nil {
		t.Fatal(err)
	}

	var kinds []string
	for _, s := range index.Symbols {
		kinds = append(kinds, string(s.Kind))
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"struct", "interface", "func"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("symbols missing %q: %s", want, joined)
		}
	}
	if len(index.Symbols) != 4 { // User, Store, NewStore, Helper
		t.Fatalf("got %d symbols, want 4: %+v", len(index.Symbols), index.Symbols)
	}
}

func TestRenderRespectsTokenBudget(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module test\n")
	for i := 0; i < 30; i++ {
		writeFile(t, root, filepath.Join("pkg", "m"+string(rune('a'+i%26))+string(rune('0'+i/26)), "file.go"),
			"package p\n\nfunc F"+strings.Repeat("x", i+1)+"() string { return \"y\" }\n")
	}

	block, err := Build(Options{Root: root, MaxTokens: 64, Encoding: tokens.EncodingCl100kBase})
	if err != nil {
		t.Fatal(err)
	}
	if block == "" {
		t.Fatal("expected a non-empty map")
	}
	count := tokens.Count(block, tokens.EncodingCl100kBase).Count
	if count > 64 {
		t.Fatalf("map used %d tokens, want <= 64", count)
	}
	if !strings.Contains(block, "## RepoMap") {
		t.Fatalf("map missing header: %s", block)
	}
}

func TestRankFilesOrdersByDependency(t *testing.T) {
	root := t.TempDir()
	// lib.go defines Core; main.go references it; main is referenced by tests.
	writeFile(t, root, "lib.go", "package app\n\nfunc Core() string { return \"core\" }\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() { app.Core() }\n")

	index, err := Parse(Options{Root: root, MaxTokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	scores := rankFiles(index, map[string]bool{})
	if len(scores) != 2 {
		t.Fatalf("got %d files, want 2", len(scores))
	}
	// lib.go is referenced (in-degree 1), main.go references but is not
	// referenced, so lib.go should rank higher.
	libIdx := index.FileIdx["lib.go"]
	mainIdx := index.FileIdx["main.go"]
	if scores[libIdx] <= scores[mainIdx] {
		t.Fatalf("lib.go score %v not above main.go score %v", scores[libIdx], scores[mainIdx])
	}
}

func TestRenderAppliesPromptMultipliers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "lib.go", "package app\n\nfunc Core() string { return \"core\" }\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() { app.Core() }\n")

	block := RenderString(t, root, "please fix Core()")
	coreLine := "lib.go:3 func Core"
	if !strings.Contains(block, coreLine) {
		t.Fatalf("prompt-weighted symbol missing from map:\n%s", block)
	}
}

func RenderString(t *testing.T, root, prompt string) string {
	t.Helper()
	index, err := Parse(Options{Root: root, MaxTokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return Render(index, Options{Root: root, Prompt: prompt, MaxTokens: 1024})
}

func TestRankFilesAppliesActiveFileMultiplier(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "dep.go", "package app\n\nfunc Core() string { return \"core\" }\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() { app.Core() }\n")
	writeFile(t, root, "util.go", "package app\n\nfunc Util() int { return 1 }\n")

	index, err := Parse(Options{Root: root, MaxTokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	// main.go references dep.go, so dep.go carries the most authority; util.go
	// is a leaf and ranks last.
	base := rankFiles(index, map[string]bool{})
	depIdx := index.FileIdx["dep.go"]
	utilIdx := index.FileIdx["util.go"]
	if base[depIdx] <= base[utilIdx] {
		t.Fatalf("dep.go %v not above util.go %v", base[depIdx], base[utilIdx])
	}

	// Marking util.go active applies a 50x multiplier that flips the order.
	active := rankFiles(index, map[string]bool{"util.go": true})
	if active[utilIdx] <= active[depIdx] {
		t.Fatalf("active util.go %v not above dep.go %v", active[utilIdx], active[depIdx])
	}
	if active[utilIdx] < 50*base[utilIdx] {
		t.Fatalf("active multiplier not applied: %v < 50*%v", active[utilIdx], base[utilIdx])
	}
}

func TestRenderPromptMentionOutranksHigherAuthority(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "dep.go", "package app\n\nfunc Core() string { return \"core\" }\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() { app.Core() }\n")
	writeFile(t, root, "util.go", "package app\n\nfunc Util() int { return 1 }\n")

	index, err := Parse(Options{Root: root, MaxTokens: 1024})
	if err != nil {
		t.Fatal(err)
	}

	// Without a prompt, the highest-authority symbol wins.
	plain := Render(index, Options{Root: root, MaxTokens: 1000})
	if !strings.Contains(plain, "dep.go:3 func Core") {
		t.Fatalf("high-authority symbol missing from unweighted map:\n%s", plain)
	}

	// Naming Util in the prompt applies a 10x symbol multiplier that promotes
	// it above Core, so Util must rank ahead of the higher-authority symbol.
	tight := Render(index, Options{Root: root, Prompt: "inspect Util()", MaxTokens: 140})
	utilPos := strings.Index(tight, "util.go:3 func Util")
	corePos := strings.Index(tight, "dep.go:3 func Core")
	if utilPos < 0 {
		t.Fatalf("prompt-weighted symbol missing from map:\n%s", tight)
	}
	if corePos >= 0 && utilPos > corePos {
		t.Fatalf("prompted symbol ranked below the unmentioned higher-authority one:\n%s", tight)
	}
}
