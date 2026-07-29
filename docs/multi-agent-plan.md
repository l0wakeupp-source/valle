# Multi-Agent Interactive Orchestration Plan

## Goal
Enable the user to select, view, steer, and chat with ANY agent or sub-agent in the hierarchy (up to depth 10), with background spawning so the orchestrator remains responsive while child agents work, then report back. Same pattern applies to tools/shells/bash commands.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Orchestrator (Primary Agent)               │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │ Subagent │  │ Subagent │  │  Swarm   │  │  Shell   │    │
│  │  (bg)    │  │  (bg)    │  │  (bg)    │  │  (bg)    │    │
│  │ depth=1  │  │ depth=1  │  │ depth=1  │  │ depth=1  │    │
│  │  ↓depth2 │  │          │  │          │  │          │    │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘    │
│                                                              │
│  [/agents] → pick agent → [chat|steer|view|kill]            │
│  [/jobs]   → pick job   → [view|kill|attach]                │
└─────────────────────────────────────────────────────────────┘
```

---

## Phase 1: Agent Registry & Depth Tracking

### 1.1 Global Agent Registry (`internal/agent/registry.go` new file)
**Purpose**: Track ALL active agents across the session, regardless of depth.

```go
type AgentEntry struct {
    ID          string    // unique per agent instance
    Name        string    // "build", "explore", "general", etc.
    ParentID    string    // "" for orchestrator
    Depth       int       // 0=orchestrator, 1..10=child
    Status      AgentStatus
    Started     time.Time
    Finished    time.Time
    EventCh     chan agent.Event  // live event stream
    Cancel      context.CancelFunc
    Description string    // user-provided label
    Output      string    // final result
    Err         error
    Children    []string  // child agent IDs
}
```

**Registry operations**:
- `Register(entry) string` → returns ID
- `Get(id) (*AgentEntry, bool)`
- `List() []*AgentEntry` → all agents
- `ListDepth(depth) []*AgentEntry` → agents at specific depth
- `ListChildren(parentID) []*AgentEntry`
- `Kill(id)` → cancel context, mark failed
- `Update(id, status, output, err)`

### 1.2 Depth Enforcement
- Current: `MaxDepth` default 1, configurable via `SubagentDepth`
- New: Configurable up to 10 via `rick.json`:
  ```json
  { "subagent_depth": 10 }
  ```
- Each spawn increments `tc.Depth + 1`
- At depth limit: return error telling agent to do work itself

### 1.3 Background Spawn Mode
**New spawn signature**:
```go
Spawn(ctx, kind, description, prompt, depth, mode) (string, error)
// mode: "foreground" | "background"
```

- **Foreground**: Block until done (current behavior)
- **Background**: Return immediately with agent ID, stream events to registry

---

## Phase 2: Orchestrator Remains Responsive

### 2.1 Non-Blocking Tool Execution (`internal/agent/agent.go`)
**Problem**: Current `execTools` blocks the agent loop.
**Solution**: For background spawns, return a "pending" marker and continue.

Add to `Event` stream:
```go
EvAgentBackground  // agent spawned in background
EvAgentReattached  // user re-attached to background agent
```

### 2.2 Inter-Agent Chat Protocol
**New tool: `chat`** (available to all agents)
```json
{
  "target_agent": "agent-id-or-name",
  "message": "your message here"
}
```

- Routes message to target agent's input stream
- Returns target's response
- Works across depths (child can message parent, sibling, etc.)

### 2.3 Steering Commands
**New tool: `steer`**
```json
{
  "target_agent": "agent-id",
  "instruction": "revise approach, focus on X, stop doing Y"
}
```

- Injects a system message into the target agent's conversation
- Does not cancel/restart — modifies live behavior

---

## Phase 3: TUI Agent Management

### 3.1 Agent Picker Modal (`internal/tui/agentpicker.go` new file)
**Trigger**: `/agents` or `Ctrl+X A`

**Layout**:
```
┌─ Active Agents ─────────────────────────────┐
│ ▶ [0] orchestrator (build)    ● running     │
│   [1] general "refactor auth" ◐ working     │
│     [1.1] explore "find tests" ● done        │
│   [2] general "write docs"    ◐ working     │
│     [2.1] general "api docs"  ○ failed      │
│       [2.1.1] search web      ● done        │
│                                                │
│  [chat] [steer] [view] [kill] [bg-spawn]     │
└──────────────────────────────────────────────┘
```

### 3.2 Agent Detail View
**Trigger**: Select agent → press Enter or `view`

**Layout**:
```
┌─ Agent: general "refactor auth" ─────────────┐
│ Status: working  ·  depth: 1  ·  tools: 47   │
│ Started: 2m30s ago  ·  tokens: 12.3k         │
│                                                │
│ Current action: editing internal/auth.go      │
│                                                │
│ ── Conversation ────────────────────────────  │
│ [filtered transcript of this agent alone]     │
│                                                │
│ ── Children ────────────────────────────────  │
│   explore "find tests" ● done                 │
│                                                │
│ [chat] [steer] [kill] [back]                  │
└────────────────────────────────────────────────┘
```

### 3.3 Background Shell/Tool Tracking (`internal/tui/jobs.go` enhancement)
Extend `JobTracker` to track:
- Bash commands (`!foo` or `bash` tool)
- Tool executions (long-running reads, searches)
- Agent spawns

**New Job kinds**:
- `"bash"` — shell command
- `"tool"` — individual tool execution
- `"agent"` — subagent/swarm background task

### 3.4 Status Bar Enhancement
```
│ build · 3 agents · 2 jobs · depth 2/10 · ctx 45%
```

---

## Phase 4: Background Reporting

### 4.1 Completion Notification
When a background agent finishes:
1. Registry updates status → `done`/`failed`
2. TUI shows brief notification: `[1] general "refactor auth" finished (47 tools, 12.3k tokens)`
3. If orchestrator is idle: auto-summarize result into conversation
4. If orchestrator is busy: queue notification, show when next idle

### 4.2 Result Surfacing
**New tool: `report`** (orchestrator-only)
```json
{
  "agent_id": "agent-uuid",
  "summary": "brief result summary",
  "full_output": "complete output..."
}
```

- Background agents call this on completion
- Injects result into orchestrator's conversation
- Orchestrator decides what to do with it

### 4.3 Polling/Attachment
User can "attach" to a background agent:
- Press Enter on agent in picker → switch to its stream
- Events flow into main chat view
- Press Escape → return to orchestrator

---

## Phase 5: Configuration & Depth 10

### 5.1 Config Schema (`internal/config/config.go`)
```go
type Config struct {
    // ... existing ...
    SubagentDepth    *int    `json:"subagent_depth"`     // default 1, max 10
    BackgroundNotify bool    `json:"background_notify"`  // auto-report
    MaxBackground    int     `json:"max_background"`     // max concurrent bg agents
}
```

### 5.2 Depth Validation
```go
const MaxAllowedDepth = 10

func validateDepth(d int) error {
    if d < 1 || d > MaxAllowedDepth {
        return fmt.Errorf("subagent_depth must be 1..%d", MaxAllowedDepth)
    }
    return nil
}
```

### 5.3 Concurrency Limits
- Default max background agents: 8
- Configurable via `max_background`
- When limit reached: new spawns queue or fail gracefully

---

## Phase 6: Implementation Order

### Step 1: Agent Registry
- Create `internal/agent/registry.go`
- Thread-safe map of agent entries
- Register/List/Get/Kill API
- Unit tests

### Step 2: Background Spawn Mode
- Modify `TaskTool.Run` to accept `background: true` param
- Return agent ID immediately
- Stream events to registry
- Modify `ParallelTaskTool` similarly

### Step 3: Depth 10 Support
- Update config schema
- Update `SubagentDepth` validation
- Propagate depth through all spawn paths
- Test depth limit enforcement

### Step 4: Chat & Steer Tools
- Create `internal/agent/chattool.go`
- Create `internal/agent/steertool.go`
- Register in tool registry
- Handle routing in agent loop

### Step 5: TUI Agent Picker
- Create `internal/tui/agentpicker.go`
- `/agents` slash command
- Modal with agent tree view
- Keyboard navigation

### Step 6: Agent Detail View
- Create `internal/tui/agentdetail.go`
- Show single agent's conversation
- Show children
- Chat/Steer/Kill actions

### Step 7: Background Job Tracking
- Enhance `JobTracker` with agent/tool/bash kinds
- Track in TUI status bar
- Completion notifications

### Step 8: Result Reporting
- `report` tool for background agents
- Auto-summarize on completion
- Queue when orchestrator busy

### Step 9: Attach/Detach
- Switch TUI view to background agent stream
- Escape returns to orchestrator
- Visual indicator of attached state

### Step 10: Polish
- Status bar integration
- Config validation
- Documentation
- End-to-end tests

---

## Key Files to Modify

| File | Change |
|------|--------|
| `internal/agent/registry.go` | **NEW** — global agent tracking |
| `internal/agent/subagent.go` | Background spawn, depth to 10 |
| `internal/agent/parallel.go` | Background mode, depth propagation |
| `internal/agent/chattool.go` | **NEW** — inter-agent messaging |
| `internal/agent/steertool.go` | **NEW** — live agent steering |
| `internal/agent/agent.go` | Non-blocking tool exec, background events |
| `internal/tui/model.go` | Agent registry integration, new messages |
| `internal/tui/agentpicker.go` | **NEW** — agent selection modal |
| `internal/tui/agentdetail.go` | **NEW** — single agent view |
| `internal/tui/subagent.go` | Background spawn UI |
| `internal/tui/swarmui.go` | Background swarm UI |
| `internal/tui/jobs.go` | Agent/tool/bash job tracking |
| `internal/tui/slash.go` | `/agents`, `/jobs` commands |
| `internal/tui/modals.go` | Agent picker modal |
| `internal/config/config.go` | `subagent_depth`, `max_background` |
| `internal/swarm/runtime.go` | Background execution mode |

---

## Testing Strategy

1. **Unit Tests**: Registry operations, depth enforcement, chat routing
2. **Integration Tests**: Spawn → background → report → orchestrator receives
3. **Depth Tests**: Verify depth 10 works, depth 11 fails gracefully
4. **Concurrency Tests**: 8+ background agents, verify limits
5. **TUI Tests**: Modal navigation, agent selection, attach/detach
6. **End-to-End**: Full orchestrator → spawn → chat → steer → report flow

---

## Migration Notes

- Existing `TaskTool` API preserved (background=false default)
- Existing `ParallelTaskTool` API preserved
- Config `subagent_depth` defaults to 1 (backward compatible)
- All new tools are additive (no breaking changes)
- Registry is opt-in per agent run
