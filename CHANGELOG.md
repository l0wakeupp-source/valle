# Changelog

## v0.1.2 — 2026-07-29

- Fixed the TUI splash version so release builds display `v0.1.2`.

### Agent Changes

- **Agent Registry** — Global, thread-safe registry tracking active agents across the session with parent/child relationships, depth tracking, and live event streams.
- **Background Agent Spawning** — `task` and `parallel_tasks` support `background: true`, returning an agent ID immediately without blocking the orchestrator.
- **Agent Depth 10** — Configurable `subagent_depth` from 1–10 in `rick.json`, replacing the previous hard cap of 1.
- **Inter-Agent Chat** — New `chat` tool for messaging any agent regardless of depth or hierarchy.
- **Agent Steering** — New `steer` tool for injecting live instructions into a running agent without restarting it.
- **TUI Agent Picker** — `/agents` opens a tree view of active agents with status, depth, and chat/steer/view/kill/attach actions.
- **Agent Detail View** — Inspect an agent's conversation transcript, children, token usage, and current action.
- **Background Job Tracking** — `JobTracker` follows bash commands, tool executions, and agent spawns with completion notifications.
- **Attach/Detach** — Switch the main transcript to a background agent's live stream; press Escape to return to the orchestrator.
- **Result Reporting** — Background agents automatically report results to the orchestrator, queuing results while it is busy.

### Optimizations

#### Token Usage

- Tool-history cap: approximately 60–93% fewer tokens per large tool result and 20–60% fewer tokens in long sessions.
- Resume previews: approximately 10–50% fewer tokens on the first turn after resume.
- Compact exports: approximately 5–20% smaller JSON files; token savings apply when export data is sent to a model.
- Import optimization: no token change, but less duplicate parsing and lower CPU/memory use.
- Session-list optimization: no token change, but fewer file reads and unmarshals.

#### CPU Usage

- Usage persistence: approximately 80–98% fewer disk writes during active use.
- Regexes: 100% of repeated regex compilation removed.
- HTTP client: per-request client allocations removed.
- HTML parsing: fewer string passes.
- Plugin dispatch: repeated filtering and allocation removed.
- Transcript rendering: repeated scans removed.
- Resize handling: vertical-only resizes no longer re-render the transcript.
- Session listing: directory scans reduced by 50%.

### Issues Patched

- Patched the NousPortal provider in the Provider List.
- Fixed `/yolo` availability.
- Fixed the broken chatbar after submitting a prompt.
