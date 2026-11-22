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
