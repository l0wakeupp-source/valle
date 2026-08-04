package repomap

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var defaultSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".bzr": true,
	"node_modules": true, "vendor": true, "third_party": true, "thirdparty": true,
	"tmp": true, "temp": true, "dist": true, "build": true, "out": true, "target": true,
	"_research": true, ".rick": true, ".cache": true, ".venv": true, "venv": true,
	"__pycache__": true, ".idea": true, ".vscode": true, "coverage": true, ".direnv": true,
	// Home-directory noise that is never a workspace worth mapping.
	"AppData": true, ".config": true, ".local": true, ".ssh": true, ".gnupg": true,
	"Desktop": true, "Documents": true, "Downloads": true, "Pictures": true,
	"Music": true, "Videos": true, "OneDrive": true, "Library": true, ".wsl": true,
}

const maxSourceFileBytes = 1 << 20

type walker struct {
	root      string
	skipDirs  []string
	skipTests bool
	maxFiles  int
	maxScan   time.Duration
	deadline  time.Time

	files     []string
	fileIdx   map[string]int
	fileMTime map[string]int64
	refs      map[string][]string
	symbols   []Symbol
	count     int
}

func (w *walker) walk() error {
	w.fileIdx = map[string]int{}
	w.fileMTime = map[string]int64{}
	w.refs = map[string][]string{}
	if w.maxScan > 0 {
		w.deadline = time.Now().Add(w.maxScan)
	}

	skip := make(map[string]bool, len(defaultSkipDirs)+len(w.skipDirs))
	for name := range defaultSkipDirs {
		skip[name] = true
	}
	for _, name := range w.skipDirs {
		skip[name] = true
	}

	return filepath.WalkDir(w.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !w.deadline.IsZero() && time.Now().After(w.deadline) {
			return errScanTimeout
		}
		if entry.IsDir() {
			if path != w.root && (skip[entry.Name()] || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if w.count >= w.maxFiles {
			return nil
		}
		rel, err := filepath.Rel(w.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".") || strings.Contains(rel, "/.") {
			return nil
		}
		if !isSourceFile(rel) {
			return nil
		}
		if w.skipTests && strings.HasSuffix(rel, "_test.go") {
			return nil
		}

		info, err := entry.Info()
		if err != nil || info.Size() > maxSourceFileBytes {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		w.count++
		fileIdx := len(w.files)
		w.files = append(w.files, rel)
		w.fileIdx[rel] = fileIdx
		w.fileMTime[rel] = info.ModTime().UnixNano()

		if strings.HasSuffix(rel, ".go") {
			w.parseGo(rel, content)
		} else {
			w.parseRegex(rel, string(content))
		}
		return nil
	})
}

func isSourceFile(rel string) bool {
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".go", ".py", ".rs", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".luau", ".lua",
		".java", ".kt", ".rb", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".php":
		return true
	}
	return false
}

// parseGo extracts symbols and references with the exact Go AST parser.
func (w *walker) parseGo(rel string, content []byte) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, content, 0)
	if err != nil {
		w.parseRegex(rel, string(content))
		return
	}

	declNames := map[token.Pos]bool{}
	references := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncDecl:
			declNames[n.Name.Pos()] = true
			kind := KindFunc
			if n.Recv != nil && len(n.Recv.List) > 0 {
				kind = KindMethod
			}
			w.addSymbol(rel, fset.Position(n.Name.Pos()).Line, kind, n.Name.Name)
		case *ast.TypeSpec:
			declNames[n.Name.Pos()] = true
			var kind Kind = KindType
			switch n.Type.(type) {
			case *ast.StructType:
				kind = KindStruct
			case *ast.InterfaceType:
				kind = KindInterface
			}
			w.addSymbol(rel, fset.Position(n.Name.Pos()).Line, kind, n.Name.Name)
		case *ast.ValueSpec:
			for _, name := range n.Names {
				declNames[name.Pos()] = true
			}
		case *ast.Ident:
			if !declNames[n.Pos()] {
				references[n.Name] = true
			}
		}
		return true
	})
	// Second pass: package-level const/var declarations only. Function-local
	// declarations are noise for a structural map.
	packageLevel := map[*ast.GenDecl]bool{}
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && (gen.Tok == token.CONST || gen.Tok == token.VAR) {
			packageLevel[gen] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		gen, ok := node.(*ast.GenDecl)
		if !ok || !packageLevel[gen] {
			return true
		}
		kind := KindConst
		if gen.Tok == token.VAR {
			kind = KindVar
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				w.addSymbol(rel, fset.Position(name.Pos()).Line, kind, name.Name)
			}
		}
		return true
	})

	names := make([]string, 0, len(references))
	for name := range references {
		names = append(names, name)
	}
	sort.Strings(names)
	w.refs[rel] = names
}

// parseRegex is a light line-based extractor for non-Go languages. It is a
// structural skeleton only; exactness is not required.
func (w *walker) parseRegex(rel, content string) {
	ext := strings.ToLower(filepath.Ext(rel))
	patterns := regexPatterns(ext)
	if len(patterns) == 0 {
		return
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for lineIndex, line := range lines {
		for _, pattern := range patterns {
			match := pattern.re.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			name := match[len(match)-1]
			if name == "" {
				continue
			}
			w.addSymbol(rel, lineIndex+1, pattern.kind, name)
		}
	}
	w.refs[rel] = nil
}

type regexPattern struct {
	re   *regexp.Regexp
	kind Kind
}

func regexPatterns(ext string) []regexPattern {
	switch ext {
	case ".py":
		return []regexPattern{
			{regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`), KindDef},
			{regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`), KindClass},
		}
	case ".rs":
		return []regexPattern{
			{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?fn\s+([A-Za-z_]\w*)`), KindFunc},
			{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?struct\s+([A-Za-z_]\w*)`), KindStruct},
			{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+([A-Za-z_]\w*)`), KindType},
			{regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?trait\s+([A-Za-z_]\w*)`), KindInterface},
		}
	case ".ts", ".tsx", ".js", ".jsx", ".mjs":
		return []regexPattern{
			{regexp.MustCompile(`^\s*export\s+(?:default\s+)?function\s+([A-Za-z_$]\w*)`), KindFunc},
			{regexp.MustCompile(`^\s*export\s+(?:default\s+)?class\s+([A-Za-z_$]\w*)`), KindClass},
			{regexp.MustCompile(`^\s*export\s+(?:default\s+)?interface\s+([A-Za-z_$]\w*)`), KindInterface},
			{regexp.MustCompile(`^\s*export\s+(?:default\s+)?type\s+([A-Za-z_$]\w*)`), KindType},
			{regexp.MustCompile(`^\s*function\s+([A-Za-z_$]\w*)`), KindFunc},
			{regexp.MustCompile(`^\s*class\s+([A-Za-z_$]\w*)`), KindClass},
		}
	case ".luau", ".lua":
		return []regexPattern{
			{regexp.MustCompile(`^\s*local\s+function\s+([A-Za-z_]\w*)`), KindFunc},
			{regexp.MustCompile(`^\s*function\s+[A-Za-z_]\w*[.:]([A-Za-z_]\w*)\s*\(`), KindMethod},
			{regexp.MustCompile(`^\s*local\s+([A-Za-z_]\w*)\s*=`), KindVar},
		}
	case ".java", ".kt":
		return []regexPattern{
			{regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*(?:abstract\s+|final\s+|static\s+)*(class|interface|enum)\s+([A-Za-z_]\w*)`), KindType},
			{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?[\w<>\[\],\s]+\s+([A-Za-z_]\w*)\s*\(`), KindMethod},
		}
	case ".c", ".h", ".cc", ".cpp", ".hpp":
		return []regexPattern{
			{regexp.MustCompile(`^\s*(?:typedef\s+)?(?:struct|class|enum|union)\s+([A-Za-z_]\w*)`), KindStruct},
			{regexp.MustCompile(`^\s*(?:static\s+)?(?:inline\s+)?(?:[\w*]+\s+)+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{`), KindFunc},
		}
	case ".rb":
		return []regexPattern{
			{regexp.MustCompile(`^\s*class\s+([A-Za-z_:]\w*)`), KindClass},
			{regexp.MustCompile(`^\s*module\s+([A-Za-z_:]\w*)`), KindType},
			{regexp.MustCompile(`^\s*def\s+([A-Za-z_]\w*(?:[.!?]\w*)?)`), KindDef},
		}
	case ".cs":
		return []regexPattern{
			{regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*(?:static\s+|abstract\s+|sealed\s+|partial\s+)*(class|interface|enum|struct)\s+([A-Za-z_]\w*)`), KindType},
		}
	case ".php":
		return []regexPattern{
			{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?function\s+([A-Za-z_]\w*)`), KindFunc},
			{regexp.MustCompile(`^\s*(?:abstract\s+|final\s+)?class\s+([A-Za-z_]\w*)`), KindClass},
		}
	}
	return nil
}

func (w *walker) addSymbol(rel string, line int, kind Kind, name string) {
	w.symbols = append(w.symbols, Symbol{Name: name, Kind: kind, File: rel, Line: line})
}
