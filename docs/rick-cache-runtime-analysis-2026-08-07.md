# Rick cache, silent-submit, and tool-call investigation

Date: 2026-08-07
Repository: `G:/projectE`
Model/provider under investigation: `opencode-zen/deepseek-v4-flash-free`

## Executive findings

1. The quoted 85% is real as an aggregate measurement, but it is not the warm-cache rate of the final request in the latest substantive session. Rick's persisted usage tracker reports 84.9654% for 2026-08-05. The latest substantive session's final request was 99.6318% cached.
2. Rick's lower aggregate was caused mainly by historical prompt-prefix rewrites, especially removal of older reasoning blocks. A reconstruction of the 62 requests in session `8c68` shows that the former "keep only newest reasoning" policy would rewrite prior wire bytes on 48 of 61 transitions. The new OpenCode/DeepSeek path retains all prior reasoning and makes all 61 reconstructed transitions append-only.
3. The reported cache-miss count was also inflated by a UI accounting bug: after one cache-bearing usage event, later events that omitted cache details could be interpreted as full misses. That affected the miss counter/notices, not the provider's billed cache-token ratio.
4. The newest failed session did accept and persist the prompt, but no completed provider response was persisted. Its lifetime was 24.918 seconds and all usage counters stayed zero. A fresh run against the same model completed in about 24.3 seconds, making cancellation just before the first response a strong explanation. The old session format did not persist the terminal error, so the exact prior error cannot be recovered.
5. The historical sessions contain balanced tool calls/results, valid JSON arguments, no duplicate call IDs, and no dangling/orphan results. Therefore the exact observed `tool_calls` error is not recoverable from those session files. Rick nevertheless had real hardening gaps: malformed SSE JSON was silently skipped, malformed/nameless calls could reach execution/history, and an empty completion could be treated as success.

## Evidence and metrics

Cache hit rate is calculated from provider-reported prompt usage:

`cache_read / (input + cache_read + cache_write)`

| Evidence | Input/miss | Cache read | Cache write | Hit rate |
|---|---:|---:|---:|---:|
| Usage tracker, 2026-08-03 | 2,704,425 | 127,356,928 | 0 | 97.9207% |
| Usage tracker, 2026-08-04 | 9,332,352 | 135,293,056 | 0 | 93.5472% |
| Usage tracker, 2026-08-05 | 33,950,727 | 191,867,136 | 0 | 84.9654% |
| Usage tracker, 2026-08-06 | 36,092,019 | 66,717,952 | 0 | 64.8944% |
| Usage tracker, 2026-08-07 at investigation time | 3,099,618 | 22,376,960 | 0 | 87.8335% |
| Session `8c68`, final request | 614 | 166,144 | 0 | 99.6318% |
| Session `12d8`, final request | 440 | 48,000 | 0 | 99.0917% |

Important limitation: a session JSON persists only the latest request's `usage`. Rick's cumulative in-session `billed` counters are UI state and were not persisted. Consequently, the exact cumulative rate shown at the end of `8c68` cannot be reconstructed request-by-request from the file. The daily 84.9654% value and the final-request 99.6318% value are both canonical provider-reported measurements, but they answer different questions.

### Latest substantive session: `2026-08-07T11-18-14_8c68.json`

- Duration: 3,098.751 seconds (51m 38.751s)
- File size: 1,473,914 bytes
- Provider messages: 126 (62 assistant responses, 64 user/tool-result messages)
- Content blocks: 86 thinking, 165 tool calls, 165 matching tool results
- Final provider usage: 614 input/miss, 166,144 cache read, 81 output
- Local tool-output optimization: 165 results; 117,347 original tokens; 80,000 provider tokens; 17,245 measured tokens saved
- Full local transcript vs bounded sent transcript: 142,634 tool-output characters removed, a 36.78% character reduction

### Follow-up session: `2026-08-07T12-12-51_12d8.json`

- Duration: 932.110 seconds
- File size: 394,326 bytes
- Provider messages: 33 (16 assistant responses, 17 user/tool-result messages)
- Content blocks: 30 thinking, 56 tool calls, 56 matching tool results
- Final provider usage: 440 input/miss, 48,000 cache read, 300 output
- Local tool-output optimization: 56 results; 42,976 original tokens; 29,900 provider tokens; 9,404 measured tokens saved

### Failed newest session: `2026-08-07T12-29-29_46f8.json`

- Duration: 24.918 seconds
- File size: 821 bytes
- Messages: one user prompt only
- Sent transcript: the same one user prompt only
- Usage: all zero
- No assistant text, reasoning, tool call, tool result, or stored error

## Why 85% was below the reported ZCode figure

The public ZCode material examined in the preceding Rick session identifies ZCode as a GLM-oriented development environment; it does not establish a public, ZCode-specific 98.5% DeepSeek measurement. Therefore 98.5% is treated here as the user's observed comparison, not as a source-verified ZCode guarantee.

The meaningful technical comparison is prompt shape:

- A prefix cache rewards byte-identical old content plus an appended tail.
- Rick formerly removed older reasoning when a newer reasoning turn arrived. That made old request bytes disappear from the next request.
- A structural replay of session `8c68` found 62 request snapshots. Under the former serialization, 48 of 61 transitions rewrote prior wire bytes. Under the current OpenCode/DeepSeek serialization, all 61 transitions are append-only.
- The session contained approximately 293,278 bytes of assistant thinking. Rotating that history is therefore a large prefix mutation, not 1,024-token cache-granularity noise.
- New sessions and provider-router cold starts still lower aggregate/daily rates. They cannot have a 98% hit on their first unique long prompt. A warm, long-running session is the correct apples-to-apples comparison.
- The final requests in both substantive sessions exceeded 99%, proving that provider caching itself works once the prefix remains stable.

The 85% gap was thus not caused by one missing cache API flag. It was mainly an aggregate of cold prompts plus old prefix mutation, compounded by a false-positive miss counter.

## Silent-submit diagnosis

What is proved:

- Submission worked: the user message was persisted in both canonical and sent history.
- No provider usage or assistant event completed before the session was saved.
- The session ended after 24.918 seconds.
- The installed model/provider worked in fresh tests after the incident.
- A same-model interactive reproduction took about 24.3 seconds to produce its answer.
- A same-model headless text request succeeded.
- A same-model live tool-call request succeeded and completed its tool/result loop.

Most likely explanation: the model was still waiting for its first streamed output and Rick was interrupted/exited around the normal first-response time. This is strongly supported by the nearly identical 24.9s failed-session lifetime and 24.3s successful reproduction, but it cannot be proved because the old session schema discarded runtime errors and cancellation reasons.

A second real failure mode also existed: an OpenAI-compatible stream containing malformed JSON or a valid-but-empty completion could finish without a visible assistant message. The fixes below close that path and preserve the terminal diagnostic for future analysis.

## Tool-call diagnosis

Historical integrity checks over `8c68` and `12d8` found:

- equal counts of `tool_use` and `tool_result` blocks;
- no dangling tool calls;
- no orphan results;
- no duplicate call IDs;
- no empty call IDs or names;
- no invalid persisted JSON tool arguments.

This excludes corrupted persisted history as the cause in those two sessions. It does not exclude a transient provider error because Rick previously displayed `MsgError` only in the live TUI and omitted it from session JSON.

Pre-fix hardening gaps:

1. Invalid JSON in an SSE `data:` frame was silently ignored.
2. A streamed tool call with malformed arguments or no function name could be emitted and executed/replayed.
3. A provider `Done` event with no text, reasoning, or tool call could be treated as a successful empty turn.
4. Provider/agent errors were excluded from persisted session diagnostics.

## Implemented fixes

### Cache correctness already in the current v0.1.13 tree

- OpenCode/DeepSeek-family wire serialization retains all reasoning blocks, preserving an append-only prefix (`internal/provider/openai/openai.go:406-417`).
- TUI history rebuilding preserves thinking blocks instead of deleting provider-required reasoning (`internal/tui/agentbridge.go`).
- Cache-miss accounting measures a miss only when that specific usage event reports cache tokens (`internal/tui/agentbridge.go:292-323`).
- Anthropic long-retention requests send the extended cache-TTL beta header (`internal/provider/anthropic/anthropic.go`).
- Disk-cache eviction now preserves insertion order even when rapid Windows writes receive identical native filesystem timestamps (`internal/cache/cache.go`).

### New reliability hardening

- Malformed SSE JSON now produces a visible provider error instead of being skipped (`internal/provider/openai/openai.go`).
- OpenAI-compatible `delta.refusal` text is emitted as visible assistant output and counts as a valid completion rather than becoming a false empty-completion error.
- OpenAI-compatible streams must reach `[DONE]` or a non-empty `finish_reason`; clean EOF cannot flush a plausible but truncated call (`internal/provider/openai/openai.go`).
- Tool calls must have a name, unique ID, and JSON-object arguments. Validation occurs both at the OpenAI-compatible adapter and in the generic agent loop before history mutation or execution (`internal/provider/openai/openai.go`, `internal/agent/agent.go`). Truly empty streamed arguments remain normalized to `{}`.
- The provider adapter and generic agent loop reject completions with no text, reasoning, or tool call, and the agent rejects streams that end without any completion event.
- Streamed tool/reasoning history now records provider-turn boundaries after each tool-result group, including consecutive tool-only turns with no intervening text or reasoning. Save and resume preserve sequential tool dependencies instead of regrouping several turns as one parallel call batch (`internal/tui/agentbridge.go`, `internal/tui/message.go`, `internal/tui/modals.go`).
- The terminal run error is stored as `run_error` but kept out of provider-facing messages. It is restored on resume, cleared for a new or successful session, and cannot contaminate cache prefixes (`internal/session/session.go`, `internal/tui/agentbridge.go`, `internal/tui/modals.go`).
- Session save and current-session-pointer failures are returned and surfaced visibly instead of being ignored. Rick writes the session before publishing its current pointer, so the pointer cannot reference a session file that failed to persist.

Regression coverage includes malformed SSE, truncated streams, malformed/non-object arguments, missing names, duplicate call IDs, empty/missing completions, pre-execution validation, provider-turn reconstruction across live events and resume, diagnostic separation/hydration, and save failures.

## Verification

Completed before deployment:

- Focused packages: `go test ./internal/provider/openai ./internal/provider/anthropic ./internal/agent ./internal/session ./internal/tui -count=1`
- Full repository: `go test ./... -count=1`
- Static checks: `go vet ./...`
- Whitespace: `git diff --check`
- Final independent read-only review after the turn-boundary and session-pointer fixes: no high/medium findings remained

All passed. Two timing-sensitive failures surfaced during repeated runs (`internal/mcp` stdio EOF and `internal/cache` tied timestamps); MCP passed in subsequent full runs and the final canonical suite, while the real disk-cache timestamp ambiguity was fixed and stress-tested 100 times.

### Deployment and installed-command verification

- Built the final dirty tree as a separate verification executable, checked its version/build metadata, then exercised both a plain completion and a real read-tool loop before installation.
- Confirmed no `rick.exe` process was running; no process was killed.
- Preserved the earlier rollback copies and created the final pre-replacement backups with stamp `20260807-135053`.
- Deployed identical verified bytes to:
  - `C:/Users/einme/bin/rick`
  - `C:/Users/einme/bin/rick.exe`
- Shell resolution: `command -v rick` resolves `C:/Users/einme/bin/rick`.
- Both commands report `rick v0.1.13`.
- The final verification build and both installed aliases matched SHA-256 `896407bbed0cbae84d56e7ccdc1f93192211d0dc70b493d897e595ebc587f195` at deployment.
- Installed extensionless-command smoke returned exactly `REVIEWED_FINAL_OK`, usage, and `done`, with no error.
- Installed `.exe` tool-loop smoke emitted successful `tool_start` and `tool_end` events, returned `REVIEWED_TOOL_OK`, usage, and `done`, with no error.
- Disposable verification files and all post-review smoke-test sessions were removed. `current.json` was restored byte-for-byte to its pre-smoke workspace map, including `C:\\Users\\einme` → `2026-08-07T12-29-29_46f8` and no `G:\\projectE` test pointer.

### Late refusal-stream correction

A delayed final review identified one additional OpenAI-compatible edge case: streamed `delta.refusal` text was ignored and then misreported as an empty completion. The parser now emits refusal text as visible assistant output, with a focused regression test.

The final source passed the full suite, vet, formatting, diff checks, disposable build, and version smoke. Its disposable artifact SHA-256 was `53bcfed4b2d2f8b6f25f450dc9d317c0a462c27dbb9777fe596902517e8dedfb`. Deployment was not performed because `rick.exe` PID `27052` was running and was not killed. The installed aliases therefore remain at the prior verified SHA-256 `896407bbed0cbae84d56e7ccdc1f93192211d0dc70b493d897e595ebc587f195` and do not yet contain this late refusal-stream correction.

## Evidence sources

Local canonical artifacts:

- `C:/Users/einme/AppData/Local/rick/sessions/2026-08-07T11-18-14_8c68.json`
- `C:/Users/einme/AppData/Local/rick/sessions/2026-08-07T12-12-51_12d8.json`
- `C:/Users/einme/AppData/Local/rick/sessions/2026-08-07T12-29-29_46f8.json`
- `C:/Users/einme/AppData/Roaming/rick/usage.json`
- Current source and Git diff under `G:/projectE`

No credentials or prompt bodies are reproduced in this report.
