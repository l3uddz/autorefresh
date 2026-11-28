package store

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// startRead returns the read state of the test library on s.
func startRead(t *testing.T, s *Store, section string) ReadState {
	t.Helper()
	st, err := s.ReadStart(srv, section)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// holds returns the holds of srv as "<section>/<key>", sorted.
func holds(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT section || '/' || rating_key FROM read_hold WHERE server = ? ORDER BY 1`, srv)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		got = append(got, h)
	}
	return got
}

func hold(t *testing.T, s *Store, section, key string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO read_hold (server, section, rating_key) VALUES (?, ?, ?)`, srv, section, key); err != nil {
		t.Fatal(err)
	}
}

// attemptEnding saves an attempt of key on s that ends at end.
func attemptEnding(t *testing.T, s *Store, key string, end time.Time) int64 {
	t.Helper()
	fixedClock(s, end.Add(-time.Duration(attemptAllowance)*time.Second))
	return attempt(t, s, AttemptSpec{RatingKey: key})
}

func TestReadSkipsAndHoldsAnItemWithALiveAttempt(t *testing.T) {
	// The case of #161: Refresh now on the other connection saves its attempt
	// while the read lists the item, and the listing shows it complete.
	a, b := openPair(t)
	apply(t, a, Read{At: base.Add(-time.Hour), Items: []Item{item("1", NoSummary)}})
	st := startRead(t, a, sec)
	fixedClock(b, base)
	attempt(t, b, AttemptSpec{RatingKey: "1"})

	fixed := apply(t, a, Read{At: base, Mark: st.Mark, Items: []Item{item("1", 0), item("2", NoArt)}})
	if len(fixed) != 0 {
		t.Errorf("fixed = %v, want none", keys(fixed))
	}
	got := rows(t, a)
	if got["1"].Reasons != NoSummary || got["2"].Reasons != NoArt {
		t.Errorf("rows = %v, want 1 kept with no-summary and 2 added", got)
	}
	if h := holds(t, a); !slices.Equal(h, []string{sec + "/1"}) {
		t.Errorf("holds = %v, want 1", h)
	}
	if n := countRows(t, a, "event"); n != 0 {
		t.Errorf("events = %d, want none", n)
	}
	if lib, err := readTimes(a, srv, sec); err != nil || !lib.LastRead.Equal(base) {
		t.Errorf("last read = %v, err %v, want the read time to advance", lib.LastRead, err)
	}
}

func TestReadAttemptEnds(t *testing.T) {
	tests := []struct {
		name string
		end  time.Time
		skip bool
	}{
		{"ended during the listing", base.Add(5 * time.Second), true},
		{"ended at the start", base, true},
		{"ended before the start", base.Add(-time.Second), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := openPair(t)
			apply(t, a, Read{At: base.Add(-time.Hour), Items: []Item{item("1", NoSummary)}})
			st := startRead(t, a, sec)
			attemptEnding(t, b, "1", tt.end)

			apply(t, a, Read{At: base, Mark: st.Mark, Items: []Item{item("1", NoArt)}})
			if got := rows(t, a)["1"].Reasons; (got == NoSummary) != tt.skip {
				t.Errorf("reasons = %v, want skipped %v", got, tt.skip)
			}
			if h := holds(t, a); (len(h) == 1) != tt.skip {
				t.Errorf("holds = %v, want a hold %v", h, tt.skip)
			}
		})
	}
}

func TestReadRejectedAttempt(t *testing.T) {
	for _, live := range []bool{false, true} {
		a, b := openPair(t)
		apply(t, a, Read{At: base.Add(-time.Hour), Items: []Item{item("1", NoSummary)}})
		st := startRead(t, a, sec)
		fixedClock(b, base)
		rejected := attempt(t, b, AttemptSpec{RatingKey: "1"})
		if live {
			attempt(t, b, AttemptSpec{RatingKey: "1"})
		}
		if err := b.RecordRequest(Request{Server: srv, RatingKey: "1", At: base, Source: SourceManual, Action: ActionRefresh,
			Error: "401", Attempt: AttemptResult{ID: rejected, Outcome: AttemptRejected}}); err != nil {
			t.Fatal(err)
		}

		apply(t, a, Read{At: base, Mark: st.Mark, Items: []Item{item("1", NoArt)}})
		if got := rows(t, a)["1"].Reasons; (got == NoSummary) != live {
			t.Errorf("another live attempt %v: reasons = %v", live, got)
		}
	}
}

func TestReadHoldsWhatItWouldChange(t *testing.T) {
	a, b := openPair(t)
	series := item("s", EpisodeNoSummary)
	movie := item("m", NoSummary)
	apply(t, a, Read{At: base.Add(-time.Hour), Shows: true, Items: []Item{item("g", NoArt), item("f", NoArt), movie}, EpisodeNoSummary: []Item{series}})
	st := startRead(t, a, sec)
	fixedClock(b, base)
	for _, key := range []string{"g", "f", "s", "m", "new"} {
		attempt(t, b, AttemptSpec{RatingKey: key})
	}

	// An incremental read with a staged 404 is not possible; the 404 comes
	// from a full read below. This read stages a folder, lists a complete
	// item without a row, and lists neither the series nor the movie.
	apply(t, a, Read{At: base, Mark: st.Mark, Shows: true, Items: []Item{item("new", 0)}, Folders: map[string]string{"f": "/new"}})
	got := rows(t, a)
	if got["s"].Reasons != EpisodeNoSummary || got["f"].Path != "" {
		t.Errorf("rows = %v, want the series reasons and the old folder kept", got)
	}
	if h := holds(t, a); !slices.Equal(h, []string{sec + "/f", sec + "/new", sec + "/s"}) {
		t.Errorf("holds = %v, want the folder, the new item, and the series, and not the unchanged movie", h)
	}

	st = startRead(t, a, sec)
	apply(t, a, Read{At: base, Mark: st.Mark, Full: true, Shows: true, Gone: []string{"g"}, Items: []Item{movie}})
	if _, ok := rows(t, a)["g"]; !ok {
		t.Error("the read deleted the row of a protected 404")
	}
	if h := holds(t, a); !slices.Contains(h, sec+"/g") {
		t.Errorf("holds = %v, want a hold for the 404", h)
	}
}

func TestReadHoldsAnAcceptedRequestAfterTheMark(t *testing.T) {
	a, b := openPair(t)
	apply(t, a, Read{At: base.Add(-time.Hour), Items: []Item{item("1", NoSummary), item("2", NoSummary)}})
	st := startRead(t, a, sec)
	// Plex accepted a request of the other connection whose attempt ended
	// before the read started, and item 2 was completed by a check.
	id := attemptEnding(t, b, "1", base.Add(-time.Minute))
	if err := b.RecordRequest(Request{Server: srv, RatingKey: "1", At: base, Source: SourceManual, Action: ActionRefresh,
		Accepted: true, Attempt: AttemptResult{ID: id, Outcome: AttemptAccepted}}); err != nil {
		t.Fatal(err)
	}
	apply(t, b, Read{At: base.Add(-time.Minute), Items: []Item{item("2", 0)}})

	apply(t, a, Read{At: base, Mark: st.Mark, Items: []Item{item("1", 0), item("2", NoArt)}})
	got := rows(t, a)
	if got["1"].Reasons != NoSummary {
		t.Errorf("item 1 = %v, want it kept", got["1"].Reasons)
	}
	if _, ok := got["2"]; ok {
		t.Error("the read added the item that a completion after the mark settled")
	}
	if h := holds(t, a); !slices.Equal(h, []string{sec + "/1"}) {
		t.Errorf("holds = %v, want only the accepted request", h)
	}
}

func TestFullReadReconcilesHolds(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{At: base.Add(-time.Hour), Items: []Item{
		item("listed", NoSummary), item("gone", NoSummary), item("failed", NoSummary), item("present", NoSummary), item("live", NoSummary),
	}})
	apply(t, s, Read{Section: "2", At: base.Add(-time.Hour)})
	for _, key := range []string{"listed", "gone", "failed", "present", "live", "norow"} {
		hold(t, s, sec, key)
	}
	// The hold of an old library after the item moved to library 1.
	hold(t, s, "2", "listed")
	fixedClock(s, base)
	attempt(t, s, AttemptSpec{RatingKey: "live"})

	// An incremental read clears nothing.
	apply(t, s, Read{At: base, Items: []Item{item("listed", NoArt)}})
	if n := len(holds(t, s)); n != 7 {
		t.Fatalf("holds after an incremental read = %d, want 7", n)
	}

	res, err := s.ApplyRead(Read{Server: srv, Section: sec, At: base, Full: true,
		Items: []Item{item("listed", NoArt), item("live", 0)}, Gone: []string{"gone"}, Present: []string{"present"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{sec + "/failed", sec + "/live", sec + "/present", "2/listed"}; !slices.Equal(holds(t, s), want) {
		t.Errorf("holds = %v, want %v", holds(t, s), want)
	}
	if !slices.Equal(res.Omitted, []string{"present"}) {
		t.Errorf("omitted = %v, want present", res.Omitted)
	}

	res, err = s.ApplyRead(Read{Server: srv, Section: "2", At: base, Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(holds(t, s), "2/listed") || len(res.Omitted) != 0 {
		t.Errorf("holds = %v, omitted = %v, want the old library's hold cleared", holds(t, s), res.Omitted)
	}
}

func TestReadOlderThanPruning(t *testing.T) {
	prunings := map[string]func(t *testing.T, b *Store){
		"attempts": func(t *testing.T, b *Store) {
			if n, err := b.PruneAttempts(base.Add(48 * time.Hour)); err != nil || n != 1 {
				t.Fatalf("pruned %d attempts, err %v", n, err)
			}
		},
		"history": func(t *testing.T, b *Store) {
			if n, _, err := b.PruneHistory(base.Add(time.Hour)); err != nil || n != 1 {
				t.Fatalf("pruned %d events, err %v", n, err)
			}
		},
		"sections": func(t *testing.T, b *Store) {
			if pruned, err := b.PruneSections(srv, []string{sec}); err != nil || len(pruned) != 1 {
				t.Fatalf("pruned %v, err %v", pruned, err)
			}
		},
	}
	for name, prune := range prunings {
		t.Run(name, func(t *testing.T) {
			a, b := openPair(t)
			at := base.Add(-time.Hour)
			apply(t, a, Read{At: at, Full: true, Items: []Item{item("1", NoSummary)}})
			apply(t, a, Read{Section: "2", At: at})
			fixedClock(a, base)
			attempt(t, a, AttemptSpec{RatingKey: "1"})
			if err := a.RecordRequest(Request{Server: srv, RatingKey: "1", At: base, Source: SourceAuto, Action: ActionRefresh}); err != nil {
				t.Fatal(err)
			}
			st := startRead(t, a, sec)
			prune(t, b)

			_, err := a.ApplyRead(Read{Server: srv, Section: sec, At: base, Mark: st.Mark, Full: true})
			if !errors.Is(err, ErrReadPruned) {
				t.Fatalf("err = %v, want ErrReadPruned", err)
			}
			if lib, err := readTimes(a, srv, sec); err != nil || !lib.LastRead.Equal(at) || !lib.LastFullRead.Equal(at) {
				t.Errorf("library = %+v, err %v, want the read times kept", lib, err)
			}
			if _, ok := rows(t, a)["1"]; !ok {
				t.Error("the rejected read changed the cache")
			}
		})
	}
}

func TestReadAfterOtherPrunings(t *testing.T) {
	a, b := openPair(t)
	apply(t, a, Read{At: base.Add(-time.Hour), Items: []Item{item("1", NoSummary)}})
	apply(t, a, Read{Server: "other", Section: "2", At: base.Add(-time.Hour)})
	if err := a.RecordRequest(Request{Server: srv, RatingKey: "1", At: base, Source: SourceAuto, Action: ActionRefresh}); err != nil {
		t.Fatal(err)
	}
	// Prunings before the mark, and one of another server after it.
	if _, _, err := b.PruneHistory(base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	st := startRead(t, a, sec)
	if pruned, err := b.PruneSections("other", nil); err != nil || len(pruned) != 1 {
		t.Fatalf("pruned %v, err %v", pruned, err)
	}
	if _, _, err := b.PruneHistory(base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The mark of a history without events is 0, so the read applies.
	apply(t, a, Read{At: base, Mark: st.Mark, Items: []Item{item("1", NoArt)}})
	if got := rows(t, a)["1"].Reasons; got != NoArt {
		t.Errorf("reasons = %v, want the read applied", got)
	}
}
