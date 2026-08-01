package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"rick/internal/provider"
	"rick/internal/session"
)

// The list renders an extra line under the selected row when it has a
// category, but handleMouse maps screen rows to indexes 1:1.
func TestBH04ListRowMappingOffByOne(t *testing.T) {
	m := &resumeModel{styles: NewStyles(nil), width: 100, height: 24}
	for i := 0; i < 8; i++ {
		m.metas = append(m.metas, session.Meta{ID: fmt.Sprintf("id%d", i), Title: fmt.Sprintf("session %d", i), Category: "work"})
	}
	m.sortAndFilter()
	m.recalculateViewport()
	m.cursor = 1
	body := m.listView(60)
	for i, line := range strings.Split(body, "\n") {
		t.Logf("row %d (screen y=%d) -> %q", i, m.listTop+i, strings.TrimSpace(line))
	}
	t.Logf("listHeight=%d visibleStart=%d entries=%d rendered lines=%d",
		m.listHeight, m.visibleStart, len(m.filtered), len(strings.Split(body, "\n")))
	// A click three rows below the top: which entry does handleMouse pick?
	row := 3
	t.Logf("click row %d selects index %d (%q) but the rendered row is %q",
		row, m.visibleStart+row, m.filtered[m.visibleStart+row].Title,
		strings.TrimSpace(strings.Split(body, "\n")[row]))
}

func TestBH04CapHistoryBreaksToolPairing(t *testing.T) {
	var history []provider.Message
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("call%d", i)
		history = append(history, provider.UserText("q"))
		history = append(history, provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
			{Type: "tool_use", ID: id, Name: "read", Input: json.RawMessage(`{}`)},
		}})
		history = append(history, provider.Message{Role: provider.RoleUser, Content: []provider.ContentBlock{
			provider.ToolResultBlock(id, "ok", false),
		}})
	}
	capped := capHistory(history)
	t.Logf("in=%d out=%d first role=%s", len(history), len(capped), capped[0].Role)
	uses := map[string]bool{}
	orphans := 0
	for _, msg := range capped {
		for _, b := range msg.Content {
			if b.Type == "tool_use" {
				uses[b.ID] = true
			}
			if b.Type == "tool_result" && !uses[b.ToolUseID] {
				orphans++
				t.Logf("ORPHAN tool_result %s in role=%s (no preceding tool_use)", b.ToolUseID, msg.Role)
			}
		}
	}
	t.Logf("orphan tool_results=%d ; first message role=%q (system role inside messages[])", orphans, capped[0].Role)
	t.Logf("second message: role=%s blocks=%d type0=%s", capped[1].Role, len(capped[1].Content), capped[1].Content[0].Type)
}

func TestBH04MessagesToChatDropsBlocks(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentBlock{
			provider.ImageBlock("image/png", "AAAA"),
			provider.TextBlock("look"),
		}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{
			{Type: "thinking", Text: "hmm", Signature: "sig"},
			provider.TextBlock("done"),
		}},
	}
	chat := messagesToChat(msgs)
	for _, c := range chat {
		t.Logf("kind=%v text=%q", c.Kind, c.Text)
	}
	t.Logf("image and signed thinking survived? %d chat entries for %d blocks", len(chat), 4)
}
