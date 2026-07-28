package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"rick/internal/tools"
)

// ParallelTaskTool allows spawning multiple subagents concurrently.
type ParallelTaskTool struct {
	Spawn    func(ctx context.Context, kind, description, prompt string, depth int) (string, error)
	Specs    map[string]SubagentSpec
	MaxDepth int
}

func (ParallelTaskTool) Name() string   { return "parallel_tasks" }
func (ParallelTaskTool) ReadOnly() bool { return false }

func (ParallelTaskTool) Description() string {
	return "Spawn multiple subagents in parallel. Each runs independently and concurrently.\n" +
		"Pass an array of tasks; all are launched at once and results are collected.\n" +
		"Useful for independent work that can proceed simultaneously."
}

func (ParallelTaskTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tasks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"subagent_type": map[string]any{"type": "string", "description": "general, explore, or custom type"},
						"description":   map[string]any{"type": "string", "description": "Short label for this task"},
						"prompt":        map[string]any{"type": "string", "description": "Full self-contained task prompt"},
					},
					"required": []string{"subagent_type", "description", "prompt"},
				},
				"description": "Array of tasks to run concurrently",
			},
			"max_concurrent": map[string]any{"type": "number", "description": "Max concurrent agents (default 4)"},
		},
		"required": []string{"tasks"},
	}
}

type parallelArgs struct {
	Tasks []struct {
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
	} `json:"tasks"`
	MaxConcurrent int `json:"max_concurrent"`
}

func (t ParallelTaskTool) Run(ctx context.Context, tc tools.Context, in json.RawMessage) (tools.Result, error) {
	var a parallelArgs
	if err := json.Unmarshal(in, &a); err != nil {
		return tools.Errf("invalid arguments: %v", err), nil
	}
	if len(a.Tasks) == 0 {
		return tools.Errf("no tasks provided"), nil
	}
	if a.MaxConcurrent <= 0 {
		a.MaxConcurrent = 4
	}

	type result struct {
		desc string
		out  string
		err  error
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, a.MaxConcurrent)
	results := make([]result, len(a.Tasks))

	for i, task := range a.Tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, tk struct {
			SubagentType string `json:"subagent_type"`
			Description  string `json:"description"`
			Prompt       string `json:"prompt"`
		}) {
			defer wg.Done()
			defer func() { <-sem }()

			out, err := t.Spawn(ctx, tk.SubagentType, tk.Description, tk.Prompt, tc.Depth+1)
			results[idx] = result{desc: tk.Description, out: out, err: err}
		}(i, task)
	}
	wg.Wait()

	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "--- Task %d: %s ---\n", i+1, r.desc)
		if r.err != nil {
			fmt.Fprintf(&b, "ERROR: %v\n\n", r.err)
		} else {
			fmt.Fprintf(&b, "%s\n\n", r.out)
		}
	}

	return tools.Result{
		Output: strings.TrimRight(b.String(), "\n"),
		Title:  fmt.Sprintf("%d parallel tasks", len(a.Tasks)),
		Meta:   map[string]any{"count": len(a.Tasks)},
	}, nil
}
