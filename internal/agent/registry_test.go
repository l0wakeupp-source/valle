package agent

import (
	"context"
	"strings"
	"testing"
)

func TestRegistryTracksHierarchyAndLifecycle(t *testing.T) {
	registry := NewRegistry(10, 8)
	rootID, err := registry.Register(AgentEntry{ID: "orchestrator", Name: "orchestrator", Depth: 0})
	if err != nil || rootID != "orchestrator" {
		t.Fatalf("register root: %q %v", rootID, err)
	}
	childID, err := registry.Register(AgentEntry{Name: "general", ParentID: rootID, Depth: 1})
	if err != nil {
		t.Fatalf("register child: %v", err)
	}
	child, ok := registry.Get(childID)
	if !ok || child.ParentID != rootID || child.Status != AgentIdle {
		t.Fatalf("unexpected child snapshot: %+v", child)
	}
	root, _ := registry.Get(rootID)
	if len(root.Children) != 1 || root.Children[0] != childID {
		t.Fatalf("child was not linked: %+v", root.Children)
	}
	if !registry.Update(childID, AgentRunning, "", nil) {
		t.Fatal("update failed")
	}
	if registry.RunningBackgroundCount() != 1 {
		t.Fatalf("running count = %d", registry.RunningBackgroundCount())
	}
	registry.Update(childID, AgentDone, "complete", nil)
	done, _ := registry.Get(childID)
	if done.Output != "complete" || done.Finished.IsZero() {
		t.Fatalf("lifecycle not recorded: %+v", done)
	}
}

func TestRegistryRejectsInvalidDepthAndMissingParent(t *testing.T) {
	registry := NewRegistry(10, 8)
	if _, err := registry.Register(AgentEntry{Name: "bad", Depth: 11}); err == nil {
		t.Fatal("expected depth error")
	}
	if _, err := registry.Register(AgentEntry{Name: "child", ParentID: "missing", Depth: 1}); err == nil {
		t.Fatal("expected missing parent error")
	}
	if err := ValidateDepth(0); err == nil || !strings.Contains(err.Error(), "1..10") {
		t.Fatalf("unexpected validation result: %v", err)
	}
}

func TestRegistryRoutesChatSteerAndKill(t *testing.T) {
	registry := NewRegistry(10, 8)
	rootID, _ := registry.Register(AgentEntry{ID: "root", Name: "root", Depth: 0})
	childID, _ := registry.Register(AgentEntry{Name: "child", ParentID: rootID, Depth: 1})
	input, ok := registry.Input(childID)
	if !ok {
		t.Fatal("missing input channel")
	}
	if err := registry.Send(childID, rootID, "hello"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := registry.Steer(childID, rootID, "focus on tests"); err != nil {
		t.Fatalf("steer: %v", err)
	}
	first := <-input
	second := <-input
	if first.Steering || first.Content != "hello" || !second.Steering {
		t.Fatalf("unexpected messages: %+v %+v", first, second)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entry, _ := registry.entry(childID)
	entry.Cancel = cancel
	if !registry.Kill(childID) {
		t.Fatal("kill failed")
	}
	status, _ := registry.Get(childID)
	if status.Status != AgentKilled {
		t.Fatalf("status = %s", status.Status)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("kill did not cancel context")
	}
}
