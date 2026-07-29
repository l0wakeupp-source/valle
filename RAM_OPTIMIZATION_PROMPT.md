# RAM Optimization Task — Implement the following changes

## Context
We are optimizing a Go CLI agent (rick) for RAM usage. The goal is to reduce memory consumption without degrading quality, features, or user experience. The project is at `G:\projectE`.

Make the following changes. Each change includes the issue, the location, and the expected outcome. Implement them in order of impact (high → medium). Do NOT change low-impact items unless trivial.

---

## CHANGE 1 — Separate session metadata from messages (HIGH)

**Files:** `internal/session/session.go`

**Issue:** `Store.List()` (line ~137) and `Store.Search()` (line ~257) read and unmarshal the ENTIRE session JSON — including all messages with full content blocks — just to extract a few metadata fields (ID, title, timestamps, model). For a store with 50 sessions × 100 messages each, this allocates hundreds of MB per list/search operation, which is immediately discarded. This is the single biggest RAM churn source.

**Expected Change:** Create a sidecar index. When `Store.Save()` is called, also write a small `manifest.json` (or `index.json`) in the sessions directory containing only: `{id, title, created, updated, model, messageCount, byteSize}`. Modify `Store.List()` to read ONLY the manifest (or individual `meta.json` sidecar files per session), not the full session JSON. Modify `Store.Search()` to use the manifest for filtering, then load full session only for matching results.

**Expected Impact:** Reduces RAM churn from O(N×M) to O(N) for list/search. For a typical session store, this is a **10–100× reduction** in peak allocation.

---

## CHANGE 2 — Deduplicate TUI message history (HIGH)

**Files:** `internal/tui/model.go`, `internal/tui/agentbridge.go`

**Issue:** `m.msgs []ChatMsg` (model.go:88) and `m.history []provider.Message` (model.go:92) store overlapping content in parallel. Every tool call's input/output is stored TWICE: once in `m.msgs[i].ToolInput`/`ToolOutput` and again in `m.history` as `provider.ContentBlock`. The `rebuildHistory()` function (agentbridge.go:266-320) rebuilds the entire `m.history` slice from `m.msgs` every turn, allocating fresh structs.

**Expected Change:** Make `m.history` the single source of truth. Remove `ToolInput` and `ToolOutput` fields from `ChatMsg` (keep only a reference/history index). The TUI should read tool data from `m.history` when rendering. Alternatively, derive `m.msgs` entries lazily from `m.history` instead of storing both. At minimum, stop storing full tool output strings in `ChatMsg` — store only the first ~200 chars for display, with full output retrievable from `m.history` on demand.

**Expected Impact:** ~2× reduction on all tool data. For tool-heavy sessions, saves **20–40% of session RAM**.

---

## CHANGE 3 — Cap unbounded TUI message history (HIGH)

**Files:** `internal/tui/model.go`, `internal/tui/agentbridge.go`

**Issue:** `m.msgs` accumulates every chat entry for the entire session with no cap. Each `ChatMsg` can hold large strings (`Text`, `ToolOutput`, `ToolInput`, `DiffOld`, `DiffNew`, base64 attachments). In a long session with 500+ tool calls, this grows without bound.

**Expected Change:** Add a configurable cap (default: 500 entries) to `m.msgs`. When the cap is exceeded, evict the oldest entries. Before eviction, write a summary line to the history (e.g., `[earlier: 5 tool calls, 3 files read]`). Ensure `m.history` (provider context) is also capped — older messages beyond the context window should be summarized into a single system message rather than retained in full. Make the cap configurable via a constant or config field.

**Expected Impact:** Caps session RAM at a fixed ceiling regardless of session length. Prevents unbounded growth.

---

## CHANGE 4 — Cap swarm agent message/output slices (HIGH)

**Files:** `internal/swarm/agent.go`

**Issue:** `Agent.Messages` (line 39) and `AgentOutput.entries` (line 30) grow without bound. Every message and output entry is appended forever. In a long-running swarm, this grows linearly with activity.

**Expected Change:** Add a cap (default: 500) to both slices. When `AddMessage()` exceeds the cap, drop the oldest 25% and reslice. Same for `Add()` on `AgentOutput`. Add a constant `maxAgentMessages = 500` and `maxOutputEntries = 500` at the top of the file.

**Expected Impact:** Bounds per-agent RAM to a fixed ceiling.

---

## CHANGE 5 — Fix swarm goroutine leak (HIGH)

**Files:** `internal/swarm/coordinator.go` (lines ~141-153)

**Issue:** `SwarmProcess.Start()` spawns a goroutine (line ~142) that calls `wg.Wait()`. If `runCtx` times out before all workers finish, the function returns but the goroutine remains blocked on `wg.Wait()` forever. The workers continue running and the goroutine leaks.

**Expected Change:** Replace the goroutine + select pattern with proper context-aware waiting. Use a `sync.Once` to close the done channel, or restructure so the goroutine is tracked and cleaned up. Ensure that when `runCtx.Done()` fires, the goroutine does not leak — either by making workers respect context cancellation or by using a buffered channel that the goroutine can drain without blocking.

**Expected Impact:** Eliminates goroutine leaks in timed-out swarms. Each leak holds references to the WaitGroup, agent state, and channels.

---

## CHANGE 6 — Replace LCS diff matrix with linear-space algorithm (HIGH)

**Files:** `internal/tools/diff.go` (lines ~83-86)

**Issue:** The diff function allocates a full `(n+1) × (m+1)` int matrix for LCS computation. For two 2000-line files, that's 4M cells × 8 bytes = **32MB** allocated transiently. The guard at line ~70 (`maxCells = 4_000_000`) only triggers a fallback but doesn't prevent the allocation up to that point.

**Expected Change:** Replace the full matrix with a streaming/rolling diff algorithm. Use a two-row rolling window (O(min(n,m)) space) for the LCS computation, or implement Myers diff algorithm (O(d) space where d = number of diffs). Keep the existing API (`Diff(old, new string) []DiffHunk`) unchanged. Remove the `maxCells` guard since the new algorithm doesn't need it.

**Expected Impact:** Reduces transient allocation from 32MB+ to ~64KB for typical diffs. Eliminates GC pressure on large file diffs.

---

## CHANGE 7 — Compact JSON serialization (MEDIUM)

**Files:** `internal/session/session.go` (line ~98), `internal/session/export.go` (line ~46)

**Issue:** `Store.Save()` uses `json.MarshalIndent(sess, "", "  ")` which produces JSON 30–50% larger than necessary. Every save allocates a new buffer of the full session size. `Export()` also uses `MarshalIndent`.

**Expected Change:** Change `Store.Save()` to use `json.Marshal` (compact format). For `Export()`, keep `MarshalIndent` since it's human-facing, but stream the output using `json.NewEncoder(w).Encode()` instead of buffering the entire `[]byte` in memory before writing. This reduces peak memory by the size of the session.

**Expected Impact:** ~40% reduction in per-save allocation size. For a 5MB session, saves ~2MB per save.

---

## CHANGE 8 — Cap snapshot history (MEDIUM)

**Files:** `internal/session/snapshot.go` (line ~153)

**Issue:** `Snapshotter.history` is a `[]Snapshot` slice that only grows. New snapshots are appended on every call. Truncation only happens when a new snapshot is taken after an undo. There is no cap on length.

**Expected Change:** Add a cap of 100 entries to `history`. When exceeded, drop the oldest snapshot and run `git gc` on the shadow repo to reclaim disk space. Add a constant `maxSnapshotHistory = 100`.

**Expected Impact:** Bounds snapshot history RAM and prevents unbounded disk growth in the shadow git repo.

---

## CHANGE 9 — Stream-parse websearch HTML responses (MEDIUM)

**Files:** `internal/tools/websearch.go` (lines ~440, ~520, ~592)

**Issue:** Bing, DDG Lite, and Brave search providers use `io.ReadAll(resp.Body)` to read the entire HTML response into memory before parsing. Search result pages can be 100KB–1MB+. With provider fallback, multiple full responses may exist simultaneously.

**Expected Change:** Use `io.LimitReader(resp.Body, 2<<20)` (2MB cap) combined with a streaming regex search or `bufio.Scanner` to extract results without loading the entire body. This limits peak memory per search to 2MB regardless of response size.

**Expected Impact:** Limits peak memory per search to 2MB instead of unbounded. Reduces allocation by 1–3MB per concurrent search.

---

## CHANGE 10 — Evict old entries from swarm board and task board (MEDIUM)

**Files:** `internal/swarm/board.go`, `internal/swarm/taskboard.go`

**Issue:** `Board.entries` map never evicts old entries. `TaskBoard.tasks` map retains all completed tasks with their full `Result` and `Error` strings forever.

**Expected Change:** For `Board`, add a TTL: entries older than 1 hour are purged on `Put()` calls. For `TaskBoard`, remove tasks immediately after their result is consumed (or cap at 200 tasks, removing oldest completed). Add constants for these limits.

**Expected Impact:** Prevents unbounded growth of shared state in long-running swarms.

---

## General Rules

- Do NOT change any function signatures that are part of the public API unless absolutely necessary.
- Do NOT remove features or change user-visible behavior.
- Run `go build ./...` after changes to verify compilation.
- Run `go test ./...` if tests exist for modified files.
- Make minimal, targeted changes. Do not refactor unrelated code.
- Use existing patterns and naming conventions in the codebase.
