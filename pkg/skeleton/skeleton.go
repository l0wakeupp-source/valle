// Package skeleton produces token-cheap AST skeletons of source files: every
// top-level declaration keeps its signature, but bodies collapse to a marker
// unless a target symbol is named, in which case that one body is kept in
// full. It is built on the pure-Go tree-sitter port (gotreesitter), so it
// compiles with CGO_ENABLED=0 and never shells out.
package skeleton

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// MaxBytes caps a serialized skeleton. Callers that need more should read the
// file directly instead.
const MaxBytes = 32 << 10

// collapseMarker is inserted where a non-target body was removed.
const collapseMarker = "... implementation hidden"

// maxHeaderBytes caps package/import headers printed verbatim; anything longer
// is truncated so a huge import block cannot blow the token budget.
const maxHeaderBytes = 2000

// languageSpec describes how one tree-sitter grammar is folded.
type languageSpec struct {
	load   func() *gotreesitter.Language
	exts   []string
	decls  map[string]bool // top-level declarations whose bodies fold
	header map[string]bool // small structural nodes printed verbatim
	unwrap map[string]bool // wrappers whose first named child is the real decl
	// commentPrefix is the marker comment syntax used by the language.
	commentPrefix string
}

// Skeleton returns an AST skeleton of the file at path. When target is
// non-empty, the matching declaration keeps its full body and every other
// body collapses to a marker; an empty target collapses all bodies. A parse
// failure or unsupported extension returns an error so callers can fall back
// to a plain read.
func Skeleton(path, target string) (string, error) {
	spec, ok := languageFor(path)
	if !ok {
		return "", fmt.Errorf("skeleton: no parser for %q", filepath.Ext(path))
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	parser := gotreesitter.NewParser(spec.load())
	tree, err := parser.Parse(source)
	if err != nil {
		return "", fmt.Errorf("skeleton: parse %s: %w", path, err)
	}
	if tree == nil || tree.RootNode() == nil {
		return "", fmt.Errorf("skeleton: empty parse of %s", path)
	}

	lang := tree.Language()
	root := tree.RootNode()
	found := target == ""
	var b strings.Builder
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		nodeType := node.Type(lang)
		if spec.header[nodeType] {
			text := node.Text(source)
			if len(text) > maxHeaderBytes {
				text = text[:maxHeaderBytes] + "\n… (header truncated)"
			}
			writeLine(&b, text)
			continue
		}
		if spec.decls[nodeType] || spec.unwrap[nodeType] {
			if fold(&b, node, &spec, lang, source, target, 12) {
				found = true
			}
		}
	}
	if !found {
		return "", fmt.Errorf("skeleton: symbol %q not found in %s", target, path)
	}
	if b.Len() > MaxBytes {
		return "", fmt.Errorf("skeleton: %s exceeds %d bytes even folded", path, MaxBytes)
	}
	return strings.TrimSpace(b.String()) + "\n", nil
}

// fold serializes one declaration. The requested target keeps its full body;
// every other declaration prints its signature and a collapse marker. When the
// target is nested (a Python method inside a class), the enclosing declarations
// keep their signatures and the fold descends into their bodies so only the
// path to the target is expanded. It reports whether the target was resolved.
func fold(b *strings.Builder, node *gotreesitter.Node, spec *languageSpec, lang *gotreesitter.Language, source []byte, target string, depth int) bool {
	if node == nil || depth <= 0 {
		return false
	}
	nodeType := node.Type(lang)
	if spec.unwrap[nodeType] {
		return fold(b, node.NamedChild(0), spec, lang, source, target, depth)
	}
	if !spec.decls[nodeType] {
		// Structural wrappers (blocks, statements) are skipped unless the
		// target lies beneath them, in which case we keep drilling.
		if target != "" && containsDeclNamed(node, lang, source, spec, target, 8) {
			matched := false
			for i := 0; i < node.NamedChildCount(); i++ {
				if fold(b, node.NamedChild(i), spec, lang, source, target, depth-1) {
					matched = true
				}
			}
			return matched
		}
		return false
	}

	start := int(node.StartByte())
	end := int(node.EndByte())
	isTarget := target != "" && declName(node, lang, source) == target
	if isTarget {
		writeLine(b, strings.TrimRight(string(source[start:end]), " \t\r\n"))
		return true
	}

	bodyStart, hasBody := bodyRange(node, lang, source)
	if !hasBody {
		// No foldable body (constant, enum, bare signature): print verbatim.
		writeLine(b, strings.TrimRight(string(source[start:end]), " \t\r\n"))
		return false
	}
	signature := strings.TrimRight(string(source[start:bodyStart]), " \t\r\n")
	if target == "" || !containsDeclNamed(node, lang, source, spec, target, 8) {
		writeLine(b, signature)
		writeLine(b, spec.commentPrefix+" "+collapseMarker)
		return false
	}

	// The target is nested: print the enclosing signature, then fold the
	// declaration's children so sibling bodies still collapse.
	writeLine(b, signature)
	matched := false
	for i := 0; i < node.NamedChildCount(); i++ {
		if fold(b, node.NamedChild(i), spec, lang, source, target, depth-1) {
			matched = true
		}
	}
	if !matched {
		writeLine(b, spec.commentPrefix+" "+collapseMarker)
	}
	return matched
}

// containsDeclNamed reports whether any declaration under node (not node
// itself) is named target.
func containsDeclNamed(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte, spec *languageSpec, target string, depth int) bool {
	if node == nil || depth <= 0 {
		return false
	}
	if spec.decls[node.Type(lang)] && declName(node, lang, source) == target {
		return true
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if containsDeclNamed(node.NamedChild(i), lang, source, spec, target, depth-1) {
			return true
		}
	}
	return false
}

// bodyRange returns the byte offset where node's body begins and whether a
// foldable body exists.
func bodyRange(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) (int, bool) {
	start := int(node.StartByte())
	if body := firstDescendant(node, lang, bodyLike, 6); body != nil {
		return int(body.StartByte()), true
	}
	// Brace-bodied grammars (Go interfaces, TS types) without a recognized
	// body node: cut at the first opening brace so the signature stays clean.
	end := int(node.EndByte())
	for i := start; i < end; i++ {
		if source[i] == '{' {
			return i, true
		}
	}
	return start, false
}

// firstDescendant returns the first node under node (depth-first, document
// order) whose type is in kinds, or nil. depth bounds recursion so a single
// declaration cannot scan the whole tree.
func firstDescendant(node *gotreesitter.Node, lang *gotreesitter.Language, kinds map[string]bool, depth int) *gotreesitter.Node {
	if node == nil {
		return nil
	}
	if kinds[node.Type(lang)] {
		return node
	}
	if depth <= 0 {
		return nil
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if found := firstDescendant(node.NamedChild(i), lang, kinds, depth-1); found != nil {
			return found
		}
	}
	return nil
}

// declName extracts the declaration's identifier via the grammar's "name"
// field, falling back to the first identifier-like token for grammars that
// name declarations through a declarator.
func declName(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte) string {
	if named := firstNamedField(node, lang, "name", 6); named != nil {
		return named.Text(source)
	}
	if id := firstDescendant(node, lang, identifierLike, 4); id != nil {
		return id.Text(source)
	}
	return ""
}

// firstNamedField returns the first descendant that carries the given field
// name in the grammar.
func firstNamedField(node *gotreesitter.Node, lang *gotreesitter.Language, field string, depth int) *gotreesitter.Node {
	if node == nil {
		return nil
	}
	if child := node.ChildByFieldName(field, lang); child != nil {
		return child
	}
	if depth <= 0 {
		return nil
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if found := firstNamedField(node.NamedChild(i), lang, field, depth-1); found != nil {
			return found
		}
	}
	return nil
}

func writeLine(b *strings.Builder, text string) {
	if text == "" {
		return
	}
	b.WriteString(text)
	b.WriteByte('\n')
}

var bodyLike = map[string]bool{
	"block": true, "statement_block": true, "compound_statement": true,
	"class_body": true, "field_declaration_list": true, "method_spec_list": true,
	"declaration_list": true, "enum_body": true, "body": true, "interface_body": true,
	"struct_body": true, "method_body": true, "function_body": true,
}

var identifierLike = map[string]bool{
	"identifier": true, "type_identifier": true, "field_identifier": true,
	"function_name": true, "method_name": true,
}

func languageFor(path string) (languageSpec, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	for _, spec := range specs {
		for _, candidate := range spec.exts {
			if ext == candidate {
				return spec, true
			}
		}
	}
	return languageSpec{}, false
}

var specs = []languageSpec{
	{
		load: grammars.GoLanguage,
		exts: []string{".go"},
		decls: map[string]bool{
			"function_declaration": true, "method_declaration": true,
			"type_declaration": true, "var_declaration": true, "const_declaration": true,
		},
		header:        map[string]bool{"package_clause": true, "import_declaration": true},
		commentPrefix: "//",
	},
	{
		load: grammars.PythonLanguage,
		exts: []string{".py"},
		decls: map[string]bool{
			"function_definition": true, "class_definition": true, "decorated_definition": true,
			"type_alias_statement": true,
		},
		unwrap:        map[string]bool{"decorated_definition": true},
		header:        map[string]bool{"import_statement": true, "from_import_statement": true},
		commentPrefix: "#",
	},
	{
		load: grammars.TypescriptLanguage,
		exts: []string{".ts", ".mts", ".cts"},
		decls: map[string]bool{
			"function_declaration": true, "class_declaration": true, "interface_declaration": true,
			"type_alias_declaration": true, "enum_declaration": true, "lexical_declaration": true,
			"abstract_class_declaration": true, "module": true, "function_signature": true,
		},
		unwrap:        map[string]bool{"export_statement": true},
		header:        map[string]bool{"import_statement": true},
		commentPrefix: "//",
	},
	{
		load: grammars.TsxLanguage,
		exts: []string{".tsx"},
		decls: map[string]bool{
			"function_declaration": true, "class_declaration": true, "interface_declaration": true,
			"type_alias_declaration": true, "enum_declaration": true, "lexical_declaration": true,
			"abstract_class_declaration": true, "function_signature": true,
		},
		unwrap:        map[string]bool{"export_statement": true},
		header:        map[string]bool{"import_statement": true},
		commentPrefix: "//",
	},
	{
		load: grammars.JavascriptLanguage,
		exts: []string{".js", ".mjs", ".jsx", ".cjs"},
		decls: map[string]bool{
			"function_declaration": true, "class_declaration": true, "lexical_declaration": true,
			"variable_declaration": true,
		},
		unwrap:        map[string]bool{"export_statement": true},
		header:        map[string]bool{"import_statement": true},
		commentPrefix: "//",
	},
	{
		load: grammars.RustLanguage,
		exts: []string{".rs"},
		decls: map[string]bool{
			"function_item": true, "struct_item": true, "enum_item": true, "impl_item": true,
			"trait_item": true, "mod_item": true, "type_item": true, "const_item": true,
			"static_item": true, "macro_definition": true,
		},
		header:        map[string]bool{"use_declaration": true, "extern_crate_declaration": true},
		commentPrefix: "//",
	},
	{
		load: grammars.LuaLanguage,
		exts: []string{".lua"},
		decls: map[string]bool{
			"function_declaration": true, "local_function": true, "variable_declaration": true,
		},
		header:        map[string]bool{},
		commentPrefix: "--",
	},
	{
		load: grammars.JavaLanguage,
		exts: []string{".java"},
		decls: map[string]bool{
			"class_declaration": true, "interface_declaration": true, "enum_declaration": true,
			"record_declaration": true, "method_declaration": true, "annotation_type_declaration": true,
		},
		header:        map[string]bool{"package_declaration": true, "import_declaration": true},
		commentPrefix: "//",
	},
	{
		load: grammars.CLanguage,
		exts: []string{".c", ".h"},
		decls: map[string]bool{
			"function_definition": true, "declaration": true, "struct_specifier": true,
			"union_specifier": true, "enum_specifier": true,
		},
		header:        map[string]bool{},
		commentPrefix: "//",
	},
	{
		load: grammars.CppLanguage,
		exts: []string{".cc", ".cpp", ".hpp", ".hh", ".cxx", ".hxx"},
		decls: map[string]bool{
			"function_definition": true, "declaration": true, "struct_specifier": true,
			"class_specifier": true, "union_specifier": true, "enum_specifier": true,
			"namespace_definition": true,
		},
		header:        map[string]bool{"preproc_include": true},
		commentPrefix: "//",
	},
	{
		load: grammars.RubyLanguage,
		exts: []string{".rb"},
		decls: map[string]bool{
			"method": true, "singleton_method": true, "class": true, "module": true,
		},
		header:        map[string]bool{"call": true},
		commentPrefix: "#",
	},
	{
		load: grammars.PhpLanguage,
		exts: []string{".php"},
		decls: map[string]bool{
			"function_definition": true, "class_declaration": true, "interface_declaration": true,
			"trait_declaration": true, "enum_declaration": true,
		},
		header:        map[string]bool{},
		commentPrefix: "//",
	},
	{
		load: grammars.CSharpLanguage,
		exts: []string{".cs"},
		decls: map[string]bool{
			"class_declaration": true, "interface_declaration": true, "struct_declaration": true,
			"enum_declaration": true, "record_declaration": true, "method_declaration": true,
			"namespace_declaration": true,
		},
		header:        map[string]bool{"using_directive": true},
		commentPrefix: "//",
	},
}
