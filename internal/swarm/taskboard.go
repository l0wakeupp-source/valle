package swarm

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
	TaskFailed     TaskStatus = "failed"
	TaskBlocked    TaskStatus = "blocked"
	TaskCancelled  TaskStatus = "cancelled"
)

var (
	ErrNoReadyTask   = errors.New("swarm: no ready task")
	ErrTaskOwnership = errors.New("swarm: task is owned by another agent")
)

type TaskSpec struct {
	ID          string
	Subject     string
	Description string
	Owner       string
	DependsOn   []string
}

type Task struct {
	ID          string
	Subject     string
	Description string
	Owner       string
	Status      TaskStatus
	DependsOn   []string
	Result      string
	Error       string
	Attempts    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type TaskSummary struct {
	Total      int
	Pending    int
	Ready      int
	InProgress int
	Completed  int
	Failed     int
	Blocked    int
	Cancelled  int
}

type TaskBoard struct {
	mu    sync.RWMutex
	order []string
	tasks map[string]*Task
}

const maxCompletedTasks = 200

func NewTaskBoard() *TaskBoard {
	return &TaskBoard{tasks: map[string]*Task{}}
}

func (b *TaskBoard) Add(spec TaskSpec) error {
	return b.AddBatch([]TaskSpec{spec})
}

// AddBatch validates the complete graph before changing the board. Dependencies
// may refer to tasks later in the batch; missing references and cycles reject
// the whole batch atomically.
func (b *TaskBoard) AddBatch(specs []TaskSpec) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(specs) == 0 {
		return nil
	}
	normalized := make([]TaskSpec, len(specs))
	for i, spec := range specs {
		spec.ID = strings.TrimSpace(spec.ID)
		spec.Subject = strings.TrimSpace(spec.Subject)
		spec.Description = strings.TrimSpace(spec.Description)
		spec.Owner = strings.TrimSpace(spec.Owner)
		spec.DependsOn = append([]string(nil), spec.DependsOn...)
		for dependencyIndex := range spec.DependsOn {
			spec.DependsOn[dependencyIndex] = strings.TrimSpace(spec.DependsOn[dependencyIndex])
			if spec.DependsOn[dependencyIndex] == "" {
				return errors.New("swarm: dependency ID is required")
			}
		}
		normalized[i] = spec
	}
	specs = normalized

	known := make(map[string]bool, len(b.tasks)+len(specs))
	graph := make(map[string][]string, len(b.tasks)+len(specs))
	for id, task := range b.tasks {
		known[id] = true
		graph[id] = append([]string(nil), task.DependsOn...)
	}
	for _, spec := range specs {
		if spec.ID == "" {
			return errors.New("swarm: task ID is required")
		}
		if spec.Subject == "" {
			return errors.New("swarm: task subject is required")
		}
		if known[spec.ID] {
			return fmt.Errorf("swarm: duplicate task %q", spec.ID)
		}
		known[spec.ID] = true
		graph[spec.ID] = append([]string(nil), spec.DependsOn...)
	}
	for id, dependencies := range graph {
		for _, dependency := range dependencies {
			if !known[dependency] {
				return fmt.Errorf("swarm: dependency %q does not exist", dependency)
			}
			if dependency == id {
				return fmt.Errorf("swarm: task %q cannot depend on itself", id)
			}
		}
	}

	state := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("swarm: dependency cycle includes %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, dependency := range graph[id] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range graph {
		if err := visit(id); err != nil {
			return err
		}
	}

	now := time.Now()
	for _, spec := range specs {
		b.tasks[spec.ID] = &Task{
			ID:          spec.ID,
			Subject:     spec.Subject,
			Description: spec.Description,
			Owner:       spec.Owner,
			Status:      TaskPending,
			DependsOn:   append([]string(nil), spec.DependsOn...),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		b.order = append(b.order, spec.ID)
	}
	return nil
}

func (b *TaskBoard) ClaimNext(agent string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, id := range b.order {
		task := b.tasks[id]
		if task.Status != TaskPending || (task.Owner != "" && task.Owner != agent) || !b.dependenciesCompleteLocked(task) {
			continue
		}
		task.Owner = agent
		task.Status = TaskInProgress
		task.Attempts++
		task.UpdatedAt = time.Now()
		return cloneTask(task), nil
	}
	return Task{}, ErrNoReadyTask
}

// Claim atomically claims one specific ready task. Repeating a claim by the
// current owner is idempotent so a worker can safely confirm its assignment.
func (b *TaskBoard) Claim(id, agent string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("swarm: task %q not found", id)
	}
	if task.Status == TaskInProgress && task.Owner == agent {
		return cloneTask(task), nil
	}
	if task.Status != TaskPending {
		return Task{}, fmt.Errorf("swarm: task %q is %s", id, task.Status)
	}
	if task.Owner != "" && task.Owner != agent {
		return Task{}, ErrTaskOwnership
	}
	if !b.dependenciesCompleteLocked(task) {
		return Task{}, ErrNoReadyTask
	}
	task.Owner = agent
	task.Status = TaskInProgress
	task.Attempts++
	task.UpdatedAt = time.Now()
	return cloneTask(task), nil
}

func (b *TaskBoard) Complete(id, agent, result string) error {
	return b.finish(id, agent, TaskCompleted, result, "")
}

func (b *TaskBoard) Fail(id, agent, reason string) error {
	return b.finish(id, agent, TaskFailed, "", reason)
}

func (b *TaskBoard) Cancel(id, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[id]
	if !ok {
		return fmt.Errorf("swarm: task %q not found", id)
	}
	if isTaskTerminal(task.Status) {
		return nil
	}
	task.Status = TaskCancelled
	task.Error = reason
	task.UpdatedAt = time.Now()
	return nil
}

func (b *TaskBoard) finish(id, agent string, status TaskStatus, result, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[id]
	if !ok {
		return fmt.Errorf("swarm: task %q not found", id)
	}
	if task.Owner != agent {
		return ErrTaskOwnership
	}
	if task.Status != TaskInProgress {
		return fmt.Errorf("swarm: task %q is %s", id, task.Status)
	}
	task.Status = status
	task.Result = result
	task.Error = reason
	task.UpdatedAt = time.Now()
	b.trimCompletedLocked()
	return nil
}

func (b *TaskBoard) Requeue(id, agent, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[id]
	if !ok {
		return fmt.Errorf("swarm: task %q not found", id)
	}
	if task.Owner != agent {
		return ErrTaskOwnership
	}
	if task.Status != TaskInProgress {
		return fmt.Errorf("swarm: task %q is %s", id, task.Status)
	}
	task.Owner = ""
	task.Status = TaskPending
	task.Error = reason
	task.UpdatedAt = time.Now()
	return nil
}

func (b *TaskBoard) Get(id string) (Task, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	task, ok := b.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("swarm: task %q not found", id)
	}
	out := cloneTask(task)
	if out.Status == TaskPending && b.isBlockedLocked(task) {
		out.Status = TaskBlocked
	}
	return out, nil
}

func (b *TaskBoard) List() []Task {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Task, 0, len(b.order))
	for _, id := range b.order {
		task := b.tasks[id]
		item := cloneTask(task)
		if item.Status == TaskPending && b.isBlockedLocked(task) {
			item.Status = TaskBlocked
		}
		out = append(out, item)
	}
	return out
}

func (b *TaskBoard) Summary() TaskSummary {
	b.mu.RLock()
	defer b.mu.RUnlock()
	summary := TaskSummary{}
	for _, id := range b.order {
		task := b.tasks[id]
		summary.Total++
		status := task.Status
		if status == TaskPending && b.isBlockedLocked(task) {
			status = TaskBlocked
		}
		switch status {
		case TaskPending:
			if b.dependenciesCompleteLocked(task) {
				summary.Ready++
			} else {
				summary.Pending++
			}
		case TaskInProgress:
			summary.InProgress++
		case TaskCompleted:
			summary.Completed++
		case TaskFailed:
			summary.Failed++
		case TaskBlocked:
			summary.Blocked++
		case TaskCancelled:
			summary.Cancelled++
		}
	}
	return summary
}

// DependenciesComplete reports whether all dependencies of a task succeeded.
func (b *TaskBoard) DependenciesComplete(id string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	task := b.tasks[id]
	return task != nil && b.dependenciesCompleteLocked(task)
}

func (b *TaskBoard) dependenciesCompleteLocked(task *Task) bool {
	for _, id := range task.DependsOn {
		dependency := b.tasks[id]
		if dependency == nil || dependency.Status != TaskCompleted {
			return false
		}
	}
	return true
}

func (b *TaskBoard) isBlockedLocked(task *Task) bool {
	return b.isBlockedTaskLocked(task, map[string]bool{})
}

func (b *TaskBoard) isBlockedTaskLocked(task *Task, visiting map[string]bool) bool {
	if task == nil || visiting[task.ID] {
		return false
	}
	visiting[task.ID] = true
	defer delete(visiting, task.ID)
	for _, id := range task.DependsOn {
		dependency := b.tasks[id]
		if dependency == nil || dependency.Status == TaskFailed || dependency.Status == TaskCancelled {
			return true
		}
		if dependency.Status == TaskPending && b.isBlockedTaskLocked(dependency, visiting) {
			return true
		}
	}
	return false
}

func (b *TaskBoard) trimCompletedLocked() {
	completed := 0
	for _, task := range b.tasks {
		if isTaskTerminal(task.Status) {
			completed++
		}
	}
	for completed > maxCompletedTasks {
		oldestID := ""
		var oldest time.Time
		for _, id := range b.order {
			task := b.tasks[id]
			if task == nil || !isTaskTerminal(task.Status) {
				continue
			}
			if oldestID == "" || task.UpdatedAt.Before(oldest) {
				oldestID, oldest = id, task.UpdatedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(b.tasks, oldestID)
		for i, id := range b.order {
			if id == oldestID {
				b.order = append(b.order[:i], b.order[i+1:]...)
				break
			}
		}
		completed--
	}
}

func cloneTask(task *Task) Task {
	out := *task
	out.DependsOn = append([]string(nil), task.DependsOn...)
	return out
}

func isTaskTerminal(status TaskStatus) bool {
	return status == TaskCompleted || status == TaskFailed || status == TaskCancelled
}

func SortTasksByUpdated(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].UpdatedAt.Before(tasks[j].UpdatedAt)
	})
}
