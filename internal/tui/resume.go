package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"rick/internal/config"
	"rick/internal/session"
)

// sortMode controls how sessions are ordered.
type sortMode int

const (
	sortDate sortMode = iota
	sortMessages
	sortTitle
)

func (s sortMode) String() string {
	switch s {
	case sortDate:
		return "date"
	case sortMessages:
		return "msgs"
	case sortTitle:
		return "title"
	}
	return "?"
}

// resumeModel is the interactive session browser.
type resumeModel struct {
	store    *session.Store
	metas    []session.Meta
	filtered []session.Meta
	cursor   int
	width    int
	height   int
	styles   *Styles
	selected string
	quit     bool

	// search
	search    textinput.Model
	searching bool

	// goto
	gotoMode  bool
	gotoInput textinput.Model

	// sort
	sortMode sortMode

	// favorites
	favs    map[string]bool
	favPath string

	// status
	statusMsg  string
	statusTime time.Time
}

// ResumeSessions launches the interactive session browser.
func ResumeSessions(styles *Styles) (string, error) {
	store, err := session.NewStore(config.DataDir() + "/sessions")
	if err != nil {
		return "", err
	}

	metas, err := store.List("")
	if err != nil {
		return "", err
	}

	favPath := filepath.Join(config.DataDir(), "favorites.json")
	favs := loadFavs(favPath)

	si := textinput.New()
	si.Placeholder = "type to filter…"
	si.CharLimit = 120
	si.Prompt = ""
	si.Width = 40

	gi := textinput.New()
	gi.Placeholder = "goto #"
	gi.CharLimit = 6
	gi.Prompt = ""
	gi.Width = 10

	m := &resumeModel{
		store:     store,
		metas:     metas,
		filtered:  metas,
		styles:    styles,
		search:    si,
		gotoInput: gi,
		favs:      favs,
		favPath:   favPath,
		sortMode:  sortDate,
	}

	m.sort()

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return "", err
	}

	return m.selected, nil
}

// Init implements tea.Model.
func (m *resumeModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m *resumeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quit = true
			return m, tea.Quit
		}

		if time.Since(m.statusTime) > 3*time.Second {
			m.statusMsg = ""
		}

		if m.gotoMode {
			return m.handleGotoKey(msg)
		}

		if m.searching {
			return m.handleSearchKey(msg)
		}

		return m.handleNormalKey(msg)
	}

	return m, nil
}

func (m *resumeModel) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quit = true
		return m, tea.Quit

	case "enter":
		if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
			m.selected = m.filtered[m.cursor].ID
			m.quit = true
			return m, tea.Quit
		}

	case "down", "j":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "pgdown":
		m.cursor = min(m.cursor+10, len(m.filtered)-1)

	case "pgup":
		m.cursor = max(m.cursor-10, 0)

	case "home":
		m.cursor = 0

	case "end":
		m.cursor = len(m.filtered) - 1

	case "/":
		m.searching = true
		return m, m.search.Focus()

	case "g":
		m.gotoMode = true
		return m, m.gotoInput.Focus()

	case "esc":
		m.search.SetValue("")
		m.applyFilter()
		m.clampCursor()

	case "s":
		m.sortMode = sortMode((int(m.sortMode) + 1) % 3)
		m.sort()
		m.clampCursor()
		m.setStatus("sort: " + m.sortMode.String())

	case "f":
		if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
			id := m.filtered[m.cursor].ID
			m.favs[id] = !m.favs[id]
			saveFavs(m.favPath, m.favs)
		}

	case "d":
		if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
			id := m.filtered[m.cursor].ID
			m.store.Delete(id)
			delete(m.favs, id)
			saveFavs(m.favPath, m.favs)
			m.metas, _ = m.store.List("")
			m.sort()
			m.clampCursor()
			m.setStatus("session deleted")
		}
	}

	return m, nil
}

func (m *resumeModel) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.search.Blur()
		return m, nil

	case "enter":
		m.searching = false
		m.search.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.applyFilter()
	m.clampCursor()
	return m, cmd
}

func (m *resumeModel) handleGotoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.gotoMode = false
		m.gotoInput.Blur()
		m.gotoInput.SetValue("")
		return m, nil

	case "enter":
		m.gotoMode = false
		m.gotoInput.Blur()
		numStr := m.gotoInput.Value()
		m.gotoInput.SetValue("")
		if num, err := strconv.Atoi(numStr); err == nil && num > 0 && num <= len(m.filtered) {
			m.cursor = num - 1
			m.selected = m.filtered[m.cursor].ID
			m.quit = true
			return m, tea.Quit
		}
		m.setStatus("invalid number: " + numStr)
		return m, nil
	}

	var cmd tea.Cmd
	m.gotoInput, cmd = m.gotoInput.Update(msg)
	return m, cmd
}

func (m *resumeModel) clampCursor() {
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *resumeModel) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(m.search.Value()))
	if query == "" {
		m.filtered = m.metas
		return
	}

	type scored struct {
		meta  session.Meta
		score int
	}
	var results []scored

	for _, meta := range m.metas {
		title := strings.ToLower(meta.Title)
		cwd := strings.ToLower(meta.Cwd)
		model := strings.ToLower(meta.Model)

		best := -1
		for _, field := range []string{title, cwd, model} {
			if i := strings.Index(field, query); i >= 0 {
				if best == -1 || i < best {
					best = i
				}
			}
		}

		if best == -1 {
			for _, field := range []string{title, cwd, model} {
				if score, ok := fuzzyMatch(query, field); ok {
					if best == -1 || score < best {
						best = score + 1000
					}
				}
			}
		}

		if best >= 0 {
			if m.favs[meta.ID] {
				best -= 500
			}
			results = append(results, scored{meta, best})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].score < results[j].score
	})

	m.filtered = make([]session.Meta, 0, len(results))
	for _, r := range results {
		m.filtered = append(m.filtered, r.meta)
	}
}

func (m *resumeModel) sort() {
	switch m.sortMode {
	case sortDate:
		sort.Slice(m.metas, func(i, j int) bool {
			if m.favs[m.metas[i].ID] != m.favs[m.metas[j].ID] {
				return m.favs[m.metas[i].ID]
			}
			return m.metas[i].Updated.After(m.metas[j].Updated)
		})
	case sortMessages:
		sort.Slice(m.metas, func(i, j int) bool {
			if m.favs[m.metas[i].ID] != m.favs[m.metas[j].ID] {
				return m.favs[m.metas[i].ID]
			}
			return m.metas[i].Messages > m.metas[j].Messages
		})
	case sortTitle:
		sort.Slice(m.metas, func(i, j int) bool {
			if m.favs[m.metas[i].ID] != m.favs[m.metas[j].ID] {
				return m.favs[m.metas[i].ID]
			}
			a := strings.ToLower(m.metas[i].Title)
			b := strings.ToLower(m.metas[j].Title)
			if a == "" {
				a = "zzz"
			}
			if b == "" {
				b = "zzz"
			}
			return a < b
		})
	}
	m.applyFilter()
}

func (m *resumeModel) setStatus(msg string) {
	m.statusMsg = msg
	m.statusTime = time.Now()
}

// View implements tea.Model.
func (m *resumeModel) View() string {
	if m.quit || m.height == 0 {
		return ""
	}

	s := m.styles
	var b strings.Builder

	// Header
	header := s.Accent.Render("sessions")
	count := fmt.Sprintf("%d/%d", len(m.filtered), len(m.metas))
	sort := s.Faint.Render("sort:" + m.sortMode.String())
	spacer := strings.Repeat(" ", max(0, m.width-lipgloss.Width(header)-lipgloss.Width(count)-lipgloss.Width(sort)-4))
	b.WriteString(header + spacer + count + "  " + sort + "\n")

	// Search bar
	if m.searching {
		b.WriteString("  " + s.Primary.Render("▸ ") + m.search.View() + "\n")
	} else if m.search.Value() != "" {
		b.WriteString("  " + s.Faint.Render("filter: ") + s.Muted.Render(m.search.Value()) + "\n")
	}

	// Goto bar
	if m.gotoMode {
		b.WriteString("  " + s.Primary.Render("→ ") + m.gotoInput.View() + "\n")
	}

	// Status message
	if m.statusMsg != "" && time.Since(m.statusTime) < 3*time.Second {
		b.WriteString("  " + s.Warning.Render(m.statusMsg) + "\n")
	}

	// Calculate list height - use full terminal minus header/footer
	reserved := 3 // header + footer + separator
	if m.searching || m.search.Value() != "" {
		reserved++
	}
	if m.gotoMode {
		reserved++
	}
	if m.statusMsg != "" && time.Since(m.statusTime) < 3*time.Second {
		reserved++
	}
	listHeight := m.height - reserved
	if listHeight < 3 {
		listHeight = 3
	}

	// Session list with numbers
	visibleStart := 0
	visibleEnd := len(m.filtered)
	if visibleEnd > listHeight {
		visibleStart = m.cursor - listHeight/2
		if visibleStart < 0 {
			visibleStart = 0
		}
		visibleEnd = visibleStart + listHeight
		if visibleEnd > len(m.filtered) {
			visibleEnd = len(m.filtered)
			visibleStart = visibleEnd - listHeight
			if visibleStart < 0 {
				visibleStart = 0
			}
		}
	}

	// Number width for alignment
	numWidth := len(fmt.Sprintf("%d", len(m.filtered)))
	if numWidth < 2 {
		numWidth = 2
	}

	for i := visibleStart; i < visibleEnd; i++ {
		meta := m.filtered[i]
		isSelected := i == m.cursor
		isFav := m.favs[meta.ID]

		// Number prefix (right-aligned)
		num := fmt.Sprintf("%*d ", numWidth, i+1)

		prefix := "  "
		if isSelected {
			prefix = s.Accent.Render("▸ ")
		}

		fav := ""
		if isFav {
			fav = s.Warning.Render("★ ")
		}

		title := meta.Title
		if title == "" {
			title = "(untitled)"
		}

		titleWidth := m.width - numWidth - 20
		if titleWidth < 10 {
			titleWidth = 10
		}
		title = truncate(title, titleWidth)

		msgCount := s.Faint.Render(fmt.Sprintf("%d msgs", meta.Messages))
		age := s.Muted.Render(humanAge(meta.Updated))

		line := fmt.Sprintf("%s%s%s%s  %s  %s", num, prefix, fav, title, msgCount, age)
		if isSelected {
			b.WriteString(s.Base.Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}

	if len(m.filtered) == 0 {
		b.WriteString(s.Muted.Render("  no sessions match") + "\n")
	}

	// Footer
	help := "↑/↓ navigate · g goto · enter resume · / filter · s sort · f fav · d del · q quit"
	b.WriteString(s.Faint.Render(truncate(help, m.width)) + "\n")

	return b.String()
}

// ---------- helpers ----------

func loadFavs(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]bool{}
	}
	var favs map[string]bool
	_ = json.Unmarshal(data, &favs)
	if favs == nil {
		favs = map[string]bool{}
	}
	return favs
}

func saveFavs(path string, favs map[string]bool) {
	data, _ := json.MarshalIndent(favs, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
}
