package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func TestRunnerSkipsOverlappingStart(t *testing.T) {
	buf := captureLog(t)

	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	r := &runner{run: func(context.Context) error {
		calls.Add(1)
		close(started)
		<-release
		return nil
	}}

	first := make(chan struct{})
	go func() {
		r.start(context.Background())
		close(first)
	}()
	<-started

	// The first run blocks, so this start must return at once.
	r.start(context.Background())
	close(release)
	<-first

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call of run, got %d", got)
	}
	if got := buf.String(); !strings.Contains(got, `"level":"warn"`) {
		t.Fatalf("expected a warning for the skipped start, got %q", got)
	}
}

func TestRunnerLogsRunError(t *testing.T) {
	buf := captureLog(t)

	r := &runner{run: func(context.Context) error {
		return errors.New("database is locked")
	}}
	r.start(context.Background())

	got := buf.String()
	if !strings.Contains(got, `"level":"error"`) || !strings.Contains(got, `"error":"database is locked"`) {
		t.Fatalf("expected an error line with the run error, got %q", got)
	}
}

func TestRunnerUnlocksAfterPanic(t *testing.T) {
	captureLog(t)

	var calls atomic.Int32
	r := &runner{run: func(context.Context) error {
		if calls.Add(1) == 1 {
			panic("run failed")
		}
		return nil
	}}

	func() {
		defer func() { _ = recover() }()
		r.start(context.Background())
	}()
	r.start(context.Background())

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls of run after a panic, got %d", got)
	}
}

// serve starts the first run at once, without waiting for the schedule.
func TestServeStartsFirstRunAtOnce(t *testing.T) {
	captureLog(t)

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	r := &runner{run: func(context.Context) error {
		calls.Add(1)
		cancel()
		return nil
	}}

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
	r := &runner{run: func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		finished.Store(true)
		return nil
	}}

	serve(ctx, r, cron.Every(24*time.Hour))
	if !finished.Load() {
		t.Fatal("serve returned before the first run returned")
	}
}
