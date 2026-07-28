package swarm

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestTaskBoardHonorsDependenciesAndOwner(t *testing.T) {
	board := NewTaskBoard()
	if err := board.Add(TaskSpec{ID: "inspect", Subject: "Inspect architecture", Owner: "morty"}); err != nil {
		t.Fatal(err)
	}
	if err := board.Add(TaskSpec{ID: "implement", Subject: "Implement change", Owner: "summer", DependsOn: []string{"inspect"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := board.ClaimNext("summer"); !errors.Is(err, ErrNoReadyTask) {
		t.Fatalf("dependent task should not be claimable, got %v", err)
	}

	first, err := board.ClaimNext("morty")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "inspect" || first.Status != TaskInProgress || first.Owner != "morty" {
		t.Fatalf("unexpected first claim: %#v", first)
	}
	if err := board.Complete("inspect", "summer", "wrong owner"); !errors.Is(err, ErrTaskOwnership) {
		t.Fatalf("wrong owner should be rejected, got %v", err)
	}
	if err := board.Complete("inspect", "morty", "architecture mapped"); err != nil {
		t.Fatal(err)
	}

	second, err := board.ClaimNext("summer")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "implement" || second.Status != TaskInProgress {
		t.Fatalf("unexpected dependent claim: %#v", second)
	}
}

func TestTaskBoardRequeuesFailedWorkerTask(t *testing.T) {
	board := NewTaskBoard()
	if err := board.Add(TaskSpec{ID: "review", Subject: "Review implementation"}); err != nil {
		t.Fatal(err)
	}
	if _, err := board.ClaimNext("rick"); err != nil {
		t.Fatal(err)
	}
	if err := board.Requeue("review", "rick", "worker interrupted"); err != nil {
		t.Fatal(err)
	}

	task, err := board.ClaimNext("beth")
	if err != nil {
		t.Fatal(err)
	}
	if task.Owner != "beth" || task.Attempts != 2 {
		t.Fatalf("requeued task was not reassigned correctly: %#v", task)
	}
}

func TestTaskBoardSummaryCountsTerminalAndBlockedStates(t *testing.T) {
	board := NewTaskBoard()
	for _, spec := range []TaskSpec{
		{ID: "done", Subject: "Done"},
		{ID: "failed", Subject: "Failed"},
		{ID: "blocked", Subject: "Blocked", DependsOn: []string{"failed"}},
	} {
		if err := board.Add(spec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := board.ClaimNext("rick"); err != nil {
		t.Fatal(err)
	}
	if err := board.Complete("done", "rick", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := board.ClaimNext("morty"); err != nil {
		t.Fatal(err)
	}
	if err := board.Fail("failed", "morty", "boom"); err != nil {
		t.Fatal(err)
	}

	summary := board.Summary()
	if summary.Completed != 1 || summary.Failed != 1 || summary.Blocked != 1 || summary.Total != 3 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestTaskBoardBatchSupportsForwardDependenciesAndRejectsCyclesAtomically(t *testing.T) {
	board := NewTaskBoard()
	if err := board.AddBatch([]TaskSpec{
		{ID: "merge", Subject: "Merge", DependsOn: []string{"left", "right"}},
		{ID: "right", Subject: "Right", DependsOn: []string{"root"}},
		{ID: "left", Subject: "Left", DependsOn: []string{"root"}},
		{ID: "root", Subject: "Root"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := board.List(); len(got) != 4 || got[0].ID != "merge" || got[3].ID != "root" {
		t.Fatalf("batch insertion order changed: %#v", got)
	}
	before := len(board.List())
	if err := board.AddBatch([]TaskSpec{
		{ID: "cycle-a", Subject: "A", DependsOn: []string{"cycle-b"}},
		{ID: "cycle-b", Subject: "B", DependsOn: []string{"cycle-a"}},
	}); err == nil {
		t.Fatal("cycle was accepted")
	}
	if got := len(board.List()); got != before {
		t.Fatalf("failed batch partially changed board: got %d tasks, want %d", got, before)
	}
}

func TestTaskBoardSpecificClaimIsAtomicAndOwnerBound(t *testing.T) {
	board := NewTaskBoard()
	if err := board.Add(TaskSpec{ID: "portal", Subject: "Portal"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	winners := make(chan string, 2)
	for _, name := range []string{"rick", "morty"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if _, err := board.Claim("portal", name); err == nil {
				winners <- name
			}
		}(name)
	}
	wg.Wait()
	close(winners)
	var winner string
	for name := range winners {
		if winner != "" {
			t.Fatalf("specific task had multiple winners: %s and %s", winner, name)
		}
		winner = name
	}
	if winner == "" {
		t.Fatal("specific task had no winner")
	}
	if _, err := board.Claim("portal", winner); err != nil {
		t.Fatalf("owner's repeated claim should be idempotent: %v", err)
	}
}

func TestTaskBoardConcurrentClaimsAreUnique(t *testing.T) {
	board := NewTaskBoard()
	for i := 0; i < 32; i++ {
		id := fmt.Sprintf("task-%02d", i)
		if err := board.Add(TaskSpec{ID: id, Subject: id}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	claimed := make(chan string, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if task, err := board.ClaimNext(fmt.Sprintf("agent-%02d", i)); err == nil {
				claimed <- task.ID
			}
		}(i)
	}
	wg.Wait()
	close(claimed)
	seen := map[string]bool{}
	for id := range claimed {
		if seen[id] {
			t.Fatalf("task claimed twice: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 32 {
		t.Fatalf("claimed %d tasks, want 32", len(seen))
	}
}
