package refresher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

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

type blockingRefresh struct {
	*fakePlex
	entered chan struct{}
}

func (p blockingRefresh) Refresh(ctx context.Context, _ string) error {
	close(p.entered)
	<-ctx.Done()
	return fmt.Errorf("%w: %w", plex.ErrUnavailable, ctx.Err())
}

func TestCancelBulkRecordsInFlightAttemptAndRemainingKey(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	p := blockingRefresh{newFakePlex(lib), make(chan struct{})}
	f := newRefreshFixture(t, Server{Name: "home", Plex: p})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	f.seed("home", lib, incompleteItem("2"), store.NoSummary, 0)
	progress := withProgress(f)
	jobs := NewJobs(f.r)
	t.Cleanup(jobs.Close)
	id, err := jobs.BulkRefresh("home", []string{"1", "2"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("bulk refresh did not reach Plex")
	}
	if _, err := jobs.RunNow(nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlapping run error = %v", err)
	}
	if err := jobs.Cancel(id); err != nil {
		t.Fatal(err)
	}
	jobs.wg.Wait()
	if len(progress.errs) != 1 || progress.errs[0] != nil || len(progress.counts) != 1 {
		t.Fatalf("progress errors = %v, item completions = %v", progress.errs, progress.counts)
	}
	if got := f.onlyRun(); got.Status != store.RunCanceled || got.Refreshed != 1 || got.Failed != 1 {
		t.Errorf("canceled job = %+v", got)
	}
	results, err := f.store.RunItems(t.Context(), id)
	if err != nil || len(results) != 2 || results[0].Outcome != store.ItemFailed || results[1].Outcome != store.ItemNotReached {
		t.Fatalf("results = %+v, %v", results, err)
	}
	if row, ok := f.row("home", lib.ID, "1"); !ok || row.Attempts != 1 {
		t.Errorf("row = %+v, found = %v", row, ok)
	}
}

func TestRunNowStartFailureReleasesAdmission(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex()})
	f.now = t0
	st := &runStore{Store: f.store, failStart: true}
	f.r.store = st
	jobs := NewJobs(f.r)
	t.Cleanup(jobs.Close)
	if _, err := jobs.RunNow(nil); !errors.Is(err, errRun) {
		t.Fatalf("first admission error = %v", err)
	}
	st.failStart = false
	ids, err := jobs.RunNow(nil)
	if err != nil || len(ids) != 1 {
		t.Fatalf("second admission = %v, %v", ids, err)
	}
	jobs.wg.Wait()
	if got := f.onlyRun(); got.ID != ids[0] || got.Source != store.RunSourceManual || got.Status != store.RunFinished {
		t.Fatalf("manual run = %+v", got)
	}
}

func TestRunNowSubsetKeepsOtherConfiguredServer(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t,
		Server{Name: "home", Plex: newFakePlex(lib)},
		Server{Name: "away", Plex: newFakePlex(lib)},
	)
	f.now = t0
	f.seed("away", lib, incompleteItem("1"), store.NoSummary, 0)
	if _, err := f.store.SetStopped(t.Context(), "away", "1", t0, true); err != nil {
		t.Fatal(err)
	}
	awayRun, err := f.store.StartRun(store.RunStart{Server: "away", Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	jobs := NewJobs(f.r)
	t.Cleanup(jobs.Close)
	if ids, err := jobs.RunNow([]string{"home"}); err != nil || len(ids) != 1 {
		t.Fatalf("start = %v, %v", ids, err)
	}
	jobs.wg.Wait()
	row, found := f.row("away", "1", "1")
	if !found || row.StoppedAt.IsZero() {
		t.Fatalf("away row = %+v, found %v", row, found)
	}
	events, err := f.store.Timeline("away", "1")
	if err != nil || len(events) != 1 || events[0].Kind != store.EventStopped {
		t.Fatalf("away events = %+v, %v", events, err)
	}
	runs, err := f.store.Runs(t.Context(), store.RunQuery{Servers: []string{"away"}})
	if err != nil || len(runs) != 1 || runs[0].ID != awayRun {
		t.Fatalf("away runs = %+v, %v", runs, err)
	}
}

type failedBulkRecheck struct{ Store }

func (failedBulkRecheck) KnownItem(string, string) (store.Item, bool, bool, error) {
	return store.Item{}, false, false, errRun
}

func TestBulkRecheckFailureDoesNotReportItemDone(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex()})
	p := withProgress(f)
	f.r.store = failedBulkRecheck{Store: f.store}
	run := &serverRun{server: f.r.servers[0]}
	item := store.RunItem{RatingKey: "1", Outcome: store.ItemNotReached}
	results := []store.RunItem{item}
	err := f.r.bulkBatch(t.Context(), run, []bulkAdmission{{item: item, cached: true}}, results)
	if !errors.Is(err, ErrStore) || results[0].Outcome != store.ItemNotReached {
		t.Fatalf("results = %+v, error = %v", results, err)
	}
	if len(p.counts) != 0 {
		t.Fatalf("reported %d item completions", len(p.counts))
	}
}
