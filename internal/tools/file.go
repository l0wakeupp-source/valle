package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// ---------- shared file-state tracking ----------
//
// The model must read a file before editing it. We track read times so edit
// and write can refuse to clobber a file that changed underneath us.

var fileState struct {
	sync.Mutex
	readAt map[string]int64 // abs path -> mtime unix nano at read time
}

func init() { fileState.readAt = map[string]int64{} }

func markRead(path string) {
	if st, err := os.Stat(path); err == nil {
		fileState.Lock()
		fileState.readAt[path] = st.ModTime().UnixNano()
		fileState.Unlock()
	}
}

func wasRead(path string) bool {
	fileState.Lock()
	defer fileState.Unlock()
	_, ok := fileState.readAt[path]
	return ok
}

// ResetFileState clears read tracking (new session).
func ResetFileState() {
	fileState.Lock()
	fileState.readAt = map[string]int64{}
	fileState.Unlock()
}

func resolvePath(cwd, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return cwd
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

func relTo(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(p)
}

func isBinary(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	chunk := b[:n]
	if bytesIndexZero(chunk) {
		return true
	}
	return !utf8.Valid(chunk) && n > 64
}

func bytesIndexZero(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// ---------- read ----------

// ReadTool reads a file with line numbers and pagination.
type ReadTool struct {
	MaxBytes      int
	MaxInputBytes int
}

// Name implements Tool.
func (ReadTool) Name() string { return "read" }

// ReadOnly implements Tool.
func (ReadTool) ReadOnly() bool { return true }

// Description implements Tool.
func (ReadTool) Description() string {
	return "Read a file from the filesystem. Returns contents with 1-indexed line numbers " +
		"in the form 'NNNN|line'. Use offset/limit for large files. Always read a file " +
		"before editing it. Prefer this over 'cat' via bash."
}

// Schema implements Tool.
func (ReadTool) Schema() map[string]any {
	return obj(map[string]any{
		"path":   strProp("File path (absolute, or relative to the project root)."),
		"offset": numProp("1-indexed line to start from. Default 1."),
		"limit":  numProp("Maximum number of lines to read. Default 2000."),
	}, "path")
}

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// Run implements Tool.
func (t ReadTool) Run(_ context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a readArgs
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Path == "" {
		return Errf("path is required"), nil
	}
	p := resolvePath(tc.Cwd, a.Path)

	st, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			if sug := suggestSimilar(p); sug != "" {
				return Errf("file not found: %s\ndid you mean: %s", relTo(tc.Cwd, p), sug), nil
			}
			return Errf("file not found: %s", relTo(tc.Cwd, p)), nil
		}
		return Errf("%v", err), nil
	}
	if st.IsDir() {
		return Errf("%s is a directory; use glob or bash ls", relTo(tc.Cwd, p)), nil
	}

	maxBytes := t.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 400 << 10
	}
	inputLimit := t.MaxInputBytes
	if inputLimit <= 0 {
		inputLimit = defaultToolInputLimit
	}
	file, err := os.Open(p)
	if err != nil {
		return Errf("%v", err), nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(inputLimit)+1))
	if err != nil {
		return Errf("%v", err), nil
	}
	if len(data) > inputLimit {
		return Errf("%s exceeds the read input limit of %d bytes", relTo(tc.Cwd, p), inputLimit), nil
	}
	if isBinary(data) {
		return Errf("%s appears to be a binary file (%d bytes)", relTo(tc.Cwd, p), len(data)), nil
	}

	offset := a.Offset
	if offset < 1 {
		offset = 1
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 2000
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	total := len(lines)
	if offset > total {
		return Result{
			Output: fmt.Sprintf("<file %s has %d lines; offset %d is past the end>", relTo(tc.Cwd, p), total, offset),
			Title:  relTo(tc.Cwd, p),
		}, nil
	}
	end := offset - 1 + limit
	if end > total {
		end = total
	}

	var b strings.Builder
	written := 0
	for i := offset - 1; i < end; i++ {
		line := lines[i]
		if len(line) > 2000 {
			line = line[:2000] + " …<truncated>"
		}
		fmt.Fprintf(&b, "%5d|%s\n", i+1, line)
		written += len(line) + 7
		if written > maxBytes {
			fmt.Fprintf(&b, "\n<output truncated at %d bytes; continue with offset=%d>\n", maxBytes, i+2)
			end = i + 1
			break
		}
	}
	markRead(p)

	foot := ""
	if end < total {
		foot = fmt.Sprintf("\n<showing lines %d-%d of %d; continue with offset=%d>", offset, end, total, end+1)
	}
	return Result{
		Output: b.String() + foot,
		Title:  fmt.Sprintf("%s (%d lines)", relTo(tc.Cwd, p), total),
		Meta:   map[string]any{"path": p, "lines": total},
	}, nil
}

func suggestSimilar(p string) string {
	dir := filepath.Dir(p)
	base := strings.ToLower(filepath.Base(p))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best []string
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if strings.Contains(n, strings.TrimSuffix(base, filepath.Ext(base))) || strings.Contains(base, n) {
			best = append(best, e.Name())
		}
	}
	sort.Strings(best)
	if len(best) > 3 {
		best = best[:3]
	}
	return strings.Join(best, ", ")
}

// ---------- write ----------

// WriteTool creates or overwrites a file.
type WriteTool struct{}

// Name implements Tool.
func (WriteTool) Name() string { return "write" }

// ReadOnly implements Tool.
func (WriteTool) ReadOnly() bool { return false }

// Description implements Tool.
func (WriteTool) Description() string {
	return "Write content to a file, creating parent directories as needed. " +
		"Overwrites the whole file: for targeted changes prefer 'edit'. " +
		"If the file already exists you must 'read' it first."
}

// Schema implements Tool.
func (WriteTool) Schema() map[string]any {
	return obj(map[string]any{
		"path":    strProp("File path to write."),
		"content": strProp("Full file content."),
	}, "path", "content")
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Run implements Tool.
func (WriteTool) Run(_ context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a writeArgs
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Path == "" {
		return Errf("path is required"), nil
	}
	p := resolvePath(tc.Cwd, a.Path)

	old := ""
	existed := false
	if b, err := os.ReadFile(p); err == nil {
		existed = true
		old = string(b)
		if !wasRead(p) {
			return Errf("refusing to overwrite %s: read it first", relTo(tc.Cwd, p)), nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Errf("%v", err), nil
	}
	if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
		return Errf("%v", err), nil
	}
	markRead(p)

	verb := "created"
	if existed {
		verb = "updated"
	}
	nl := strings.Count(a.Content, "\n") + 1
	return Result{
		Output: fmt.Sprintf("%s %s (%d lines, %d bytes)", verb, relTo(tc.Cwd, p), nl, len(a.Content)),
		Title:  fmt.Sprintf("write %s", relTo(tc.Cwd, p)),
		Meta:   map[string]any{"path": p, "old": old, "new": a.Content, "created": !existed},
	}, nil
}

// ---------- edit ----------

// EditTool performs exact string replacement — the primary edit mechanism.
type EditTool struct{}

// Name implements Tool.
func (EditTool) Name() string { return "edit" }

// ReadOnly implements Tool.
func (EditTool) ReadOnly() bool { return false }

// Description implements Tool.
func (EditTool) Description() string {
	return "Replace an exact string in a file. old_string must appear EXACTLY once " +
		"unless replace_all is true — include enough surrounding context to make it " +
		"unique. Read the file first. Use an empty new_string to delete the match."
}

// Schema implements Tool.
func (EditTool) Schema() map[string]any {
	return obj(map[string]any{
		"path":        strProp("File path to edit."),
		"old_string":  strProp("Exact text to find, including indentation."),
		"new_string":  strProp("Replacement text. Empty string deletes the match."),
		"replace_all": boolProp("Replace every occurrence instead of requiring uniqueness."),
	}, "path", "old_string", "new_string")
}

type editArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// Run implements Tool.
func (EditTool) Run(_ context.Context, tc Context, in json.RawMessage) (Result, error) {
	var a editArgs
	if err := decodeArgs(in, &a); err != nil {
		return Errf("invalid arguments: %v", err), nil
	}
	if a.Path == "" {
		return Errf("path is required"), nil
	}
	if a.OldString == a.NewString {
		return Errf("old_string and new_string are identical"), nil
	}
	p := resolvePath(tc.Cwd, a.Path)

	raw, err := os.ReadFile(p)
	if err != nil {
		return Errf("cannot read %s: %v", relTo(tc.Cwd, p), err), nil
	}
	if !wasRead(p) {
		return Errf("refusing to edit %s: read it first", relTo(tc.Cwd, p)), nil
	}
	content := string(raw)

	newContent, n, err := applyReplace(content, a.OldString, a.NewString, a.ReplaceAll)
	if err != nil {
		return Errf("%v", err), nil
	}
	if err := os.WriteFile(p, []byte(newContent), 0o644); err != nil {
		return Errf("%v", err), nil
	}
	markRead(p)

	return Result{
		Output: fmt.Sprintf("edited %s (%d replacement(s))\n\n%s",
			relTo(tc.Cwd, p), n, UnifiedDiff(relTo(tc.Cwd, p), content, newContent, 3)),
		Title: fmt.Sprintf("edit %s", relTo(tc.Cwd, p)),
		Meta:  map[string]any{"path": p, "old": content, "new": newContent, "count": n},
	}, nil
}

// applyReplace handles exact match then a small set of whitespace-tolerant
// fallbacks (trailing whitespace, CRLF, leading-indent shift).
func applyReplace(content, old, new string, all bool) (string, int, error) {
	if old == "" {
		return "", 0, fmt.Errorf("old_string must not be empty (use 'write' to create a file)")
	}
	count := strings.Count(content, old)
	if count == 0 {
		if alt, ok := fuzzyFind(content, old); ok {
			old = alt
			count = strings.Count(content, old)
		}
	}
	switch {
	case count == 0:
		return "", 0, fmt.Errorf("old_string not found in file; re-read the file and copy the exact text")
	case count > 1 && !all:
		return "", 0, fmt.Errorf("old_string appears %d times; add surrounding context to make it unique, or set replace_all", count)
	}
	if all {
		return strings.ReplaceAll(content, old, new), count, nil
	}
	return strings.Replace(content, old, new, 1), 1, nil
}

// fuzzyFind tries tolerant variants of old and returns the literal substring
// of content that should be replaced.
func fuzzyFind(content, old string) (string, bool) {
	// 1. CRLF normalisation.
	if strings.Contains(content, "\r\n") {
		if v := strings.ReplaceAll(old, "\n", "\r\n"); strings.Contains(content, v) {
			return v, true
		}
	}
	// 2. Trailing whitespace differences, line by line.
	oldLines := strings.Split(old, "\n")
	trimmed := make([]string, len(oldLines))
	for i, l := range oldLines {
		trimmed[i] = strings.TrimRight(l, " \t")
	}
	contentLines := strings.Split(content, "\n")
	for i := 0; i+len(oldLines) <= len(contentLines); i++ {
		match := true
		for j := range oldLines {
			if strings.TrimRight(contentLines[i+j], " \t") != trimmed[j] {
				match = false
				break
			}
		}
		if match {
			return strings.Join(contentLines[i:i+len(oldLines)], "\n"), true
		}
	}
	// 3. Indentation shift: compare after removing common leading whitespace.
	deindent := func(ls []string) []string {
		out := make([]string, len(ls))
		for i, l := range ls {
			out[i] = strings.TrimLeft(l, " \t")
		}
		return out
	}
	oldD := deindent(oldLines)
	for i := 0; i+len(oldLines) <= len(contentLines); i++ {
		cD := deindent(contentLines[i : i+len(oldLines)])
		match := true
		for j := range oldD {
			if strings.TrimRight(cD[j], " \t") != strings.TrimRight(oldD[j], " \t") {
				match = false
				break
			}
		}
		if match {
			return strings.Join(contentLines[i:i+len(oldLines)], "\n"), true
		}
	}
	return "", false
}
