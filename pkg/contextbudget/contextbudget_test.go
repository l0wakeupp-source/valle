package contextbudget

import (
	"strings"
	"testing"

	"rick/internal/provider"
)

func toolUse(id string) provider.Message {
	return provider.Message{
		Role: provider.RoleAssistant,
		Content: []provider.ContentBlock{
			{Type: "tool_use", ID: id, Name: "bash"},
		},
	}
}

func toolResult(id, content string) provider.Message {
	return provider.Message{
		Role: provider.RoleUser,
		Content: []provider.ContentBlock{
			{Type: "tool_result", ToolUseID: id, Content: content},
		},
	}
}

func TestVerifyPairSafetyAcceptsValidTranscript(t *testing.T) {
	messages := []provider.Message{
		provider.UserText("run it"),
		toolUse("c1"),
		toolResult("c1", "ok"),
		toolUse("c2"),
		toolResult("c2", "done"),
	}
	if err := VerifyPairSafety(messages); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyPairSafetyRejectsOrphanedResult(t *testing.T) {
	messages := []provider.Message{
		provider.UserText("run it"),
		toolResult("orphan", "no tool_use here"),
	}
	if err := VerifyPairSafety(messages); err == nil {
		t.Fatal("expected orphaned tool_result to be rejected")
	}
}

func TestVerifyPairSafetyRejectsUnpairedToolUse(t *testing.T) {
	messages := []provider.Message{
		provider.UserText("run it"),
		toolUse("c1"),
	}
	if err := VerifyPairSafety(messages); err == nil {
		t.Fatal("expected dangling tool_use to be rejected")
	}
}

func TestApplyDedupReplacesRepeatedPayloads(t *testing.T) {
	payload := strings.Repeat(`{"data":"`+"abcdefgh"+`"}`, 300) // > MinDedupBytes
	messages := []provider.Message{
		provider.UserText("first"),
		toolUse("c1"),
		toolResult("c1", payload),
		provider.UserText("again"),
		toolUse("c2"),
		toolResult("c2", payload),
	}
	budget := New(Options{})
	result := budget.ApplyDedup(messages)

	if result.Replaced != 1 {
		t.Fatalf("Replaced = %d, want 1", result.Replaced)
	}
	if result.SavedBytes <= 0 {
		t.Fatalf("SavedBytes = %d, want > 0", result.SavedBytes)
	}

	// First occurrence is untouched; second is a self-contained reference and
	// the original is retrievable via the content address.
	first := result.View[2].Content[0].Content
	second := result.View[5].Content[0].Content
	if first != payload {
		t.Fatal("first occurrence was unexpectedly replaced")
	}
	if !strings.Contains(second, "duplicate payload sha256:") {
		t.Fatalf("second occurrence not deduplicated: %s", second[:60])
	}
	hash := Hash(payload)
	original, ok := budget.StoredPayload(hash)
	if !ok || original != payload {
		t.Fatal("content-addressed original is not retrievable")
	}
}

func TestApplyDedupSupersedesTrimmedOriginal(t *testing.T) {
	budget := New(Options{})
	payload := strings.Repeat("identical large payload ", 200)

	// Turn 1: payload appears twice; only the second is collapsed.
	turn1 := []provider.Message{
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "tool_use", ID: "c1", Name: "bash"}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "tool_result", ToolUseID: "c1", Content: payload}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "tool_use", ID: "c2", Name: "bash"}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "tool_result", ToolUseID: "c2", Content: payload}}},
	}
	first := budget.ApplyDedup(turn1).View
	if !strings.Contains(first[1].Content[0].Content, payload) {
		t.Fatal("first occurrence was collapsed while its original was present")
	}
	if !strings.Contains(first[3].Content[0].Content, "duplicate payload sha256:") {
		t.Fatal("second occurrence was not collapsed")
	}

	// Turn 2: the original occurrence is trimmed away; the surviving
	// occurrence must stay collapsed so the provider-facing bytes are stable.
	turn2 := turn1[2:]
	second := budget.ApplyDedup(turn2).View
	if !strings.Contains(second[1].Content[0].Content, "duplicate payload sha256:") {
		t.Fatalf("surviving occurrence flipped back to full payload: %s", second[1].Content[0].Content[:60])
	}
	// The original payload stays retrievable by its content address.
	if original, ok := budget.StoredPayload(Hash(payload)); !ok || original != payload {
		t.Fatal("content-addressed original is not retrievable")
	}
}

func TestChooseBoundariesRequiresStability(t *testing.T) {
	budget := New(Options{MinStableTurns: 2, LiveZoneTurns: 2, MaxStableBytes: 64, MinCacheTokens: 1})
	history := []provider.Message{
		provider.UserText(strings.Repeat("stable old context ", 20)),
		provider.UserText(strings.Repeat("more stable context ", 20)),
		provider.UserText(strings.Repeat("even more stable ", 20)),
		provider.UserText("current request"),
	}
	// First observation: no boundary yet.
	first := budget.ChooseBoundaries(history)
	if len(first) != 0 {
		t.Fatalf("first observation produced boundaries: %v", first)
	}
	// Identical second observation: the stable prefix now qualifies.
	second := budget.ChooseBoundaries(history)
	if len(second) == 0 {
		t.Fatal("second identical observation produced no boundary")
	}
	// The live zone (newest turns) must never be a boundary.
	if second[2] || second[3] {
		t.Fatalf("boundary placed on a live-zone message: %v", second)
	}
}

func TestChooseBoundariesNeverSplitsToolPair(t *testing.T) {
	budget := New(Options{MinStableTurns: 2, LiveZoneTurns: 1, MaxStableBytes: 64, MinCacheTokens: 1})
	history := []provider.Message{
		provider.UserText(strings.Repeat("stable intro ", 20)),
		provider.UserText(strings.Repeat("stable second ", 20)),
		toolUse("c1"),
		toolResult("c1", strings.Repeat("result ", 20)),
		provider.UserText(strings.Repeat("current ", 20)),
	}
	budget.ChooseBoundaries(history)
	boundaries := budget.ChooseBoundaries(history)

	for index := range boundaries {
		if index == 3 {
			t.Fatalf("boundary at %d lands inside a tool_use/tool_result pair", index)
		}
	}
	// With a one-turn live zone the boundary may sit at the pair's start
	// (the pair stays intact, after the boundary), warming the cache sooner.
	if !boundaries[2] {
		t.Fatalf("expected a boundary at the tool pair start, got %v", boundaries)
	}
}

func TestCompressLiveIsReversible(t *testing.T) {
	budget := New(Options{})
	payload := `{"items":[` + strings.Repeat(`"value",`, 50) + `"last"],"note":"x"}`
	compressed, changed := budget.CompressLive("call-42", payload)
	if !changed {
		t.Fatal("expected compression to change the payload")
	}
	if len(compressed) >= len(payload) {
		t.Fatalf("compression did not shrink: %d -> %d", len(payload), len(compressed))
	}
	original, ok := budget.LiveOriginal("call-42")
	if !ok || original != payload {
		t.Fatal("live-zone original is not retrievable")
	}
}

func TestCompressLiveStoresNonJSONViaCap(t *testing.T) {
	budget := New(Options{LiveZoneCapBytes: 200})
	payload := strings.Repeat("line of text\n", 100)
	compressed, changed := budget.CompressLive("call-7", payload)
	if !changed {
		t.Fatal("expected capping to change the payload")
	}
	if len(compressed) > 200+64 {
		t.Fatalf("capped output too large: %d", len(compressed))
	}
	if !strings.Contains(compressed, "retrieve_uncompressed_context") {
		t.Fatal("cap marker missing from output")
	}
	if original, ok := budget.LiveOriginal("call-7"); !ok || original != payload {
		t.Fatal("capped original not retrievable")
	}
}
