package refresher

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// runs returns the stored runs, newest first.
func (f *fixture) runs() []store.Run {
	f.t.Helper()
	runs, err := f.store.Runs(0)
	if err != nil {
		f.t.Fatal(err)
	}
	return runs
}

// onlyRun returns the only stored run.
func (f *fixture) onlyRun() store.Run {
	f.t.Helper()
	runs := f.runs()
	if len(runs) != 1 {
		f.t.Fatalf("runs = %+v, want 1", runs)
	}
	return runs[0]
}

func TestRunWritesFinishedRunRow(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	got := f.onlyRun()
	want := store.Run{
		ID: got.ID, Server: testServer, Kind: store.RunKindRun, Source: store.RunSourceSchedule,
		Status: store.RunFinished, StartedAt: t0, FinishedAt: t0,
		ReadItems: 1, Refreshed: 1, Incomplete: 1, Due: 1, Selected: 1,
	}
	if got != want {
		t.Errorf("run = %+v, want %+v", got, want)
	}
}

func TestCompletionWithoutRequestCountsCompletedOnly(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1&updatedAt>>="+unixString(seedAt.Add(-oversample))] = []plex.Item{completeItem("101")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)

	f.run(t0)

	if got := f.onlyRun(); got.Completed != 1 || got.Fixed != 0 || got.Refreshed != 0 {
		t.Errorf("run = %+v, want Completed 1, Fixed 0, Refreshed 0", got)
	}
}

func TestUnavailableServerWritesFailedRunRow(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.librariesErr = errUnavailable
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.runUnavailable(t0)

	got := f.onlyRun()
	if got.Status != store.RunFailed || !strings.Contains(got.Error, "unavailable") || !got.FinishedAt.Equal(t0) {
		t.Errorf("run = %+v, want a failed run that finished at %v with an unavailable error", got, t0)
	}
}

func TestCanceledRunWritesCanceledRow(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fp.onCall = func(call string) {
		if call == "Refresh 101" {
			cancel()
		}
	}

	f.now = t0
	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	got := f.onlyRun()
	if got.Status != store.RunCanceled || got.Error != "" || got.Refreshed != 1 || got.Due != 2 || got.Selected != 2 {
		t.Errorf("run = %+v, want a canceled run with 1 refresh, 2 due, and 2 selected", got)
	}
}

// startedRun stores a finished run that started at.
func (f *fixture) startedRun(at time.Time) {
	f.t.Helper()
	start := store.RunStart{Server: testServer, Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: at}
	id, err := f.store.StartRun(start)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.FinishRun(id, start, store.RunResult{FinishedAt: at, Status: store.RunFinished}); err != nil {
		f.t.Fatal(err)
	}
}

func TestRunPrunesOldHistory(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.HistoryRetention = 24 * time.Hour
	f.startedRun(t0.Add(-48 * time.Hour))
	f.startedRun(t0.Add(-time.Hour))
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 1) // an event at seedAttempt

	f.run(t0)

	var started []time.Time
	for _, run := range f.runs() {
		started = append(started, run.StartedAt)
	}
	if want := []time.Time{t0, t0.Add(-time.Hour)}; len(started) != 2 || !started[0].Equal(want[0]) || !started[1].Equal(want[1]) {
		t.Errorf("run start times = %v, want %v", started, want)
	}
	lines := f.logLines("debug", "Pruned history")
	if len(lines) != 1 {
		t.Fatalf("got %d pruned history lines, want 1", len(lines))
	}
	wantField(t, lines[0], "runs", float64(1))
	wantField(t, lines[0], "events", float64(1))
}

func TestDryRunWritesNoRunRowAndKeepsHistory(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newFixture(t, Server{Name: testServer, Plex: fp}) // a dry run
	f.r.opts.HistoryRetention = 24 * time.Hour
	f.startedRun(t0.Add(-48 * time.Hour))

	f.run(t0)

	if got := f.onlyRun(); !got.StartedAt.Equal(t0.Add(-48 * time.Hour)) {
		t.Errorf("run = %+v, want only the old run", got)
	}
	if lines := f.logLines("debug", "Pruned history"); len(lines) != 0 {
		t.Errorf("got pruned history lines %v, want none", lines)
	}
}

// runStore fails StartRun or FinishRun with errRun, and counts the calls.
// afterStart, if set, runs after a successful StartRun.
type runStore struct {
	*store.Store
	failStart, failFinish bool
	finishes              int
	afterStart            func()
}

var errRun = errors.New("run row failed")

func (s *runStore) StartRun(r store.RunStart) (int64, error) {
	if s.failStart {
		return 0, errRun
	}
	id, err := s.Store.StartRun(r)
	if s.afterStart != nil {
		s.afterStart()
	}
	return id, err
}

func (s *runStore) FinishRun(id int64, start store.RunStart, r store.RunResult) error {
	s.finishes++
	if s.failFinish {
		return errRun
	}
	return s.Store.FinishRun(id, start, r)
}

func TestRunStartRunErrorNamesServer(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
	st := &runStore{Store: f.store, failStart: true}
	f.r.store = st

	err := f.r.Run(context.Background())

	if !errors.Is(err, errRun) || !strings.Contains(err.Error(), "server "+testServer+": ") {
		t.Errorf("error = %v, want server %s: %v", err, testServer, errRun)
	}
	if st.finishes != 0 {
		t.Errorf("FinishRun calls = %d, want 0", st.finishes)
	}
}

func TestRunFinishRunError(t *testing.T) {
	t.Run("returned when the run has no error", func(t *testing.T) {
		f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
		f.r.store = &runStore{Store: f.store, failFinish: true}

		err := f.r.Run(context.Background())

		if !errors.Is(err, errRun) || !strings.Contains(err.Error(), "server "+testServer+": ") {
			t.Errorf("error = %v, want server %s: %v", err, testServer, errRun)
		}
	})
	t.Run("loses to the error of the run", func(t *testing.T) {
		fp := newFakePlex(moviesLib)
		fp.librariesErr = errServer
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		f.r.store = &runStore{Store: f.store, failFinish: true}

		err := f.r.Run(context.Background())

		if err == nil || errors.Is(err, errRun) {
			t.Errorf("error = %v, want the error of the run, not %v", err, errRun)
		}
		lines := f.logLines("error", "Recording run failed")
		if len(lines) != 1 {
			t.Fatalf("got %d recording failure lines, want 1", len(lines))
		}
		wantField(t, lines[0], "server", testServer)
	})
	t.Run("canceled run returns it", func(t *testing.T) {
		f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
		st := &runStore{Store: f.store, failFinish: true}
		f.r.store = st
		ctx, cancel := context.WithCancel(context.Background())
		st.afterStart = cancel

		err := f.r.Run(ctx)

		if !errors.Is(err, errRun) || st.finishes != 1 {
			t.Errorf("error = %v, FinishRun calls = %d, want %v and 1", err, st.finishes, errRun)
		}
	})
}
