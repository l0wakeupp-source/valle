package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"rick/internal/swarm"
)

func TestRenderSwarmCardOrderTokensFullResultsAndFixedTime(t *testing.T) {
	started := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	finished := started.Add(12 * time.Second)
	longResult := "FULL-RESULT-START\n" + strings.Repeat("portal-data-", 30) + "\nFULL-RESULT-END"
	view := &SwarmView{
		Name: "citadel", Goal: "repair the portal index", Active: false, Started: started, Finished: finished,
		AgentOrd: []string{"worker-b", "worker-a"},
		Agents: map[string]*AgentView{
			"worker-a": {Name: "Rick", Color: "#00D4AA", Status: swarm.StatusDone, Started: started, Finished: finished, CurrentAction: "done", TokensIn: 11, TokensOut: 12, CacheRead: 13, CacheWrite: 14, ToolsUsed: 2, Result: "second"},
			"worker-b": {Name: "Morty", Color: "#FFE600", Status: swarm.StatusDone, Started: started, Finished: finished, CurrentAction: "done", TokensIn: 21, TokensOut: 22, CacheRead: 23, CacheWrite: 24, ToolsUsed: 3, Result: longResult},
		},
	}
	now := finished.Add(time.Hour)
	got := stripANSI(renderSwarmCard(view, 44, now, NewStyles(nil), "*"))
	if strings.Index(got, "Morty") > strings.Index(got, "Rick") {
		t.Fatalf("agent order changed:\n%s", got)
	}
	for _, want := range []string{"in 21", "out 22", "cache-read 23", "cache-write 24", "FULL-RESULT-START", "FULL-RESULT-END", "elapsed", "12s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	gotAgain := stripANSI(renderSwarmCard(view, 44, now, NewStyles(nil), "*"))
	if gotAgain != got {
		t.Fatal("fixed-time render is not deterministic")
	}
}

func TestRenderSwarmCardHonorsTrueNarrowWidth(t *testing.T) {
	view := &SwarmView{Name: "very-long-team-name", Goal: strings.Repeat("goal", 12), Active: true, AgentOrd: []string{"a"}, Agents: map[string]*AgentView{
		"a": {Name: "Rick", Color: "#00D4AA", Status: swarm.StatusWorking, Started: time.Unix(0, 0), CurrentAction: strings.Repeat("working", 10), TokensIn: 123, TokensOut: 456},
	}}
	for _, width := range []int{32, 40, 47} {
		got := renderSwarmCard(view, width, time.Unix(5, 0), NewStyles(nil), "*")
		plain := stripANSI(got)
		if strings.Contains(plain, "workingworking") || strings.Contains(plain, "tools ") {
			t.Fatalf("width=%d retained low-priority detail:\n%s", width, plain)
		}
		for i, line := range strings.Split(got, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width=%d line %d has width %d: %q", width, i, w, stripANSI(line))
			}
		}
	}
}

func TestRenderSwarmCardShowsRoleActionAndTaskSummaryAtWideWidths(t *testing.T) {
	board := swarm.NewTaskBoard()
	if err := board.Add(swarm.TaskSpec{ID: "inspect", Subject: "Inspect"}); err != nil {
		t.Fatal(err)
	}
	view := &SwarmView{
		Name: "citadel", Active: true, Tasks: board, AgentOrd: []string{"a"},
		Agents: map[string]*AgentView{"a": {Name: "Rick", Role: "portal engineer", Status: swarm.StatusWorking, Started: time.Unix(0, 0), CurrentAction: "reading schematics", ToolsUsed: 3}},
	}
	got := stripANSI(renderSwarmCard(view, 80, time.Unix(5, 0), NewStyles(nil), "*"))
	for _, want := range []string{"tasks ready 1", "role portal engineer", "reading schematics", "tools 3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
}

func TestIndependentSwarmCardsDoNotShareResults(t *testing.T) {
	one := &SwarmView{Name: "one", AgentOrd: []string{"a"}, Agents: map[string]*AgentView{"a": {Name: "Rick", Status: swarm.StatusDone, Result: "ONLY-ONE"}}}
	two := &SwarmView{Name: "two", AgentOrd: []string{"b"}, Agents: map[string]*AgentView{"b": {Name: "Morty", Status: swarm.StatusDone, Result: "ONLY-TWO"}}}
	renderedOne := stripANSI(renderSwarmCard(one, 30, time.Unix(0, 0), NewStyles(nil), "*"))
	renderedTwo := stripANSI(renderSwarmCard(two, 30, time.Unix(0, 0), NewStyles(nil), "*"))
	if strings.Contains(renderedOne, "ONLY-TWO") || strings.Contains(renderedTwo, "ONLY-ONE") {
		t.Fatalf("cards leaked state:\n%s\n%s", renderedOne, renderedTwo)
	}
}

func TestResetSwarmRuntimeCancelsAndRemovesActiveTeams(t *testing.T) {
	manager := swarm.NewSwarmManager()
	team := swarm.NewSwarmContext(context.Background(), "team-1", "citadel", "goal", swarm.TopologyMesh)
	manager.Add(team)
	model := Model{
		deps:         Deps{SwarmManager: manager},
		activeSwarms: 1,
		teamViews:    map[string]*SwarmView{"team-1": {SwarmID: "team-1", Active: true}},
	}
	model.resetSwarmRuntime()
	select {
	case <-team.Ctx.Done():
	default:
		t.Fatal("active team context was not cancelled")
	}
	if _, err := manager.Get("team-1"); err == nil {
		t.Fatal("active team remained registered")
	}
	if model.activeSwarms != 0 || len(model.teamViews) != 0 {
		t.Fatalf("runtime state was not cleared: active=%d views=%d", model.activeSwarms, len(model.teamViews))
	}
}

func TestRenderSwarmMessageUsesSuppliedBlockWidth(t *testing.T) {
	model := Model{
		styles: NewStyles(nil),
		teamViews: map[string]*SwarmView{
			"team": {
				Name: "citadel", Active: true, AgentOrd: []string{"rick"},
				Agents: map[string]*AgentView{"rick": {Name: "Rick Sanchez", Color: "#00D4AA", Status: swarm.StatusWorking}},
			},
		},
	}
	rendered := model.renderSwarmMessage(ChatMsg{SwarmID: "team"}, 20)
	for lineIndex, line := range strings.Split(rendered, "\n") {
		if lineWidth := lipgloss.Width(line); lineWidth > 20 {
			t.Fatalf("line %d width = %d: %q", lineIndex, lineWidth, stripANSI(line))
		}
	}
}
