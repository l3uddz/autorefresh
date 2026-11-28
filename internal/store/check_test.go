package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// cacheItem caches item 1 on a, with the clock of a at base.
func cacheItem(t *testing.T, a *Store) {
	t.Helper()
	fixedClock(a, base)
	apply(t, a, Read{At: base.Add(-time.Hour), Items: []Item{item("1", NoSummary)}})
}

// checkSetup caches item 1 on a and saves the attempt of the action that its
// check follows up on a, at base. It returns the chain.
func checkSetup(t *testing.T, a *Store) []int64 {
	t.Helper()
	cacheItem(t, a)
	return []int64{attempt(t, a, AttemptSpec{RatingKey: "1", Action: ActionMatch})}
}

func exec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// observe returns the observation of a check of item 1 on s at at.
func observe(t *testing.T, s *Store, chain []int64, at time.Time) Observation {
	t.Helper()
	obs, err := s.Observe(srv, chain, at)
	if err != nil {
		t.Fatal(err)
	}
	return obs
}

// saveChecks runs both saves of a check of item 1 with obs: a metadata
// result that completes the item, and a 404. It returns their errors.
func saveChecks(t *testing.T, s *Store, obs Observation) (check, gone error) {
	t.Helper()
	_, check = s.ApplyCheck(srv, item("1", 0), base, 0, obs)
	if check == nil {
		return nil, errors.New("the check saved, so the 404 was not tried")
	}
	return check, goneErr(s.ApplyCheckGone(srv, "1", base, SourceAuto, 0, obs, ""))
}

func TestCheckYields(t *testing.T) {
	// older runs on the other connection before the chain is saved, and
	// change after the observation.
	tests := map[string]struct {
		older  func(t *testing.T, b *Store) int64
		change func(t *testing.T, a, b *Store, chain []int64, older int64)
	}{
		"newer attempt": {change: func(t *testing.T, a, b *Store, chain []int64, older int64) {
			fixedClock(b, base.Add(time.Second))
			attempt(t, b, AttemptSpec{RatingKey: "1"})
		}},
		"older match": {older: func(t *testing.T, b *Store) int64 {
			// Answered before the chain began.
			id := attemptEnding(t, b, "1", base.Add(time.Minute))
			exec(t, b, `UPDATE write_attempt SET action = 'match', outcome = 'accepted', answered_seq = ? WHERE id = ?`, id, id)
			return id
		}},
		"older refresh answered after the chain began": {
			older: func(t *testing.T, b *Store) int64 { return attemptEnding(t, b, "1", base.Add(time.Minute)) },
			change: func(t *testing.T, a, b *Store, chain []int64, older int64) {
				exec(t, b, `UPDATE write_attempt SET outcome = 'accepted', answered_seq = ? WHERE id = ?`, chain[0], older)
			},
		},
		"older accepted refresh without its answer order": {older: func(t *testing.T, b *Store) int64 {
			id := attemptEnding(t, b, "1", base.Add(time.Minute))
			exec(t, b, `UPDATE write_attempt SET outcome = 'accepted' WHERE id = ?`, id)
			return id
		}},
		"older refresh without an answer": {
			older: func(t *testing.T, b *Store) int64 { return attemptEnding(t, b, "1", base.Add(time.Minute)) },
		},
		"attempt that ended during the check": {change: func(t *testing.T, a, b *Store, chain []int64, older int64) {
			// Observed at base; the attempt ends at base + 1 s, before the save
			// at base + 2 s.
			attemptEnding(t, b, "1", base.Add(time.Second))
			fixedClock(a, base.Add(2*time.Second))
		}},
		"generation change": {change: func(t *testing.T, a, b *Store, chain []int64, older int64) {
			exec(t, b, `UPDATE prune_fence SET generation = generation + 1 WHERE server = ''`)
		}},
		"chain ended": {change: func(t *testing.T, a, b *Store, chain []int64, older int64) {
			fixedClock(a, base.Add(time.Hour))
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			a, b := openPair(t)
			cacheItem(t, a)
			var older int64
			if tt.older != nil {
				older = tt.older(t, b)
			}
			chain := []int64{attempt(t, a, AttemptSpec{RatingKey: "1", Action: ActionMatch})}
			if older != 0 && older >= chain[0] {
				t.Fatalf("older attempt %d is not older than the chain %v", older, chain)
			}
			obs := observe(t, a, chain, base)
			if tt.change != nil {
				tt.change(t, a, b, chain, older)
			}

			check, gone := saveChecks(t, a, obs)
			if !errors.Is(check, ErrCheckYielded) || !errors.Is(gone, ErrCheckYielded) {
				t.Fatalf("errors = %v and %v, want ErrCheckYielded", check, gone)
			}
			if got := rows(t, a)["1"].Reasons; got != NoSummary {
				t.Errorf("reasons = %v, want the row unchanged", got)
			}
			if h := holds(t, a); !slices.Equal(h, []string{sec + "/1"}) {
				t.Errorf("holds = %v, want a hold for 1", h)
			}
			if n := countRows(t, a, "event"); n != 0 {
				t.Errorf("events = %d, want none", n)
			}
		})
	}
}

func TestCheckSaves(t *testing.T) {
	match := func(t *testing.T, a *Store) int64 {
		return attempt(t, a, AttemptSpec{RatingKey: "1", Action: ActionMatch})
	}
	// Each case saves what comes before the check and returns its chain.
	tests := map[string]func(t *testing.T, a, b *Store) []int64{
		"older refresh accepted before the chain": func(t *testing.T, a, b *Store) []int64 {
			older := attemptEnding(t, b, "1", base.Add(time.Minute))
			if err := b.RecordRequest(Request{Server: srv, RatingKey: "1", At: base, Source: SourceManual, Action: ActionRefresh,
				Accepted: true, Attempt: AttemptResult{ID: older, Outcome: AttemptAccepted}}); err != nil {
				t.Fatal(err)
			}
			return []int64{match(t, a)}
		},
		"attempt in the chain": func(t *testing.T, a, b *Store) []int64 {
			m := match(t, a)
			return []int64{m, attempt(t, a, AttemptSpec{RatingKey: "1", FollowsUp: m})}
		},
		"hold": func(t *testing.T, a, b *Store) []int64 {
			hold(t, b, sec, "1")
			return []int64{match(t, a)}
		},
	}
	for name, before := range tests {
		t.Run(name, func(t *testing.T) {
			a, b := openPair(t)
			fixedClock(a, base)
			apply(t, a, Read{At: base.Add(-time.Hour), Items: []Item{item("1", NoSummary)}})
			obs := observe(t, a, before(t, a, b), base)
			held := holds(t, a)
			if fixed, err := a.ApplyCheck(srv, item("1", 0), base, 0, obs); err != nil || !fixed {
				t.Fatalf("fixed = %v, err = %v, want the check saved", fixed, err)
			}
			if h := holds(t, a); !slices.Equal(h, held) {
				t.Errorf("holds = %v, want them unchanged from %v", h, held)
			}
		})
	}
}

func TestCheckGoneSaves(t *testing.T) {
	a, _ := openPair(t)
	chain := checkSetup(t, a)
	if _, _, err := a.ApplyCheckGone(srv, "1", base, SourceAuto, 0, observe(t, a, chain, base), ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := rows(t, a)["1"]; ok {
		t.Error("the row was kept")
	}
	if n := countRows(t, a, "event"); n != 1 {
		t.Errorf("events = %d, want the gone event", n)
	}
}

func TestCheckAfterTheLibraryIsGone(t *testing.T) {
	a, b := openPair(t)
	chain := checkSetup(t, a)
	obs := observe(t, a, chain, base)
	if _, err := b.PruneSections(srv, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyCheck(srv, item("1", 0), base, 0, obs); !errors.Is(err, ErrNoItem) {
		t.Errorf("check: err = %v, want ErrNoItem", err)
	}
	if _, _, err := a.ApplyCheckGone(srv, "1", base, SourceAuto, 0, obs, ""); !errors.Is(err, ErrNoItem) {
		t.Errorf("check 404: err = %v, want ErrNoItem", err)
	}
	if n := countRows(t, a, "read_hold") + countRows(t, a, "event"); n != 0 {
		t.Errorf("holds and events = %d, want none", n)
	}
}

func TestAddHold(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("1", NoSummary)}})
	for _, key := range []string{"1", "1", "none"} {
		if err := s.AddHold(context.Background(), srv, key); err != nil {
			t.Fatal(err)
		}
	}
	if h := holds(t, s); !slices.Equal(h, []string{sec + "/1"}) {
		t.Errorf("holds = %v, want one for the cached row only", h)
	}
	if _, err := s.PruneSections(srv, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHold(context.Background(), srv, "1"); err != nil || countRows(t, s, "read_hold") != 0 {
		t.Errorf("err = %v, holds = %d, want none without a library", err, countRows(t, s, "read_hold"))
	}
}
