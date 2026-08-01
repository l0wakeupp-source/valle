package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"rick/internal/config"
	"rick/internal/permission"
	"rick/internal/provider"
	"rick/internal/tools"
)

func TestWorkspaceSandboxWriteApprovalGating(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.WriteTool{})
	perms := permission.New(&config.Permission{
		Default: config.PermAsk,
		Write:   config.PermAsk,
	}, root)
	perms.SetSandboxRoot(root, true)

	var asked atomic.Int32
	runner := New(Config{
		Tools: registry,
		Perms: perms,
		Cwd:   root,
		Ask: func(context.Context, permission.Request) PermissionDecision {
			asked.Add(1)
			return DecideReject
		},
	})

	if runner.Cfg().SandboxRoot != root {
		t.Fatalf("agent sandbox root = %q, want %q", runner.Cfg().SandboxRoot, root)
	}

	inside := filepath.Join(root, "inside.txt")
	result, event := runner.execOne(context.Background(), provider.ToolCall{
		ID:    "inside-write",
		Name:  "write",
		Input: mustJSON(map[string]any{"path": inside, "content": "inside"}),
	})
	if result.IsError || event == nil || event.IsError {
		t.Fatalf("inside-fence write failed: result=%#v event=%#v", result, event)
	}
	if got := asked.Load(); got != 0 {
		t.Fatalf("inside-fence approval callback count = %d, want 0", got)
	}
	if data, err := os.ReadFile(inside); err != nil || string(data) != "inside" {
		t.Fatalf("inside-fence write result = %q, err=%v", data, err)
	}

	outsidePath := filepath.Join(outside, "outside.txt")
	result, event = runner.execOne(context.Background(), provider.ToolCall{
		ID:    "outside-write",
		Name:  "write",
		Input: mustJSON(map[string]any{"path": outsidePath, "content": "outside"}),
	})
	if !result.IsError || event == nil || !event.IsError {
		t.Fatalf("outside-fence write result=%#v event=%#v, want rejection", result, event)
	}
	if got := asked.Load(); got != 1 {
		t.Fatalf("outside-fence approval callback count = %d, want 1", got)
	}
	if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
		t.Fatalf("outside-fence file exists after rejection, stat err=%v", err)
	}
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
