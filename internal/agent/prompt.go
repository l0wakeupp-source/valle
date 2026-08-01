package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// BuildPrompt is the system prompt for the default all-tools agent.
const BuildPrompt = `You are rick, a CLI coding agent. Work in the project directory the user opened.

Be concise — no preamble, no summaries, no emoji. Plain text; markdown only for lists/code.

- Use the dedicated tools over shell equivalents where available.
- For web research, use websearch; do not use bash, curl, wget, or fetch.
- Read before you edit. Match existing style and dependencies.
- Make the smallest change. No comments unless non-obvious.
- Verify with the project's build/test/lint after changing code.
- Never commit or push unless asked.
- For 3+ step tasks: call todowrite first, one item in_progress, mark done as you go.
- Stop if a tool call is rejected — ask how to proceed.
- Answer questions about code with path:line citations.`

// PlanPrompt is the read-mostly planning agent.
const PlanPrompt = `You are rick in PLAN mode. Investigate and plan — do NOT edit or run mutating commands.

Deliver a concise plan:
- What the change is and why.
- Files and functions involved (path:line).
- Ordered, bite-sized steps, each independently verifiable.
- Risks and unknowns.

When done, tell the user to switch to build mode (Tab) to execute.`

// GeneralSubagentPrompt drives the general-purpose subagent.
const GeneralSubagentPrompt = `You are a subagent spawned by rick for one focused task. You have all tools except
delegation. Work autonomously — no user questions. If ambiguous, pick the most
reasonable option and say so.

For web research, use websearch; do not use bash, curl, wget, or fetch.

Your final message is all the parent sees: what you found or did, the exact file
paths and line numbers, and anything the parent needs to continue. Do not reference
context the parent cannot see.`

// ExploreSubagentPrompt drives the read-only search subagent.
const ExploreSubagentPrompt = `You are a read-only exploration subagent. Search and read only — never modify.

For web research, use websearch; do not use bash, curl, wget, or fetch.

Be fast: grep and glob aggressively, read only what matters, stop when you can answer the question.

Report concrete findings with file paths and line numbers, not a search narrative.`

// Environment renders the environment block appended to every system prompt.
func Environment(cwd, model, agentName string, gitInfo string) string {
	var b strings.Builder
	b.WriteString("\n\n## Environment\n")
	fmt.Fprintf(&b, "Working directory: %s\n", cwd)
	fmt.Fprintf(&b, "Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "Today: %s\n", time.Now().Format("Monday, 2 January 2006"))
	fmt.Fprintf(&b, "Model: %s\n", model)
	fmt.Fprintf(&b, "Agent: %s\n", agentName)
	if gitInfo != "" {
		fmt.Fprintf(&b, "Git: %s\n", gitInfo)
	}
	if runtime.GOOS == "windows" {
		b.WriteString("Shell: bash (git-bash/MSYS). Use POSIX syntax; PowerShell builtins will not work.\n")
	}
	return b.String()
}

// ProjectContext loads RICK.md / AGENTS.md style instruction files.
func ProjectContext(root string, extraGlobs []string) string {
	var parts []string
	seen := map[string]bool{}

	add := func(path string) {
		abs, _ := filepath.Abs(path)
		if seen[abs] {
			return
		}
		seen[abs] = true
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			return
		}
		if len(data) > 32<<10 {
			data = append(data[:32<<10], []byte("\n…<truncated>")...)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		parts = append(parts, fmt.Sprintf("### %s\n%s", filepath.ToSlash(rel), strings.TrimSpace(string(data))))
	}

	for _, name := range []string{"RICK.md", "AGENTS.md", "CLAUDE.md", ".rick/RICK.md"} {
		add(filepath.Join(root, name))
	}
	for _, g := range extraGlobs {
		p := g
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, g)
		}
		matches, err := filepath.Glob(p)
		if err != nil {
			continue
		}
		for _, m := range matches {
			add(m)
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return "\n\n## Project instructions\nThe following files describe this project's conventions. Follow them.\n\n" +
		strings.Join(parts, "\n\n")
}

// TitlePrompt asks the small model for a session title.
const TitlePrompt = `Generate a short title (max 6 words) summarising this conversation topic.
Reply with the title only — no quotes, no punctuation at the end, no preamble.`

// CompactPrompt asks the model to summarise a conversation for compaction.
const CompactPrompt = `Summarise the conversation so far so that work can continue without the full
history. Preserve, in this order:

1. The user's overall goal and any explicit constraints or preferences.
2. Key technical findings: file paths, function names, architecture decisions.
3. Every change already made (file path + what changed).
4. The current state: what works, what is broken, what was being attempted.
5. The immediate next step.

Be specific and dense — include exact identifiers and paths. Omit conversational
filler. Write it as notes to your future self, not as a report to the user.`

// InitPrompt drives the /init command.
const InitPrompt = `Analyse this codebase and write a RICK.md at the project root.

Read the README, build files, and enough source to understand the architecture. Then
write RICK.md containing:

- One paragraph on what the project is.
- Build, test, lint and run commands (exact command lines).
- High-level architecture: main packages/modules and their relationships.
- Code conventions actually used (naming, error handling, testing, formatting) —
  describe what you observed, not generic best practice.
- Anything a new contributor would trip over.

Keep it under 60 lines. If RICK.md or AGENTS.md already exists, read and improve it
rather than starting over. Write with the write tool, then confirm in one line.`
