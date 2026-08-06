# Changelog

## v0.1.12 — 2026-08-06

### Performance

- Lowered prompt weight: only the most recent reasoning/thinking block is echoed to providers (DeepSeek/GLM still need the prior turn, but stale reasoning from older turns was inflating every request — roughly two-thirds of a long session's bytes, breaking the provider cache and spiking CPU on compaction).
- RepoMap disk cache key no longer includes the task prompt (it only affects ranking, not the skeleton), so a changed first task on the same git tree reuses the cached structural map instead of rebuilding it.

## v0.1.5 — 2026-08-02

### Performance

- Reduced avoidable allocations and bounded memory use across TUI event draining, resume search, tool file reads, OSV auditing, security findings, and swarm coordination.
- Added regression coverage for the RAM/CPU optimization paths.

## v0.1.5 — 2026-08-01

### Reasoning / Thinking

- `/thinking` now shows only the efforts supported by the active model instead of always showing off/minimal/low/medium/high.
- Provider model catalogs can advertise supported efforts, defaults, mandatory reasoning, and token-budget-only reasoning; OpenRouter's concrete, null, and omitted effort metadata are handled distinctly.
- Added model-specific vocabularies such as `max` and `xhigh`, boolean enablement for Qwen/GLM models, and mandatory-model menus without an invalid off option.
- OpenRouter requests use its normalized `reasoning` object for effort and enablement-only models. Plain models still receive no reasoning fields by default.

### Bug Fixes

- Provider-aware reasoning detection and wire-format handling now cover GLM, Gemini, DeepSeek, Qwen, OpenAI, Anthropic-compatible MiniMax, and gateway model variants.
- Reasoning history and streamed reasoning content remain available where the provider requires them.

### Testing

- Added provider, catalog, OpenAI-compatible wire, streaming, and TUI regression coverage for model-specific reasoning menus and request formats.

## v0.1.4 — 2026-08-01

### Performance

- Fixed slow session loading in `/sessions` and on resume.

### Bug Fixes

- Approved commands now execute correctly when `/yolo` is off (no longer reported as failed).
- Scroll wheel now scrolls chat history instead of prompt history.
- Direct `!` shell commands now go through the permission system.
- Fixed multi-line diff calculation.
- Fixed provider resolution incorrectly consuming the compaction cooldown.
- Repeated tool-call detection now canonicalizes equivalent JSON inputs.
- Undo snapshots are taken once per model tool-call turn instead of once per mutating call.
- Snapshot comparisons no longer stage worktree files in the shadow index, and restores remove files absent from the target snapshot.
- Linux process-count limits now use architecture-specific `RLIMIT_NPROC` values.
- Bubblewrap no longer exposes the entire home directory; only selected toolchain cache directories are mounted read-only.

### UI / Icons

- Removed the graphical “rick” PNG icon and all related terminal graphics code.
- Pixelated icon is now the single default.

### UX

- Tab autocomplete for slash commands (type `/s`, Tab to complete).
- Added direct `/theme <name>` support.
- Mouse tracking stays enabled in chat view.
- Blocked prompt submission while compaction is running.

### Token & Output Efficiency

- Split system prompt into stable (cacheable) and volatile parts; Anthropic cache markers applied.
  - Cacheable prefix ≈ 84–184 tokens depending on agent.
- Shortened prompts and tool descriptions (~143 + ~175 tokens saved).
- Compact line numbers (1|line instead of padded) — noticeable savings on large files.
- ~15 KiB output caps on Git, tree, diagnostics, and test commands.
- Compaction cooldown, no overlapping runs, and ignore stale results.

### Session / Resume

- Store a truncated last-prompt preview in lightweight session metadata (used for search/filter, no full transcript load).

### Web Providers

- Added `/webproviders` with `/webprovider` and `/web` aliases for interactive web-search configuration.
- Added routing, result limits, search budgets, parallel execution, domain filters, and provider enable/disable controls.
- Added configuration for DuckDuckGo, Ollama, Exa, and Tavily, including provider-specific endpoints and search options.
- Added persistence to global `rick.json` with immediate refresh of the active web-search tool.
- Added redacted API-key editing: blank keeps the current key, while `-` clears it.

### Web Search

- Added configurable multi-provider web search with DuckDuckGo Lite HTML and Instant Answer JSON, Ollama Web Search, Exa, Tavily, Bing, Brave, and SearXNG support.
- Independent providers can run in parallel; results are normalized, deduplicated across sources, merged, and deterministically ranked using provider contributions and source rank.
- Added provider-specific configuration, domain filters, safe-search/region/time-range options, bounded concurrency, request variants in cache keys, captured fixtures, and parser/request/merge/ranking tests.

### Backlog (from results-30-07-2026)

- Sandbox-aware approval gating: couple sandbox confinement to approval so writes inside the workspace-write fence are auto-approved (no prompt), while outside-fence writes still prompt. See `results/sandbox-approval-gating-prompt.md`.

## v0.1.3 — 2026-07-30

- Restored normal terminal text selection by scoping mouse reporting to active interactive agent/task controls.
- Added dynamic mouse capture for active prompts and activity controls, with terminal mouse reset on startup and after completion.
- Added prompt history, activity routing, lifecycle, provider, session, and regression coverage across the TUI and agent systems.

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
