package swarm

import (
	"context"
	"fmt"
	"time"
)

// TeamResult is the terminal outcome of one teammate. Results returned by
// RunTaskTeam always follow the caller-provided order, regardless of finish order.
type TeamResult struct {
	Name     string
	Output   string
	Err      error
	Status   AgentStatus
	Started  time.Time
	Finished time.Time
}

// RuntimeEvent is emitted synchronously by RunTeam for worker lifecycle and
// runner events. The callback must return quickly.
type RuntimeEvent struct {
	Name  string
	Kind  EventType
	Value any
	Time  time.Time
}

// TeamJob binds a teammate runner to its authoritative shared task.
type TeamJob struct {
	Name   string
	TaskID string
	Runner Runner
}

type teamJobDone struct {
	index  int
	output string
	err    error
}

type teamJobEvent struct {
	index int
	value any
}

// RunTaskTeam schedules only dependency-ready tasks, claims each assignment for
// its trusted teammate, and preserves caller order in the returned results.
func RunTaskTeam(ctx context.Context, jobs []TeamJob, board *TaskBoard, maxConcurrent int, onEvent func(RuntimeEvent)) []TeamResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if board == nil {
		board = NewTaskBoard()
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	if maxConcurrent > len(jobs) {
		maxConcurrent = len(jobs)
	}
	results := make([]TeamResult, len(jobs))
	started := make([]bool, len(jobs))
	finished := make([]bool, len(jobs))
	for i, job := range jobs {
		results[i] = TeamResult{Name: job.Name, Status: StatusIdle}
	}
	emit := func(event RuntimeEvent) {
		if onEvent != nil {
			event.Time = time.Now()
			onEvent(event)
		}
	}
	if len(jobs) == 0 {
		return results
	}

	doneCh := make(chan teamJobDone, len(jobs))
	eventCh := make(chan teamJobEvent, 256)
	running, completed := 0, 0

	finishWithoutRun := func(index int, err error) {
		started[index], finished[index] = true, true
		completed++
		results[index].Err = err
		results[index].Status = StatusFailed
		results[index].Finished = time.Now()
		_ = board.Cancel(jobs[index].TaskID, err.Error())
		emit(RuntimeEvent{Name: jobs[index].Name, Kind: EventAgentFailed, Value: err})
	}

	for completed < len(jobs) {
		launched := false
		for running < maxConcurrent {
			candidate := -1
			for i, job := range jobs {
				if started[i] {
					continue
				}
				task, err := board.Get(job.TaskID)
				if err != nil {
					finishWithoutRun(i, err)
					continue
				}
				if task.Status == TaskBlocked {
					started[i], finished[i] = true, true
					completed++
					results[i].Err = fmt.Errorf("swarm: task %q is blocked by a failed dependency", job.TaskID)
					results[i].Status = StatusFailed
					results[i].Finished = time.Now()
					emit(RuntimeEvent{Name: job.Name, Kind: EventAgentFailed, Value: results[i].Err})
					continue
				}
				if task.Status == TaskPending && board.DependenciesComplete(job.TaskID) {
					candidate = i
					break
				}
			}
			if candidate < 0 {
				break
			}
			job := jobs[candidate]
			if _, err := board.Claim(job.TaskID, job.Name); err != nil {
				finishWithoutRun(candidate, err)
				continue
			}
			started[candidate] = true
			running++
			launched = true
			results[candidate].Started = time.Now()
			results[candidate].Status = StatusWorking
			emit(RuntimeEvent{Name: job.Name, Kind: EventAgentStart})
			go func(index int, job TeamJob) {
				defer func() {
					if recovered := recover(); recovered != nil {
						doneCh <- teamJobDone{index: index, err: fmt.Errorf("swarm: runner %q panicked: %v", job.Name, recovered)}
					}
				}()
				if job.Runner == nil {
					doneCh <- teamJobDone{index: index, err: fmt.Errorf("swarm: runner for %q is unavailable", job.Name)}
					return
				}
				output, err := job.Runner.Run(ctx, func(value any) {
					select {
					case eventCh <- teamJobEvent{index: index, value: value}:
					case <-ctx.Done():
					}
				})
				doneCh <- teamJobDone{index: index, output: output, err: err}
			}(candidate, job)
		}

		if completed >= len(jobs) {
			break
		}
		if running == 0 && !launched {
			for i := range jobs {
				if finished[i] {
					continue
				}
				if task, err := board.Get(jobs[i].TaskID); err == nil && task.Status == TaskBlocked {
					finished[i] = true
					completed++
					results[i].Err = fmt.Errorf("swarm: task %q is blocked", jobs[i].TaskID)
					results[i].Status = StatusFailed
					results[i].Finished = time.Now()
					emit(RuntimeEvent{Name: jobs[i].Name, Kind: EventAgentFailed, Value: results[i].Err})
					continue
				}
				finishWithoutRun(i, fmt.Errorf("swarm: task %q cannot become ready", jobs[i].TaskID))
			}
			break
		}

		select {
		case event := <-eventCh:
			emit(RuntimeEvent{Name: jobs[event.index].Name, Kind: EventAgentTool, Value: event.value})
		case completion := <-doneCh:
			running--
			completed++
			finished[completion.index] = true
			finishTeamJob(board, jobs[completion.index], &results[completion.index], completion.output, completion.err, emit)
		case <-ctx.Done():
			for i, job := range jobs {
				if finished[i] {
					continue
				}
				_ = board.Cancel(job.TaskID, ctx.Err().Error())
				finished[i] = true
				results[i].Err = ctx.Err()
				results[i].Status = StatusFailed
				results[i].Finished = time.Now()
				emit(RuntimeEvent{Name: job.Name, Kind: EventAgentFailed, Value: ctx.Err()})
			}
			return results
		}
	}
	for {
		select {
		case event := <-eventCh:
			emit(RuntimeEvent{Name: jobs[event.index].Name, Kind: EventAgentTool, Value: event.value})
		default:
			return results
		}
	}
}

func finishTeamJob(board *TaskBoard, job TeamJob, result *TeamResult, output string, err error, emit func(RuntimeEvent)) {
	result.Output = output
	result.Err = err
	result.Finished = time.Now()
	task, taskErr := board.Get(job.TaskID)
	if err != nil {
		result.Status = StatusFailed
		if taskErr == nil && task.Status == TaskInProgress {
			_ = board.Fail(job.TaskID, job.Name, err.Error())
		}
		emit(RuntimeEvent{Name: job.Name, Kind: EventAgentFailed, Value: err})
		return
	}
	if taskErr != nil {
		result.Err = taskErr
		result.Status = StatusFailed
		emit(RuntimeEvent{Name: job.Name, Kind: EventAgentFailed, Value: taskErr})
		return
	}
	switch task.Status {
	case TaskInProgress:
		if completionErr := board.Complete(job.TaskID, job.Name, output); completionErr != nil {
			result.Err = completionErr
			result.Status = StatusFailed
			emit(RuntimeEvent{Name: job.Name, Kind: EventAgentFailed, Value: completionErr})
			return
		}
	case TaskFailed, TaskCancelled, TaskBlocked:
		reason := task.Error
		if reason == "" {
			reason = fmt.Sprintf("task ended as %s", task.Status)
		}
		result.Err = fmt.Errorf("swarm: task %q failed: %s", job.TaskID, reason)
		result.Status = StatusFailed
		emit(RuntimeEvent{Name: job.Name, Kind: EventAgentFailed, Value: result.Err})
		return
	case TaskCompleted:
	default:
		result.Err = fmt.Errorf("swarm: task %q ended in invalid state %s", job.TaskID, task.Status)
		result.Status = StatusFailed
		emit(RuntimeEvent{Name: job.Name, Kind: EventAgentFailed, Value: result.Err})
		return
	}
	result.Status = StatusDone
	emit(RuntimeEvent{Name: job.Name, Kind: EventAgentDone, Value: output})
}

// RunTeam executes independent teammates with bounded concurrency. A failure
// remains StatusFailed (including cancellation); it is never reported as done.
func RunTeam(ctx context.Context, order []string, runners map[string]Runner, maxConcurrent int, onEvent func(RuntimeEvent)) []TeamResult {
	board := NewTaskBoard()
	tasks := make([]TaskSpec, 0, len(order))
	jobs := make([]TeamJob, 0, len(order))
	for index, name := range order {
		taskID := fmt.Sprintf("independent-%d", index)
		tasks = append(tasks, TaskSpec{ID: taskID, Subject: name, Owner: name})
		jobs = append(jobs, TeamJob{Name: name, TaskID: taskID, Runner: runners[name]})
	}
	if err := board.AddBatch(tasks); err != nil {
		results := make([]TeamResult, len(order))
		for index, name := range order {
			results[index] = TeamResult{Name: name, Status: StatusFailed, Err: err, Finished: time.Now()}
		}
		return results
	}
	return RunTaskTeam(ctx, jobs, board, maxConcurrent, onEvent)
}
