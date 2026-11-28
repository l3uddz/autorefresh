package refresher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// attemptLog records each saved write attempt, each Plex write, and the
// attempt of each recorded request, in order. before runs before each save
// of an attempt, and after after it, if set.
type attemptLog struct {
	Store
	mu            sync.Mutex
	steps         []string
	before, after func(ctx context.Context, spec store.AttemptSpec)
	fail          error // the error of each save, if set
}

func (s *attemptLog) step(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, fmt.Sprintf(format, args...))
}

func (s *attemptLog) BeginAttempt(ctx context.Context, spec store.AttemptSpec) (int64, error) {
	if s.before != nil {
		s.before(ctx, spec)
	}
	if s.fail != nil {
		return 0, s.fail
	}
	id, err := s.Store.BeginAttempt(ctx, spec)
	if err == nil {
		s.step("attempt %s %d", spec.RatingKey, id)
	}
	if s.after != nil {
		s.after(ctx, spec)
	}
	return id, err
}

func (s *attemptLog) RecordRequest(r store.Request) error {
	s.step("record %d %s", r.Attempt.ID, r.Attempt.Outcome)
	return s.Store.RecordRequest(r)
}

func (s *attemptLog) RecordManual(m store.ManualRecord) error {
	s.step("record %d %s", m.Attempt.ID, m.Attempt.Outcome)
	return s.Store.RecordManual(m)
}

func (s *attemptLog) RecordGone(server, key string, at time.Time, source store.Source, action store.Action, runID int64, a store.AttemptResult) error {
	s.step("gone %d %s", a.ID, a.Outcome)
	return s.Store.RecordGone(server, key, at, source, action, runID, a)
}

// logAttempts wraps the store of f in an attemptLog that also records the
// writes of fp.
func logAttempts(f *fixture, fp *fakePlex) *attemptLog {
	l := &attemptLog{Store: f.store}
	fp.onCall = func(call string) {
		if len(sends([]string{call})) == 1 {
			l.step("%s", call)
		}
	}
	f.r.store = l
	return l
}

// otherProcess opens the database of f on a second connection, as another
// autorefresh process does, with the clock of the refresher.
func otherProcess(f *fixture) *store.Store {
	f.t.Helper()
	other, err := store.Open(f.file)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { other.Close() })
	other.SetClock(func() time.Time { return f.r.now() })
	return other
}

// queueState returns the state of the cached item key, with no backoff.
func (f *fixture) queueState(key string) store.QueueState {
	f.t.Helper()
	items, _, err := f.store.ListItems(context.Background(), store.ListQuery{
		Tab: store.TabAttention, WithIgnored: true, Limit: 100, Rules: store.DueRules{Now: f.r.now()},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, it := range items {
		if it.RatingKey == key {
			return it.State
		}
	}
	return ""
}

func TestEachWriteSavesAnAttemptFirst(t *testing.T) {
	cand := plex.Candidate{GUID: "plex://movie/m", Name: "Film", Year: 2020}
	tests := []struct {
		name string
		do   func(t *testing.T) (*fixture, *fakePlex, func() error)
		want []string
	}{
		{"run refresh", func(t *testing.T) (*fixture, *fakePlex, func() error) {
			fp := newFakePlex(moviesLib)
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
			return f, fp, func() error { f.now = t0; return f.r.Run(t.Context()) }
		}, []string{"attempt 101 1", "Refresh 101", "record 1 accepted"}},
		{"run match and its refresh", func(t *testing.T) (*fixture, *fakePlex, func() error) {
			f, fp, _ := runMatchFixture(t, checkAnswer{item: completeItem("101")})
			return f, fp, func() error { f.now = t0; return f.r.Run(t.Context()) }
		}, []string{"attempt 101 1", "Match 101 plex://movie/m", "record 1 accepted", "attempt 101 2", "Refresh 101", "record 2 accepted"}},
		{"Refresh now", func(t *testing.T) (*fixture, *fakePlex, func() error) {
			fp := newFakePlex(moviesLib)
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.now = t0
			f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
			return f, fp, func() error { _, err := f.r.RefreshItem(t.Context(), testServer, "101"); return err }
		}, []string{"attempt 101 1", "Refresh 101", "record 1 accepted"}},
		{"automatic fix match of Refresh now", func(t *testing.T) (*fixture, *fakePlex, func() error) {
			f, fp := refreshNowFixture(t)
			return f, fp, func() error { _, err := f.r.RefreshItem(t.Context(), testServer, "101"); return err }
		}, []string{"attempt 101 1", "Match 101 plex://movie/m", "record 1 accepted", "attempt 101 2", "Refresh 101", "record 2 accepted"}},
		{"Apply match", func(t *testing.T) (*fixture, *fakePlex, func() error) {
			f, fp, _ := matchCheckFixture(t, checkAnswer{item: completeItem("1")})
			return f, fp, func() error { return f.r.ApplyMatch(t.Context(), "home", "1", cand) }
		}, []string{"attempt 1 1", "Match 1 plex://movie/m", "record 1 accepted", "attempt 1 2", "Refresh 1", "record 2 accepted"}},
		{"bulk refresh", func(t *testing.T) (*fixture, *fakePlex, func() error) {
			fp := newFakePlex(moviesLib)
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.now = t0
			f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
			return f, fp, func() error {
				jobs := NewJobs(f.r)
				defer jobs.Close()
				_, err := jobs.BulkRefresh(testServer, []string{"101"})
				jobs.wg.Wait()
				return err
			}
		}, []string{"attempt 101 1", "Refresh 101", "record 1 accepted"}},
		{"refresh that finds the item gone", func(t *testing.T) (*fixture, *fakePlex, func() error) {
			fp := newFakePlex(moviesLib)
			fp.refreshErrs["101"] = plex.ErrNotFound
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
			return f, fp, func() error { f.now = t0; return f.r.Run(t.Context()) }
		}, []string{"attempt 101 1", "Refresh 101", "gone 1 rejected"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, fp, do := tt.do(t)
			l := logAttempts(f, fp)
			if err := do(); err != nil {
				t.Fatal(err)
			}
			wantCalls(t, l.steps, tt.want...)
		})
	}
}

func TestFailedAttemptSaveSendsNothing(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.now = t0
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	l := logAttempts(f, fp)
	l.fail = errDisk

	_, err := f.r.RefreshItem(t.Context(), testServer, "101")
	if !errors.Is(err, ErrNotSent) || !errors.Is(err, ErrStore) || !errors.Is(err, errDisk) {
		t.Errorf("err = %v, want a store error that wraps ErrNotSent", err)
	}
	err = f.r.Run(t.Context())
	if !errors.Is(err, ErrNotSent) || !errors.Is(err, errDisk) {
		t.Errorf("run: err = %v, want the store error", err)
	}
	if len(l.steps) != 0 {
		t.Errorf("steps = %q, want no send and no record", l.steps)
	}
	if row, _ := f.row(testServer, "1", "101"); row.Attempts != 0 {
		t.Errorf("attempts = %d, want none recorded", row.Attempts)
	}
}

// A save whose deadline passes after its commit sends nothing and drops its
// attempt, also in a run, whose counts stay unchanged.
func TestAttemptDeadlinePassedAfterCommit(t *testing.T) {
	for _, run := range []bool{false, true} {
		t.Run(fmt.Sprintf("run %v", run), func(t *testing.T) {
			fp := newFakePlex(moviesLib)
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp, Timeout: 20 * time.Millisecond})
			f.now = t0
			f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
			l := logAttempts(f, fp)
			l.after = func(ctx context.Context, _ store.AttemptSpec) { <-ctx.Done() }

			var err error
			if run {
				err = f.r.Run(t.Context())
			} else {
				_, err = f.r.RefreshItem(t.Context(), testServer, "101")
			}
			if !errors.Is(err, ErrNotSent) || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrStore) {
				t.Errorf("err = %v, want a store error with the deadline", err)
			}
			wantCalls(t, l.steps, "attempt 101 1")
			if got := f.queueState("101"); got != store.StateDue {
				t.Errorf("state = %s, want due after the attempt was dropped", got)
			}
			if run {
				if got := f.onlyRun(); got.Refreshed != 0 || got.Failed != 0 || got.Status != store.RunFailed {
					t.Errorf("run = %+v, want a failed run without counted requests", got)
				}
			}
		})
	}
}

func TestAttemptDropBlockedKeepsTheAttempt(t *testing.T) {
	prev := cleanupTimeout
	cleanupTimeout = 50 * time.Millisecond
	t.Cleanup(func() { cleanupTimeout = prev })
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp, Timeout: 20 * time.Millisecond})
	f.now = t0
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	l := logAttempts(f, fp)

	// The other process holds the write lock during the drop.
	db, err := sql.Open("sqlite", f.file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	l.after = func(ctx context.Context, _ store.AttemptSpec) {
		<-ctx.Done()
		if _, err := conn.ExecContext(t.Context(), `BEGIN IMMEDIATE`); err != nil {
			t.Error(err)
		}
	}

	if _, err := f.r.RefreshItem(t.Context(), testServer, "101"); !errors.Is(err, ErrNotSent) {
		t.Errorf("err = %v, want ErrNotSent", err)
	}
	if _, err := conn.ExecContext(t.Context(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, l.steps, "attempt 101 1")
	if n := len(f.logLines("warn", "Dropping write attempt failed")); n != 1 {
		t.Errorf("drop warnings = %d, want 1", n)
	}
	if got := f.queueState("101"); got != store.StateWaiting {
		t.Errorf("state = %s, want waiting while the attempt stays", got)
	}
}

// A write that another process saves after the recheck of a run, or a row
// that it moves into a library that the run did not read, holds the item.
func TestRunSkipsAnItemThatAnotherProcessHolds(t *testing.T) {
	tests := map[string]func(t *testing.T, other *store.Store){
		"attempt": func(t *testing.T, other *store.Store) {
			if _, err := other.BeginAttempt(t.Context(), store.AttemptSpec{Server: testServer, RatingKey: "101", Action: store.ActionRefresh}); err != nil {
				t.Fatal(err)
			}
		},
		"moved row": func(t *testing.T, other *store.Store) {
			moved := storeItem(testServer, plex.Library{ID: "9", Type: "movie"}, incompleteItem("101"), store.NoSummary)
			if _, err := other.ApplyRead(store.Read{Server: testServer, Section: "9", At: t0, Items: []store.Item{moved}}); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			fp := newFakePlex(moviesLib)
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.now = t0
			f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
			other := otherProcess(f)
			l := logAttempts(f, fp)
			l.before = func(context.Context, store.AttemptSpec) { change(t, other) }

			if err := f.r.Run(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(l.steps) != 0 {
				t.Errorf("steps = %q, want no send", l.steps)
			}
			skipped := f.logLines("debug", "Skipped item")
			if len(skipped) != 1 {
				t.Fatalf("skip lines = %d, want 1", len(skipped))
			}
			wantField(t, skipped[0], "reason", "held")
			wantField(t, f.lastSummary(testServer), "refresh_requests", float64(0))
		})
	}
}

// After an accepted match of a run, a hold or an attempt of another process
// before the follow-up refresh skips the refresh and its checks, and the
// match stays recorded and counted.
func TestRunSkipsAHeldFollowUpRefresh(t *testing.T) {
	tests := map[string]func(t *testing.T, other *store.Store){
		"attempt": func(t *testing.T, other *store.Store) {
			if _, err := other.BeginAttempt(t.Context(), store.AttemptSpec{Server: testServer, RatingKey: "101", Action: store.ActionRefresh}); err != nil {
				t.Fatal(err)
			}
		},
		"hold": func(t *testing.T, other *store.Store) {
			if err := other.AddHold(t.Context(), testServer, "101"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			f, fp, checks := runMatchFixture(t, checkAnswer{item: completeItem("101")})
			other := otherProcess(f)
			l := logAttempts(f, fp)
			l.before = func(_ context.Context, spec store.AttemptSpec) {
				if spec.FollowsUp != 0 {
					change(t, other)
				}
			}

			f.run(t0)

			wantCalls(t, l.steps, "attempt 101 1", "Match 101 plex://movie/m", "record 1 accepted")
			if *checks != 0 {
				t.Errorf("checks = %d, want none", *checks)
			}
			skipped := f.logLines("info", "Skipped follow-up refresh")
			if len(skipped) != 1 {
				t.Fatalf("skip lines = %d, want 1", len(skipped))
			}
			wantField(t, skipped[0], "reason", "held")
			wantField(t, f.lastSummary(testServer), "match_requests", float64(1))
			if f.matchEvent("101").ID == 0 {
				t.Error("the match is not recorded")
			}
			// The refusal saved a hold, which stays after the other attempt.
			f.now = t0.Add(time.Hour)
			if got := f.queueState("101"); got != store.StateWaiting {
				t.Errorf("state = %s, want waiting for the hold", got)
			}
		})
	}
}

func TestDryRunSavesNoAttempt(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	l := logAttempts(f, fp)

	f.run(t0)

	if len(l.steps) != 0 || len(f.logLines("info", "Dry run")) != 1 {
		t.Errorf("steps = %q, want none and a dry run line", l.steps)
	}
}
