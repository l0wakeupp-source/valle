package delta

import (
	"strings"
	"testing"
)

const sample = `package store

func Core() string {
	return "core"
}

func helper() int {
	value := 41
	return value + 1
}
`

func TestEncodeAnnotatesChangedLineOnly(t *testing.T) {
	newText := strings.Replace(sample, `return "core"`, `return "core2"`, 1)

	view, ok := Encode(sample, newText, 0)
	if !ok {
		t.Fatal("expected a delta")
	}
	if strings.Contains(view, "func helper() int") {
		t.Fatalf("delta leaked an unchanged line:\n%s", view)
	}
	if !strings.Contains(view, "[-core-]{+core2+}") {
		t.Fatalf("delta lacks word annotation:\n%s", view)
	}
	if !strings.Contains(view, "<delta: 1 changed lines>") {
		t.Fatalf("delta lacks header:\n%s", view)
	}
}

func TestEncodeNoChanges(t *testing.T) {
	if _, ok := Encode(sample, sample, 0); ok {
		t.Fatal("identical texts must not produce a delta")
	}
}

func TestEncodeRejectsWholeFileRewrite(t *testing.T) {
	rewritten := strings.Repeat("// comment\n", strings.Count(sample, "\n")+1)
	if _, ok := Encode(sample, rewritten, 0); ok {
		t.Fatal("a full rewrite should fall back to a full read")
	}
}

func TestEncodePureInsertAndDelete(t *testing.T) {
	added := sample + "func extra() {}\n"
	view, ok := Encode(sample, added, 0)
	if !ok || !strings.Contains(view, "+ func extra() {}") {
		t.Fatalf("inserted line missing from delta:\n%s", view)
	}

	// A large stable base so the deletion delta is materially smaller than the
	// full remaining file (otherwise Encode correctly falls back to a read).
	bigBase := strings.Repeat(sample, 60)
	removed := strings.Replace(bigBase, "func helper() int {\n\tvalue := 41\n\treturn value + 1\n}\n", "", 1)
	view, ok = Encode(bigBase, removed, 0)
	if !ok || !strings.Contains(view, "- func helper() int {") {
		t.Fatalf("deleted line missing from delta:\n%s", view)
	}
}

func TestStoreDeliversFullThenDelta(t *testing.T) {
	store := NewStore()
	defer store.Reset()

	out, isDelta := store.Deliver("a.go", sample, 0)
	if isDelta || out != sample {
		t.Fatal("first delivery must be the full text")
	}

	changed := strings.Replace(sample, "value + 1", "value + 2", 1)
	out, isDelta = store.Deliver("a.go", changed, 0)
	if !isDelta {
		t.Fatal("changed file should be delivered as a delta")
	}
	if strings.Contains(out, "value := 41") {
		t.Fatalf("delta repeated an unchanged line:\n%s", out)
	}

	// The delta becomes the new baseline: an identical follow-up read is full.
	out, isDelta = store.Deliver("a.go", changed, 0)
	if isDelta || out != changed {
		t.Fatal("unchanged follow-up read must be the full text")
	}
}

func TestStoreResetClearsBaselines(t *testing.T) {
	store := NewStore()
	store.Deliver("a.go", sample, 0)
	store.Reset()

	out, isDelta := store.Deliver("a.go", strings.Replace(sample, "core", "core2", 1), 0)
	if isDelta || out == "" {
		t.Fatal("reset store must deliver the full text")
	}
}
