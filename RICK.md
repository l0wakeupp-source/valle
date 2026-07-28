# RICK.md

rick is a Go terminal AI coding agent. It talks to LLM providers, gives the model
tools to read/search/edit files and run commands, and wraps it in a Bubble Tea TUI.

## Commands

```
go build ./...               # build everything
go build -o rick.exe ./cmd/rick
go run ./cmd/rickverify       # 247 unit/integration checks
go run ./cmd/ricke2e          # 19 end-to-end checks over real HTTP+SSE
go vet ./...
```

Verification lives in the two `cmd/` harnesses — there is no `go test`. Both must
stay green.

## Architecture

```
tui  ->  agent  ->  provider
              \->  tools  ->  permission
```

- `provider` owns its stream channel; closes it exactly once.
- `agent.Runner.Run` owns its output channel the same way.
- `tools.Registry` maps names to implementations.
- `tui.Model` is the only place that mutates UI state — inside `Update`.

## Hard rules

1. Never mutate `*tui.Model` from a goroutine — send a message instead.
2. Never close a channel you don't own.
3. Snapshots must stay outside the work tree (shadow repo destroys its own index).
4. Every `tool_use` needs a matching `tool_result` in the next user message.
5. Permission checks happen in `agent.execOne`, not in tools.

## Conventions

- Comments explain *why*, not *what*. Exported symbols get doc comments.
- Model-facing errors are `tools.Result{IsError: true}`; say what to do next.
- Prefer stdlib; every dependency must earn its place.
- Styles from theme (`m.styles.Muted`), never hardcoded.
- New config key needs: struct field, merge case, default, schema entry.

## Adding things

- **Tool**: implement `tools.Tool`, register in `buildDeps`, permission case if mutating, checks in `rickverify`.
- **Provider**: implement `provider.Provider` in `internal/provider/<name>/`, case in `buildProviders`.
- **Slash command**: add to `slashCommands` in `picker.go`, case in `runSlash` in `keys.go`.

## Gotchas

- Windows bash tool prefers git-bash; `cmd.exe` is last resort.
- Outside a TTY lipgloss strips colour — headless verifiers force TrueColor.
- Welcome banner seeds on first `WindowSizeMsg`, not in `Init`.
- `/new` clears transcript — run assertions about earlier content before it.
