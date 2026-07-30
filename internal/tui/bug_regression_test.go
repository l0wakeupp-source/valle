package tui

import (
	"encoding/json"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"

	"rick/internal/agent"
	"rick/internal/provider"
)

func TestPhotoSyncDoesNotQueueDuplicateDraws(t *testing.T) {
	model := &Model{photoBox: photoKey{proto: imageKitty, cols: 20, rows: 10}}
	if model.syncPhoto() == nil {
		t.Fatal("expected the first photo sync to schedule a draw")
	}
	if model.syncPhoto() != nil {
		t.Fatal("photo sync queued a duplicate draw while the first draw was pending")
	}
}

func TestPhotoSyncIgnoresStaleDrawAfterStateChange(t *testing.T) {
	oldBox := photoKey{proto: imageKitty, cols: 20, rows: 10}
	newBox := photoKey{proto: imageKitty, cols: 24, rows: 12}
	model := &Model{photoBox: oldBox, width: 100}
	oldDraw := model.syncPhoto()
	if oldDraw == nil {
		t.Fatal("expected initial draw")
	}
	oldMessage, ok := oldDraw().(photoDrawnMsg)
	if !ok {
		t.Fatal("initial photo command returned unexpected message")
	}

	model.photoBox = photoKey{}
	clear := model.syncPhoto()
	if clear == nil {
		t.Fatal("expected clear after removing the photo box")
	}
	model.photoBox = newBox
	newDraw := model.syncPhoto()
	if newDraw == nil {
		t.Fatal("expected redraw for the new photo box")
	}
	if oldMessage.generation == model.photoGeneration {
		t.Fatal("photo generations did not advance")
	}
	model.Update(oldMessage)
	if model.photoPending != newBox {
		t.Fatalf("stale draw changed newer pending state: %#v", model.photoPending)
	}
}
func TestMessagesToChatIndexesToolResults(t *testing.T) {
	history := make([]provider.Message, 0, 400)
	for i := 0; i < 200; i++ {
		callID := "call-" + string(rune(i+1000))
		history = append(history,
			provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "tool_use", ID: callID, Name: "shell"}}},
			provider.Message{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "tool_result", ToolUseID: callID, Content: "result"}}},
		)
	}
	rendered := messagesToChat(history)
	if len(rendered) != 200 {
		t.Fatalf("rendered %d messages, want 200", len(rendered))
	}
	for _, message := range rendered {
		if message.ToolOutput != "result" {
			t.Fatalf("tool result was not attached to its indexed tool message: %#v", message)
		}
	}
}

func TestSlashArrowSelectionUsesCommandMatches(t *testing.T) {
	model := newModelChoiceTestModel()
	model.input = textarea.New()
	model.input.SetValue("/")
	model.viewport = viewport.New(100, 20)

	first, ok := model.slashSelection()
	if !ok || first == "" {
		t.Fatal("expected slash command matches")
	}
	if !model.moveSlashCursor(1) {
		t.Fatal("down arrow did not move the slash selection")
	}
	second, ok := model.slashSelection()
	if !ok || second == first {
		t.Fatalf("slash selection did not advance: first=%q second=%q", first, second)
	}
}

func TestStaleAgentDrainCannotConsumeCurrentRun(t *testing.T) {
	channel := make(chan agent.Event, 1)
	channel <- agent.Event{Kind: agent.EvText, Text: "current run"}
	model := &Model{agentRunID: 2, agentCh: channel}
	if _, cmd := model.drainAgent(1); cmd != nil {
		t.Fatal("stale drain unexpectedly scheduled another command")
	}
	if len(channel) != 1 {
		t.Fatal("stale drain consumed an event from the current run")
	}
}

func TestDuplicateToolEndIsIdempotent(t *testing.T) {
	model := &Model{
		ready:        true,
		tx:           newTranscript(),
		pendingTools: map[string]int{},
		toolOutputs:  map[string]string{},
		width:        100,
		height:       30,
		viewport:     viewport.New(100, 20),
	}
	input, _ := json.Marshal(map[string]string{"command": "pwd"})
	tool := &agent.ToolEvent{CallID: "call-1", Name: "shell", Title: "shell", Input: input, Output: "ok"}
	model.applyAgentEvent(agent.Event{Kind: agent.EvToolStart, Tool: tool})
	model.applyAgentEvent(agent.Event{Kind: agent.EvToolEnd, Tool: tool})
	count := len(model.msgs)
	model.applyAgentEvent(agent.Event{Kind: agent.EvToolEnd, Tool: tool})
	if len(model.msgs) != count {
		t.Fatalf("duplicate tool end appended a second message: before=%d after=%d", count, len(model.msgs))
	}
}
