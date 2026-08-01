// Command rickui verifies the visual pass: splash, theme tokens, mode borders,
// and the scrolling behaviour under the three stress cases from the spec.
package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"rick/internal/config"
	"rick/internal/permission"
	"rick/internal/provider"
	"rick/internal/provider/anthropic"
	"rick/internal/provider/openai"
	"rick/internal/session"
	"rick/internal/theme"
	"rick/internal/tools"
	"rick/internal/tui"
)

var pass, fail int
var failures []string

func check(name string, cond bool, detail ...string) {
	if cond {
		pass++
		fmt.Printf("  PASS  %s\n", name)
		return
	}
	fail++
	msg := name
	if len(detail) > 0 {
		msg += " — " + strings.Join(detail, " ")
	}
	failures = append(failures, msg)
	fmt.Printf("  FAIL  %s\n", msg)
}

func section(s string) { fmt.Printf("\n== %s ==\n", s) }

func send(m *tui.Model, msgs ...tea.Msg) *tui.Model {
	for _, msg := range msgs {
		nm, cmd := m.Update(msg)
		m = nm.(*tui.Model)
		m = runCmd(m, cmd, 0)
	}
	return m
}

func runCmd(m *tui.Model, cmd tea.Cmd, depth int) *tui.Model {
	if cmd == nil || depth > 3 {
		return m
	}
	out := cmd()
	if out == nil {
		return m
	}
	if batch, ok := out.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = runCmd(m, c, depth+1)
		}
		return m
	}
	if strings.Contains(fmt.Sprintf("%T", out), "TickMsg") ||
		strings.Contains(fmt.Sprintf("%T", out), "themePoll") {
		return m
	}
	nm, next := m.Update(out)
	m = nm.(*tui.Model)
	return runCmd(m, next, depth+1)
}

func key(m *tui.Model, k string) *tui.Model {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		msg = tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		msg = tea.KeyMsg{Type: tea.KeyPgDown}
	case "end":
		msg = tea.KeyMsg{Type: tea.KeyEnd}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	return send(m, msg)
}

// stubProviders builds a deterministic provider set with known model lists,
// so the two-stage picker can be driven without network access.
func stubProviders() map[string]provider.Provider {
	oa := openai.New("longcat", "k", "https://api.longcat.chat/openai/v1")
	oa.SetModels([]provider.ModelInfo{
		{ID: "LongCat-2.0", Name: "LongCat-2.0", ContextWindow: 128000},
	})
	nv := openai.New("nvidia", "k", "https://integrate.api.nvidia.com/v1")
	nv.SetModels([]provider.ModelInfo{
		{ID: "nemotron-70b", ContextWindow: 32000},
		{ID: "nemotron-8b", ContextWindow: 16000},
	})
	return map[string]provider.Provider{
		"anthropic": anthropic.New("sk-demo", ""),
		"longcat":   oa,
		"nvidia":    nv,
	}
}

func typeStr(m *tui.Model, s string) *tui.Model {
	for _, r := range s {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func wheel(m *tui.Model, up bool, n int) *tui.Model {
	btn := tea.MouseButtonWheelDown
	if up {
		btn = tea.MouseButtonWheelUp
	}
	for i := 0; i < n; i++ {
		m = send(m, tea.MouseMsg{Button: btn, Action: tea.MouseActionPress})
	}
	return m
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRE.ReplaceAllString(s, "") }

// borderColor returns the SGR code of the first box-drawing line.
func borderColor(view string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "╭") {
			if m := regexp.MustCompile(`\x1b\[38;2;([0-9;]+)m`).FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

func build(tmp, themeName string, w, h int) *tui.Model {
	return buildWithCredentials(tmp, themeName, w, h, nil)
}

func buildWithCredentials(tmp, themeName string, w, h int, creds *config.Credentials) *tui.Model {
	if creds == nil {
		creds = &config.Credentials{Providers: map[string]config.Credential{}}
	}
	cwd := filepath.Join(tmp, "proj")
	_ = os.MkdirAll(cwd, 0o755)
	loaded, _ := config.Load(cwd)
	loaded.TUI.Theme = themeName

	dirs := []string{filepath.Join(tmp, "themes")}
	themes := theme.Load(dirs...)
	store, _ := session.NewStore(filepath.Join(tmp, "s"))
	snaps, _ := session.NewSnapshotter(cwd, filepath.Join(tmp, "d"))

	return send(tui.New(tui.Deps{
		Loaded: loaded, Themes: themes, ThemeDirs: theme.NewWatcher(dirs...),
		Registry: tools.NewRegistry(), Todos: tools.NewTodoStore(),
		Perms: permission.New(loaded.Config.Permission, cwd),
		Store: store, Snapshots: snaps,
		Providers:   stubProviders(),
		Credentials: creds,
		Cwd:         cwd, Version: "v1.2.0",
	}), tea.WindowSizeMsg{Width: w, Height: h})
}

func main() {
	termenv.SetDefaultOutput(termenv.NewOutput(os.Stdout, termenv.WithProfile(termenv.TrueColor)))
	lipgloss.SetColorProfile(termenv.TrueColor)

	tmp, _ := os.MkdirTemp("", "rickui-")
	defer os.RemoveAll(tmp)
	os.Setenv("RICK_HOME", filepath.Join(tmp, "cfg"))
	os.Setenv("RICK_DATA", filepath.Join(tmp, "data"))

	testSplash(tmp)
	testThemes(tmp)
	testModeBorders(tmp)
	testTokenPurity()
	testScrolling(tmp)
	testReportedIssues(tmp)
	testFrameHeight(tmp)
	testFreshStart(tmp)
	testContextAccounting(tmp)
	testReasoningLevels(tmp)
	testStatusGauge(tmp)
	testSplashArt(tmp)
	testPerformance(tmp)
	testStatsReset(tmp)
	testTerminalImage(tmp)
	testModelPersistence(tmp)
	testImageDetection(tmp)

	fmt.Printf("\n%d passed, %d failed\n", pass, fail)
	if fail > 0 {
		fmt.Println("\nfailures:")
		for _, f := range failures {
			fmt.Println("  - " + f)
		}
		os.Exit(1)
	}
}

func testSplash(tmp string) {
	section("startup splash")

	m := build(tmp, "pickle-rick", 88, 30)
	v := m.View()
	p := plain(v)

	check("logo is rendered", strings.Contains(p, `/ ___/ / ___/ //_/`), "no logo")
	check("tagline is shown",
		strings.Contains(p, "a lightweight coding agent for your terminal"))
	check("version is shown", strings.Contains(p, "v1.2.0"))
	check("model is shown", strings.Contains(p, "sonnet"))
	check("directory is shown", strings.Contains(p, "proj"))
	check("a tip is shown", strings.Contains(p, "/help") || strings.Contains(p, "@ to") ||
		strings.Contains(p, "! to") || strings.Contains(p, "/theme") ||
		strings.Contains(p, "tab to") || strings.Contains(p, "ctrl+x") ||
		strings.Contains(p, "/auth") || strings.Contains(p, "/sessions"), p)
	check("input box is bordered", strings.Contains(p, "╭") && strings.Contains(p, "╰"))

	// The logo must be in the accent colour, not plain text.
	logoLine := ""
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, "___/") {
			logoLine = l
			break
		}
	}
	check("LOGO IS ACCENT-COLOURED (light green)",
		strings.Contains(logoLine, "38;2;124;227;139"), logoLine)

	// The splash must disappear once a conversation starts.
	m2 := build(tmp, "pickle-rick", 88, 30)
	m2.InputSetValue("hello there")
	m2 = key(m2, "enter")
	check("SPLASH DISAPPEARS AFTER THE FIRST MESSAGE",
		!strings.Contains(plain(m2.View()), "a lightweight coding agent"), "splash still showing")

	// Narrow terminals must degrade, not overflow.
	// Measure display width, not bytes: box-drawing runes are multi-byte.
	m3 := build(tmp, "pickle-rick", 40, 18)
	for _, line := range strings.Split(plain(m3.View()), "\n") {
		if w := lipgloss.Width(line); w > 40 {
			check("narrow terminal does not overflow", false, fmt.Sprintf("%d cols: %q", w, line))
			return
		}
	}
	check("narrow terminal does not overflow", true)
}

func testThemes(tmp string) {
	section("theme system")

	dark := build(tmp, "pickle-rick", 88, 30)
	black := build(tmp, "rick-black", 88, 30)
	vampire := build(tmp, "evil-rick", 88, 30)

	check("pickle-rick loads", dark.ThemeName() == "pickle-rick", dark.ThemeName())
	check("rick-black loads", black.ThemeName() == "rick-black", black.ThemeName())
	check("evil-rick loads", vampire.ThemeName() == "evil-rick", vampire.ThemeName())

	check("dark accent is #7CE38B", borderColor(dark.View()) == "124;227;139", borderColor(dark.View()))
	check("BLACK THEME HAS DIFFERENT ACCENT (#00D4AA)",
		borderColor(black.View()) == "0;211;170", borderColor(black.View()))
	check("the two themes are visibly different",
		borderColor(dark.View()) != borderColor(black.View()))

	// /theme prints a numbered list into the conversation.
	m := build(tmp, "pickle-rick", 88, 30)
	m.InputSetValue("/theme")
	m = key(m, "enter")
	check("/theme prints inline, not as a window", !m.ModalOpen(), "opened a modal")
	check("/theme arms a numbered choice", m.PendingKind() != 0, fmt.Sprint(m.PendingKind()))
	pv := plain(m.View())
	check("list includes pickle-rick", strings.Contains(pv, "pickle-rick"))
	check("list includes rick-black", strings.Contains(pv, "rick-black"))
	check("list includes evil-rick", strings.Contains(pv, "evil-rick"))

	// Cancelling leaves the theme alone.
	before := m.ThemeName()
	m.InputSetValue("")
	m = key(m, "enter")
	check("cancelling keeps the original theme", m.ThemeName() == before,
		before+" -> "+m.ThemeName())
	check("cancelling clears the choice", m.PendingKind() == 0)

	// Selecting by name applies and persists.
	m.InputSetValue("/theme")
	m = key(m, "enter")
	m.InputSetValue("rick-black")
	m = key(m, "enter")
	check("SELECTING A THEME APPLIES IT", m.ThemeName() == "rick-black", m.ThemeName())
	check("choice is written to tui.json", func() bool {
		b, err := os.ReadFile(filepath.Join(config.GlobalDir(), "tui.json"))
		return err == nil && strings.Contains(string(b), "rick-black")
	}(), "not persisted")

	// Hot reload: a custom theme file appearing must be picked up.
	custom := filepath.Join(tmp, "themes")
	_ = os.MkdirAll(custom, 0o755)
	_ = os.WriteFile(filepath.Join(custom, "hotpink.json"), []byte(`{
	  "name":"hotpink","defs":{"p":"#FF69B4"},
	  "theme":{"text":"p","primary":"p","accent":"p","promptBorder":"p","border":"p"}}`), 0o644)

	w := theme.NewWatcher(custom)
	// Watcher is primed at construction; the file predates it, so touch it.
	time.Sleep(15 * time.Millisecond)
	_ = os.Chtimes(filepath.Join(custom, "hotpink.json"), time.Now(), time.Now())
	check("WATCHER DETECTS A THEME FILE CHANGE", w.Changed(), "no change reported")
	check("watcher is quiet when nothing changed", !w.Changed(), "spurious change")

	reg := theme.Load(custom)
	check("custom theme is loaded from disk", reg.Get("hotpink") != nil)
	check("built-ins sort before custom themes",
		reg.SortedNames()[0] == "pickle-rick", fmt.Sprint(reg.SortedNames()[:3]))
}

func testModeBorders(tmp string) {
	section("plan vs build mode")

	m := build(tmp, "pickle-rick", 88, 30)
	buildColor := borderColor(m.View())
	m = key(m, "tab")
	planColor := borderColor(m.View())

	check("build mode border is the green accent", buildColor == "124;227;139", buildColor)
	check("PLAN MODE BORDER IS BLUE (#5CB8E0)", planColor == "92;184;224", planColor)
	check("MODE IS DISTINGUISHABLE BY BORDER COLOUR ALONE",
		buildColor != planColor, buildColor+" vs "+planColor)

	m = key(m, "tab")
	check("tabbing back restores the build colour", borderColor(m.View()) == buildColor)
}

func testTokenPurity() {
	section("theme token purity")

	// The DoD requires no hardcoded colours outside the theme package.
	var offenders []string
	roots := []string{"internal", "cmd"}
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			slash := filepath.ToSlash(path)
			// Skip the theme package (colours belong there) and this file,
			// which contains the search pattern as data.
			if strings.Contains(slash, "internal/theme/") ||
				strings.Contains(slash, "cmd/rickui/") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for i, line := range strings.Split(string(b), "\n") {
				if strings.Contains(line, `lipgloss.Color("#`) ||
					strings.Contains(line, "lipgloss.AdaptiveColor{") {
					offenders = append(offenders, fmt.Sprintf("%s:%d", path, i+1))
				}
			}
			return nil
		})
	}
	check("NO HARDCODED COLOURS OUTSIDE THE THEME PACKAGE",
		len(offenders) == 0, strings.Join(offenders, " "))
}

func testScrolling(tmp string) {
	section("scrolling — the three stress cases")

	// Case 1: long output.
	m := build(tmp, "pickle-rick", 80, 24)
	for i := 0; i < 200; i++ {
		m.PushSystem(fmt.Sprintf("line %03d of long tool output", i))
	}
	check("auto-sticks to the bottom as content arrives", m.Following())
	check("bottom of the content is visible",
		strings.Contains(plain(m.View()), "line 199"), "tail not shown")

	// Scrolling up must stop the auto-follow.
	m = key(m, "pgup")
	check("SCROLLING UP DISENGAGES AUTO-FOLLOW", !m.Following())
	offset := m.ScrollOffset()

	// New content must NOT yank the user back down.
	for i := 0; i < 20; i++ {
		m.PushSystem(fmt.Sprintf("late line %d", i))
	}
	check("NEW CONTENT DOES NOT YANK A SCROLLED-UP USER",
		m.ScrollOffset() == offset, fmt.Sprintf("%d -> %d", offset, m.ScrollOffset()))
	check("unseen counter tracks what arrived", m.Pending() >= 20, fmt.Sprint(m.Pending()))
	check("status shows a new-content indicator",
		strings.Contains(plain(m.View()), "new"), "no indicator")

	// Returning to the bottom must resume following.
	m = key(m, "end")
	check("END returns to live", m.Following())
	check("unseen counter resets", m.Pending() == 0, fmt.Sprint(m.Pending()))
	m.PushSystem("after resume")
	check("following again after returning to the bottom",
		strings.Contains(plain(m.View()), "after resume"))

	// Scrolling back down manually also resumes.
	m = key(m, "pgup")
	check("scrolled away again", !m.Following())
	for i := 0; i < 40; i++ {
		m = key(m, "pgdown")
	}
	check("SCROLLING BACK TO THE BOTTOM RESUMES AUTO-FOLLOW", m.Following())

	// Increments must be small and consistent, not full-page jumps.
	m2 := build(tmp, "pickle-rick", 80, 24)
	for i := 0; i < 200; i++ {
		m2.PushSystem(fmt.Sprintf("row %03d", i))
	}
	bottom := m2.ScrollOffset()
	m2 = wheel(m2, true, 1)
	step1 := bottom - m2.ScrollOffset()
	m2 = wheel(m2, true, 1)
	step2 := (bottom - step1) - m2.ScrollOffset()
	check("wheel scrolls by a small step", step1 > 0 && step1 <= 5, fmt.Sprint(step1))
	check("wheel steps are consistent", step1 == step2, fmt.Sprintf("%d vs %d", step1, step2))
	check("wheel up disengages following", !m2.Following())
	m2 = wheel(m2, false, 50)
	check("wheel down returns to live", m2.Following())

	// Case 2: rapid streaming must not fight the user's scroll position.
	m3 := build(tmp, "pickle-rick", 80, 24)
	for i := 0; i < 100; i++ {
		m3.PushSystem(fmt.Sprintf("history %03d", i))
	}
	m3 = key(m3, "pgup")
	held := m3.ScrollOffset()
	for i := 0; i < 60; i++ {
		m3.PushStreamChunk(fmt.Sprintf("token%d ", i))
	}
	check("STREAMING DOES NOT MOVE A SCROLLED-UP USER",
		m3.ScrollOffset() == held, fmt.Sprintf("%d -> %d", held, m3.ScrollOffset()))

	m3 = key(m3, "end")
	for i := 0; i < 60; i++ {
		m3.PushStreamChunk(fmt.Sprintf("more%d ", i))
	}
	check("streaming follows the tail when live", m3.Following())

	// Case 3: resize mid-stream must preserve relative position.
	m4 := build(tmp, "pickle-rick", 80, 24)
	for i := 0; i < 300; i++ {
		m4.PushSystem(fmt.Sprintf("resize row %03d with enough text to wrap when the terminal narrows", i))
	}
	m4 = key(m4, "pgup")
	m4 = key(m4, "pgup")
	fracBefore := m4.ScrollFraction()
	m4 = send(m4, tea.WindowSizeMsg{Width: 50, Height: 24})
	fracAfter := m4.ScrollFraction()
	drift := fracBefore - fracAfter
	if drift < 0 {
		drift = -drift
	}
	check("RESIZE PRESERVES RELATIVE SCROLL POSITION", drift < 0.08,
		fmt.Sprintf("%.3f -> %.3f", fracBefore, fracAfter))
	check("resize does not snap to top", m4.ScrollOffset() > 0, fmt.Sprint(m4.ScrollOffset()))
	check("resize does not snap to bottom while scrolled up", !m4.Following())

	// Resizing while following must keep following.
	m5 := build(tmp, "pickle-rick", 80, 24)
	for i := 0; i < 100; i++ {
		m5.PushSystem(fmt.Sprintf("row %d", i))
	}
	m5 = send(m5, tea.WindowSizeMsg{Width: 60, Height: 30})
	check("resize while live keeps following the tail", m5.Following())
	check("tail is still visible after resize",
		strings.Contains(plain(m5.View()), "row 99"), "tail lost")

	// Render caching: a stream chunk must not re-render the whole history.
	m6 := build(tmp, "pickle-rick", 80, 24)
	for i := 0; i < 50; i++ {
		m6.PushSystem(fmt.Sprintf("cached %d", i))
	}
	before := m6.RenderCount()
	m6.PushStreamChunk("x")
	added := m6.RenderCount() - before
	check("STREAM CHUNK DOES NOT RE-RENDER THE HISTORY", added == 0,
		fmt.Sprintf("%d entries re-rendered", added))

	// A width change must invalidate everything, though.
	m6 = send(m6, tea.WindowSizeMsg{Width: 60, Height: 24})
	check("a width change does re-render the history",
		m6.RenderCount()-before >= 50, fmt.Sprint(m6.RenderCount()-before))
}

// testReportedIssues covers the round of bugs reported from real use: the
// /new crash, mouse capture blocking copy, single-press ctrl+c, commands
// opening overlay windows, and the unreachable top of the /auth list.
func testReportedIssues(tmp string) {
	section("reported issues")

	run := func(m *tui.Model, cmd string) *tui.Model {
		m.InputSetValue(cmd)
		return key(m, "enter")
	}

	// --- /new must work, including mid-run ---
	m := build(tmp, "pickle-rick", 88, 28)
	for i := 0; i < 40; i++ {
		m.PushSystem(fmt.Sprintf("history %d", i))
	}
	m = run(m, "/new")
	check("/new clears the transcript", m.MsgCount() == 0, fmt.Sprint(m.MsgCount()))
	check("/new clears the render cache", m.CacheLen() == 0, fmt.Sprint(m.CacheLen()))
	check("/new clears rendered content", m.ChatContent() == "",
		fmt.Sprintf("%d chars left", len(m.ChatContent())))
	check("splash returns after /new",
		strings.Contains(plain(m.View()), "lightweight coding agent"), "no splash")

	// The reported crash: /new while the agent is streaming. submit() used to
	// swallow every slash command while running, so /new silently did nothing
	// and left msgs and the cache disagreeing.
	m2 := build(tmp, "pickle-rick", 88, 28)
	for i := 0; i < 20; i++ {
		m2.PushSystem(fmt.Sprintf("row %d", i))
	}
	m2.ForceRunning(true)
	for i := 0; i < 30; i++ {
		m2.PushStreamChunk(fmt.Sprintf("token%d ", i))
	}
	m2 = run(m2, "/new")
	check("/NEW WORKS WHILE THE AGENT IS RUNNING", m2.MsgCount() == 0, fmt.Sprint(m2.MsgCount()))
	check("  stream buffer is emptied", m2.StreamLen() == 0, fmt.Sprint(m2.StreamLen()))
	check("  NO STALE CONTENT SURVIVES", m2.ChatContent() == "",
		fmt.Sprintf("%q", m2.ChatContent()))
	check("  msgs and cache agree", m2.MsgCount() == m2.CacheLen(),
		fmt.Sprintf("%d vs %d", m2.MsgCount(), m2.CacheLen()))
	check("  the view still renders", len(m2.View()) > 0)

	// --- ctrl+c needs two presses ---
	m3 := build(tmp, "pickle-rick", 88, 28)
	m3, quit := keyQuit(m3)
	check("FIRST ctrl+c DOES NOT QUIT", !quit, "quit on the first press")
	check("  it warns the user", strings.Contains(plain(m3.View()), "again to exit"), m3.StatusLine())
	_, quit2 := keyQuit(m3)
	check("SECOND ctrl+c QUITS", quit2, "did not quit")

	// Typing between presses must disarm.
	m4 := build(tmp, "pickle-rick", 88, 28)
	m4, _ = keyQuit(m4)
	m4 = typeStr(m4, "x")
	m4.InputSetValue("")
	m4b, quit3 := keyQuit(m4)
	_ = m4b
	check("typing disarms the pending quit", !quit3, "quit after typing")

	// A non-empty prompt: the first press clears the line instead of quitting.
	m5 := build(tmp, "pickle-rick", 88, 28)
	m5.InputSetValue("half typed")
	m5, quit4 := keyQuit(m5)
	check("ctrl+c clears a half-typed line first", !quit4 && m5.InputValue() == "",
		fmt.Sprintf("quit=%v input=%q", quit4, m5.InputValue()))

	// --- commands render inline, not as overlay windows ---
	for _, cmd := range []string{"/help", "/config", "/tools", "/permissions"} {
		mm := run(build(tmp, "pickle-rick", 88, 30), cmd)
		check("inline: "+cmd, !mm.ModalOpen() && len(mm.ChatContent()) > 0,
			fmt.Sprintf("modal=%v content=%d", mm.ModalOpen(), len(mm.ChatContent())))
	}
	mh := run(build(tmp, "pickle-rick", 88, 34), "/help")
	check("/help leads with commands",
		strings.Contains(mh.ChatContent(), "/models") &&
			strings.Contains(mh.ChatContent(), "/auth") &&
			strings.Contains(mh.ChatContent(), "/theme"), "commands missing")
	check("/help documents the prompt syntax",
		strings.Contains(mh.ChatContent(), "@path") && strings.Contains(mh.ChatContent(), "!command"))
	check("/help still lists keys", strings.Contains(mh.ChatContent(), "ctrl+c"))

	// --- /models: provider first, then model ---
	mm := run(build(tmp, "pickle-rick", 88, 30), "/models")
	check("/MODELS ASKS FOR A PROVIDER FIRST", mm.PendingKind() == 1,
		fmt.Sprint(mm.PendingKind()))
	check("  it renders in the conversation", !mm.ModalOpen())
	check("  providers are numbered", strings.Contains(plain(mm.View()), " 1 "), "no numbering")
	check("  model counts are shown", strings.Contains(plain(mm.View()), "model"), "no counts")

	mm = run(mm, "1")
	check("PICKING A PROVIDER THEN OFFERS ITS MODELS", mm.PendingKind() == 2,
		fmt.Sprint(mm.PendingKind()))
	check("  the model list is numbered", strings.Contains(plain(mm.View()), " 1 "))
	check("  back is offered", strings.Contains(plain(mm.View()), "b back"), "no back option")

	// "b" goes back — run it on its own model, since driving the TUI mutates
	// state in place and the two branches must not share a pending choice.
	backM := run(build(tmp, "pickle-rick", 88, 30), "/models")
	backM = run(backM, "1")
	backM = run(backM, "b")
	check("b returns to the provider list", backM.PendingKind() == 1, fmt.Sprint(backM.PendingKind()))

	chosen := run(mm, "1")
	check("picking a model sets it", strings.Contains(chosen.ModelID(), "/"), chosen.ModelID())
	check("  the choice is cleared", chosen.PendingKind() == 0, fmt.Sprint(chosen.PendingKind()))
	check("  it is confirmed in the chat",
		strings.Contains(chosen.ChatContent(), "model:"), "no confirmation")

	// Direct model selection must return the slash command's model and command
	// values instead of dropping them in submit().
	direct := run(build(tmp, "pickle-rick", 88, 30), "/model rick")
	check("DIRECT /MODEL SWITCHES THE ACTIVE MODEL", direct.ModelID() == "rick", direct.ModelID())
	check("  direct model selection is confirmed", strings.Contains(direct.ChatContent(), "model: rick"))

	// Key management must remain inside /auth after entering its submenu.
	creds := &config.Credentials{Providers: map[string]config.Credential{
		"anthropic": {APIKey: "test-key", APIKeys: []string{"test-key", "test-key-2"}, Models: []string{"model-a"}},
	}}
	keys := run(buildWithCredentials(tmp, "pickle-rick", 88, 30, creds), "/auth")
	keys = typeStr(keys, "1")
	keys = key(keys, "enter")
	keys = key(keys, "1")
	check("/AUTH MANAGE KEYS STAYS OPEN", keys.AuthActive(), "auth closed")
	check("  key submenu is rendered", strings.Contains(plain(keys.View()), "Keys for anthropic"))
	keys = key(keys, "esc")
	check("  esc returns to provider editing", keys.AuthActive(), "auth closed after back")

	// A non-numeric reply must fall through as a normal prompt, never trap.
	esc := run(build(tmp, "pickle-rick", 88, 30), "/models")
	esc.InputSetValue("")
	esc = key(esc, "enter")
	check("empty input cancels a pending choice", esc.PendingKind() == 0)

	// --- /auth must be reachable on a short terminal ---
	short := build(tmp, "pickle-rick", 80, 18)
	short = run(short, "/auth")
	v := plain(short.View())
	check("/auth fits a short terminal", short.AuthRowCount() > 0)
	check("  the first provider is visible", strings.Contains(v, "Anthropic"), "top row hidden")
	check("  it shows there is more below", strings.Contains(v, "more below"), "no overflow hint")
	for _, line := range strings.Split(v, "\n") {
		if lipgloss.Width(line) > 80 {
			check("  /auth does not overflow the width", false, fmt.Sprintf("%d cols", lipgloss.Width(line)))
			break
		}
	}
	check("  /auth does not overflow the height",
		len(strings.Split(strings.TrimRight(short.View(), "\n"), "\n")) <= 18,
		fmt.Sprint(len(strings.Split(short.View(), "\n"))))

	// Scrolling reveals the rest of the catalogue.
	before := plain(short.View())
	short = key(short, "pgdown")
	after := plain(short.View())
	check("/AUTH SCROLLS TO REACH THE REST", before != after, "view did not change")
	check("  scrolled view shows later providers",
		strings.Contains(after, "more above"), "no upward hint")
	short = key(short, "home")
	check("home returns to the top",
		strings.Contains(plain(short.View()), "Anthropic"), "top not restored")

	// --- the TUI must fit a range of window sizes ---
	for _, size := range [][2]int{{40, 12}, {60, 20}, {80, 24}, {100, 30}, {160, 50}, {200, 60}} {
		w, h := size[0], size[1]
		mm := build(tmp, "pickle-rick", w, h)
		mm.PushSystem("some output to fill the view")
		for _, line := range strings.Split(plain(mm.View()), "\n") {
			if lipgloss.Width(line) > w {
				check(fmt.Sprintf("fits %dx%d", w, h), false,
					fmt.Sprintf("line is %d cols: %q", lipgloss.Width(line), line))
				return
			}
		}
		check(fmt.Sprintf("fits %dx%d", w, h), true)
	}

	// --- the status line carries the Hermes-style stats ---
	// The stats are deliberately minimal: a percentage always, a bar only
	// once the context is genuinely filling, and a token total once there is
	// something to total.
	ms := build(tmp, "pickle-rick", 100, 30)
	ms.SetModelID("anthropic/claude-sonnet-4-5-20250929")
	ms.PushSystem("x")
	ms.ApplyUsage(120_000, 5_000, 0)
	sv := plain(ms.View())
	check("STATUS SHOWS A CONTEXT GAUGE", strings.Contains(sv, "█"), sv)
	check("  status shows a percentage", strings.Contains(sv, "%"), sv)
	check("  status shows the token split", strings.Contains(sv, "↑"), sv)
}

// keyQuit sends ctrl+c and reports whether the program was told to quit.
func keyQuit(m *tui.Model) (*tui.Model, bool) {
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	out := nm.(*tui.Model)
	if cmd == nil {
		return out, false
	}
	msg := cmd()
	return out, fmt.Sprintf("%T", msg) == "tea.QuitMsg"
}

// frameHeight counts the rows a rendered frame occupies.
func frameHeight(m *tui.Model) int {
	return len(strings.Split(strings.TrimRight(m.View(), "\n"), "\n"))
}

// testFrameHeight guards the root cause of "/new does nothing": bubbletea's
// alt-screen renderer only repaints the lines a frame emits, so a frame that
// is shorter than its predecessor leaves the old rows visible. Every view
// must therefore occupy the same number of rows.
func testFrameHeight(tmp string) {
	section("frame height stability")

	run := func(m *tui.Model, cmd string) *tui.Model {
		m.InputSetValue(cmd)
		return key(m, "enter")
	}

	const h = 30
	m := build(tmp, "pickle-rick", 90, h)
	empty := frameHeight(m)
	check("splash fills the terminal", empty >= h-3, fmt.Sprintf("%d of %d", empty, h))

	for i := 0; i < 60; i++ {
		m.PushSystem(fmt.Sprintf("conversation line %d", i))
	}
	full := frameHeight(m)
	check("a full transcript is the same height", full == empty,
		fmt.Sprintf("empty=%d full=%d", empty, full))

	// The reported bug: this frame used to shrink, orphaning old rows.
	m = run(m, "/new")
	after := frameHeight(m)
	check("/NEW KEEPS THE FRAME HEIGHT (no orphaned rows)", after == full,
		fmt.Sprintf("before=%d after=%d", full, after))
	check("  and the old text is gone",
		!strings.Contains(plain(m.View()), "conversation line"), "stale text visible")

	// Every command that prints must also keep the height.
	for _, cmd := range []string{"/help", "/config", "/tools", "/models", "/theme"} {
		mm := build(tmp, "pickle-rick", 90, h)
		base := frameHeight(mm)
		mm = run(mm, cmd)
		check("stable height: "+cmd, frameHeight(mm) == base,
			fmt.Sprintf("%d -> %d", base, frameHeight(mm)))
	}

	// And so must a resize.
	m2 := build(tmp, "pickle-rick", 90, h)
	m2.PushSystem("hello")
	for _, size := range [][2]int{{70, 20}, {120, 40}, {50, 15}} {
		m2 = send(m2, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		got := frameHeight(m2)
		check(fmt.Sprintf("stable height after resize to %dx%d", size[0], size[1]),
			got >= size[1]-3 && got <= size[1], fmt.Sprintf("%d rows of %d", got, size[1]))
	}

	// A frame must never be taller than the terminal either.
	for _, hh := range []int{12, 18, 24, 40} {
		mm := build(tmp, "pickle-rick", 80, hh)
		mm = run(mm, "/auth")
		check(fmt.Sprintf("/auth fits height %d", hh), frameHeight(mm) <= hh,
			fmt.Sprintf("%d rows of %d", frameHeight(mm), hh))
	}
}

// testFreshStart checks that launching rick begins a new conversation rather
// than silently resuming the previous one.
func testFreshStart(tmp string) {
	section("fresh start on launch")

	dir := filepath.Join(tmp, "freshproj")
	_ = os.MkdirAll(dir, 0o755)
	loaded, _ := config.Load(dir)
	store, _ := session.NewStore(filepath.Join(tmp, "fresh-sess"))
	snaps, _ := session.NewSnapshotter(dir, filepath.Join(tmp, "fresh-snap"))

	// Leave a previous session on disk, marked current.
	prev := &session.Session{ID: session.NewID(), Cwd: dir, Title: "yesterday's work"}
	prev.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old question"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "old answer"}}},
	}
	_ = store.Save(prev)
	_ = store.SetCurrent(dir, prev.ID)

	mk := func(resumeID string) *tui.Model {
		return send(tui.New(tui.Deps{
			Loaded: loaded, Themes: theme.Load(), ThemeDirs: theme.NewWatcher(),
			Registry: tools.NewRegistry(), Todos: tools.NewTodoStore(),
			Perms: permission.New(loaded.Config.Permission, dir),
			Store: store, Snapshots: snaps,
			Credentials: &config.Credentials{Providers: map[string]config.Credential{}},
			Providers:   map[string]provider.Provider{"anthropic": anthropic.New("sk", "")},
			Cwd:         dir, Version: "vtest", ResumeID: resumeID,
		}), tea.WindowSizeMsg{Width: 90, Height: 28})
	}

	// No ResumeID (what `rick` now passes) -> a clean slate.
	m := mk("")
	check("LAUNCH STARTS A NEW CONVERSATION", m.MsgCount() == 0 && m.HistoryLen() == 0,
		fmt.Sprintf("msgs=%d history=%d", m.MsgCount(), m.HistoryLen()))
	v := plain(m.View())
	check("  the logo is shown", strings.Contains(v, "___/"), "no logo")
	check("  the tagline is shown", strings.Contains(v, "lightweight coding agent"))
	check("  no old text is carried in", !strings.Contains(v, "old question"), "old text present")
	check("  the earlier session is offered, not loaded",
		strings.Contains(v, "yesterday") || strings.Contains(v, "/sessions"), v)

	// --continue / --resume still work.
	m2 := mk(prev.ID)
	if cmd := m2.Init(); cmd != nil {
		if out := cmd(); out != nil {
			if batch, ok := out.(tea.BatchMsg); ok {
				for _, c := range batch {
					if msg := c(); msg != nil &&
						!strings.Contains(fmt.Sprintf("%T", msg), "TickMsg") {
						nm, _ := m2.Update(msg)
						m2 = nm.(*tui.Model)
					}
				}
			}
		}
	}
	check("--resume still loads the session", m2.HistoryLen() == 2, fmt.Sprint(m2.HistoryLen()))
	check("  resumed text is visible", strings.Contains(plain(m2.View()), "old question"))

	// /new on a resumed session clears it.
	m2.InputSetValue("/new")
	m2 = key(m2, "enter")
	check("/new clears a resumed session",
		m2.MsgCount() == 0 && m2.HistoryLen() == 0,
		fmt.Sprintf("msgs=%d history=%d", m2.MsgCount(), m2.HistoryLen()))
	check("  splash returns", strings.Contains(plain(m2.View()), "lightweight coding agent"))
}

// testContextAccounting covers the usage gauge. The bug: input tokens were
// summed across turns, but every request resends the whole conversation, so
// one short exchange reported tens of thousands of tokens.
func testContextAccounting(tmp string) {
	section("context accounting")

	// Window detection.
	for _, c := range []struct {
		id   string
		want int
	}{
		{"LongCat-2.0", 1_000_000},
		{"longcat/LongCat-2.0", 1_000_000},
		{"claude-sonnet-4-5-20250929", 200_000},
		{"gpt-5", 400_000},
		{"gpt-4o", 128_000},
		{"gemini-2.5-pro", 1_048_576},
		{"deepseek-chat", 128_000},
		{"kimi-k2-0905-preview", 256_000},
		{"some-model-128k", 128_000},
		{"weird-1m", 1_000_000},
		{"totally-unknown", 0},
	} {
		got := provider.KnownContextWindow(c.id)
		check("window: "+c.id, got == c.want, fmt.Sprintf("got %d want %d", got, c.want))
	}

	m := build(tmp, "pickle-rick", 100, 30)
	m.SetModelID("longcat/LongCat-2.0")
	check("LONGCAT IS DETECTED AS 1M CONTEXT", m.ContextWindow() == 1_000_000,
		fmt.Sprint(m.ContextWindow()))

	// A short exchange must read as a sliver, not tens of thousands.
	m.PushSystem("hello")
	m.ApplyUsage(1200, 300, 0)
	pct := m.ContextPct()
	check("A SHORT MESSAGE IS A TINY FRACTION OF 1M", pct <= 1, fmt.Sprintf("%d%%", pct))

	// The regression: a second turn resends the conversation, so the newest
	// input already covers everything. Summing turns double-counts it.
	m.ApplyUsage(1500, 400, 0)
	check("A SECOND TURN DOES NOT DOUBLE-COUNT", m.ContextPct() <= 1,
		fmt.Sprintf("%d%%", m.ContextPct()))
	check("  occupancy tracks the newest call", m.UsageInput() == 1500,
		fmt.Sprint(m.UsageInput()))
	check("  billed totals still accumulate", m.BilledTotal() == 1200+300+1500+400,
		fmt.Sprint(m.BilledTotal()))

	// Input tokens are cache misses (new tokens). Cache hits and writes are
	// tracked separately.
	m.ApplyUsage(500, 100, 2000)
	check("input shows only cache miss", m.UsageInput() == 500,
		fmt.Sprint(m.UsageInput()))

	// A genuinely full context must report high.
	m2 := build(tmp, "pickle-rick", 100, 30)
	m2.SetModelID("anthropic/claude-sonnet-4-5-20250929")
	check("claude window detected", m2.ContextWindow() == 200_000, fmt.Sprint(m2.ContextWindow()))
	m2.ApplyUsage(150_000, 10_000, 0)
	p2 := m2.ContextPct()
	check("a full context reports high", p2 >= 75 && p2 <= 85, fmt.Sprintf("%d%%", p2))

	// The gauge must stay quiet when nothing has been sent.
	m3 := build(tmp, "pickle-rick", 100, 30)
	m3.PushSystem("x")
	check("STATUS IS QUIET BEFORE ANY USAGE",
		!strings.Contains(plain(m3.View()), "%"), plain(m3.StatusLine()))

	// The bar is always drawn once there is usage, so its fill is readable
	// at a glance; at low usage it is nearly all track.
	m3.ApplyUsage(2000, 200, 0)
	sv := plain(m3.View())
	check("low usage shows a percentage", strings.Contains(sv, "%"), sv)
	check("  the bar is mostly empty at low usage",
		strings.Count(sv, "░") > strings.Count(sv, "█"), sv)

	// A fuller context fills more of it.
	m4 := build(tmp, "pickle-rick", 100, 30)
	m4.SetModelID("anthropic/claude-sonnet-4-5-20250929")
	m4.PushSystem("x")
	m4.ApplyUsage(120_000, 5_000, 0)
	check("the bar appears when the context fills",
		strings.Contains(plain(m4.View()), "█"),
		fmt.Sprintf("window=%d pct=%d%%", m4.ContextWindow(), m4.ContextPct()))
}

// testReasoningLevels covers effort detection and the /thinking command.
func testReasoningLevels(tmp string) {
	section("reasoning levels")

	for _, c := range []struct {
		id    string
		style provider.ReasoningStyle
	}{
		{"claude-sonnet-4-5-20250929", provider.ReasoningStyleAnthropic},
		{"claude-3-5-sonnet-20241022", provider.ReasoningStyleNone},
		{"gpt-5", provider.ReasoningStyleOpenAI},
		{"gpt-4o", provider.ReasoningStyleNone},
		{"o3", provider.ReasoningStyleOpenAI},
		{"deepseek-reasoner", provider.ReasoningStyleAlways},
		{"qwen3-max", provider.ReasoningStyleQwen},
		{"grok-4", provider.ReasoningStyleOpenAI},
		{"llama-3.3-70b", provider.ReasoningStyleNone},
	} {
		got, _ := provider.DetectReasoning(c.id)
		check("detect: "+c.id, got == c.style, fmt.Sprintf("got %q want %q", got, c.style))
	}

	// Defaults to medium where supported.
	_, deflt := provider.DetectReasoning("gpt-5")
	check("REASONING DEFAULTS TO MEDIUM", deflt == provider.ReasoningMedium, string(deflt))

	// Anthropic budgets scale with max_tokens and respect the 1024 floor.
	check("medium budget is half of max_tokens",
		provider.ReasoningMedium.Budget(8192) == 4096,
		fmt.Sprint(provider.ReasoningMedium.Budget(8192)))
	check("budgets never fall below the 1024 floor",
		provider.ReasoningLow.Budget(2048) >= 1024,
		fmt.Sprint(provider.ReasoningLow.Budget(2048)))
	check("off means no budget", provider.ReasoningOff.Budget(8192) == 0)
	check("a tiny max_tokens disables thinking", provider.ReasoningHigh.Budget(1200) == 0,
		fmt.Sprint(provider.ReasoningHigh.Budget(1200)))

	// Parsing accepts shorthand.
	for _, c := range []struct{ in, want string }{
		{"med", "medium"}, {"HIGH", "high"}, {"off", "off"}, {"min", "minimal"},
	} {
		got, ok := provider.ParseEffort(c.in)
		check("parse: "+c.in, ok && string(got) == c.want, fmt.Sprintf("%q ok=%v", got, ok))
	}
	_, ok := provider.ParseEffort("nonsense")
	check("nonsense is rejected", !ok)

	// The command, on a reasoning model.
	m := build(tmp, "pickle-rick", 90, 30)
	m.SetModelID("openai/gpt-5")
	check("a reasoning model starts at medium", m.Reasoning() == "medium", m.Reasoning())

	// Newer GLM ids use a provider-specific thinking object and must still
	// start with the normal reasoning controls.
	glm := build(tmp, "pickle-rick", 90, 30)
	glm.SetModelID("zai/glm-4.7")
	check("GLM-4.7 IS DETECTED AS BOOLEAN THINKING", glm.Reasoning() == "on", glm.Reasoning())

	// An unknown/custom id is not treated as a confirmed non-reasoning model.
	// It starts safely off, but /thinking remains available for new models.
	unknown := build(tmp, "pickle-rick", 90, 30)
	unknown.SetModelID("gateway/vendor-new-model")
	check("UNKNOWN MODELS START WITH THINKING OFF", unknown.Reasoning() == "off", unknown.Reasoning())
	unknown.InputSetValue("/thinking")
	unknown = key(unknown, "enter")
	check("UNKNOWN MODELS STILL OFFER EXPLICIT THINKING CONTROLS", unknown.PendingKind() != 0, fmt.Sprint(unknown.PendingKind()))
	check("UNKNOWN MODELS DO NOT CLAIM THINKING IS IMPOSSIBLE",
		!strings.Contains(unknown.ChatContent(), "not a reasoning model"), unknown.ChatContent())

	m.InputSetValue("/thinking")
	m = key(m, "enter")
	check("/THINKING OFFERS ONLY MODEL-SUPPORTED LEVELS", m.PendingKind() != 0, fmt.Sprint(m.PendingKind()))
	v := plain(m.View())
	for _, lvl := range []string{"minimal", "low", "medium", "high"} {
		check("  offers "+lvl, strings.Contains(v, lvl), v)
	}
	check("  does not offer unsupported off", !strings.Contains(v, "off"), v)
	check("  the current level is marked", strings.Contains(v, "current"), v)

	m.InputSetValue("high")
	m = key(m, "enter")
	check("selecting a level applies it", m.Reasoning() == "high", m.Reasoning())

	// Direct form.
	m.InputSetValue("/thinking low")
	m = key(m, "enter")
	check("/thinking <level> sets it directly", m.Reasoning() == "low", m.Reasoning())

	m.InputSetValue("/thinking nonsense")
	m = key(m, "enter")
	check("an unknown level is rejected",
		strings.Contains(m.ChatContent(), "unknown level"), "no error")
	check("  the level is unchanged", m.Reasoning() == "low", m.Reasoning())

	// A non-reasoning model says so rather than pretending.
	m2 := build(tmp, "pickle-rick", 90, 30)
	m2.SetModelID("openai/gpt-4o")
	check("a non-reasoning model reports off", m2.Reasoning() == "off", m2.Reasoning())
	m2.InputSetValue("/thinking")
	m2 = key(m2, "enter")
	check("NON-REASONING MODELS SAY SO",
		strings.Contains(m2.ChatContent(), "not a reasoning model"), m2.ChatContent())
	check("  and no choice is armed", m2.PendingKind() == 0)

	// An always-on reasoner cannot be tuned.
	m3 := build(tmp, "pickle-rick", 90, 30)
	m3.SetModelID("deepseek/deepseek-reasoner")
	m3.InputSetValue("/thinking")
	m3 = key(m3, "enter")
	check("always-on reasoners explain themselves",
		strings.Contains(m3.ChatContent(), "always reasons"), m3.ChatContent())

	// Switching models re-detects support.
	m4 := build(tmp, "pickle-rick", 90, 30)
	m4.SetModelID("openai/gpt-5")
	check("gpt-5 supports reasoning", m4.Reasoning() == "medium", m4.Reasoning())
	m4.SetModelID("openai/gpt-4o")
	check("SWITCHING TO A PLAIN MODEL CLEARS THE LEVEL", m4.Reasoning() == "off", m4.Reasoning())
	m4.SetModelID("anthropic/claude-sonnet-4-5-20250929")
	check("switching back restores a level", m4.Reasoning() == "medium", m4.Reasoning())

	// The status line surfaces it.
	m5 := build(tmp, "pickle-rick", 100, 30)
	m5.SetModelID("openai/gpt-5")
	m5.PushSystem("x")
	check("status shows the reasoning level",
		strings.Contains(plain(m5.View()), "reasoning"), plain(m5.StatusLine()))
}

// testStatusGauge covers the context bar and the token split.
func testStatusGauge(tmp string) {
	section("context gauge & token split")

	mk := func(w int, model string) *tui.Model {
		m := build(tmp, "pickle-rick", w, 24)
		m.SetModelID(model)
		m.PushSystem("x")
		return m
	}
	statusOf := func(m *tui.Model) string {
		lines := strings.Split(strings.TrimRight(plain(m.View()), "\n"), "\n")
		return lines[len(lines)-1]
	}

	// Nothing sent yet: no gauge at all.
	quiet := mk(120, "longcat/LongCat-2.0")
	check("QUIET BEFORE ANY USAGE",
		!strings.Contains(statusOf(quiet), "%") && !strings.Contains(statusOf(quiet), "█"),
		statusOf(quiet))

	// The reference layout: used/total, bar, percentage.
	m := mk(120, "longcat/LongCat-2.0")
	m.ApplyUsage(399_000, 40_000, 120_000)
	sv := statusOf(m)
	check("GAUGE SHOWS used/total", strings.Contains(sv, "559k/1M"), sv)
	check("  bar has a filled portion", strings.Contains(sv, "█"), sv)
	check("  bar has an empty portion", strings.Contains(sv, "░"), sv)
	check("  percentage is shown", strings.Contains(sv, "55%"), sv)

	// The split, by kind.
	check("TOKEN SPLIT SHOWS INPUT", strings.Contains(sv, "↑399k"), sv)
	check("  shows output", strings.Contains(sv, "↓40k"), sv)
	check("  SHOWS CACHE HITS", strings.Contains(sv, "⚡120k"), sv)

	// Fill proportion must track the percentage.
	for _, c := range []struct {
		in, out, want int
	}{
		{100_000, 0, 10},
		{250_000, 0, 25},
		{500_000, 0, 50},
		{750_000, 0, 75},
	} {
		mm := mk(120, "longcat/LongCat-2.0")
		mm.ApplyUsage(c.in, c.out, 0)
		line := statusOf(mm)
		filled := strings.Count(line, "█")
		wantFilled := c.want * 16 / 100
		check(fmt.Sprintf("bar fill at %d%%", c.want),
			filled == wantFilled && strings.Contains(line, fmt.Sprintf("%d%%", c.want)),
			fmt.Sprintf("filled=%d want=%d line=%s", filled, wantFilled, line))
	}

	// Non-zero usage must never render an empty bar.
	tiny := mk(120, "longcat/LongCat-2.0")
	tiny.ApplyUsage(500, 100, 0)
	check("a sliver of usage still shows one block",
		strings.Count(statusOf(tiny), "█") >= 1, statusOf(tiny))

	// Overflow is clamped rather than showing 1.3M/1M.
	over := mk(120, "longcat/LongCat-2.0")
	over.ApplyUsage(1_200_000, 100_000, 0)
	so := statusOf(over)
	check("OVERFLOW IS CLAMPED TO THE WINDOW", strings.Contains(so, "1M/1M"), so)
	check("  and reads as 100%", strings.Contains(so, "100%"), so)

	// Cache is omitted when there is none.
	nocache := mk(120, "anthropic/claude-sonnet-4-5-20250929")
	nocache.ApplyUsage(8_000, 1_200, 0)
	check("no cache segment when there are no hits",
		!strings.Contains(statusOf(nocache), "⚡"), statusOf(nocache))

	// Colour: green normally, amber at 70%, red at 90%.
	colorOf := func(pct int) string {
		mm := build(tmp, "pickle-rick", 120, 24)
		mm.SetModelID("longcat/LongCat-2.0")
		mm.PushSystem("x")
		mm.ApplyUsage(pct*10_000, 0, 0)
		v := mm.View()
		lines := strings.Split(strings.TrimRight(v, "\n"), "\n")
		last := lines[len(lines)-1]
		switch {
		case strings.Contains(last, "\x1b[38;2;229;107;107m"):
			return "red"
		case strings.Contains(last, "\x1b[38;2;227;197;124m"):
			return "amber"
		case strings.Contains(last, "\x1b[38;2;124;227;139m"):
			return "green"
		}
		return "none"
	}
	check("gauge is green at 40%", colorOf(40) == "green", colorOf(40))
	check("GAUGE TURNS AMBER AT 75%", colorOf(75) == "amber", colorOf(75))
	check("GAUGE TURNS RED AT 95%", colorOf(95) == "red", colorOf(95))

	// Narrow terminals: the bar shrinks, then the split is dropped, but the
	// status line must never wrap.
	for _, w := range []int{140, 120, 100, 90, 80, 70, 60, 50, 40} {
		mm := mk(w, "longcat/LongCat-2.0")
		mm.ApplyUsage(399_000, 40_000, 120_000)
		line := statusOf(mm)
		if lipgloss.Width(line) > w {
			check(fmt.Sprintf("status fits width %d", w), false,
				fmt.Sprintf("%d cols: %q", lipgloss.Width(line), line))
			continue
		}
		check(fmt.Sprintf("status fits width %d", w), true)
		check(fmt.Sprintf("  gauge survives at width %d", w),
			strings.Contains(line, "%"), line)
	}

	// humanTokens formatting.
	for _, c := range []struct {
		n    int
		want string
	}{
		{500, "500"}, {1_500, "1k"}, {45_000, "45k"},
		{999_000, "999k"}, {1_000_000, "1M"}, {1_500_000, "1.5M"},
	} {
		got := tui.HumanTokens(c.n)
		check(fmt.Sprintf("format %d", c.n), got == c.want, got+" want "+c.want)
	}
}

// testSplashArt covers the mascot on the startup screen.
func testSplashArt(tmp string) {
	section("splash mascot")

	// The decoder must reproduce the source dimensions exactly.
	full := tui.RenderArt(150)
	check("art decodes at native width", len(full) > 0, fmt.Sprint(len(full)))
	check("  row count matches the aspect ratio",
		len(full) == tui.ArtHeightFor(150), fmt.Sprint(len(full)))

	// Half-block rendering: two source pixels per cell, so the rendered
	// height is about a quarter of the width (118x70 art -> ~35 rows at
	// native width, half that again for the 2:1 cell ratio).
	// The mascot is a portrait: taller than wide. A terminal cell is twice as
	// tall as it is wide and a half-block packs two pixels into one cell, so
	// the DISPLAYED ratio is rows*2/cols. Getting this wrong by a factor of
	// two is exactly what made the art look squashed.
	const srcRatio = 454.0 / 365.0 // the source PNG
	for _, w := range []int{24, 32, 40, 48} {
		rows := tui.ArtHeightFor(w)
		got := float64(rows*2) / float64(w)
		off := (got - srcRatio) / srcRatio
		if off < 0 {
			off = -off
		}
		check(fmt.Sprintf("ASPECT RATIO IS CORRECT AT WIDTH %d", w), off < 0.10,
			fmt.Sprintf("%d rows => %.2f, want %.2f (%.0f%% off)", rows, got, srcRatio, off*100))
	}
	check("the art is taller than it is wide",
		tui.ArtHeightFor(40)*2 > 40, fmt.Sprint(tui.ArtHeightFor(40)))

	// The guard above only holds if the stored data has square pixels. Assert
	// that directly: an HTML-style source, whose rows are character cells,
	// would encode a portrait as a squat grid and silently halve the height.
	aw, ah := tui.ArtDimensions()
	stored := float64(ah) / float64(aw)
	check("THE SOURCE DATA HAS SQUARE PIXELS",
		stored > srcRatio*0.92 && stored < srcRatio*1.08,
		fmt.Sprintf("stored %dx%d = %.2f, source %.2f", aw, ah, stored, srcRatio))

	// Colour must survive: the mascot is blue-haired with cream skin.
	art := strings.Join(tui.RenderArt(40), "\n")
	check("ART IS IN COLOUR", strings.Contains(art, "\x1b[38;2;"), "no colour codes")
	// Colours are averaged from the source, so assert on hue rather than an
	// exact triple: the palette changes whenever the art is regenerated.
	blue, skin := 0, 0
	for _, m := range regexp.MustCompile(`[34]8;2;(\d+);(\d+);(\d+)`).FindAllStringSubmatch(art, -1) {
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		switch {
		case b > r+40 && b > 150:
			blue++ // hair
		case r > 150 && g > 120 && b < g:
			skin++ // face
		}
	}
	check("  the blue hair is present", blue > 20, fmt.Sprint(blue))
	check("  the cream face is present", skin > 20, fmt.Sprint(skin))
	check("  transparent areas stay unpainted", strings.Contains(art, " "), "no gaps")

	// Every rendered line must be exactly the requested width once styling
	// is stripped, or the layout will stagger.
	for _, w := range []int{24, 32, 40} {
		ok := true
		for _, line := range tui.RenderArt(w) {
			if lipgloss.Width(line) != w {
				ok = false
				check(fmt.Sprintf("art lines are exactly %d wide", w), false,
					fmt.Sprintf("got %d", lipgloss.Width(line)))
				break
			}
		}
		if ok {
			check(fmt.Sprintf("art lines are exactly %d wide", w), true)
		}
	}

	// Placement in the splash.
	m := build(tmp, "pickle-rick", 120, 32)
	v := plain(m.View())
	check("WIDE TERMINALS SHOW THE MASCOT",
		strings.Contains(v, "▀") || strings.Contains(v, "▄"), "no art")
	check("  the logo is still on the left", strings.Contains(v, "___/"), v)

	// The logo must be left of the art on the same rows.
	var logoCol, artCol int = -1, -1
	for _, line := range strings.Split(v, "\n") {
		if i := strings.Index(line, "_____"); i >= 0 && logoCol < 0 {
			logoCol = i
		}
		if i := strings.IndexAny(line, "▀▄"); i >= 0 && artCol < 0 {
			artCol = i
		}
	}
	check("THE MASCOT IS ON THE RIGHT", artCol > logoCol,
		fmt.Sprintf("logo@%d art@%d", logoCol, artCol))

	// Vertical centring: the art block should not start at row 0 when there
	// is spare height.
	lines := strings.Split(v, "\n")
	firstArt := -1
	for i, l := range lines {
		if strings.ContainsAny(l, "▀▄") {
			firstArt = i
			break
		}
	}
	check("the mascot is vertically centred", firstArt > 0, fmt.Sprint(firstArt))

	// Nothing may exceed the terminal width at any size.
	for _, size := range [][2]int{{200, 50}, {140, 40}, {120, 32}, {100, 28}, {90, 26}, {80, 24}} {
		mm := build(tmp, "pickle-rick", size[0], size[1])
		worst := 0
		for _, l := range strings.Split(plain(mm.View()), "\n") {
			if n := lipgloss.Width(l); n > worst {
				worst = n
			}
		}
		check(fmt.Sprintf("splash fits %dx%d", size[0], size[1]), worst <= size[0],
			fmt.Sprintf("widest=%d", worst))
	}

	// Narrow or short terminals drop the art rather than cramping the text.
	for _, size := range [][2]int{{70, 24}, {60, 20}, {50, 20}, {100, 14}} {
		mm := build(tmp, "pickle-rick", size[0], size[1])
		vv := plain(mm.View())
		check(fmt.Sprintf("no mascot at %dx%d", size[0], size[1]),
			!strings.ContainsAny(vv, "▀▄"), "art shown when it should not be")
		check(fmt.Sprintf("  text still renders at %dx%d", size[0], size[1]),
			strings.Contains(vv, "rick") || strings.Contains(vv, "_"), vv)
	}

	// The art disappears once the conversation starts.
	m2 := build(tmp, "pickle-rick", 120, 32)
	check("mascot shown before the first message",
		strings.ContainsAny(plain(m2.View()), "▀▄"), "no art")
	m2.PushSystem("hello")
	check("MASCOT GONE ONCE THE CHAT STARTS",
		!strings.ContainsAny(plain(m2.View()), "▀▄"), "art still shown")

	// And returns on /new.
	m2.InputSetValue("/new")
	m2 = key(m2, "enter")
	check("mascot returns on /new", strings.ContainsAny(plain(m2.View()), "▀▄"), "no art")

	// Frame height must stay stable with the art present.
	m3 := build(tmp, "pickle-rick", 120, 32)
	before := len(strings.Split(strings.TrimRight(m3.View(), "\n"), "\n"))
	m3.PushSystem("x")
	after := len(strings.Split(strings.TrimRight(m3.View(), "\n"), "\n"))
	check("frame height is stable with the mascot", before == after,
		fmt.Sprintf("%d -> %d", before, after))
}

// testPerformance guards the render hot path.
//
// The TUI became sluggish because statusBar() shelled out to git on every
// frame: two subprocesses plus a full worktree walk, ~25ms per render at 25
// frames a second while streaming. These budgets are deliberately loose —
// they exist to catch a return to per-frame I/O, not to police microseconds.
func testPerformance(tmp string) {
	section("render performance")

	m := build(tmp, "pickle-rick", 120, 32)
	m.PushSystem("hello")

	timeOf := func(n int, fn func()) time.Duration {
		fn() // warm caches
		start := time.Now()
		for i := 0; i < n; i++ {
			fn()
		}
		return time.Since(start) / time.Duration(n)
	}

	// A frame must be far cheaper than a 40ms drain tick, or the UI cannot
	// keep up with streaming.
	frame := timeOf(50, func() { _ = m.View() })
	check("A FRAME RENDERS IN UNDER 5ms", frame < 5*time.Millisecond, frame.String())

	status := timeOf(50, func() { _ = m.StatusBar() })
	check("THE STATUS BAR DOES NO PER-FRAME I/O", status < time.Millisecond, status.String())

	// The splash renders the mascot; it must be cached, not redrawn.
	sp := build(tmp, "pickle-rick", 120, 32)
	splash := timeOf(50, func() { _ = sp.View() })
	check("THE SPLASH IS CACHED", splash < 5*time.Millisecond, splash.String())

	// Identical width must return the identical cached slice.
	a1 := tui.RenderArt(40)
	a2 := tui.RenderArt(40)
	check("art renders are cached", &a1[0] == &a2[0], "re-rendered")

	// Frame cost must not grow with transcript length: that is the property
	// the block cache exists to provide.
	var costs []time.Duration
	for _, n := range []int{10, 200, 1000} {
		mm := build(tmp, "pickle-rick", 120, 32)
		for i := 0; i < n; i++ {
			mm.PushSystem(fmt.Sprintf("message %d with some text", i))
		}
		costs = append(costs, timeOf(30, func() { _ = mm.View() }))
	}
	check("FRAME COST IS FLAT IN TRANSCRIPT LENGTH",
		costs[2] < costs[0]*8+time.Millisecond,
		fmt.Sprintf("10msgs=%v 1000msgs=%v", costs[0], costs[2]))

	// Streaming: a chunk must not re-render or re-join the whole history.
	stream := func(n int) time.Duration {
		mm := build(tmp, "pickle-rick", 120, 32)
		for i := 0; i < n; i++ {
			mm.PushSystem(fmt.Sprintf("scrollback %d", i))
		}
		mm.ForceRunning(true)
		return timeOf(100, func() {
			mm.PushStreamChunk("token ")
			mm.Refresh()
		})
	}
	small, large := stream(10), stream(1000)
	check("A STREAM CHUNK IS UNDER 2ms", large < 2*time.Millisecond, large.String())
	check("  and scales sub-linearly with backlog",
		large < small*12+time.Millisecond,
		fmt.Sprintf("10msgs=%v 1000msgs=%v", small, large))

	// Allocation churn is what the GC turns into stutter.
	allocPerOp := func(n int, fn func()) float64 {
		var a, b runtime.MemStats
		fn()
		runtime.GC()
		runtime.ReadMemStats(&a)
		for i := 0; i < n; i++ {
			fn()
		}
		runtime.ReadMemStats(&b)
		return float64(b.TotalAlloc-a.TotalAlloc) / float64(n) / 1024
	}
	kb := allocPerOp(100, func() { _ = m.View() })
	check("A FRAME ALLOCATES UNDER 400KB", kb < 400, fmt.Sprintf("%.0f KB", kb))

	// No goroutine may be leaked per render or per chunk.
	before := runtime.NumGoroutine()
	gm := build(tmp, "pickle-rick", 120, 32)
	gm.ForceRunning(true)
	for i := 0; i < 500; i++ {
		gm.PushStreamChunk("word ")
		gm.Refresh()
		_ = gm.View()
	}
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	check("NO GOROUTINE LEAK OVER 500 CHUNKS", after <= before+2,
		fmt.Sprintf("%d -> %d", before, after))

	// The git cache must serve reads without spawning processes, and must
	// notice a directory change rather than reporting the old branch.
	git1 := timeOf(200, func() { _ = tui.CachedGitBranch(tmp) })
	check("GIT READS ARE CACHED", git1 < 100*time.Microsecond, git1.String())
}

// testStatsReset guards session-scoped counters.
//
// /new cleared the context gauge but not the billed totals or turn timer, so
// a brand-new conversation still reported the previous one's "↑56k ↓636 3.1s".
func testStatsReset(tmp string) {
	section("stats reset")

	statusOf := func(m *tui.Model) string {
		lines := strings.Split(strings.TrimRight(plain(m.View()), "\n"), "\n")
		return lines[len(lines)-1]
	}

	m := build(tmp, "pickle-rick", 120, 30)
	m.SetModelID("longcat/LongCat-2.0")
	m.PushSystem("a question")
	m.ApplyUsage(56000, 636, 4000)
	m.SetTurnElapsed(3100 * time.Millisecond)

	before := statusOf(m)
	check("stats show while a session is live", strings.Contains(before, "56k"), before)
	check("  and the turn time shows", strings.Contains(before, "3.1s"), before)

	m.InputSetValue("/new")
	m = key(m, "enter")
	after := statusOf(m)

	check("/NEW CLEARS THE TOKEN SPLIT", !strings.Contains(after, "56k"), after)
	check("  clears the output count", !strings.Contains(after, "636"), after)
	check("  clears the cache count", !strings.Contains(after, "⚡"), after)
	check("  CLEARS THE TURN TIMER", !strings.Contains(after, "3.1s"), after)
	check("  clears the context gauge", !strings.Contains(after, "%"), after)
	check("  billed total is zero", m.BilledTotal() == 0, fmt.Sprint(m.BilledTotal()))
	check("  occupancy is zero", m.UsageInput() == 0, fmt.Sprint(m.UsageInput()))

	// A fresh session must accumulate from zero, not from the old total.
	m.ApplyUsage(1000, 200, 0)
	check("the next turn counts from zero", m.BilledTotal() == 1200,
		fmt.Sprint(m.BilledTotal()))

	// Compaction is different: occupancy drops but spend is still spend.
	c := build(tmp, "pickle-rick", 120, 30)
	c.SetModelID("longcat/LongCat-2.0")
	c.ApplyUsage(50000, 5000, 0)
	spent := c.BilledTotal()
	c.CompactUsage()
	check("COMPACTION KEEPS THE BILLED TOTAL", c.BilledTotal() == spent,
		fmt.Sprintf("%d -> %d", spent, c.BilledTotal()))
	check("  but clears occupancy", c.UsageInput() == 0, fmt.Sprint(c.UsageInput()))
}

// testTerminalImage covers the real-photo path.
//
// Terminals that speak kitty or iTerm2 graphics get the actual PNG; everyone
// else keeps the half-block art. The image must never leak into a terminal
// that cannot draw it, because the escape would print as raw garbage.
func testTerminalImage(tmp string) {
	section("terminal image")

	png := tui.RickPNG()
	check("the photo is embedded", len(png) > 1000, fmt.Sprint(len(png)))
	check("  and is a valid PNG", len(png) > 8 && string(png[1:4]) == "PNG", "bad magic")

	// Kitty: chunked, every chunk flagged, final chunk m=0 or nothing draws.
	k := tui.KittyImage(png, 40, 25)
	check("KITTY ESCAPE IS WELL FORMED",
		strings.HasPrefix(k, "\x1b_Ga=T,f=100") && strings.HasSuffix(k, "\x1b\\"), "malformed")
	check("  it is sized in cells", strings.Contains(k, "c=40,r=25"), "no size")
	check("  it does not move the cursor", strings.Contains(k, "C=1"), "no C=1")
	last := strings.LastIndex(k, "\x1b_G")
	hdr := k[last : last+strings.Index(k[last:], ";")]
	check("  THE FINAL CHUNK IS m=0", strings.Contains(hdr, "m=0"), hdr)
	check("  earlier chunks are m=1", strings.Contains(k, "m=1;"), "no continuation")
	check("  chunks respect the 4KB limit", longestChunk(k) <= 4096, fmt.Sprint(longestChunk(k)))

	// iTerm2: one escape, BEL terminated, honest byte count.
	it := tui.ItermImage(png, 40, 25)
	check("ITERM ESCAPE IS WELL FORMED",
		strings.HasPrefix(it, "\x1b]1337;File=inline=1") && strings.HasSuffix(it, "\a"),
		"malformed")
	check("  it declares the real size", strings.Contains(it, fmt.Sprintf("size=%d", len(png))),
		"wrong size")
	check("  it preserves the aspect ratio", strings.Contains(it, "preserveAspectRatio=1"))

	// Detection must refuse anything it cannot verify.
	check("plain xterm gets no image", tui.DetectImageProto() == "none", tui.DetectImageProto())

	// The splash must fall back cleanly.
	plainTerm := build(tmp, "pickle-rick", 120, 32)
	plainTerm.ForceImageProto("none")
	pv := plainTerm.View()
	check("WITHOUT GRAPHICS THE BLOCK ART IS USED", strings.ContainsAny(pv, "▀▄"), "no art")
	check("  and no escape leaks through", !strings.Contains(pv, "\x1b_G") &&
		!strings.Contains(pv, "\x1b]1337"), "ESCAPE LEAKED INTO A PLAIN TERMINAL")

	// With graphics the photo replaces the blocks, same footprint.
	for _, proto := range []string{"kitty", "iterm2"} {
		g := build(tmp, "pickle-rick", 120, 32)
		g.ForceImageProto(proto)
		gv := g.View()

		// The escape must NOT be in the frame: bubbletea runs every line
		// through ansi.Truncate, which discards the ~130KB payload and
		// leaves a stub that draws nothing. It goes out of band instead.
		check(proto+" keeps the escape OUT of the frame",
			!strings.Contains(gv, "\x1b_G") && !strings.Contains(gv, "\x1b]1337"),
			"escape would be truncated by the renderer")
		check("  a box is reserved for it", g.PhotoBoxSize() != "0x0", g.PhotoBoxSize())
		check("  and drops the block art", !strings.ContainsAny(plain(gv), "▀▄"), "blocks remain")

		// The out-of-band write carries the whole image, positioned.
		var buf strings.Builder
		g.DrawPhotoTo(&buf)
		out := buf.String()
		marker := "\x1b_Ga=T"
		if proto == "iterm2" {
			marker = "\x1b]1337"
		}
		check("  "+proto+" WRITES THE FULL IMAGE OUT OF BAND",
			strings.Contains(out, marker) && len(out) > 100_000, fmt.Sprint(len(out)))
		check("  it saves and restores the cursor",
			strings.HasPrefix(out, "\x1b7") && strings.HasSuffix(out, "\x1b8"), "cursor not preserved")
		check("  it positions the image", strings.Contains(out, "H"), "no cursor move")

		// Every frame line must survive the renderer's clipping.
		damaged := 0
		for _, l := range strings.Split(gv, "\n") {
			if lipgloss.Width(l) > 120 {
				damaged++
			}
		}
		check("  NO LINE EXCEEDS THE TERMINAL WIDTH", damaged == 0, fmt.Sprint(damaged))

		// Layout must not shift: the image occupies the same cell box.
		blockRows := len(strings.Split(strings.TrimRight(plainTerm.View(), "\n"), "\n"))
		photoRows := len(strings.Split(strings.TrimRight(gv, "\n"), "\n"))
		check("  the frame height is unchanged", blockRows == photoRows,
			fmt.Sprintf("%d vs %d", blockRows, photoRows))

		// And the text column must be untouched.
		check("  the logo still renders", strings.Contains(plain(gv), "___/"), "logo gone")
	}

	// The photo must be the full-resolution source, not a downscale: kitty
	// and iTerm2 do their own scaling, so shipping a shrunken PNG throws away
	// quality the terminal could have shown.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(png))
	check("THE PHOTO IS FULL RESOLUTION", err == nil && cfg.Width >= 365 && cfg.Height >= 454,
		fmt.Sprintf("%dx%d", cfg.Width, cfg.Height))

	// A graphics terminal may use more columns than the block art, which
	// gains nothing from extra cells.
	blockW := build(tmp, "pickle-rick", 160, 44)
	blockW.ForceImageProto("none")
	photoW := build(tmp, "pickle-rick", 160, 44)
	photoW.ForceImageProto("kitty")
	check("A REAL IMAGE MAY USE MORE COLUMNS",
		photoW.SplashArtWidth() > blockW.SplashArtWidth(),
		fmt.Sprintf("block=%d photo=%d", blockW.SplashArtWidth(), photoW.SplashArtWidth()))
	check("  the block art stays capped", blockW.SplashArtWidth() <= 40,
		fmt.Sprint(blockW.SplashArtWidth()))

	// Encoding the PNG per frame would undo the render-cost work.
	pm := build(tmp, "pickle-rick", 120, 32)
	pm.ForceImageProto("kitty")
	_ = pm.View() // warm
	start := time.Now()
	for i := 0; i < 50; i++ {
		_ = pm.View()
	}
	per := time.Since(start) / 50
	check("THE PHOTO ESCAPE IS CACHED", per < 5*time.Millisecond, per.String())

	// It must disappear with the splash.
	pm.PushSystem("hello")
	check("the photo goes when the chat starts",
		!strings.Contains(pm.View(), "\x1b_G"), "image still drawn")
}

// longestChunk measures the biggest kitty payload between escape headers.
func longestChunk(s string) int {
	worst := 0
	for _, part := range strings.Split(s, "\x1b_G")[1:] {
		if i := strings.Index(part, ";"); i >= 0 {
			body := part[i+1:]
			if j := strings.Index(body, "\x1b"); j >= 0 {
				body = body[:j]
			}
			if len(body) > worst {
				worst = len(body)
			}
		}
	}
	return worst
}

// testModelPersistence covers remembering the last selected model.
//
// The choice is written to the global rick.json, which is the lowest config
// tier — so it must survive a restart without ever overriding a project's
// own setting, and without dropping keys it does not understand.
func testModelPersistence(tmp string) {
	section("model persistence")

	dir := filepath.Join(tmp, "persist")
	_ = os.MkdirAll(dir, 0o755)
	home := filepath.Join(dir, "cfg")
	proj := filepath.Join(dir, "proj")
	_ = os.MkdirAll(proj, 0o755)

	// Point config at an isolated home for the duration.
	oldHome := os.Getenv("RICK_HOME")
	os.Setenv("RICK_HOME", home)
	defer os.Setenv("RICK_HOME", oldHome)

	launch := func() *tui.Model {
		loaded, err := config.Load(proj)
		if err != nil {
			return nil
		}
		store, _ := session.NewStore(filepath.Join(dir, "s"))
		snaps, _ := session.NewSnapshotter(proj, filepath.Join(dir, "d"))
		return send(tui.New(tui.Deps{
			Loaded: loaded, Themes: theme.Load(), ThemeDirs: theme.NewWatcher(),
			Registry: tools.NewRegistry(), Todos: tools.NewTodoStore(),
			Perms: permission.New(loaded.Config.Permission, proj),
			Store: store, Snapshots: snaps,
			Credentials: &config.Credentials{Providers: map[string]config.Credential{}},
			Providers:   map[string]provider.Provider{"anthropic": anthropic.New("sk", "")},
			Cwd:         proj, Version: "vtest",
		}), tea.WindowSizeMsg{Width: 100, Height: 30})
	}

	m := launch()
	deflt := m.ModelID()
	check("a fresh install uses the default model", deflt != "", deflt)

	// Switch, then relaunch.
	m.InputSetValue("/model longcat/LongCat-2.0")
	m = key(m, "enter")
	check("the model switches", m.ModelID() == "longcat/LongCat-2.0", m.ModelID())

	saved := filepath.Join(home, "rick.json")
	raw, err := os.ReadFile(saved)
	check("the choice is written to rick.json", err == nil, fmt.Sprint(err))
	check("  not to tui.json (runtime vs presentation)",
		!fileHas(filepath.Join(home, "tui.json"), "longcat"), "model leaked into tui.json")
	check("  and names the model", strings.Contains(string(raw), "longcat/LongCat-2.0"),
		string(raw))

	restarted := launch()
	check("A RESTART RESTORES THE LAST MODEL",
		restarted.ModelID() == "longcat/LongCat-2.0", restarted.ModelID())
	check("  and its context window", restarted.ContextWindow() == 1_000_000,
		fmt.Sprint(restarted.ContextWindow()))

	// A project config outranks the remembered global default.
	projCfg := filepath.Join(proj, "rick.json")
	_ = os.WriteFile(projCfg, []byte(`{"model":"anthropic/claude-opus-4-5"}`), 0o644)
	check("A PROJECT CONFIG STILL WINS",
		launch().ModelID() == "anthropic/claude-opus-4-5", launch().ModelID())
	_ = os.Remove(projCfg)

	// The write must preserve everything else in the file.
	_ = os.WriteFile(saved,
		[]byte(`{"model":"a/b","max_tokens":4096,"future_key":{"x":1}}`), 0o644)
	check("saving succeeds", config.SaveModelChoice("openai/gpt-5") == nil)
	after, _ := os.ReadFile(saved)
	check("  the model is updated", strings.Contains(string(after), "openai/gpt-5"))
	check("  KNOWN KEYS SURVIVE", strings.Contains(string(after), "4096"), string(after))
	check("  UNKNOWN KEYS SURVIVE", strings.Contains(string(after), "future_key"),
		string(after))

	// A commented .jsonc must not be silently rewritten as plain JSON: the
	// round-trip through encoding/json would delete every comment in it.
	jsonc := filepath.Join(home, "rick.jsonc")
	body := "{\n  // my notes\n  \"model\": \"a/b\"\n}\n"
	_ = os.WriteFile(jsonc, []byte(body), 0o644)
	err2 := config.SaveModelChoice("openai/gpt-5")
	check("A COMMENTED .jsonc IS REFUSED, NOT GUTTED", err2 != nil, "silently overwrote it")
	kept, _ := os.ReadFile(jsonc)
	check("  the comments survive", strings.Contains(string(kept), "// my notes"), string(kept))
	check("  and it says which file to edit",
		err2 != nil && strings.Contains(err2.Error(), "rick.jsonc"), fmt.Sprint(err2))
	_ = os.Remove(jsonc)

	// Resuming a session adopts its model WITHOUT changing the default.
	_ = os.WriteFile(saved, []byte(`{"model":"openai/gpt-5"}`), 0o644)
	store, _ := session.NewStore(filepath.Join(dir, "s2"))
	old := &session.Session{ID: session.NewID(), Cwd: proj, Model: "anthropic/claude-opus-4-5"}
	_ = store.Save(old)
	before, _ := os.ReadFile(saved)
	loaded, _ := config.Load(proj)
	snaps, _ := session.NewSnapshotter(proj, filepath.Join(dir, "d2"))
	rm := send(tui.New(tui.Deps{
		Loaded: loaded, Themes: theme.Load(), ThemeDirs: theme.NewWatcher(),
		Registry: tools.NewRegistry(), Todos: tools.NewTodoStore(),
		Perms: permission.New(loaded.Config.Permission, proj),
		Store: store, Snapshots: snaps,
		Credentials: &config.Credentials{Providers: map[string]config.Credential{}},
		Providers:   map[string]provider.Provider{"anthropic": anthropic.New("sk", "")},
		Cwd:         proj, Version: "vtest", ResumeID: old.ID,
	}), tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd := rm.Init(); cmd != nil {
		if out := cmd(); out != nil {
			if batch, ok := out.(tea.BatchMsg); ok {
				for _, c := range batch {
					if msg := c(); msg != nil &&
						!strings.Contains(fmt.Sprintf("%T", msg), "TickMsg") {
						nm, _ := rm.Update(msg)
						rm = nm.(*tui.Model)
					}
				}
			}
		}
	}
	check("resuming adopts the session's model",
		rm.ModelID() == "anthropic/claude-opus-4-5", rm.ModelID())
	nowRaw, _ := os.ReadFile(saved)
	check("  WITHOUT CHANGING THE SAVED DEFAULT", string(nowRaw) == string(before),
		string(nowRaw))
}

// fileHas reports whether a file exists and contains sub.
func fileHas(path, sub string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(raw), sub)
}

// testImageDetection covers resolving the terminal's graphics protocol.
//
// Environment variables alone are not reliable: launching rick through
// cmd.exe (which is what WezTerm's Windows shortcut does), a wrapper script
// or ssh drops WEZTERM_PANE and friends, so a capable terminal looks dumb.
func testImageDetection(tmp string) {
	section("image detection")

	set := func(kv map[string]string) func() {
		saved := map[string]string{}
		for k, v := range kv {
			saved[k] = os.Getenv(k)
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
		return func() {
			for k, v := range saved {
				if v == "" {
					os.Unsetenv(k)
				} else {
					os.Setenv(k, v)
				}
			}
		}
	}

	clear := map[string]string{
		"TERM": "xterm-256color", "TERM_PROGRAM": "", "KITTY_WINDOW_ID": "",
		"WEZTERM_PANE": "", "GHOSTTY_RESOURCES_DIR": "", "KONSOLE_VERSION": "",
		"WT_SESSION": "", "TMUX": "", "VSCODE_INJECTION": "",
	}

	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"kitty via KITTY_WINDOW_ID", map[string]string{"KITTY_WINDOW_ID": "1"}, "kitty"},
		{"kitty via TERM", map[string]string{"TERM": "xterm-kitty"}, "kitty"},
		{"ghostty", map[string]string{"GHOSTTY_RESOURCES_DIR": "/x"}, "kitty"},
		{"WEZTERM USES KITTY", map[string]string{"WEZTERM_PANE": "0"}, "kitty"},
		{"wezterm via TERM_PROGRAM", map[string]string{"TERM_PROGRAM": "WezTerm"}, "kitty"},
		{"iterm2", map[string]string{"TERM_PROGRAM": "iTerm.app"}, "iterm2"},
		{"konsole", map[string]string{"KONSOLE_VERSION": "22"}, "kitty"},
		{"plain xterm", nil, "none"},
		{"windows terminal without opt-in", map[string]string{"WT_SESSION": "x"}, "none"},
	} {
		env := map[string]string{}
		for k, v := range clear {
			env[k] = v
		}
		for k, v := range c.env {
			env[k] = v
		}
		restore := set(env)
		got := tui.DetectImageProto()
		restore()
		check("env: "+c.name, got == c.want, fmt.Sprintf("got %q want %q", got, c.want))
	}

	// tmux swallows graphics escapes unless passthrough is configured.
	restore := set(map[string]string{"KITTY_WINDOW_ID": "1", "TMUX": "/tmp/x"})
	check("TMUX DISABLES IMAGES", tui.DetectImageProto() == "none",
		tui.DetectImageProto())
	restore()

	// The real failure: WezTerm launched through cmd.exe. No env survives,
	// so the safe environment verdict is "none".
	restore = set(clear)
	check("A STRIPPED ENVIRONMENT DISABLES IMAGES", tui.DetectImageProto() == "none",
		tui.DetectImageProto())

	// Under the harness stdin is not a terminal, so the query must decline
	// immediately rather than hanging or guessing.
	start := time.Now()
	q := tui.QueryImageProto()
	took := time.Since(start)
	check("  the query declines when stdin is not a tty", q == "none", q)
	check("  AND RETURNS IMMEDIATELY", took < 200*time.Millisecond, took.String())

	// Detection must never query stdin, even when the environment is unknown.
	start = time.Now()
	check("UNKNOWN TERMINAL DETECTION IS INSTANT", tui.DetectImageSupport() == "none",
		tui.DetectImageSupport())
	check("  and costs nothing", time.Since(start) < 50*time.Millisecond,
		time.Since(start).String())
	restore()

	// A known terminal is detected without a probe.
	restore = set(map[string]string{"KITTY_WINDOW_ID": "1"})
	start = time.Now()
	check("KNOWN TERMINAL DETECTION IS INSTANT", tui.DetectImageSupport() == "kitty",
		tui.DetectImageSupport())
	check("  and costs nothing", time.Since(start) < 50*time.Millisecond,
		time.Since(start).String())
	restore()

	// The probe itself must be well formed: kitty query, XTVERSION, DA1.
	seq := tui.ProbeSequence()
	check("the probe asks kitty", strings.Contains(seq, "\x1b_Gi=31") &&
		strings.Contains(seq, "a=q"), "no kitty query")
	check("  it asks for the terminal name", strings.Contains(seq, "\x1b[>q"), "no XTVERSION")
	check("  IT ENDS WITH DA1 AS A FENCE", strings.HasSuffix(seq, "\x1b[c"), seq)
}
