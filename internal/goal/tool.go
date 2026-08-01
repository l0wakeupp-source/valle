package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"rick/internal/tools"
)

// GoalWriteTool creates or updates a goal with title, description, budget,
// and steps.
type GoalWriteTool struct{ Store *Store }

func (GoalWriteTool) Name() string { return "goalwrite" }

func (GoalWriteTool) ReadOnly() bool { return false }

func (GoalWriteTool) Description() string {
	return "Create or update a persistent goal with optional steps and a token budget. " +
		"The active goal is tracked across turns and its token usage is enforced automatically."
}

func (GoalWriteTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"goal_id":      map[string]any{"type": "string", "description": "Existing goal id to update (omit to create a new goal)"},
			"title":        map[string]any{"type": "string", "description": "Goal title (required for new goals)"},
			"description":  map[string]any{"type": "string", "description": "Optional longer description"},
			"token_budget": map[string]any{"type": "number", "description": "Max tokens before the goal is aborted (0 = unlimited)"},
			"steps": map[string]any{
				"type":        "array",
				"description": "Ordered list of step descriptions",
				"items":       map[string]any{"type": "string"},
			},
		},
		"required": []string{"title"},
	}
}

func (t GoalWriteTool) Run(_ context.Context, _ tools.Context, in json.RawMessage) (tools.Result, error) {
	var args struct {
		GoalID      string   `json:"goal_id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		TokenBudget int      `json:"token_budget"`
		Steps       []string `json:"steps"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return tools.Errf("invalid input: %v", err), nil
	}
	if strings.TrimSpace(args.Title) == "" {
		return tools.Errf("title is required"), nil
	}

	g := &Goal{ID: args.GoalID, Title: strings.TrimSpace(args.Title), Description: args.Description, Status: "active", TokenBudget: args.TokenBudget}
	verb := "created"
	if args.GoalID != "" {
		loaded, err := t.Store.Load(args.GoalID)
		if err != nil {
			return tools.Errf("load: %v", err), nil
		}
		g = loaded
		g.Title = strings.TrimSpace(args.Title)
		g.Description = args.Description
		g.Status = "active"
		g.TokenBudget = args.TokenBudget
		verb = "updated"
	}
	if args.Steps != nil {
		steps := make([]Step, 0, len(args.Steps))
		for i, s := range args.Steps {
			stepID := fmt.Sprintf("s%d", i+1)
			status := "pending"
			if i < len(g.Steps) && g.Steps[i].ID == stepID {
				status = g.Steps[i].Status
			}
			steps = append(steps, Step{ID: stepID, Content: s, Status: status})
		}
		g.Steps = steps
	}
	if err := t.Store.Save(g); err != nil {
		return tools.Errf("save: %v", err), nil
	}
	if err := t.Store.SetActive(g.ID); err != nil {
		return tools.Errf("set active: %v", err), nil
	}
	return tools.Result{
		Output: fmt.Sprintf("goal %s: %s (%s)", verb, g.Title, Progress(g)),
		Title:  "goal: " + g.Title,
	}, nil
}

// GoalReadTool reads the active goal and its progress.
type GoalReadTool struct{ Store *Store }

func (GoalReadTool) Name() string { return "goalread" }

func (GoalReadTool) ReadOnly() bool { return true }

func (GoalReadTool) Description() string {
	return "Read the active goal, its steps, progress, and token usage."
}

func (GoalReadTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t GoalReadTool) Run(_ context.Context, _ tools.Context, _ json.RawMessage) (tools.Result, error) {
	g, err := t.Store.GetActive()
	if err != nil {
		return tools.Errf("load: %v", err), nil
	}
	if g == nil {
		return tools.Result{Output: "no active goal", Title: "goal: none"}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "goal: %s [%s]\n", g.Title, g.Status)
	if g.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", g.Description)
	}
	fmt.Fprintf(&b, "progress: %s\n", Progress(g))
	for _, st := range g.Steps {
		marker := "○"
		switch st.Status {
		case "done":
			marker = "✓"
		case "in_progress":
			marker = "▶"
		case "skipped":
			marker = "⊘"
		}
		fmt.Fprintf(&b, "  %s [%s] %s\n", marker, st.ID, st.Content)
	}
	return tools.Result{Output: b.String(), Title: "goal: " + g.Title}, nil
}

// GoalStepTool marks a step done, in_progress, or skipped.
type GoalStepTool struct{ Store *Store }

func (GoalStepTool) Name() string { return "goalstep" }

func (GoalStepTool) ReadOnly() bool { return false }

func (GoalStepTool) Description() string {
	return "Update a step's status in the active goal (done, in_progress, or skipped)."
}

func (GoalStepTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"step_id": map[string]any{"type": "string", "description": "Step id (e.g. s1, s2)"},
			"status":  map[string]any{"type": "string", "enum": []string{"done", "in_progress", "skipped", "pending"}, "description": "New status"},
		},
		"required": []string{"step_id", "status"},
	}
}

func (t GoalStepTool) Run(_ context.Context, _ tools.Context, in json.RawMessage) (tools.Result, error) {
	var args struct {
		StepID string `json:"step_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return tools.Errf("invalid input: %v", err), nil
	}
	g, err := t.Store.GetActive()
	if err != nil {
		return tools.Errf("load: %v", err), nil
	}
	if g == nil {
		return tools.Errf("no active goal"), nil
	}
	if err := t.Store.UpdateStep(g.ID, args.StepID, args.Status); err != nil {
		return tools.Errf("%v", err), nil
	}
	updated, err := t.Store.Load(g.ID)
	if err != nil {
		return tools.Errf("reload: %v", err), nil
	}
	return tools.Result{
		Output: fmt.Sprintf("step %s → %s (%s)", args.StepID, args.Status, Progress(updated)),
		Title:  fmt.Sprintf("step %s: %s", args.StepID, args.Status),
	}, nil
}

// GoalAbortTool aborts the active goal.
type GoalAbortTool struct{ Store *Store }

func (GoalAbortTool) Name() string { return "goalabort" }

func (GoalAbortTool) ReadOnly() bool { return false }

func (GoalAbortTool) Description() string {
	return "Abort the active goal, stopping all further work on it."
}

func (GoalAbortTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "Optional reason for aborting"},
		},
	}
}

func (t GoalAbortTool) Run(_ context.Context, _ tools.Context, in json.RawMessage) (tools.Result, error) {
	var args struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(in, &args)

	g, err := t.Store.GetActive()
	if err != nil {
		return tools.Errf("load: %v", err), nil
	}
	if g == nil {
		return tools.Errf("no active goal"), nil
	}
	g.Status = "aborted"
	if args.Reason != "" {
		g.Description += "\n[aborted: " + args.Reason + "]"
	}
	if err := t.Store.Save(g); err != nil {
		return tools.Errf("save: %v", err), nil
	}
	if err := t.Store.ClearActive(); err != nil {
		return tools.Errf("clear active: %v", err), nil
	}
	return tools.Result{
		Output: fmt.Sprintf("goal %q aborted (%s)", g.Title, Progress(g)),
		Title:  "goal aborted: " + g.Title,
	}, nil
}
