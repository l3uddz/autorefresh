package refresher

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestJobsBulkRefreshesIgnoredItem(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.r.opts.IgnoreReasons = store.NoSummary
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	jobs := NewJobs(f.r)
	t.Cleanup(jobs.Close)
	id, err := jobs.BulkRefresh("home", []string{"1"})
	if err != nil {
		t.Fatal(err)
	}
	jobs.wg.Wait()
	items, err := f.store.RunItems(t.Context(), id)
	if err != nil || len(items) != 1 || items[0].Outcome != store.ItemAccepted {
		t.Fatalf("results = %+v, %v", items, err)
	}
}

func TestJobsScheduledSkipsOverlappingStart(t *testing.T) {
	buf := captureJobsLog(t)

	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	r := &Jobs{run: func(context.Context) error {
		calls.Add(1)
		close(started)
		<-release
		return nil
	}}

	first := make(chan struct{})
	go func() {
		r.Scheduled(context.Background())
		close(first)
	}()
	<-started

	// The first run blocks, so this start must return at once.
	r.Scheduled(context.Background())
	close(release)
	<-first

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call of run, got %d", got)
	}
	if got := buf.String(); !strings.Contains(got, `"level":"warn"`) {
		t.Fatalf("expected a warning for the skipped start, got %q", got)
	}
}

func TestJobsScheduledLogsRunError(t *testing.T) {
	buf := captureJobsLog(t)

	r := &Jobs{run: func(context.Context) error {
		return errors.New("database is locked")
	}}
	r.Scheduled(context.Background())

	got := buf.String()
	if !strings.Contains(got, `"level":"error"`) || !strings.Contains(got, `"error":"database is locked"`) {
		t.Fatalf("expected an error line with the run error, got %q", got)
	}
}

func TestJobsScheduledUnlocksAfterPanic(t *testing.T) {
	captureJobsLog(t)

	var calls atomic.Int32
	r := &Jobs{run: func(context.Context) error {
		if calls.Add(1) == 1 {
			panic("run failed")
		}
		return nil
	}}

	func() {
		defer func() { _ = recover() }()
		r.Scheduled(context.Background())
	}()
	r.Scheduled(context.Background())

	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls of run after a panic, got %d", got)
	}
}

func captureJobsLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })
	return &buf
}
