package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyMetaFromFileSkipsMessageBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.json")
	contents := `{"id":"legacy","title":"old session","cwd":"C:\\work","model":"openai/gpt","category":"research","favorite":true,"messages":[{"role":"user","content":"large body"},{"role":"assistant","content":[{"type":"text","text":"reply"}]}]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := legacyMetaFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "legacy" || got.Title != "old session" || got.Cwd != `C:\work` || got.Model != "openai/gpt" || got.Category != "research" || !got.Favorite || got.Messages != 2 {
		t.Fatalf("legacy metadata = %+v", got)
	}
}
