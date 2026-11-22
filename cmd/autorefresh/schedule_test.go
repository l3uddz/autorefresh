package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

type fakeStarter func(context.Context)

func (f fakeStarter) Scheduled(ctx context.Context) { f(ctx) }

// serve starts the first run at once, without waiting for the schedule.
func TestServeStartsFirstRunAtOnce(t *testing.T) {
	captureLog(t)

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	r := fakeStarter(func(context.Context) {
		calls.Add(1)
		cancel()
	})

	done := make(chan struct{})
	go func() {
		serve(ctx, r, cron.Every(24*time.Hour))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not start a run at once")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call of run, got %d", got)
	}
}

// serve returns only after the busy first run returned, so main does not
// close the database under it.
func TestServeWaitsForFirstRun(t *testing.T) {
	captureLog(t)

	ctx, cancel := context.WithCancel(context.Background())
	var finished atomic.Bool
	r := fakeStarter(func(ctx context.Context) {
		cancel()
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		finished.Store(true)
	})

	serve(ctx, r, cron.Every(24*time.Hour))
	if !finished.Load() {
		t.Fatal("serve returned before the first run returned")
	}
}

type shutdownJobs struct {
	started chan struct{}
	order   chan string
}

func (j shutdownJobs) Scheduled(ctx context.Context) {
	close(j.started)
	<-ctx.Done()
	j.order <- "scheduled-return"
}

func (j shutdownJobs) Close() { j.order <- "jobs-close" }

type stopFunc func(context.Context) error

func (stop stopFunc) Stop(ctx context.Context) error { return stop(ctx) }

func TestServeUntilStoppedDrainsInOrder(t *testing.T) {
	jobs := shutdownJobs{started: make(chan struct{}), order: make(chan string, 3)}
	ui := stopFunc(func(context.Context) error {
		jobs.order <- "ui-stop"
		return nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveUntilStopped(ctx, ui, jobs, cron.Every(24*time.Hour))
	}()
	select {
	case <-jobs.started:
	case <-time.After(5 * time.Second):
		t.Fatal("schedule did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	for _, want := range []string{"ui-stop", "scheduled-return", "jobs-close"} {
		select {
		case got := <-jobs.order:
			if got != want {
				t.Fatalf("shutdown step = %q, want %q", got, want)
			}
		default:
			t.Fatalf("missing %q", want)
		}
	}
}
