package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"rick/internal/agent"
	"rick/internal/config"
	"rick/internal/goal"
	"rick/internal/mcp"
	"rick/internal/permission"
	"rick/internal/plugin"
	"rick/internal/provider"
	"rick/internal/sandbox"
	"rick/internal/session"
	"rick/internal/swarm"
	"rick/internal/theme"
	"rick/internal/tools"
	"rick/internal/usage"
)

// Deps is everything the TUI needs from the outside world.
type Deps struct {
	Loaded       *config.Loaded
	Themes       *theme.Registry
	ThemeDirs    *theme.Watcher
	Registry     *tools.Registry
	Todos        *tools.TodoStore
	Perms        *permission.Engine
	Sandbox      *sandbox.Holder
	Store        *session.Store
	Snapshots    *session.Snapshotter
	Providers    map[string]provider.Provider
	MCP          *mcp.Manager
	Plugins      *plugin.Registry
	Skills       []plugin.Skill
	SwarmManager *swarm.SwarmManager
	Goals        *goal.Store
	Agent        string
	Cwd          string
	Version      string
	ResumeID     string
	InitialMsg   string
	// Credentials is already-loaded auth data (avoids double-load at startup).
	Credentials *config.Credentials
	// ImageProto names the terminal's graphics protocol ("kitty", "iterm2"
	// or ""). It is resolved before the program starts, because detecting it
	// reads a reply from stdin and bubbletea owns stdin once running.
	ImageProto string
	// Usage persists cumulative token usage per model per day.
	Usage *usage.Tracker
}

// modal identifies the active overlay, if any.
type modalKind int

const (
	modalNone modalKind = iota
	modalPermission
)

// Model is the root bubbletea model.
type Model struct {
	deps      Deps
	styles    *Styles
	themeName string

	width, height int
	ready         bool

	viewport   viewport.Model
	tx         *transcript
	input      textarea.Model
	mdRenderer *glamour.TermRenderer

	msgs        []ChatMsg
	chatContent string

	// conversation state
	history   []provider.Message
	sess      *session.Session
	agentName string
	modelID   string

	// streaming
	running      bool
	agentCh      chan agent.Event
	agentCancel  context.CancelFunc
	streamBuf    strings.Builder
	thinkBuf     strings.Builder
	pendingTools map[string]int // callID -> index into msgs
	spinnerTick  int

	// permission prompt
	permReq    permission.Request
	permReply  chan agent.PermissionDecision
	permGate   chan struct{}
	permCursor int

	// generic list modal (models, themes, sessions, help)
	modal modalKind

	listFilter string

	// file picker
	picker filePicker

	// presentation flags
	showThinking  bool
	toolDetails   bool
	rawMode       bool
	diffMode      DiffMode
	diffThreshold int

	// leader key
	leaderActive bool
	leaderKey    string

	// active team tracking; all view state is owned by the Bubble Tea loop.
	activeSwarms int
	teamViews    map[string]*SwarmView

	// input history
	inputHist []string
	histIdx   int
	histDraft string

	// status
	status     string
	statusTime time.Time
	usage      session.Usage
	ctxWindow  int
	quitting   bool

	// provider auth flow
	auth  authState
	creds *config.Credentials
	// providers declared by rick.json/env, which /auth must not delete
	pinnedProviders map[string]bool

	// terminal graphics protocol, detected once at startup
	imageProto imageProto

	// photoBox is the cell box the current frame reserved for the mascot
	// photo; photoDrawn is what is actually on screen. They differ only
	// when the image needs (re)drawing.
	photoBox   photoKey
	photoDrawn photoKey
	photoRow   int

	// consecutive quiet theme polls, used to back the interval off
	themeIdle int

	// startup tip, chosen once per launch
	tip string
	// title of the most recent session here, if any
	resumable string

	// reasoning effort for the active model, and whether it supports any
	reasoning      provider.ReasoningEffort
	reasoningStyle provider.ReasoningStyle

	// billed totals across the session (usage tracks context occupancy)
	billed session.Usage

	// timing for the current/last turn
	turnStart   time.Time
	turnElapsed time.Duration

	// armed inline numbered selection
	pending pendingChoice

	// ctrl+c must be pressed twice to quit
	quitArmed bool
	quitAt    time.Time

	// theme hot-reload
	themeWatch *theme.Watcher

	// cumulative entry renders, for verifying the render cache
	renderCount int

	// active subagent labels
	childActive []string

	// job tracking for background processes
	jobs *JobTracker

	// active swarms panel
	swarmPanel string

	// program handle, needed so background goroutines can Send messages
	program *tea.Program

	// attachments for the pending prompt (images, files, etc.)
	attachments              []attachment
	clipboardShortcutWasDown bool
	lastClipboardPaste       time.Time
	focused                  bool

	// per-tool expand/collapse (mouse click toggles when toolDetails is off)
	expandedTools map[string]bool

	// disabledTools tracks tools the user has toggled off via /tools
	disabledTools map[string]bool

	// toolRowMap maps content rows to tool call IDs for mouse click handling
	toolRowMap []toolRowEntry

	// double-click detection
	lastClickTime time.Time
	lastClickY    int
}

// toolRowEntry records the content-row span of one rendered tool block.
type toolRowEntry struct {
	callID   string
	startRow int
	endRow   int
}

// SetProgram wires the running tea.Program so async work can deliver messages.
func (m *Model) SetProgram(p *tea.Program) { m.program = p }

// tick messages
type spinnerTickMsg time.Time
type themePollMsg time.Time
type readAgentMsg struct{}
type statusMsg struct{ text string }
type errMsg struct{ err error }
type permAskMsg struct {
	req   permission.Request
	reply chan agent.PermissionDecision
}
type todosChangedMsg struct{ items []tools.TodoItem }

// New builds the root model.
func New(d Deps) *Model {
	cfg := d.Loaded.Config
	tuiCfg := d.Loaded.TUI

	themeName := tuiCfg.Theme
	th := d.Themes.Get(themeName)
	if th == nil {
		themeName = "pickle-rick"
		th = d.Themes.Get(themeName)
	}
	if th == nil {
		names := d.Themes.Names()
		if len(names) > 0 {
			themeName = names[0]
			th = d.Themes.Get(themeName)
		}
	}

	ta := textarea.New()
	ta.Placeholder = "ask anything · / commands · @ files · ! shell"
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.Focus()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "shift+enter", "ctrl+enter"),
		key.WithHelp("alt+enter/shift+enter/ctrl+enter", "newline"),
	)
	ta.KeyMap.DeleteWordBackward = key.NewBinding(key.WithKeys("ctrl+backspace", "alt+backspace", "ctrl+w"), key.WithHelp("ctrl+backspace", "delete word"))

	m := &Model{
		deps:          d,
		styles:        NewStyles(th),
		themeName:     themeName,
		input:         ta,
		agentName:     "build",
		modelID:       cfg.Model,
		tx:            newTranscript(),
		tip:           pickTip(),
		resumable:     latestSessionTitle(d),
		imageProto:    parseImageProto(d.ImageProto),
		themeWatch:    d.ThemeDirs,
		pendingTools:  map[string]int{},
		permGate:      make(chan struct{}, 1),
		showThinking:  tuiCfg.ShowThinking == nil || *tuiCfg.ShowThinking,
		toolDetails:   tuiCfg.ToolDetails != nil && *tuiCfg.ToolDetails,
		diffMode:      DiffMode(orString(tuiCfg.DiffMode, "auto")),
		diffThreshold: orInt(tuiCfg.DiffThreshold, 120),
		leaderKey:     orString(tuiCfg.Keybinds.Leader, "ctrl+x"),
		histIdx:       -1,
		teamViews:     map[string]*SwarmView{},
		ctxWindow:     200000,
		jobs:          NewJobTracker(50),
		focused:       true,
		expandedTools: map[string]bool{},
		disabledTools: map[string]bool{},
	}
	m.permGate <- struct{}{}

	// Credentials are already loaded in buildDeps — reuse them.
	if d.Credentials != nil {
		m.creds = d.Credentials
	} else {
		m.creds = &config.Credentials{Providers: map[string]config.Credential{}}
	}
	// Anything already configured at startup that /auth did not save is
	// owned by rick.json or the environment; never delete those.
	m.pinnedProviders = map[string]bool{}
	for id := range cfg.Providers {
		if _, ours := m.creds.Providers[id]; !ours {
			m.pinnedProviders[id] = true
		}
	}
	if d.Agent == "plan" || d.Agent == "build" {
		m.agentName = d.Agent
	}
	m.applyAgentPermissions()
	m.registerTaskTool()
	m.rebuildMarkdown(80)
	m.updateContextWindow()
	return m
}

// updateContextWindow syncs the status gauge and reasoning support with the
// active model.
func (m *Model) updateContextWindow() {
	provID, modelID := config.SplitModel(m.modelID)

	// Reasoning support is a property of the model, so re-detect on every
	// switch and keep the user's level when the new model also supports it.
	style, deflt := provider.DetectReasoning(modelID)
	m.reasoningStyle = style
	switch {
	case style == provider.ReasoningStyleNone:
		m.reasoning = provider.ReasoningOff
	case m.reasoning == "" || m.reasoning == provider.ReasoningOff:
		m.reasoning = deflt
	}

	// Gather every candidate and take the best-informed one. Provider stubs
	// and generic /models responses often report a placeholder window, so a
	// value derived from the model id must be able to win.
	known := provider.KnownContextWindow(modelID)
	var stored, advertised int
	if m.creds != nil {
		if cred, ok := m.creds.Providers[provID]; ok {
			stored = cred.ContextWindows[modelID]
		}
	}
	if p, ok := m.deps.Providers[provID]; ok {
		for _, mi := range p.Models() {
			if mi.ID == modelID {
				advertised = mi.ContextWindow
				break
			}
		}
	}
	switch {
	case known > 0:
		// The id names a model we know; trust that over a generic default,
		// but let a larger reported window through (e.g. an extended-context
		// deployment of the same model).
		m.ctxWindow = maxInt(known, maxInt(stored, advertised))
	case stored > 0:
		m.ctxWindow = stored
	case advertised > 0:
		m.ctxWindow = advertised
	default:
		m.ctxWindow = 200000
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func orString(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func orInt(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus(), tea.EnterAltScreen}
	// Some terminals (and piped/CI invocations) never deliver a
	// WindowSizeMsg. Without a fallback the UI would sit on "starting rick…"
	// forever, so seed a sane size that a real WindowSizeMsg overrides.
	cmds = append(cmds, func() tea.Msg { return ensureSizeMsg{} }, m.themePollCmd())
	if clipboardShortcutSupported() {
		cmds = append(cmds, clipboardShortcutTick())
	}
	if m.imageProto != imageNone {
		cmds = append(cmds, photoTick())
	}
	if m.deps.ResumeID != "" {
		cmds = append(cmds, func() tea.Msg { return resumeMsg{id: m.deps.ResumeID} })
	}
	if m.deps.InitialMsg != "" {
		msg := m.deps.InitialMsg
		cmds = append(cmds, func() tea.Msg { return submitMsg{text: msg} })
	}
	return tea.Batch(cmds...)
}

type resumeMsg struct{ id string }
type submitMsg struct{ text string }
type ensureSizeMsg struct{}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			return m, nil // ignore degenerate sizes from some terminals
		}
		return m, m.handleResize(msg)

	case tea.FocusMsg:
		m.focused = true
		return m, nil

	case tea.BlurMsg:
		m.focused = false
		return m, nil

	case clipboardShortcutTickMsg:
		down := clipboardShortcutDown()
		pressed := down && !m.clipboardShortcutWasDown
		m.clipboardShortcutWasDown = down
		if pressed && m.focused && time.Since(m.lastClipboardPaste) > 250*time.Millisecond {
			m.handleClipboardPaste()
		}
		return m, clipboardShortcutTick()

	case ensureSizeMsg:
		if !m.ready {
			w, h := terminalSize()
			return m, m.handleResize(tea.WindowSizeMsg{Width: w, Height: h})
		}
		return m, nil

	case tea.MouseMsg:
		if m.modal != modalNone || m.auth.active {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollBy(-m.scrollStep())
		case tea.MouseButtonWheelDown:
			m.scrollBy(m.scrollStep())
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress {
				m.handleMouseClick(msg)
			}
		case tea.MouseButtonRight:
			// ignore gracefully
		}
		return m, nil

	case tea.KeyMsg:
		if m.auth.active {
			return m.handleAuthKey(msg, msg.String())
		}
		return m.handleKey(msg)

	case SubmitText:
		return m.submit(string(msg))

	case authProbeMsg:
		m.applyAuthProbe(msg)
		return m, nil

	case oauthStartMsg:
		return m, m.applyOAuthStart(msg)

	case oauthDoneMsg:
		return m.applyOAuthDone(msg)

	case photoTickMsg:
		return m, tea.Batch(m.syncPhoto(), photoTick())

	case photoDrawnMsg:
		if msg.box == m.photoBox {
			m.photoDrawn = msg.box
		}
		return m, nil

	case photoClearedMsg:
		if m.photoBox == (photoKey{}) && m.photoDrawn == msg.box {
			m.photoDrawn = photoKey{}
		}
		return m, nil

	case themePollMsg:
		if m.themeWatch != nil && m.themeWatch.Changed() {
			m.deps.Themes = theme.Load(m.themeWatch.Dirs()...)
			if th := m.deps.Themes.Get(m.themeName); th != nil {
				m.styles = NewStyles(th)
				m.rebuildMarkdown(m.contentWidth())
				m.tx.invalidateAll(m.contentWidth())
				m.refresh()
				m.setStatus("theme reloaded: " + m.themeName)
			}
			m.themeIdle = 0
		} else if m.themeIdle < 8 {
			m.themeIdle++
		}
		return m, m.themePollCmd()

	case spinnerTickMsg:
		m.spinnerTick++
		if m.running || m.auth.busy || m.activeSwarms > 0 {
			for i, msg := range m.msgs {
				if msg.Kind == MsgTool && msg.ToolRunning {
					m.touch(i)
				}
				if msg.Kind == MsgSwarm && m.activeSwarms > 0 {
					m.touch(i)
				}
			}
			m.refresh()
			return m, m.spinnerCmd()
		}
		return m, nil

	case readAgentMsg:
		return m.drainAgent()

	case permAskMsg:
		m.permReq = msg.req
		m.permReply = msg.reply
		m.permCursor = 0
		m.modal = modalPermission
		return m, nil

	case todosChangedMsg:
		m.refresh()
		return m, nil

	case statusMsg:
		m.setStatus(msg.text)
		return m, nil

	case errMsg:
		m.appendMsg(ChatMsg{Kind: MsgError, Text: msg.err.Error(), Time: time.Now()})
		return m, nil

	case resumeMsg:
		m.doResume(msg.id)
		return m, nil

	case submitMsg:
		return m.submit(msg.text)

	case shellDoneMsg:
		if msg.idx < len(m.msgs) {
			out := msg.output
			isErr := msg.err != nil
			if isErr {
				out += "\n" + msg.err.Error()
			}
			m.msgs[msg.idx].ToolRunning = false
			m.msgs[msg.idx].ToolOutput = out
			m.msgs[msg.idx].ToolErr = isErr
			// Feed the result into the conversation so the model can see it.
			m.history = append(m.history,
				provider.UserText(fmt.Sprintf("I ran this shell command:\n```\n%s\n```\nOutput:\n```\n%s\n```",
					m.msgs[msg.idx].ToolTitle, truncate(out, 8000))))
		}
		m.refresh()
		return m, nil

	case subagentEventMsg:
		m.applySubagentEvent(msg)
		return m, nil

	case swarmStartMsg:
		plan, err := m.beginSwarm(msg)
		if err != nil {
			msg.reply <- swarmStartReply{err: err}
			return m, nil
		}
		msg.reply <- swarmStartReply{text: fmt.Sprintf("Team %q started with %d teammates.", msg.name, len(msg.agents))}
		return m, func() tea.Msg { m.runSwarmPlan(plan); return nil }

	case swarmWorkerMsg:
		m.applySwarmWorker(msg)
		return m, nil

	case swarmCompleteMsg:
		m.applySwarmComplete(msg)
		return m, nil

	case compactDoneMsg:
		if msg.err != nil {
			m.appendMsg(ChatMsg{Kind: MsgError, Text: "compact: " + msg.err.Error(), Time: time.Now()})
			return m, nil
		}
		summary := strings.TrimSpace(msg.summary)
		if summary == "" {
			m.setStatus("compact produced no summary")
			return m, nil
		}
		newHistory := []provider.Message{
			provider.UserText("Summary of the conversation so far:\n\n" + summary),
			provider.AssistantText("Understood. Continuing from that state."),
		}
		m.history = append(newHistory, msg.tail...)
		m.msgs = append([]ChatMsg{{Kind: MsgSystem,
			Text: "context compacted\n\n" + summary, Time: time.Now()}},
			messagesToChat(msg.tail)...)
		// Occupancy only: compaction shrinks the context but the tokens
		// already spent this session were still spent.
		m.usage = session.Usage{}
		m.refresh()
		m.setStatus("context compacted")
		m.saveSession()
		return m, nil

	case refreshDoneMsg:
		m.applyRefreshDone()
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleResize(msg tea.WindowSizeMsg) tea.Cmd {
	widthChanged := msg.Width != m.width
	m.width, m.height = msg.Width, msg.Height

	inputH := m.inputHeight()
	vpH := m.height - inputH - 4 // header + status + padding
	if vpH < 3 {
		vpH = 3
	}

	if !m.ready {
		m.viewport = viewport.New(m.width, vpH)
		m.viewport.YPosition = 1
		m.ready = true
		m.input.SetWidth(m.width - 4)
		m.rebuildMarkdown(m.contentWidth())
		m.seedWelcome()
		return nil
	}

	// Remember where the user was proportionally: after re-wrapping, the
	// absolute line offset is meaningless but the fraction still is.
	frac := relativePos(&m.viewport)
	following := m.tx.following()

	m.viewport.Width = m.width
	m.viewport.Height = vpH
	m.input.SetWidth(m.width - 4)

	if widthChanged {
		// Wrapping changed, so every cached block is stale.
		m.rebuildMarkdown(m.contentWidth())
		m.tx.invalidateAll(m.contentWidth())
	}
	m.refresh()

	if !following {
		restorePos(&m.viewport, frac)
	}
	return nil
}

func (m *Model) contentWidth() int {
	w := m.width - 2
	if w < 1 {
		w = 1
	}
	if w > 160 {
		w = 160
	}
	return w
}

func (m *Model) inputHeight() int {
	lines := m.input.LineCount()
	if lines < 1 {
		lines = 1
	}
	if lines > 8 {
		lines = 8
	}
	h := lines + 2 // border
	if m.picker.active {
		h += m.picker.height() + 1
	} else if strings.HasPrefix(m.input.Value(), "/") {
		h += m.autocompleteHeight() + 1
	}
	return h
}

func (m *Model) rebuildMarkdown(width int) {
	if width < 1 {
		width = 1
	}
	style := "dark"
	if m.themeName == "light" {
		style = "light"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
	if err == nil {
		m.mdRenderer = r
	}
}

// themePollCmd re-checks the theme directories once a second so edits to a
// theme file show up without a restart.
func (m *Model) themePollCmd() tea.Cmd {
	// Hot-reload matters only while someone is editing a theme file, so back
	// off from 1s to 8s once nothing has changed for a while. An edit is then
	// picked up within 8s and the idle UI stops touching the disk every
	// second for the entire session.
	d := time.Duration(1+m.themeIdle) * time.Second
	return tea.Tick(d, func(t time.Time) tea.Msg { return themePollMsg(t) })
}

func (m *Model) spinnerCmd() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(t time.Time) tea.Msg { return spinnerTickMsg(t) })
}

// trimHeight clips a block to at most n rows.
func trimHeight(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

// padHeight pads a block with blank lines so it occupies exactly n rows,
// trimming it if it is already taller.
func padHeight(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		return strings.Join(lines[:n], "\n")
	}
	return s + strings.Repeat("\n", n-len(lines))
}

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusTime = time.Now()
}

func (m *Model) appendMsg(msg ChatMsg) {
	m.msgs = append(m.msgs, msg)
	m.tx.noteAppend()
	m.refresh()
}

// refresh re-renders only what changed and re-applies the scroll policy.
//
// Entries are cached by the transcript, so a streaming chunk repaints one
// line instead of the whole history — that full re-layout was the cause of
// the flicker and the scroll snapping.
func (m *Model) refresh() {
	if !m.ready {
		return
	}
	w := m.contentWidth()

	// The streaming tail is volatile; keep it outside the cache.
	var live strings.Builder
	if m.thinkBuf.Len() > 0 && m.showThinking {
		live.WriteString(m.styles.Thinking.Render(wrapIndent(m.thinkBuf.String(), w-2, "  ")))
	}
	if m.streamBuf.Len() > 0 {
		if live.Len() > 0 {
			live.WriteString("\n")
		}
		live.WriteString(wrapIndent(m.streamBuf.String(), w, ""))
	}
	m.tx.live = live.String()

	m.tx.render(len(m.msgs), w, func(i int) string {
		m.renderCount++
		return m.renderMsg(m.msgs[i], w)
	})
	m.chatContent = m.tx.content
	m.tx.apply(&m.viewport)
	m.rebuildToolRowMap()
}

// touch marks one entry as needing a re-render.
func (m *Model) touch(i int) { m.tx.invalidate(i) }

// rebuildToolRowMap scans the rendered content and records which rows belong
// to tool blocks, so mouse clicks can toggle expand/collapse.
func (m *Model) rebuildToolRowMap() {
	m.toolRowMap = m.toolRowMap[:0]
	lines := strings.Split(m.chatContent, "\n")
	row := 0
	for i := range m.msgs {
		msg := &m.msgs[i]
		if msg.Kind != MsgTool || msg.CallID == "" {
			// Skip non-tool blocks: count their lines to keep row in sync.
			if i < len(m.tx.blocks) && m.tx.blocks[i] != "" {
				row += strings.Count(m.tx.blocks[i], "\n") + 1
				row++ // inter-block separator
			}
			continue
		}
		block := ""
		if i < len(m.tx.blocks) {
			block = m.tx.blocks[i]
		}
		if block == "" {
			continue
		}
		nLines := strings.Count(block, "\n") + 1
		m.toolRowMap = append(m.toolRowMap, toolRowEntry{
			callID:   msg.CallID,
			startRow: row,
			endRow:   row + nLines - 1,
		})
		row += nLines
		row++ // inter-block separator
	}
	_ = lines
}

// handleMouseClick processes a left-button press in the transcript area.
func (m *Model) handleMouseClick(msg tea.MouseMsg) {
	// Ignore clicks outside the viewport (status bar, input area).
	if msg.Y < m.viewport.YPosition || msg.Y >= m.viewport.YPosition+m.viewport.Height {
		return
	}

	// Double-click detection: same row within 400ms copies a file path.
	now := time.Now()
	if msg.Y == m.lastClickY && now.Sub(m.lastClickTime) < 400*time.Millisecond {
		m.lastClickTime = time.Time{}
		m.handleDoubleClick(msg)
		return
	}
	m.lastClickTime = now
	m.lastClickY = msg.Y

	// Map the screen row to a content row.
	contentRow := m.viewport.YOffset + (msg.Y - m.viewport.YPosition)

	// Find the tool block at this row and toggle it.
	for _, entry := range m.toolRowMap {
		if contentRow >= entry.startRow && contentRow <= entry.endRow {
			m.expandedTools[entry.callID] = !m.expandedTools[entry.callID]
			// Invalidate the tool's cached render so it re-renders expanded.
			for i := range m.msgs {
				if m.msgs[i].CallID == entry.callID {
					m.touch(i)
					break
				}
			}
			m.refresh()
			return
		}
	}
}

// handleDoubleClick copies a file path from the clicked line to clipboard.
func (m *Model) handleDoubleClick(msg tea.MouseMsg) {
	contentRow := m.viewport.YOffset + (msg.Y - m.viewport.YPosition)
	lines := strings.Split(m.chatContent, "\n")
	if contentRow < 0 || contentRow >= len(lines) {
		return
	}
	line := lines[contentRow]
	if match := linkRe.FindString(line); match != "" {
		copyToClipboardOSC52(match)
		m.setStatus("copied: " + match)
	}
}

// seedWelcome primes the transcript. The splash itself is rendered by View
// while the conversation is empty, so nothing is written into msgs — that way
// it disappears the moment real content arrives, without a special case.
func (m *Model) seedWelcome() { m.refresh() }

// View implements tea.Model.
func (m *Model) View() string {
	if !m.ready {
		return "starting rick…"
	}
	if m.quitting {
		return ""
	}

	var main string
	if len(m.msgs) == 0 && m.streamBuf.Len() == 0 {
		// Pre-conversation: banner instead of an empty box. It must still
		// fill the viewport's height — a short frame leaves the previous,
		// taller frame's lines on screen in alt-screen mode, which is what
		// made /new look like it did nothing.
		main = padHeight(m.splash(), m.viewport.Height)
	} else {
		// The conversation has started: no splash, so no image.
		m.photoBox = photoKey{}
		main = m.viewport.View()
	}
	body := main + "\n" + m.footer()

	if m.auth.active {
		return m.overlay(body, m.authView())
	}

	switch m.modal {
	case modalPermission:
		// Rendered inline above the input, not as a centered overlay: the
		// conversation stays visible and the prompt reads as part of the flow.
		return main + "\n" + m.permissionView() + "\n" + m.footer()
	}
	return body
}

func (m *Model) overlay(body, panel string) string {
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		panel, lipgloss.WithWhitespaceChars(" "))
}

func shortModel(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	id = strings.TrimPrefix(id, "claude-")
	// strip a trailing date stamp
	parts := strings.Split(id, "-")
	if len(parts) > 1 {
		last := parts[len(parts)-1]
		if len(last) == 8 && isDigits(last) {
			id = strings.Join(parts[:len(parts)-1], "-")
		}
	}
	return id
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

func (m *Model) footer() string {
	s := m.styles
	var b strings.Builder

	if m.deps.Todos != nil && m.activeSwarms == 0 {
		if items := m.deps.Todos.Items(); len(items) > 0 {
			b.WriteString(m.renderTodos(items, m.contentWidth()) + "\n")
		}
	}

	// The input border carries the mode: green for build, blue for plan, so
	// the current mode is readable at a glance without parsing any text.
	border := s.PromptBorder
	if m.agentName == "plan" {
		border = s.PlanBorder
	}

	prompt := s.Accent.Render("› ")
	switch {
	case m.running:
		prompt = s.Accent.Render(m.spinnerFrame() + " ")
	case strings.HasPrefix(m.input.Value(), "!"):
		prompt = s.Warning.Render("! ")
	case strings.HasPrefix(m.input.Value(), "/"):
		prompt = s.Secondary.Render("/ ")
	}
	inner := prompt + m.input.View()
	b.WriteString(border.Width(m.width-2).Render(inner) + "\n")

	// No attachment display in footer — markers are inline in the input

	if m.picker.active {
		b.WriteString(m.pickerView() + "\n")
	} else if strings.HasPrefix(m.input.Value(), "/") {
		if ac := m.autocompleteView(); ac != "" {
			b.WriteString(ac + "\n")
		}
	}

	if sb := m.statusBar(); sb != "" {
		b.WriteString(sb)
	}

	// Show active jobs and swarms
	if jobs := m.jobs.Render(m.contentWidth(), m.styles); jobs != "" {
		b.WriteString(jobs)
	}
	if m.swarmPanel != "" {
		b.WriteString(m.swarmPanel)
	}

	return b.String()
}

func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		s := fmt.Sprintf("%.1fM", float64(n)/1e6)
		return strings.Replace(s, ".0M", "M", 1) // 1.0M reads worse than 1M
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ---------- exported test helpers ----------

// InputSetValue sets the input text (test helper).
func (m *Model) InputSetValue(s string) { m.input.SetValue(s) }

// InputValue returns the input text (test helper).
func (m *Model) InputValue() string { return m.input.Value() }

// ChatContent returns the rendered transcript buffer (test helper).
func (m *Model) ChatContent() string { return m.chatContent }

// Msgs returns the chat entries (test helper).
func (m *Model) Msgs() []ChatMsg { return m.msgs }

// ThemeName returns the active theme name (test helper).
func (m *Model) ThemeName() string { return m.themeName }

// AgentName returns the active agent (test helper).
func (m *Model) AgentName() string { return m.agentName }

// RunSlash dispatches a slash command and returns the resulting transcript
// text (test helper).
func (m *Model) RunSlash(text string) string {
	m.RunSlash(text)
	if len(m.msgs) == 0 {
		return ""
	}
	return m.msgs[len(m.msgs)-1].Text
}

// ModelID returns the active model id (test helper).
func (m *Model) ModelID() string { return m.modelID }

// ModalOpen reports whether an overlay is showing (test helper).
func (m *Model) ModalOpen() bool { return m.modal != modalNone }

// PickerActive reports whether the @ picker is open (test helper).
func (m *Model) PickerActive() bool { return m.picker.active }

// PickerResults returns filtered picker entries (test helper).
func (m *Model) PickerResults() int { return len(m.picker.results) }

// RenderAutocomplete exposes the slash autocomplete (test helper).
func (m *Model) RenderAutocomplete() string { return m.autocompleteView() }

// Cwd returns the working directory (test helper).
func (m *Model) Cwd() string { return m.deps.Cwd }

// StatusLine returns the status bar (test helper).
func (m *Model) StatusLine() string { return m.statusBar() }

// AuthActive reports whether the /auth flow is open (test helper).
func (m *Model) AuthActive() bool { return m.auth.active }

// AuthStageName names the current /auth stage (test helper).
func (m *Model) AuthStageName() string {
	switch m.auth.stage {
	case authList:
		return "list"
	case authEnterKey:
		return "key"
	case authEditMenu:
		return "edit"
	case authAddName:
		return "add-name"
	case authAddURL:
		return "add-url"
	case authAddKey:
		return "add-key"
	case authProbing:
		return "probing"
	case authPickModel:
		return "pick-model"
	case authEnterModel:
		return "enter-model"
	case authDeviceCode:
		return "device-code"
	}
	return "unknown"
}

// AuthRowCount is the number of providers listed (test helper).
func (m *Model) AuthRowCount() int { return len(m.auth.rows) }

// AuthModelCount is the number of models offered (test helper).
func (m *Model) AuthModelCount() int { return len(m.auth.models) }

// Following reports whether the viewport tracks the tail (test helper).
func (m *Model) Following() bool { return m.tx.following() }

// Pending is the unseen-entry count (test helper).
func (m *Model) Pending() int { return m.tx.pending() }

// ScrollOffset is the viewport's line offset (test helper).
func (m *Model) ScrollOffset() int { return m.viewport.YOffset }

// ScrollFraction is the scroll position as 0..1 (test helper).
func (m *Model) ScrollFraction() float64 { return relativePos(&m.viewport) }

// RenderCount is the cumulative number of entry renders (test helper). It
// stays flat while streaming if the render cache is working.
func (m *Model) RenderCount() int { return m.renderCount }

// StreamLen is the streaming buffer size (test helper).
func (m *Model) StreamLen() int { return m.streamBuf.Len() }

// Ready reports whether the viewport is initialised (test helper).
func (m *Model) Ready() bool { return m.ready }

// ForceRefresh re-renders (test helper).
func (m *Model) ForceRefresh() { m.refresh() }

// HistoryLen is the provider-message count (test helper).
func (m *Model) HistoryLen() int { return len(m.history) }

// SetUsage seeds token counters (test helper).
func (m *Model) SetUsage(in, out int) {
	m.usage.Input, m.usage.Output = in, out
}

// StatusBar renders just the status line (test helper).
func (m *Model) StatusBar() string { return m.StatusLine() }

// resetStats clears every per-session counter shown in the status bar.
//
// These are session-scoped, not process-scoped: leaving them behind made a
// brand-new conversation report the previous one's tokens and turn time.
func (m *Model) resetStats() {
	m.usage = session.Usage{}
	m.billed = session.Usage{}
	m.turnStart = time.Time{}
	m.turnElapsed = 0
}

// ForceImageProto pretends the terminal supports graphics (test helper).
func (m *Model) ForceImageProto(name string) {
	switch name {
	case "kitty":
		m.imageProto = imageKitty
	case "iterm2":
		m.imageProto = imageITerm
	default:
		m.imageProto = imageNone
	}
}

// SetTurnElapsed fakes a completed turn duration (test helper).
func (m *Model) SetTurnElapsed(d time.Duration) {
	m.turnStart = time.Now().Add(-d)
	m.turnElapsed = d
}

// CompactUsage clears occupancy as /compact does (test helper).
func (m *Model) CompactUsage() { m.usage = session.Usage{} }

// Refresh rebuilds the transcript (test helper).
func (m *Model) Refresh() { m.refresh() }

// HumanTokens formats a token count (test helper).
func HumanTokens(n int) string { return humanTokens(n) }

// SetModelID switches model and re-detects its capabilities (test helper).
func (m *Model) SetModelID(id string) { m.setModel(id) }

// setModel switches the active model, re-detects its context window and
// reasoning support, and remembers the choice for the next launch.
//
// Every model change goes through here. Doing it inline at each call site is
// how the previous bug crept in: five places set modelID and each had to
// remember to call updateContextWindow, so a sixth would silently skip it.
func (m *Model) setModel(id string) {
	if id == "" || id == m.modelID {
		return
	}
	m.modelID = id
	m.updateContextWindow()
	// The switch itself always succeeds; only remembering it can fail, so
	// say so in the status line rather than silently forgetting — matching
	// how /theme reports a failed save.
	if err := config.SaveModelChoice(id); err != nil {
		m.setStatus("model: " + shortModel(id) + " (not saved: " + err.Error() + ")")
	}
}

// ContextWindow is the active model's window (test helper).
func (m *Model) ContextWindow() int { return m.ctxWindow }

// ContextPct is the gauge percentage (test helper).
func (m *Model) ContextPct() int { return m.contextPct() }

// UsageInput is the current context occupancy (test helper).
func (m *Model) UsageInput() int { return m.usage.Input }

// BilledTotal is the cumulative billed token count (test helper).
func (m *Model) BilledTotal() int { return m.billed.Input + m.billed.Output }

// Reasoning is the active effort level (test helper).
func (m *Model) Reasoning() string { return string(m.reasoning) }

// ApplyUsage feeds a usage event, as the agent would (test helper).
func (m *Model) ApplyUsage(input, output, cache int) {
	m.usage.Input = input
	m.usage.Output = output
	m.usage.CacheRead = cache
	m.billed.Input += input
	m.billed.Output += output
	m.billed.CacheRead += cache
}

// SubmitText is a message that submits text, as if typed (test helper).
type SubmitText string

// PendingKind is the armed inline selection type, 0 when none (test helper).
func (m *Model) PendingKind() int { return int(m.pending.kind) }

// PendingCount is the number of armed options (test helper).
func (m *Model) PendingCount() int { return len(m.pending.options) }

// MsgCount is the transcript entry count (test helper).
func (m *Model) MsgCount() int { return len(m.msgs) }

// CacheLen is the render cache size (test helper).
func (m *Model) CacheLen() int { return len(m.tx.blocks) }

// ForceRunning flips the running flag (test helper).
func (m *Model) ForceRunning(v bool) { m.running = v }

// Drain runs one agent-drain cycle (test helper).
func (m *Model) Drain() *Model {
	nm, _ := m.drainAgent()
	return nm.(*Model)
}

// PushSystem appends a system line (test helper).
func (m *Model) PushSystem(text string) {
	m.appendMsg(ChatMsg{Kind: MsgSystem, Text: text, Time: time.Now()})
}

// PushStreamChunk simulates one streamed token (test helper).
func (m *Model) PushStreamChunk(s string) {
	m.streamBuf.WriteString(s)
	m.refresh()
}

// AuthInputBuf is the /auth prompt buffer (test helper).
func (m *Model) AuthInputBuf() string { return m.auth.inputBuf }

// AuthClearInput empties the /auth prompt buffer (test helper).
func (m *Model) AuthClearInput() { m.auth.inputBuf = "" }

// AuthDraftURL is the normalised URL under construction (test helper).
func (m *Model) AuthDraftURL() string { return m.auth.draftURL }

// AuthStatus is the flow's status line, stripped of styling (test helper).
func (m *Model) AuthStatus() string { return stripANSI(m.auth.statusLine) }

// AuthReset returns the flow to a fresh provider list (test helper).
func (m *Model) AuthReset() {
	m.auth = authState{active: true, stage: authList}
	m.rebuildAuthRows()
}

// ProviderCount is the number of live providers (test helper).
func (m *Model) ProviderCount() int { return len(m.deps.Providers) }

// stripANSI removes escape sequences so tests can assert on plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// terminalSize resolves a usable size when the terminal never reports one.
func terminalSize() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err == nil && w > 0 && h > 0 {
		return w, h
	}
	w, h = 100, 30
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			w = n
		}
	}
	if v := os.Getenv("LINES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			h = n
		}
	}
	return w, h
}
