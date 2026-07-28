package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"rick/internal/swarm"
)

// JobStatus tracks the state of a background job.
type JobStatus string

const (
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
	JobKilled  JobStatus = "killed"
)

// Job represents a tracked background process, bash command, or tool execution.
type Job struct {
	ID       string
	Kind     string // "bash", "tool", "swarm-agent"
	Label    string
	Status   JobStatus
	Started  time.Time
	Finished time.Time
	Output   string
	Error    string
	Children []*Job
	mu       sync.RWMutex
}

// JobTracker manages all background jobs visible in the TUI.
type JobTracker struct {
	mu   sync.RWMutex
	jobs []*Job
	max  int
}

// NewJobTracker creates a job tracker with a max history.
func NewJobTracker(max int) *JobTracker {
	if max <= 0 {
		max = 50
	}
	return &JobTracker{max: max}
}

// Add registers a new job.
func (t *JobTracker) Add(j *Job) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.jobs = append(t.jobs, j)
	if len(t.jobs) > t.max {
		t.jobs = t.jobs[len(t.jobs)-t.max:]
	}
}

// Update modifies a job's status.
func (t *JobTracker) Update(id string, status JobStatus, output string, err string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, j := range t.jobs {
		if j.ID == id {
			j.mu.Lock()
			j.Status = status
			if output != "" {
				j.Output = output
			}
			if err != "" {
				j.Error = err
			}
			if status != JobRunning {
				j.Finished = time.Now()
			}
			j.mu.Unlock()
			return
		}
	}
}

// Active returns all running jobs.
func (t *JobTracker) Active() []*Job {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var active []*Job
	for _, j := range t.jobs {
		j.mu.RLock()
		if j.Status == JobRunning {
			active = append(active, j)
		}
		j.mu.RUnlock()
	}
	return active
}

// Recent returns the most recent N jobs.
func (t *JobTracker) Recent(n int) []*Job {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if n <= 0 || n > len(t.jobs) {
		n = len(t.jobs)
	}
	out := make([]*Job, n)
	copy(out, t.jobs[len(t.jobs)-n:])
	return out
}

// Count returns total and active job counts.
func (t *JobTracker) Count() (total, active int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	total = len(t.jobs)
	for _, j := range t.jobs {
		j.mu.RLock()
		if j.Status == JobRunning {
			active++
		}
		j.mu.RUnlock()
	}
	return
}

// Render generates a status panel showing all active jobs.
func (t *JobTracker) Render(width int, styles *Styles) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var active []*Job
	for _, j := range t.jobs {
		j.mu.RLock()
		if j.Status == JobRunning {
			active = append(active, j)
		}
		j.mu.RUnlock()
	}

	if len(active) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", styles.Muted.Render(fmt.Sprintf("active jobs: %d", len(active))))
	for _, j := range active {
		j.mu.RLock()
		elapsed := time.Since(j.Started)
		icon := "⠋"
		iconStyle := styles.Secondary
		if j.Status == JobDone {
			icon = "✓"
			iconStyle = styles.Success
		} else if j.Status == JobFailed {
			icon = "✗"
			iconStyle = styles.Error
		}
		elapsedStr := ""
		if elapsed > time.Second {
			elapsedStr = fmt.Sprintf(" %s", styles.Faint.Render(elapsed.Round(time.Second).String()))
		}
		label := j.Label
		if label == "" {
			label = j.Kind
		}
		fmt.Fprintf(&b, "  %s %s%s\n", iconStyle.Render(icon), styles.Base.Render(truncate(label, width-10)), elapsedStr)
		j.mu.RUnlock()
	}
	return b.String()
}

// renderSwarmPanel generates a status panel for active swarms.
func (m *Model) renderSwarmPanel(width int) string {
	if m.deps.SwarmManager == nil {
		return ""
	}

	ids := m.deps.SwarmManager.List()
	if len(ids) == 0 {
		return ""
	}

	s := m.styles
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", s.Accent.Render("active swarms"))

	for _, id := range ids {
		sw, _ := m.deps.SwarmManager.Get(id)
		if sw == nil {
			continue
		}

		fmt.Fprintf(&b, "  %s %s\n", s.Primary.Render("◆"), s.Base.Render(sw.Name))
		fmt.Fprintf(&b, "    %s %s\n", s.Faint.Render("goal:"), s.Muted.Render(truncate(sw.Goal, width-12)))

		for name, ag := range sw.Agents {
			status := ag.GetStatus()
			icon := "⠋"
			iconStyle := s.Secondary
			switch status {
			case swarm.StatusDone:
				icon = "✓"
				iconStyle = s.Success
			case swarm.StatusFailed:
				icon = "✗"
				iconStyle = s.Error
			case swarm.StatusIdle:
				icon = "○"
				iconStyle = s.Faint
			}
			fmt.Fprintf(&b, "      %s %s\n", iconStyle.Render(icon), s.Base.Render(name))
		}

		if sw.Board.Len() > 0 {
			fmt.Fprintf(&b, "    %s %d entries\n", s.Faint.Render("board:"), sw.Board.Len())
			for _, e := range sw.Board.List() {
				fmt.Fprintf(&b, "      %s %s = %s\n", s.Muted.Render(e.Author+":"), s.Faint.Render(e.Key), s.Muted.Render(truncate(e.Value, width-20)))
			}
		}
	}
	return b.String()
}
