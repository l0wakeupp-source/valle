package skeleton

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const goSource = `package store

import "fmt"

type User struct {
	ID   int
	Name string
}

type Store interface {
	Get(id int) (*User, error)
	Put(u *User) error
}

func NewStore() *memoryStore { return &memoryStore{} }

func (m *memoryStore) Get(id int) (*User, error) {
	user, ok := m.items[id]
	if !ok {
		return nil, fmt.Errorf("missing %d", id)
	}
	return user, nil
}
`

func TestSkeletonCollapsesBodies(t *testing.T) {
	path := writeFile(t, t.TempDir(), "store.go", goSource)

	out, err := Skeleton(path, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"func NewStore() *memoryStore",
		"type User struct",
		"type Store interface",
		"func (m *memoryStore) Get(id int) (*User, error)",
		"implementation hidden",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("skeleton missing %q:\n%s", want, out)
		}
	}
	// Bodies of non-target declarations must not leak through.
	for _, gone := range []string{"return &memoryStore{}", `fmt.Errorf("missing %d", id)`, "ID   int"} {
		if strings.Contains(out, gone) {
			t.Fatalf("skeleton leaked collapsed body %q:\n%s", gone, out)
		}
	}
}

func TestSkeletonKeepsTargetBody(t *testing.T) {
	path := writeFile(t, t.TempDir(), "store.go", goSource)

	out, err := Skeleton(path, "Get")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `fmt.Errorf("missing %d", id)`) {
		t.Fatalf("target body missing:\n%s", out)
	}
	if !strings.Contains(out, "func NewStore() *memoryStore\n"+collapseComment("//")) {
		t.Fatalf("non-target body not collapsed:\n%s", out)
	}
}

func TestSkeletonTargetNotFound(t *testing.T) {
	path := writeFile(t, t.TempDir(), "store.go", goSource)
	if _, err := Skeleton(path, "MissingSymbol"); err == nil {
		t.Fatal("expected an error for an unknown target symbol")
	}
}

func TestSkeletonUnsupportedExtension(t *testing.T) {
	path := writeFile(t, t.TempDir(), "data.bin", "\x00\x01")
	if _, err := Skeleton(path, ""); err == nil {
		t.Fatal("expected an error for an unsupported extension")
	}
}

const pythonSource = `import os


class Config:
    def __init__(self):
        self.path = os.getcwd()

    def load(self):
        with open(self.path) as handle:
            return handle.read()


def default_config():
    return Config()
`

func TestSkeletonPython(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.py", pythonSource)

	out, err := Skeleton(path, "load")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"class Config",
		"def __init__(self)",
		"def load(self)",
		"def default_config()",
		"return handle.read()", // target body preserved
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("skeleton missing %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"return Config()", "self.path = os.getcwd()"} {
		if strings.Contains(out, gone) {
			t.Fatalf("skeleton leaked collapsed body %q:\n%s", gone, out)
		}
	}
}

func collapseComment(prefix string) string {
	return prefix + " " + collapseMarker
}
