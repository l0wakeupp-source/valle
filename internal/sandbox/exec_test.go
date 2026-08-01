package sandbox

import (
	"path/filepath"
	"testing"
)

func TestHomeCacheRootsExcludeCredentialDirectories(t *testing.T) {
	home := filepath.Join("test", "home")
	roots := homeCacheRoots(home)
	for _, forbidden := range []string{".cargo", ".ssh", ".aws", ".config"} {
		for _, root := range roots {
			if filepath.Clean(root) == filepath.Join(home, forbidden) {
				t.Fatalf("cache allowlist exposes credential-bearing directory: %q", root)
			}
		}
	}
	for _, required := range []string{".cargo/registry", ".cargo/git"} {
		found := false
		for _, root := range roots {
			if filepath.Clean(root) == filepath.Join(home, filepath.FromSlash(required)) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("cache allowlist is missing %q", required)
		}
	}
}
