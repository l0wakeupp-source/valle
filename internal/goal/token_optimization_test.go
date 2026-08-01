package goal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"rick/internal/tools"
)

func goalArgs(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func TestGoalStepReportsFreshProgress(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := (GoalWriteTool{Store: store}).Run(context.Background(), tools.Context{}, goalArgs(map[string]any{
		"title": "ship",
		"steps": []string{"one", "two"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created.Output, "0/2 steps") {
		t.Fatalf("create output = %q", created.Output)
	}

	updated, err := (GoalStepTool{Store: store}).Run(context.Background(), tools.Context{}, goalArgs(map[string]string{
		"step_id": "s1",
		"status":  "done",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated.Output, "1/2 steps") {
		t.Fatalf("step output = %q, want fresh progress", updated.Output)
	}
}

func TestGoalWriteUpdatesExplicitGoal(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := (GoalWriteTool{Store: store}).Run(context.Background(), tools.Context{}, goalArgs(map[string]any{
		"title": "old",
		"steps": []string{"one"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.GetActive()
	if err != nil {
		t.Fatal(err)
	}

	result, err := (GoalWriteTool{Store: store}).Run(context.Background(), tools.Context{}, goalArgs(map[string]any{
		"goal_id": active.ID,
		"title":   "new",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "goal updated: new") {
		t.Fatalf("update output = %q", result.Output)
	}
	loaded, err := store.Load(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != active.ID || loaded.Title != "new" || len(loaded.Steps) != 1 || !strings.Contains(created.Output, "old") {
		t.Fatalf("updated goal = %#v", loaded)
	}
}
