package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestWrapANSIProbe(t *testing.T) {
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render("hello world this is styled")
	got := wrapIndent(styled, 40, "")
	fmt.Printf("input=%q\noutput=%q\nlines=%d\n", styled, got, len(got))
	fmt.Println("lipgloss.Width(esc)=", lipgloss.Width("\x1b"), "width([)=", lipgloss.Width("["))
}
