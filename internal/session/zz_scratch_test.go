package session

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"rick/internal/provider"
)

func TestScratchInvalidRawMessageBreaksSave(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{
		ID: "sess1",
		Messages: []provider.Message{
			{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
				{Type: "tool_use", ID: "t1", Name: "read", Input: json.RawMessage(`{"path":"a`)},
			}},
		},
	}
	err = st.Save(s)
	t.Logf("Save with truncated tool input err=%v", err)
	if _, statErr := os.Stat(dir + "/sess1.json"); statErr != nil {
		t.Logf("session file NOT written: %v", statErr)
	}
}

func TestScratchCodexToolRolesDropped(t *testing.T) {
	payload := []byte(`{
	  "id":"c1","model":"gpt-5",
	  "messages":[
	    {"role":"user","content":"hi"},
	    {"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}]},
	    {"role":"tool","tool_call_id":"call_1","content":"file.txt"},
	    {"role":"assistant","content":"done"}
	  ]}`)
	sess, err := ParseCodex(payload)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range sess.Messages {
		for _, b := range m.Content {
			t.Logf("msg[%d] role=%s block=%s id=%s tool_use_id=%s", i, m.Role, b.Type, b.ID, b.ToolUseID)
		}
	}
	// count pairing
	uses, results := map[string]bool{}, map[string]bool{}
	for _, m := range sess.Messages {
		for _, b := range m.Content {
			if b.Type == "tool_use" {
				uses[b.ID] = true
			}
			if b.Type == "tool_result" {
				results[b.ToolUseID] = true
			}
		}
	}
	for id := range uses {
		if !results[id] {
			t.Logf("ORPHAN tool_use %s (no tool_result)", id)
		}
	}
}

func TestScratchCodexToolResultInAssistantMessage(t *testing.T) {
	payload := []byte(`{"id":"c2","messages":[
	  {"role":"assistant","content":"x","tool_calls":[{"id":"c","type":"function","function":{"name":"n","arguments":"{}"}}],
	   "tool_outputs":[{"tool_call_id":"c","content":"out"}]}]}`)
	sess, err := ParseCodex(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range sess.Messages {
		types := []string{}
		for _, b := range m.Content {
			types = append(types, b.Type)
		}
		t.Logf("role=%s blocks=%v", m.Role, types)
	}
}

func TestScratchTitleUTF8(t *testing.T) {
	long := strings.Repeat("日", 60)
	title := Title([]provider.Message{provider.UserText(long)})
	t.Logf("valid utf8=%v title=%q", utf8.ValidString(title), title)

	m := metaFrom(&Session{Messages: []provider.Message{provider.UserText(strings.Repeat("é", 800))}})
	t.Logf("lastPrompt valid utf8=%v len=%d", utf8.ValidString(m.LastPrompt), len(m.LastPrompt))
}

func TestScratchConcurrentSetCurrent(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = st.SetCurrent(string(rune('a'+i)), "id")
		}(i)
	}
	wg.Wait()
	m, err := st.currentMap()
	t.Logf("entries=%d err=%v (want 20)", len(m), err)
}

func TestScratchConcurrentRenameVsSave(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	s := &Session{ID: "x", Title: "orig", Messages: []provider.Message{provider.UserText("one")}}
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	// Simulate the browser renaming while the chat loop appends a message.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = st.Rename("x", "renamed") }()
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		s.Messages = append(s.Messages, provider.UserText("two"))
		_ = st.Save(s)
	}()
	wg.Wait()
	got, _ := st.Load("x")
	t.Logf("title=%q messages=%d", got.Title, len(got.Messages))
}

func TestScratchCorruptSessionInvisible(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	// truncated session file, no meta companion (crash during a legacy write)
	os.WriteFile(dir+"/broken.json", []byte(`{"id":"broken","title":"t","messages":[{"role":"user"`), 0o644)
	metas, err := st.List("")
	t.Logf("listed=%d err=%v", len(metas), err)
}

func TestScratchSnapshotRace(t *testing.T) {
	s := &Snapshotter{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.enabledProbe() }()
	}
	wg.Wait()
}

// enabledProbe mimics Enabled()'s unsynchronized fast-path read/write without
// shelling out to git.
func (s *Snapshotter) enabledProbe() bool {
	if !s.enabled {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.enabled {
			return true
		}
		s.enabled = true
	}
	return s.enabled
}

func TestScratchTitleUTF8Misaligned(t *testing.T) {
	long := strings.Repeat("a", 47) + "é" + strings.Repeat("b", 40)
	title := Title([]provider.Message{provider.UserText(long)})
	t.Logf("valid utf8=%v title=%q", utf8.ValidString(title), title)
	blob, err := json.Marshal(Meta{Title: title})
	t.Logf("marshal err=%v json=%s", err, blob)

	prompt := strings.Repeat("a", 999) + "é"
	m := metaFrom(&Session{Messages: []provider.Message{provider.UserText(prompt)}})
	t.Logf("lastPrompt valid utf8=%v tail=%q", utf8.ValidString(m.LastPrompt), m.LastPrompt[995:])
}

func TestScratchNativeImportSilentlyLossy(t *testing.T) {
	orig := &Session{
		ID: "s1", Title: "t", Cwd: "/p", Model: "m", Agent: "build",
		Created: time.Now(), Updated: time.Now(),
		Usage:   Usage{Input: 10, Output: 20},
		Messages: []provider.Message{
			provider.UserText("hi"),
			{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
				{Type: "tool_use", ID: "t1", Name: "read", Input: json.RawMessage(`{"p":1}`)},
			}},
			{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock("t1", "ok", false)}},
		},
		Snapshots: []Snapshot{{ID: "abc", Label: "edit"}},
	}
	blob, _ := json.Marshal(orig)
	// A future rick adds one field to the session document.
	var doc map[string]any
	json.Unmarshal(blob, &doc)
	doc["schema_version"] = 2
	future, _ := json.Marshal(doc)

	got, err := Import(strings.NewReader(string(future)), SourceAuto)
	if err != nil {
		t.Logf("import failed: %v", err)
		return
	}
	blocks := 0
	for _, m := range got.Messages {
		blocks += len(m.Content)
	}
	t.Logf("IMPORT SUCCEEDED but lossy: messages=%d blocks=%d snapshots=%d usage=%+v cwd=%q agent=%q",
		len(got.Messages), blocks, len(got.Snapshots), got.Usage, got.Cwd, got.Agent)
	for _, m := range got.Messages {
		for _, b := range m.Content {
			t.Logf("  role=%s type=%s text=%q", m.Role, b.Type, b.Text)
		}
	}
}
