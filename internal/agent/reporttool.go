package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"rick/internal/tools"
)

// ReportTool lets a background agent explicitly surface a result to its parent.
type ReportTool struct {
	Registry *Registry
}

func (ReportTool) Name() string   { return "report" }
func (ReportTool) ReadOnly() bool { return false }
func (ReportTool) Description() string {
	return "Report a completed background-agent result to its parent orchestrator."
}
func (ReportTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":     map[string]any{"type": "string"},
			"full_output": map[string]any{"type": "string"},
		},
		"required": []string{"summary"},
	}
}

type reportArgs struct {
	Summary    string `json:"summary"`
	FullOutput string `json:"full_output"`
}

func (t ReportTool) Run(ctx context.Context, tc tools.Context, raw json.RawMessage) (tools.Result, error) {
	_ = ctx
	if t.Registry == nil || tc.AgentID == "" {
		return tools.Result{}, fmt.Errorf("report is unavailable outside a registered agent")
	}
	var args reportArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return tools.Result{}, fmt.Errorf("invalid report arguments: %v", err)
	}
	if err := t.Registry.Report(tc.AgentID, args.Summary, args.FullOutput); err != nil {
		return tools.Result{}, fmt.Errorf("report: %v", err)
	}
	return tools.Result{Output: "result reported to parent"}, nil
}
