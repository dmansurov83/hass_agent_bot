package scheduler

import (
	"context"
	"os"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)

	job, err := e.ScheduleIn(50*time.Millisecond, Action{Domain: "light", Service: "turn_off"}, "test")
	if err != nil {
		t.Fatalf("ScheduleIn: %v", err)
	}

	time.Sleep(120 * time.Millisecond)

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

	job, err := e.ScheduleIn(1*time.Hour, Action{Domain: "light", Service: "turn_on"}, "never")
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

	e.ScheduleIn(2*time.Hour, Action{Domain: "switch", Service: "turn_off"}, "a")
	e.ScheduleIn(1*time.Hour, Action{Domain: "light", Service: "turn_on"}, "b")

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

	// Create engine and add a job
	e1 := New(exec, path)
	ctx1, cancel1 := context.WithCancel(context.Background())
	go e1.Run(ctx1)
	e1.ScheduleIn(1*time.Hour, Action{Domain: "light", Service: "turn_on", Text: "восстановлен"}, "test-persist")
	cancel1()
	e1.Stop()

	// Create new engine and verify the job is restored.
	// load() runs inside Run() in a goroutine; call it synchronously for the test.
	e2 := New(exec, path)
	e2.load()
	ctx2, cancel2 := context.WithCancel(context.Background())
	go e2.Run(ctx2)
	defer cancel2()
	defer e2.Stop()

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

	os.Remove(path)
}

func TestPastTime(t *testing.T) {
	exec := func(ctx context.Context, a Action) error { return nil }
	e := New(exec, "")

	_, err := e.ScheduleAt(time.Now().Add(-time.Hour), Action{Domain: "light", Service: "turn_on"}, "past")
	if err == nil {
		t.Errorf("expected error for past time")
	}
}