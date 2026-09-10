package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	cronlib "github.com/robfig/cron/v3"
)

// Action is a deferred action: a raw MCP tool call or an AI prompt executed later.
type Action struct {
	Tool        string         `json:"tool"`
	Args        map[string]any `json:"args"`
	Text        string         `json:"text,omitempty"`
	AgentPrompt string         `json:"agent_prompt,omitempty"` // non-empty → AI task
	ChatID      int64          `json:"chat_id,omitempty"`      // TG chat to send AI result to
}

// JobType distinguishes one-shot timers from recurring cron jobs.
type JobType string

const (
	TypeTimer JobType = "timer"
	TypeCron  JobType = "cron"
)

// Job is a scheduled action.
type Job struct {
	ID        string         `json:"id"`
	Type      JobType        `json:"type"`
	RunAt     time.Time      `json:"run_at,omitempty"`    // for timers
	CronExpr  string         `json:"cron_expr,omitempty"` // for cron
	Action    Action         `json:"action"`
	CreatedAt time.Time      `json:"created_at"`
	Label     string         `json:"label,omitempty"`
	cronEntry cronlib.EntryID // internal, not persisted
}

// Executor runs a Job's action (usually via HA MCP).
type Executor func(ctx context.Context, action Action) error

type Engine struct {
	mu       sync.Mutex
	ticker   *time.Ticker
	jobs     map[string]*Job
	executor Executor
	done     chan struct{}
	path     string
	log      *slog.Logger
	cron     *cronlib.Cron
}

// New creates a scheduler engine. path is the JSON persistence file ("" disables persistence).
func New(executor Executor, path string) *Engine {
	return &Engine{
		jobs:     make(map[string]*Job),
		executor: executor,
		done:     make(chan struct{}),
		path:     path,
		log:      slog.Default(),
		cron:     cronlib.New(cronlib.WithSeconds()),
	}
}

// Run starts the background loop. Returns when ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	if e.path != "" {
		e.load()
	}
	// start cron
	e.cron.Start()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	e.ticker = ticker

	for {
		select {
		case <-ctx.Done():
			e.cron.Stop()
			e.persist()
			return
		case <-e.done:
			e.cron.Stop()
			e.persist()
			return
		case <-ticker.C:
			e.fireDue(ctx)
		}
	}
}

// Stop stops the engine loop (for tests / shutdown).
func (e *Engine) Stop() {
	select {
	case <-e.done:
	default:
		close(e.done)
	}
}

// ScheduleAt adds a job to run at a specific time.
func (e *Engine) ScheduleAt(runAt time.Time, action Action, label string) (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if runAt.Before(time.Now()) {
		return nil, fmt.Errorf("scheduler: run time %v is in the past", runAt)
	}
	job := &Job{
		ID:        uuid.NewString(),
		Type:      TypeTimer,
		RunAt:     runAt,
		Action:    action,
		CreatedAt: time.Now(),
		Label:     label,
	}
	e.jobs[job.ID] = job
	e.persistLocked()
	return job, nil
}

// ScheduleIn adds a job to run after a duration.
func (e *Engine) ScheduleIn(d time.Duration, action Action, label string) (*Job, error) {
	return e.ScheduleAt(time.Now().Add(d), action, label)
}

// ScheduleCron adds a recurring cron job. expr is a standard 6-field cron (sec min hour dom mon dow).
func (e *Engine) ScheduleCron(expr string, action Action, label string) (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	job := &Job{
		ID:        uuid.NewString(),
		Type:      TypeCron,
		CronExpr:  expr,
		Action:    action,
		CreatedAt: time.Now(),
		Label:     label,
	}

	// wrap in a closure that fires the action
	cronAction := action
	cronLabel := label
	cronID, err := e.cron.AddFunc(expr, func() {
		e.log.Info("scheduler: firing cron job", "id", job.ID, "label", cronLabel)
		if e.executor != nil {
			_ = e.executor(context.Background(), cronAction)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("scheduler: bad cron expr %q: %w", expr, err)
	}
	job.cronEntry = cronID

	e.jobs[job.ID] = job
	e.persistLocked()
	return job, nil
}

// Cancel removes a job by ID.
func (e *Engine) Cancel(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	j, ok := e.jobs[id]
	if !ok {
		return false
	}
	if j.Type == TypeCron {
		e.cron.Remove(j.cronEntry)
	}
	delete(e.jobs, id)
	e.persistLocked()
	return true
}

// List returns a copy of all jobs sorted by RunAt.
func (e *Engine) List() []Job {
	e.mu.Lock()
	defer e.mu.Unlock()

	jobs := make([]Job, 0, len(e.jobs))
	for _, j := range e.jobs {
		jobs = append(jobs, *j)
	}
	for i := 1; i < len(jobs); i++ {
		for j := i; j > 0 && jobs[j].RunAt.Before(jobs[j-1].RunAt); j-- {
			jobs[j], jobs[j-1] = jobs[j-1], jobs[j]
		}
	}
	return jobs
}

// Get returns a job by ID.
func (e *Engine) Get(id string) (*Job, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	j, ok := e.jobs[id]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

func (e *Engine) fireDue(ctx context.Context) {
	now := time.Now()
	var toFire []*Job

	e.mu.Lock()
	for id, j := range e.jobs {
		if j.Type == TypeTimer && !j.RunAt.After(now) {
			toFire = append(toFire, j)
			delete(e.jobs, id)
		}
	}
	e.mu.Unlock()

	if len(toFire) > 0 {
		e.persist()
	}

	for _, j := range toFire {
		e.log.Info("scheduler: firing timer", "id", j.ID, "label", j.Label)
		if e.executor != nil {
			if err := e.executor(ctx, j.Action); err != nil {
				e.log.Error("scheduler: executor error", "id", j.ID, "error", err)
			}
		}
	}
}

// --- persistence ---

func (e *Engine) load() {
	data, err := os.ReadFile(e.path)
	if err != nil {
		if !os.IsNotExist(err) {
			e.log.Warn("scheduler: load jobs", "error", err)
		}
		return
	}
	var jobs []Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		e.log.Warn("scheduler: parse jobs file", "error", err)
		return
	}
	now := time.Now()
	for _, j := range jobs {
		if j.Type == TypeCron {
			cronID, err := e.cron.AddFunc(j.CronExpr, func() {
				if e.executor != nil {
					_ = e.executor(context.Background(), j.Action)
				}
			})
			if err != nil {
				e.log.Warn("scheduler: restore cron job", "id", j.ID, "expr", j.CronExpr, "error", err)
				continue
			}
			j.cronEntry = cronID
			e.jobs[j.ID] = &j
		} else if j.RunAt.After(now) {
			e.jobs[j.ID] = &j
		}
	}
	e.log.Info("scheduler: restored jobs", "count", len(e.jobs))
}

func (e *Engine) persistLocked() {
	if e.path == "" {
		return
	}

	jobs := make([]Job, 0, len(e.jobs))
	for _, j := range e.jobs {
		jobs = append(jobs, *j)
	}
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		e.log.Error("scheduler: marshal jobs", "error", err)
		return
	}
	if err := os.WriteFile(e.path, data, 0o644); err != nil {
		e.log.Error("scheduler: save jobs", "error", err)
	}
}

func (e *Engine) persist() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.persistLocked()
}