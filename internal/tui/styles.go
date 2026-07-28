// Package tui implements rick's terminal interface.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"rick/internal/theme"
)

// Styles is the resolved style set for the active theme.
type Styles struct {
	th *theme.Theme

	Base      lipgloss.Style
	Muted     lipgloss.Style
	Faint     lipgloss.Style
	Primary   lipgloss.Style
	Accent    lipgloss.Style
	Secondary lipgloss.Style
	Error     lipgloss.Style
	Warning   lipgloss.Style
	Success   lipgloss.Style
	Info      lipgloss.Style

	UserLabel      lipgloss.Style
	AssistantLabel lipgloss.Style
	SystemLabel    lipgloss.Style
	Thinking       lipgloss.Style

	ToolName   lipgloss.Style
	ToolOK     lipgloss.Style
	ToolErr    lipgloss.Style
	ToolOutput lipgloss.Style

	DiffAdded   lipgloss.Style
	DiffRemoved lipgloss.Style
	DiffContext lipgloss.Style
	DiffLineNum lipgloss.Style
	DiffHeader  lipgloss.Style

	Border       lipgloss.Style
	BorderFocus  lipgloss.Style
	PromptBorder lipgloss.Style
	PlanBorder   lipgloss.Style
	Panel        lipgloss.Style
	Overlay      lipgloss.Style
	OverlayWarn  lipgloss.Style
	StatusBar    lipgloss.Style
	Pill         lipgloss.Style
	PillActive   lipgloss.Style
}

// NewStyles builds the style set for a theme. A nil theme falls back to the
// default rather than panicking — callers can pass a lookup result straight
// through.
func NewStyles(th *theme.Theme) *Styles {
	if th == nil {
		th = theme.Load().Get("pickle-rick")
	}
	c := th.Color
	s := &Styles{th: th}

	s.Base = lipgloss.NewStyle().Foreground(c("text"))
	s.Muted = lipgloss.NewStyle().Foreground(c("textMuted"))
	s.Faint = lipgloss.NewStyle().Foreground(c("textFaint"))
	s.Primary = lipgloss.NewStyle().Foreground(c("primary"))
	s.Accent = lipgloss.NewStyle().Foreground(c("accent"))
	s.Secondary = lipgloss.NewStyle().Foreground(c("secondary"))
	s.Error = lipgloss.NewStyle().Foreground(c("error"))
	s.Warning = lipgloss.NewStyle().Foreground(c("warning"))
	s.Success = lipgloss.NewStyle().Foreground(c("success"))
	s.Info = lipgloss.NewStyle().Foreground(c("info"))

	s.UserLabel = lipgloss.NewStyle().Foreground(c("user")).Bold(true)
	s.AssistantLabel = lipgloss.NewStyle().Foreground(c("primary")).Bold(true)
	s.SystemLabel = lipgloss.NewStyle().Foreground(c("textMuted")).Italic(true)
	s.Thinking = lipgloss.NewStyle().Foreground(c("thinking")).Italic(true).Faint(true)

	s.ToolName = lipgloss.NewStyle().Foreground(c("tool"))
	s.ToolOK = lipgloss.NewStyle().Foreground(c("toolOk"))
	s.ToolErr = lipgloss.NewStyle().Foreground(c("toolErr"))
	s.ToolOutput = lipgloss.NewStyle().Foreground(c("textMuted"))

	s.DiffAdded = lipgloss.NewStyle().Foreground(c("diffAdded"))
	s.DiffRemoved = lipgloss.NewStyle().Foreground(c("diffRemoved"))
	s.DiffContext = lipgloss.NewStyle().Foreground(c("diffContext"))
	s.DiffLineNum = lipgloss.NewStyle().Foreground(c("diffLineNumber"))
	s.DiffHeader = lipgloss.NewStyle().Foreground(c("textMuted")).Bold(true)

	rounded := func(role string) lipgloss.Style {
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(c(role)).
			Padding(0, 1)
	}
	s.Border = rounded("border")
	s.BorderFocus = rounded("borderActive")
	s.PromptBorder = rounded("promptBorder")
	s.PlanBorder = rounded("planModeBorder")
	s.Panel = lipgloss.NewStyle().Foreground(c("text"))
	s.Overlay = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(c("border")).
		Padding(1, 2)
	s.OverlayWarn = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(c("warning")).
		Padding(1, 2)
	s.StatusBar = lipgloss.NewStyle().Foreground(c("textMuted"))
	s.Pill = lipgloss.NewStyle().Foreground(c("textFaint"))
	s.PillActive = lipgloss.NewStyle().Foreground(c("primary"))

	return s
}

// Theme returns the underlying theme.
func (s *Styles) Theme() *theme.Theme { return s.th }

// Rule renders a horizontal rule of the given width.
func (s *Styles) Rule(w int) string {
	if w < 1 {
		return ""
	}
	return s.Faint.Render(strings.Repeat("─", w))
}

// truncate shortens a string to w display cells, appending an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// padRight pads a string to w display cells.
func padRight(s string, w int) string {
	d := w - lipgloss.Width(s)
	if d <= 0 {
		return s
	}
	return s + strings.Repeat(" ", d)
}
