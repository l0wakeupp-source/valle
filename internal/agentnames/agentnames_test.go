package agentnames

import (
	"testing"
)

func TestAssignUniqueNames(t *testing.T) {
	inUse = map[string]struct{}{}

	c1 := Assign()
	c2 := Assign()

	if c1.Name == c2.Name {
		t.Errorf("expected unique names, got duplicate %s", c1.Name)
	}
	if c1.Color == "" || c2.Color == "" {
		t.Error("expected non-empty colors")
	}
}

func TestAssignRelease(t *testing.T) {
	inUse = map[string]struct{}{}

	c := Assign()
	names := Peek()
	found := false
	for _, n := range names {
		if n == c.Name {
			found = true
		}
	}
	if !found {
		t.Errorf("assigned name %q not in pool", c.Name)
	}

	Release(c.Name)
	names = Peek()
	for _, n := range names {
		if n == c.Name {
			t.Errorf("released name %q still in pool", c.Name)
		}
	}
}

func TestPeekReturnsActive(t *testing.T) {
	inUse = map[string]struct{}{}

	c1 := Assign()
	c2 := Assign()

	names := Peek()
	if len(names) != 2 {
		t.Errorf("expected 2 in pool, got %d", len(names))
	}
	_ = c1
	_ = c2
}

func TestColorFormat(t *testing.T) {
	c := Assign()
	// Color should be a valid hex color like #RRGGBB
	if len(c.Color) != 7 || c.Color[0] != '#' {
		t.Errorf("expected #RRGGBB format, got %q", c.Color)
	}
	Release(c.Name)
}
