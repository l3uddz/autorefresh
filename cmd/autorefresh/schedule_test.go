package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
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
