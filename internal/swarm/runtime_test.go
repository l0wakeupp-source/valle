package swarm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type runnerFunc func(context.Context, func(any)) (string, error)

func (f runnerFunc) Run(ctx context.Context, onEvent func(any)) (string, error) {
	return f(ctx, onEvent)
}

func TestRuntimeBoundsConcurrencyAndOrdersResults(t *testing.T) {
	var active, peak atomic.Int32
	runners := map[string]Runner{}
	order := []string{"rick", "morty", "summer", "beth", "jerry"}
	for i, name := range order {
		i, name := i, name
		runners[name] = runnerFunc(func(ctx context.Context, _ func(any)) (string, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(time.Duration(len(order)-i) * time.Millisecond)
			return "result-" + name, nil
		})
	}
	results := RunTeam(context.Background(), order, runners, 2, nil)
	if peak.Load() > 2 {
		t.Fatalf("peak concurrency %d, want <= 2", peak.Load())
	}
	for i, result := range results {
		if result.Name != order[i] || result.Output != "result-"+order[i] || result.Status != StatusDone {
			t.Fatalf("result[%d] = %#v", i, result)
		}
	}
}

func TestRuntimeCancellationAndFailureState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var once sync.Once
	runners := map[string]Runner{
		"rick": runnerFunc(func(ctx context.Context, _ func(any)) (string, error) {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return "", ctx.Err()
		}),
		"morty": runnerFunc(func(context.Context, func(any)) (string, error) { return "", errors.New("boom") }),
	}
	go func() { <-started; cancel() }()
	results := RunTeam(ctx, []string{"rick", "morty"}, runners, 2, nil)
	if len(results) != 2 || results[0].Status != StatusFailed || results[0].Err == nil {
		t.Fatalf("cancelled worker not failed: %#v", results)
	}
	for _, result := range results {
		if result.Status == StatusDone && result.Err != nil {
			t.Fatalf("failed worker marked done: %s: %v", result.Name, result.Err)
		}
	}
}

func TestTaskRuntimeSchedulesDependenciesAndPreservesDeclaredResultOrder(t *testing.T) {
	board := NewTaskBoard()
	if err := board.AddBatch([]TaskSpec{
		{ID: "dependent", Subject: "Dependent", Owner: "morty", DependsOn: []string{"root"}},
		{ID: "root", Subject: "Root", Owner: "rick"},
	}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	runOrder := []string{}
	job := func(name string) Runner {
		return runnerFunc(func(context.Context, func(any)) (string, error) {
			mu.Lock()
			runOrder = append(runOrder, name)
			mu.Unlock()
			return "result-" + name, nil
		})
	}
	results := RunTaskTeam(context.Background(), []TeamJob{
		{Name: "morty", TaskID: "dependent", Runner: job("morty")},
		{Name: "rick", TaskID: "root", Runner: job("rick")},
	}, board, 2, nil)
	if len(runOrder) != 2 || runOrder[0] != "rick" || runOrder[1] != "morty" {
		t.Fatalf("dependency scheduling order = %#v", runOrder)
	}
	if results[0].Name != "morty" || results[0].Output != "result-morty" || results[1].Name != "rick" {
		t.Fatalf("declared result order changed: %#v", results)
	}
	if summary := board.Summary(); summary.Completed != 2 {
		t.Fatalf("tasks were not completed by runtime: %#v", summary)
	}
}

func TestTaskRuntimeBlocksDependentsAfterFailure(t *testing.T) {
	board := NewTaskBoard()
	if err := board.AddBatch([]TaskSpec{
		{ID: "root", Subject: "Root", Owner: "rick"},
		{ID: "dependent", Subject: "Dependent", Owner: "morty", DependsOn: []string{"root"}},
	}); err != nil {
		t.Fatal(err)
	}
	var dependentRuns atomic.Int32
	results := RunTaskTeam(context.Background(), []TeamJob{
		{Name: "rick", TaskID: "root", Runner: runnerFunc(func(context.Context, func(any)) (string, error) { return "", errors.New("portal exploded") })},
		{Name: "morty", TaskID: "dependent", Runner: runnerFunc(func(context.Context, func(any)) (string, error) { dependentRuns.Add(1); return "bad", nil })},
	}, board, 2, nil)
	if dependentRuns.Load() != 0 {
		t.Fatal("blocked dependent was executed")
	}
	if results[0].Status != StatusFailed || results[1].Status != StatusFailed || results[1].Err == nil {
		t.Fatalf("failure states incorrect: %#v", results)
	}
	if task, _ := board.Get("dependent"); task.Status != TaskBlocked {
		t.Fatalf("dependent status = %q, want blocked", task.Status)
	}
}

func TestTaskRuntimeDeliversAllWorkerEvents(t *testing.T) {
	board := NewTaskBoard()
	if err := board.Add(TaskSpec{ID: "events", Subject: "Events", Owner: "worker"}); err != nil {
		t.Fatal(err)
	}
	const eventCount = 600
	runner := runnerFunc(func(_ context.Context, emit func(any)) (string, error) {
		for index := 0; index < eventCount; index++ {
			emit(index)
		}
		return "done", nil
	})
	received := 0
	RunTaskTeam(context.Background(), []TeamJob{{Name: "worker", TaskID: "events", Runner: runner}}, board, 1, func(event RuntimeEvent) {
		if event.Kind == EventAgentTool {
			received++
		}
	})
	if received != eventCount {
		t.Fatalf("received %d worker events, want %d", received, eventCount)
	}
}

func TestTaskRuntimeHonorsExplicitWorkerFailure(t *testing.T) {
	board := NewTaskBoard()
	if err := board.Add(TaskSpec{ID: "fail", Subject: "Fail", Owner: "worker"}); err != nil {
		t.Fatal(err)
	}
	runner := runnerFunc(func(context.Context, func(any)) (string, error) {
		if err := board.Fail("fail", "worker", "validation rejected"); err != nil {
			return "", err
		}
		return "reported failure", nil
	})
	results := RunTaskTeam(context.Background(), []TeamJob{{Name: "worker", TaskID: "fail", Runner: runner}}, board, 1, nil)
	if len(results) != 1 || results[0].Err == nil || results[0].Status != StatusFailed {
		t.Fatalf("explicit task failure was reported as success: %#v", results)
	}
}

func TestTaskRuntimeRecoversRunnerPanic(t *testing.T) {
	board := NewTaskBoard()
	if err := board.Add(TaskSpec{ID: "panic", Subject: "Panic", Owner: "worker"}); err != nil {
		t.Fatal(err)
	}
	results := RunTaskTeam(context.Background(), []TeamJob{{
		Name: "worker", TaskID: "panic", Runner: runnerFunc(func(context.Context, func(any)) (string, error) {
			panic("boom")
		}),
	}}, board, 1, nil)
	if len(results) != 1 || results[0].Err == nil || results[0].Status != StatusFailed {
		t.Fatalf("panic result = %#v", results)
	}
	task, err := board.Get("panic")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskFailed {
		t.Fatalf("task status = %s, want failed", task.Status)
	}
}

func TestRunTeamRecoversRunnerPanic(t *testing.T) {
	results := RunTeam(context.Background(), []string{"worker"}, map[string]Runner{
		"worker": runnerFunc(func(context.Context, func(any)) (string, error) {
			panic("boom")
		}),
	}, 1, nil)
	if len(results) != 1 || results[0].Err == nil || results[0].Status != StatusFailed {
		t.Fatalf("panic result = %#v", results)
	}
}

func TestTaskRuntimeCancellationDoesNotWaitForUncooperativeRunner(t *testing.T) {
	board := NewTaskBoard()
	if err := board.Add(TaskSpec{ID: "slow", Subject: "Slow", Owner: "worker"}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	started := make(chan struct{})
	runner := runnerFunc(func(context.Context, func(any)) (string, error) {
		close(started)
		<-release
		return "late", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []TeamResult, 1)
	go func() {
		done <- RunTaskTeam(ctx, []TeamJob{{Name: "worker", TaskID: "slow", Runner: runner}}, board, 1, nil)
	}()
	<-started
	cancel()
	select {
	case results := <-done:
		if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
			t.Fatalf("cancellation result = %#v", results)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("runtime waited for a runner that ignored cancellation")
	}
	close(release)
	task, err := board.Get("slow")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskCancelled {
		t.Fatalf("task status = %s", task.Status)
	}
}

func TestSwarmProcessCancellationDoesNotDeadlock(t *testing.T) {
	team := NewSwarmContext(context.Background(), "team", "citadel", "goal", TopologyMesh)
	team.AddAgent("rick", "worker")
	process := NewSwarmProcess(team)
	release := make(chan struct{})
	started := make(chan struct{})
	process.RegisterRunner("rick", runnerFunc(func(context.Context, func(any)) (string, error) {
		close(started)
		<-release
		return "late", nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := process.Start(ctx)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Start error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("SwarmProcess deadlocked after cancellation")
	}
	close(release)
	if _, err := process.Start(context.Background()); err == nil {
		t.Fatal("SwarmProcess allowed a second start")
	}
}
