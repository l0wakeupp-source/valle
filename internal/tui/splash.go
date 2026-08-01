package tui

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// logoSmall is figlet's "small" font rendering of "rick", captured at build
// time so there is no runtime figlet dependency.
var logoSmall = []string{
	`  _    _   `,
	` _ _(_)__| |__`,
	`| '_| / _| / /`,
	`|_| |_\__|_\_\`,
}

// logoSlant is figlet's "slant" font, for terminals with room to spare.
var logoSlant = []string{
	`       _      __  `,
	` _____(_)____/ /__`,
	`/ ___/ / ___/ //_/`,
	`/ /  / / /__/ ,<   `,
	`/_/  /_/\___/_/|_| `,
}

// logoTiny is the fallback when the terminal is too narrow for either.
var logoTiny = []string{`rick`}

const tagline = "a lightweight coding agent for your terminal"

// tips rotate on each launch. Keep them short and actionable.
var tips = []string{
	"/help for commands",
	"@ to reference a file",
	"! to run a shell command",
	"/theme to change the look",
	"tab to switch between build and plan mode",
	"ctrl+x then h for keybindings",
	"/auth to connect a provider",
	"/sessions to resume earlier work",
}

// splash renders the startup screen shown before the first prompt.
//
// Wide terminals get the mascot on the right, vertically centred against the
// text block on the left. Narrow ones drop it rather than cramping the text.
func (m *Model) splash() string {
	left := m.splashText(m.splashTextWidth())
	art := m.splashArt()
	if art == nil {
		return left
	}
	out, _ := joinSplash(left, art, m.contentWidth(), m.viewport.Height)
	return out
}

// splashArtWidth is how many columns the mascot may use, or 0 for none.
//
// The portrait is taller than it is wide (~1.25:1), so height is the binding
// constraint far more often than width: at 46 columns it needs 29 rows, which
// does not fit an 80x24 terminal. Size from whichever runs out first.
func (m *Model) splashArtWidth() int {
	spare := m.contentWidth() - 56
	if spare < 20 || m.height < 20 {
		return 0
	}
	// Half-blocks lose detail as they grow — each cell is still just two
	// colours — so the block art is capped.
	cap := 40
	if spare > cap {
		spare = cap
	}
	// Leave two rows of breathing room above and below.
	for w := spare; w >= 20; w -= 2 {
		if artHeightFor(w) <= m.viewport.Height-2 {
			return w
		}
	}
	return 0
}

// splashTextWidth is the width available to the left-hand text block.
func (m *Model) splashTextWidth() int {
	if aw := m.splashArtWidth(); aw > 0 {
		return m.contentWidth() - aw - 3
	}
	return m.contentWidth()
}

// SplashArtLines is the rendered mascot block (test helper).
func (m *Model) SplashArtLines() []string { return m.splashArt() }

// SplashArtWidth is the mascot's column budget (test helper).
func (m *Model) SplashArtWidth() int { return m.splashArtWidth() }

// splashArt renders the pixelated mascot, or nil when there is no room.
func (m *Model) splashArt() []string {
	aw := m.splashArtWidth()
	if aw == 0 {
		return nil
	}
	return renderArt(aw)
}

// joinSplash places the art to the right of the text, each block centred
// vertically within the available height.
// joinSplash returns the composed splash and the image's top row.
func joinSplash(left string, art []string, width, height int) (string, int) {
	leftLines := strings.Split(left, "\n")
	artW := 0
	for _, l := range art {
		if n := lipgloss.Width(l); n > artW {
			artW = n
		}
	}
	if width-artW-3 < 1 {
		return left, 0
	}

	// Centre the shorter block against the taller one.
	rows := len(leftLines)
	if len(art) > rows {
		rows = len(art)
	}
	if height > rows {
		rows = height
	}
	lPad := (rows - len(leftLines)) / 2
	aPad := (rows - len(art)) / 2

	// The art starts at a fixed column so its right edge lands inside the
	// terminal regardless of how long each text line happens to be.
	artCol := width - artW
	if artCol < 1 {
		artCol = 1
	}

	var b strings.Builder
	for i := 0; i < rows; i++ {
		var lt, rt string
		if j := i - lPad; j >= 0 && j < len(leftLines) {
			lt = leftLines[j]
		}
		if j := i - aPad; j >= 0 && j < len(art) {
			rt = art[j]
		}
		if rt == "" {
			b.WriteString(strings.TrimRight(lt, " ") + "\n")
			continue
		}
		if lw := lipgloss.Width(lt); lw > artCol-1 {
			lt = truncate(lt, artCol-1)
		}
		gap := artCol - lipgloss.Width(lt)
		if gap < 1 {
			gap = 1
		}
		b.WriteString(lt + strings.Repeat(" ", gap) + rt + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), aPad
}

// splashText renders the left-hand block: logo, tagline, context and tip.
func (m *Model) splashText(w int) string {
	s := m.styles

	logo := logoSmall
	switch {
	case w < 24:
		logo = logoTiny
	case w >= 40 && m.height >= 24:
		logo = logoSlant
	}

	var b strings.Builder
	b.WriteString("\n")
	for _, line := range logo {
		b.WriteString("  " + s.Accent.Render(line) + "\n")
	}
	b.WriteString("\n")
	// Narrow terminals get the short form rather than a wrapped or clipped
	// line — the splash should stay calm at any width.
	tag := tagline
	if w < len(tagline)+4 {
		tag = "a lightweight coding agent"
	}
	if w < len(tag)+4 {
		tag = ""
	}
	if tag != "" {
		b.WriteString("  " + s.Muted.Render(tag) + "\n")
	}
	b.WriteString("\n")

	// Context: version, model, directory, branch.
	label := func(k, v string) string {
		if v == "" {
			return ""
		}
		return s.Faint.Render(k+" ") + s.Muted.Render(v)
	}
	ctx := []string{
		label("", m.deps.Version),
		label("model", m.displayModel()),
		label("dir", prettyPath(m.deps.Cwd)),
	}
	if br := cachedGitBranch(m.deps.Cwd); br != "" {
		ctx = append(ctx, label("branch", br))
	}
	// Drop context segments from the right until the line fits.
	segs := compact(ctx)
	sep := s.Faint.Render("  ·  ")
	for len(segs) > 1 && lipgloss.Width(strings.Join(segs, sep))+2 > w {
		segs = segs[:len(segs)-1]
	}
	line := "  " + strings.Join(segs, sep)
	if lipgloss.Width(line) > w {
		line = truncate(line, w)
	}
	b.WriteString(line + "\n")

	switch {
	case !m.hasAnyProvider():
		b.WriteString("\n  " + s.Warning.Render("no provider connected") +
			s.Faint.Render("  ·  run ") + s.Accent.Render("/auth") + s.Faint.Render(" or ") + s.Accent.Render("/webproviders") + "\n")
	case m.resumable != "":
		// rick starts fresh; surface the earlier session instead of
		// resuming it silently. Fall back to shorter forms before dropping
		// the hint, since the text column narrows when the art is shown.
		for _, line := range []string{
			s.Faint.Render("earlier session: ") + s.Muted.Render(m.resumable) +
				s.Faint.Render("  ·  ") + s.Accent.Render("/sessions") + s.Faint.Render(" to resume"),
			s.Muted.Render(truncate(m.resumable, 24)) +
				s.Faint.Render("  ·  ") + s.Accent.Render("/sessions"),
			s.Accent.Render("/sessions") + s.Faint.Render(" to resume earlier work"),
			s.Accent.Render("/sessions"),
		} {
			if lipgloss.Width(line) <= w-2 {
				b.WriteString("\n  " + line + "\n")
				return b.String()
			}
		}
		fallthrough
	default:
		if tip := fitTip(m.tip, w-2); tip != "" {
			b.WriteString("\n  " + s.Faint.Render(tip) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func homeDir() (string, error) { return os.UserHomeDir() }

func timeSince(t time.Time) float64 { return time.Since(t).Seconds() }

func compact(in []string) []string {
	out := in[:0]
	for _, v := range in {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// pickTip chooses a tip for this launch.
func pickTip() string { return tips[rand.Intn(len(tips))] }

// fitTip returns the chosen tip, or the shortest available one, or nothing —
// a tip is never worth wrapping or overflowing the splash for.
func fitTip(tip string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(tip) <= width {
		return tip
	}
	best := ""
	for _, t := range tips {
		if len(t) <= width && len(t) > len(best) {
			best = t
		}
	}
	return best
}

// prettyPath shortens a path, using ~ for the home directory.
func prettyPath(p string) string {
	home, err := homeDir()
	if err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil &&
			!strings.HasPrefix(rel, "..") && rel != "." {
			return "~/" + filepath.ToSlash(rel)
		}
		if p == home {
			return "~"
		}
	}
	slash := filepath.ToSlash(p)
	parts := strings.Split(slash, "/")
	if len(parts) > 3 {
		return ".../" + strings.Join(parts[len(parts)-2:], "/")
	}
	return slash
}

// parseGitBranch extracts the branch name from session.GitInfo output,
// formatted as "branch NAME[, N file(s) modified]".
func parseGitBranch(info string) string {
	if info == "" {
		return ""
	}
	info = strings.TrimPrefix(info, "branch ")
	if i := strings.IndexByte(info, ','); i > 0 {
		info = info[:i]
	}
	return strings.TrimSpace(info)
}

func (m *Model) hasAnyProvider() bool { return len(m.deps.Providers) > 0 }

// latestSessionTitle names the most recent session for this directory, so the
// splash can offer it without resuming it.
func latestSessionTitle(d Deps) string {
	if d.Store == nil || d.ResumeID != "" {
		return ""
	}
	metas, err := d.Store.List(d.Cwd)
	if err != nil || len(metas) == 0 {
		return ""
	}
	title := metas[0].Title
	if title == "" {
		title = "(untitled)"
	}
	return truncate(title, 40)
}

// statsSegment renders the context gauge and token counters, in the spirit of
// Hermes' status line: a compact bar, the percentage, and the token split.
// statsSegment renders the right-hand stats: a context gauge, then the token
// split, then turn time and reasoning level.
//
// Layout, widest first:
//
//	399k/1M ████████░░░░░░░░ 40%  ↑12.4k ↓3.4k ⚡2.0k  8.2s  reasoning:medium
//
// Each piece is dropped in order of least value when the terminal is narrow,
// so the gauge survives longest.
func (m *Model) statsSegment() string {
	s := m.styles

	gauge := m.contextGauge()
	tokens := m.tokenSplit()

	var tail []string
	if !m.turnStart.IsZero() {
		d := m.turnElapsed
		if m.running {
			d = time.Since(m.turnStart)
		}
		if d >= time.Second {
			tail = append(tail, s.Faint.Render(humanDuration(d)))
		}
	}
	if r := m.reasoningSegment(); r != "" {
		tail = append(tail, s.Faint.Render(r))
	}

	sep := s.Faint.Render("  ")
	// Try the full line, then shed the least useful parts until it fits.
	budget := m.width/2 + 8
	for _, attempt := range [][]string{
		append([]string{gauge, tokens}, tail...),
		append([]string{gauge, tokens}, firstOf(tail)...),
		{gauge, tokens},
		{gauge},
		{m.contextGaugeCompact()},
	} {
		line := strings.Join(compact(attempt), sep)
		if line == "" {
			continue
		}
		if lipgloss.Width(line) <= budget {
			return line
		}
	}
	return ""
}

func firstOf(v []string) []string {
	if len(v) == 0 {
		return nil
	}
	return v[:1]
}

// contextGauge draws "used/total ███░░░ pct".
func (m *Model) contextGauge() string {
	s := m.styles
	if m.ctxWindow <= 0 {
		return ""
	}
	used := m.usage.Input + m.usage.CacheRead + m.usage.CacheWrite + m.usage.Output
	if used <= 0 {
		return ""
	}
	// The next request cannot exceed the window; showing 1.3M/1M would just
	// look broken. Clamp and let the 100% read as "full".
	if used > m.ctxWindow {
		used = m.ctxWindow
	}
	pct := m.contextPct()

	fill := s.Accent
	switch {
	case pct >= 90:
		fill = s.Error
	case pct >= 70:
		fill = s.Warning
	}

	width := 16
	if m.width < 100 {
		width = 10
	}
	if m.width < 80 {
		width = 6
	}
	filled := pct * width / 100
	if filled == 0 && pct > 0 {
		filled = 1 // never show an empty bar for non-zero usage
	}
	if filled > width {
		filled = width
	}

	bar := fill.Render(strings.Repeat("█", filled)) +
		s.Faint.Render(strings.Repeat("░", width-filled))

	return s.Muted.Render(humanTokens(used)+"/"+humanTokens(m.ctxWindow)) + " " +
		bar + " " + fill.Render(fmt.Sprintf("%d%%", pct))
}

// contextGaugeCompact is the last-resort form for very narrow terminals.
func (m *Model) contextGaugeCompact() string {
	pct := m.contextPct()
	if pct <= 0 {
		return ""
	}
	style := m.styles.Faint
	switch {
	case pct >= 90:
		style = m.styles.Error
	case pct >= 70:
		style = m.styles.Warning
	}
	return style.Render(fmt.Sprintf("%d%%", pct))
}

// tokenSplit reports what the session has actually spent, by kind. Cache hits
// are billed differently from fresh input, so they are worth separating.
// Input = cache miss (new tokens), ⚡ = cache hit (read), ✏ = cache write.
func (m *Model) tokenSplit() string {
	s := m.styles
	if m.billed.Input+m.billed.Output+m.billed.CacheRead+m.billed.CacheWrite == 0 {
		return ""
	}
	parts := []string{
		s.Faint.Render("↑" + humanTokens(m.billed.Input)),
		s.Faint.Render("↓" + humanTokens(m.billed.Output)),
	}
	if m.billed.CacheRead > 0 {
		parts = append(parts, s.Secondary.Render("⚡"+humanTokens(m.billed.CacheRead)))
	}
	if m.billed.CacheWrite > 0 {
		parts = append(parts, s.Secondary.Render("✏"+humanTokens(m.billed.CacheWrite)))
	}
	return strings.Join(parts, " ")
}

// contextPct is how full the context window is, 0 when nothing is known.
//
// usage.Input already includes the whole replayed conversation plus cache
// reads, so it is the occupancy of the next request; adding the reply gives
// what the following turn will carry.
func (m *Model) contextPct() int {
	if m.ctxWindow <= 0 {
		return 0
	}
	used := m.usage.Input + m.usage.CacheRead + m.usage.CacheWrite + m.usage.Output
	if used <= 0 {
		return 0
	}
	pct := used * 100 / m.ctxWindow
	if pct > 100 {
		pct = 100
	}
	if pct < 1 {
		return 1
	}
	return pct
}

// humanDuration formats an elapsed turn compactly.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// statusLine is the single dim line pinned under the input box.
func (m *Model) statusBar() string {
	s := m.styles
	if m.deps.Loaded.TUI.HideStatus {
		return ""
	}

	left := ""
	switch {
	case m.leaderActive:
		left = s.Accent.Render("leader") +
			s.Faint.Render("  h help · m models · t themes · n new · l sessions · u undo · r redo · d details")
	case m.status != "" && timeSince(m.statusTime) < 6:
		left = s.Muted.Render(m.status)
	case m.running:
		left = s.Muted.Render(m.spinnerFrame()+" working") + s.Faint.Render("  esc to interrupt")
	default:
		segs := []string{m.displayModel(), prettyPath(m.deps.Cwd)}
		if br := cachedGitBranch(m.deps.Cwd); br != "" {
			segs = append(segs, br)
		}
		segs = append(segs, m.agentName)
		left = s.Faint.Render(strings.Join(segs, " · "))
	}

	if m.deps.Perms != nil && m.deps.Perms.Yolo() {
		left = s.Error.Render("⚠ YOLO") + s.Faint.Render(" · ") + left
	}

	right := m.statsSegment()
	// A scrolled-away user gets an unobtrusive nudge back to live.
	if !m.tx.following() {
		badge := s.Accent.Render("↓ new")
		if n := m.tx.pending(); n > 0 {
			badge = s.Accent.Render(fmt.Sprintf("↓ %d new", n))
		}
		if right != "" {
			right = badge + s.Faint.Render("  ·  ") + right
		} else {
			right = badge
		}
	}

	// Keep the line on one row. The stats are the reason to look at the bar,
	// so squeeze the left (model/path, all of it recoverable elsewhere)
	// before sacrificing them.
	avail := m.width - 2
	rw := lipgloss.Width(right)
	if rw > avail {
		right, rw = "", 0
	}
	if lw := lipgloss.Width(left); lw+rw+2 > avail {
		room := avail - rw - 2
		if room < 8 {
			left = ""
		} else {
			left = truncate(left, room)
		}
	}
	gap := avail - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}
