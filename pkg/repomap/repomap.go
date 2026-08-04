// Package repomap builds a compact, token-bounded structural skeleton of a
// repository: symbol definitions (functions, types, structs, interfaces) plus
// a PageRank over the symbol reference graph. The map is meant to replace
// brute-force file dumps in the LLM system prompt.
//
// The parser is pure Go (go/parser for .go files, line-regex fallbacks for
// common scripting languages) so the package builds with CGO_ENABLED=0; it
// intentionally avoids tree-sitter C bindings.
package repomap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"rick/internal/tokens"
)

// Kind is a coarse symbol category used in the serialized map.
type Kind string

// Symbol kinds.
const (
	KindFunc      Kind = "func"
	KindMethod    Kind = "method"
	KindStruct    Kind = "struct"
	KindInterface Kind = "interface"
	KindType      Kind = "type"
	KindConst     Kind = "const"
	KindVar       Kind = "var"
	KindClass     Kind = "class"
	KindDef       Kind = "def"
)

// Symbol is one named definition extracted from a source file.
type Symbol struct {
	Name string
	Kind Kind
	File string // relative POSIX path from the repo root
	Line int
}

// Key uniquely identifies a symbol inside a file (name is file-local).
func (s Symbol) Key() string { return s.File + ":" + s.Name }

// DefaultMaxTokens is the serialized map budget used when none is supplied.
const DefaultMaxTokens = 1024

// Options configures a map build.
type Options struct {
	// Root is the repository directory to scan.
	Root string
	// Prompt is the active chat prompt; identifiers and paths it mentions
	// are weighted 10x and 50x respectively.
	Prompt string
	// MaxTokens bounds the serialized map. Zero means DefaultMaxTokens.
	MaxTokens int
	// Encoding selects the exact tokenizer used for the budget. Zero means
	// EncodingCl100kBase.
	Encoding tokens.Encoding
	// SkipDirs are extra directory base-names to ignore (in addition to the
	// built-in noise list).
	SkipDirs []string
	// SkipTests excludes *_test.go files.
	SkipTests bool
	// MaxFiles caps how many source files are parsed.
	MaxFiles int
	// MaxScan bounds how long the tree walk may run; a directory that cannot
	// be scanned within the budget is skipped entirely. Zero means
	// DefaultMaxScan (2s).
	MaxScan time.Duration
}

// DefaultMaxScan bounds a RepoMap tree walk so a huge or pathological
// directory never blocks a turn.
const DefaultMaxScan = time.Second

// errScanTimeout is returned when the tree walk exceeds the scan budget.
var errScanTimeout = errors.New("repomap: scan exceeded time budget")

// Index is an immutable parsed repository: symbols, file list, and the
// reference graph edges between files. It is safe to share between builds.
type Index struct {
	Root      string
	Symbols   []Symbol
	Files     []string            // relative paths, index-aligned with FileIdx
	FileIdx   map[string]int      // relative path -> Files index
	Refs      map[string][]string // file path -> referenced symbol names
	SymbolIdx map[string]int      // symbol name -> first Symbol index (for prompt matching)
	FileMTime map[string]int64    // relative path -> mod time at parse time
}

// Build parses Root and renders a budget-bounded RepoMap block. Directories
// that do not look like a code project are skipped (no manifest file such as
// go.mod or package.json, no .git, and fewer than three top-level source
// files), and a scan that exceeds the time budget is abandoned.
func Build(opts Options) (string, error) {
	if !LooksLikeProject(opts.Root) || isHomeRoot(opts.Root) {
		return "", nil
	}
	index, err := Parse(opts)
	if err != nil {
		return "", err
	}
	if len(index.Symbols) == 0 {
		return "", nil
	}
	return Render(index, opts), nil
}

// isHomeRoot reports whether root is the user's home directory, which is a
// directory of many small projects and never a useful single RepoMap.
func isHomeRoot(root string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return false
	}
	return strings.EqualFold(absRoot, absHome)
}

// Parse walks Root and extracts symbols plus the reference graph. A scan that
// exceeds the time budget is abandoned. Parse deliberately skips the
// project-marker check (that lives in Build) so callers that already know the
// root is a project can parse directly.
func Parse(opts Options) (*Index, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("repomap: root is required")
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = DefaultMaxTokens
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 4000
	}
	if opts.MaxScan <= 0 {
		opts.MaxScan = DefaultMaxScan
	}
	walker := &walker{root: opts.Root, skipDirs: opts.SkipDirs, skipTests: opts.SkipTests, maxFiles: opts.MaxFiles, maxScan: opts.MaxScan}
	if err := walker.walk(); err != nil {
		return nil, err
	}

	index := &Index{
		Root:      opts.Root,
		Files:     walker.files,
		FileIdx:   walker.fileIdx,
		Refs:      walker.refs,
		SymbolIdx: map[string]int{},
		FileMTime: walker.fileMTime,
	}
	for _, symbol := range walker.symbols {
		index.Symbols = append(index.Symbols, symbol)
		if _, ok := index.SymbolIdx[symbol.Name]; !ok {
			index.SymbolIdx[symbol.Name] = len(index.Symbols) - 1
		}
	}
	return index, nil
}

// Render serializes the top-ranked symbols of index into a compact block no
// larger than opts.MaxTokens (or DefaultMaxTokens).
func Render(index *Index, opts Options) string {
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = DefaultMaxTokens
	}
	encoding := opts.Encoding
	if encoding == "" {
		encoding = tokens.EncodingCl100kBase
	}

	activeFiles := activeFileSet(index.Files, opts.Prompt)
	mentioned := mentionedIdentifiers(index, opts.Prompt)

	fileScore := rankFiles(index, activeFiles)
	symbolScore := make([]float64, len(index.Symbols))
	for i, symbol := range index.Symbols {
		multiplier := 1.0
		if mentioned[symbol.Name] {
			multiplier *= 10
		}
		if activeFiles[symbol.File] {
			multiplier *= 50
		}
		fileIdx, ok := index.FileIdx[symbol.File]
		if !ok {
			fileIdx = 0
		}
		symbolScore[i] = fileScore[fileIdx] * multiplier
	}

	order := make([]int, len(index.Symbols))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		if symbolScore[order[a]] != symbolScore[order[b]] {
			return symbolScore[order[a]] > symbolScore[order[b]]
		}
		if index.Symbols[order[a]].File != index.Symbols[order[b]].File {
			return index.Symbols[order[a]].File < index.Symbols[order[b]].File
		}
		return index.Symbols[order[a]].Line < index.Symbols[order[b]].Line
	})

	header := fmt.Sprintf("## RepoMap\nStructural skeleton of %d symbols across %d files (PageRank-ranked; identifiers in your request get 10x weight, files already in play 50x).\n",
		len(index.Symbols), len(index.Files))

	var buf bytes.Buffer
	buf.WriteString(header)
	used := tokens.Count(header, encoding).Count
	for _, i := range order {
		symbol := index.Symbols[i]
		line := fmt.Sprintf("%s:%d %s %s\n", symbol.File, symbol.Line, symbol.Kind, symbol.Name)
		cost := tokens.Count(line, encoding).Count
		if used+cost > opts.MaxTokens {
			break
		}
		buf.WriteString(line)
		used += cost
	}
	return strings.TrimSpace(buf.String())
}

// LooksLikeProject reports whether a directory is worth mapping: it has a
// project manifest (go.mod, package.json, Cargo.toml, a solution/project file,
// a Makefile, ...) or a .git dir, or at least three source files directly
// inside it. This prevents mapping home directories and other non-project
// trees.
func LooksLikeProject(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	sourceCount := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if name == ".git" {
				return true
			}
			continue
		}
		if isProjectManifest(name) {
			return true
		}
		if isSourceFile(name) {
			sourceCount++
		}
	}
	return sourceCount >= 3
}

// isProjectManifest reports whether a file name is a common project manifest.
func isProjectManifest(name string) bool {
	lower := strings.ToLower(name)
	if projectManifestNames[lower] {
		return true
	}
	return strings.HasSuffix(lower, ".csproj") || strings.HasSuffix(lower, ".sln") ||
		strings.HasSuffix(lower, ".xcodeproj") || strings.HasSuffix(lower, ".kproj")
}

var projectManifestNames = map[string]bool{
	"go.mod": true, "go.work": true, "package.json": true, "pnpm-lock.yaml": true,
	"yarn.lock": true, "cargo.toml": true, "pyproject.toml": true, "setup.py": true,
	"setup.cfg": true, "requirements.txt": true, "pom.xml": true, "build.gradle": true,
	"makefile": true, "cmakelists.txt": true, "meson.build": true, "mix.exs": true,
	"composer.json": true, "gemfile": true, "gemspec": true, "pubspec.yaml": true,
	"workspace.json": true, "deno.json": true, "bun.lockb": true, "tsconfig.json": true,
}

// activeFileSet marks files whose path or base name appears in the prompt.
func activeFileSet(files []string, prompt string) map[string]bool {
	set := map[string]bool{}
	if prompt == "" {
		return set
	}
	words := promptWords(prompt)
	for _, file := range files {
		base := file
		if i := strings.LastIndex(file, "/"); i >= 0 {
			base = file[i+1:]
		}
		for _, word := range words {
			if word == file || word == base || strings.HasSuffix(word, "/"+file) {
				set[file] = true
				break
			}
		}
	}
	return set
}

// mentionedIdentifiers returns the set of defined symbol names that appear as
// identifiers in the prompt.
func mentionedIdentifiers(index *Index, prompt string) map[string]bool {
	set := map[string]bool{}
	if prompt == "" {
		return set
	}
	for _, word := range promptWords(prompt) {
		if _, ok := index.SymbolIdx[word]; ok {
			set[word] = true
		}
	}
	return set
}

func promptWords(prompt string) []string {
	var words []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	for _, r := range prompt {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '/' || r == '.' || r == '-' {
			current.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words
}
