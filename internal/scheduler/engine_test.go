package scheduler

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduleIn(t *testing.T) {
	var fired int32
	exec := func(ctx context.Context, a Action) error {
		atomic.AddInt32(&fired, 1)
		return nil
	}
	e := New(exec, "")
	t.Cleanup(e.Stop)
	// Run without goroutine — fireDue directly
	ctx := context.Background()

	job, err := e.ScheduleIn(1*time.Millisecond, Action{Tool: "HassTurnOff", Args: map[string]any{"name": "test"}}, "test")
	if err != nil {
		t.Fatalf("ScheduleIn: %v", err)
	}

	// Manually advance time: fire jobs that are past run time
	time.Sleep(5 * time.Millisecond)
	e.fireDue(ctx)

	if atomic.LoadInt32(&fired) != 1 {
		t.Errorf("expected 1 fire, got %d", fired)
	}

	if _, ok := e.Get(job.ID); ok {
		t.Errorf("job should be removed after firing")
	}
}

func TestCancel(t *testing.T) {
	exec := func(ctx context.Context, a Action) error { return nil }
	e := New(exec, "")
	t.Cleanup(e.Stop)

	job, err := e.ScheduleIn(1*time.Hour, Action{Tool: "HassTurnOn", Args: map[string]any{"name": "test"}}, "never")
	if err != nil {
		t.Fatalf("ScheduleIn: %v", err)
	}

	if !e.Cancel(job.ID) {
		t.Errorf("Cancel should return true")
	}
	if e.Cancel(job.ID) {
		t.Errorf("Cancel of removed job should return false")
	}
}

func TestList(t *testing.T) {
	exec := func(ctx context.Context, a Action) error { return nil }
	e := New(exec, "")

	e.ScheduleIn(2*time.Hour, Action{Tool: "HassTurnOff", Args: map[string]any{"name": "switch"}}, "a")
	e.ScheduleIn(1*time.Hour, Action{Tool: "HassTurnOn", Args: map[string]any{"name": "light"}}, "b")

	jobs := e.List()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Label != "b" || jobs[1].Label != "a" {
		t.Errorf("wrong order: %+v", jobs)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	exec := func(ctx context.Context, a Action) error { return nil }

	// Engine 1: add a job without starting the Run loop (persist happens on ScheduleIn)
	e1 := New(exec, path)
	e1.ScheduleIn(1*time.Hour, Action{Tool: "HassTurnOn", Args: map[string]any{"name": "light"}, Text: "восстановлен"}, "test-persist")

	// No Run goroutine → file written synchronously by persistLocked
	e2 := New(exec, path)
	e2.load()

	jobs := e2.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 restored job, got %d", len(jobs))
	}
	if jobs[0].Label != "test-persist" {
		t.Errorf("label = %q", jobs[0].Label)
	}
	if jobs[0].Action.Text != "восстановлен" {
		t.Errorf("action text = %q", jobs[0].Action.Text)
	}
}

func TestPersistenceRestoredJobsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	exec := func(ctx context.Context, a Action) error { return nil }

	e1 := New(exec, path)
	e1.ScheduleIn(1*time.Hour, Action{Tool: "HassTurnOn", Args: map[string]any{"name": "light"}}, "keep-me")

	// second engine reads from the same file
	e2 := New(exec, path)
	e2.load()
	jobs := e2.List()
	if len(jobs) != 1 || jobs[0].Label != "keep-me" {
		t.Fatalf("restored jobs wrong: %+v", jobs)
	}
}

func TestPastTime(t *testing.T) {
	exec := func(ctx context.Context, a Action) error { return nil }
	e := New(exec, "")

	_, err := e.ScheduleAt(time.Now().Add(-time.Hour), Action{Tool: "HassTurnOn"}, "past")
	if err == nil {
		t.Errorf("expected error for past time")
	}
}