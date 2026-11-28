package store

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"
)

// openPair opens one database file twice, so a test runs two connections,
// as two processes do.
func openPair(t *testing.T) (*Store, *Store) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "store.db")
	a, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return a, b
}

// fixedClock sets the clock of s to at.
func fixedClock(s *Store, at time.Time) { s.SetClock(func() time.Time { return at }) }

// attempt saves an attempt of key and returns its id.
func attempt(t *testing.T, s *Store, spec AttemptSpec) int64 {
	t.Helper()
	if spec.Server == "" {
		spec.Server = srv
	}
	if spec.Action == "" {
		spec.Action = ActionRefresh
	}
	id, err := s.BeginAttempt(context.Background(), spec)
	if err != nil {
		t.Fatalf("BeginAttempt: %v", err)
	}
	return id
}

// attemptTimes returns the start and end of the attempt id.
func attemptTimes(t *testing.T, s *Store, id int64) (started, ends int64) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT started_at, ends_at FROM write_attempt WHERE id = ?`, id).Scan(&started, &ends); err != nil {
		t.Fatal(err)
	}
	return started, ends
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBeginAttemptEnd(t *testing.T) {
	s := openTest(t)
	noon := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	fixedClock(s, noon)

	id := attempt(t, s, AttemptSpec{RatingKey: "1", Timeout: 30 * time.Second})
	started, ends := attemptTimes(t, s, id)
	if started != noon.Unix() || ends != noon.Add(10*time.Minute+30*time.Second).Unix() {
		t.Errorf("attempt = %d to %d, want 12:00:00 to 12:10:30", started, ends)
	}

	id = attempt(t, s, AttemptSpec{RatingKey: "2", Timeout: 1500 * time.Millisecond})
	if _, ends := attemptTimes(t, s, id); ends != noon.Unix()+2+600 {
		t.Errorf("end = %d, want the timeout rounded up to 2 s", ends-noon.Unix())
	}

	// The longest timeout near the largest time saturates instead of wrapping.
	fixedClock(s, time.Unix(math.MaxInt64-100, 0))
	id = attempt(t, s, AttemptSpec{RatingKey: "3", Timeout: time.Duration(math.MaxInt64)})
	if _, ends := attemptTimes(t, s, id); ends != math.MaxInt64 {
		t.Errorf("end = %d, want the saturated end", ends)
	}
}

func TestBeginAttemptCancelledWhileLocked(t *testing.T) {
	a, b := openPair(t)
	tx, err := b.db.Begin() // an immediate transaction takes the write lock
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := a.BeginAttempt(ctx, AttemptSpec{Server: srv, RatingKey: "1", Action: ActionRefresh}); err == nil {
		t.Fatal("BeginAttempt saved while another connection held the write lock")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("BeginAttempt took %v, want the end of its context", took)
	}
	tx.Rollback()
	if n := countRows(t, a, "write_attempt"); n != 0 {
		t.Errorf("attempts = %d, want 0", n)
	}
}

func TestBeginAttemptAdmission(t *testing.T) {
	a, b := openPair(t)
	fixedClock(a, base)
	fixedClock(b, base)
	apply(t, a, Read{Items: []Item{item("1", NoSummary), item("2", NoSummary), item("3", NoSummary)}})
	auto := func(key string) AttemptSpec {
		return AttemptSpec{Server: srv, RatingKey: key, Action: ActionRefresh, Automatic: true, Sections: []string{sec}}
	}

	// The other connection writes item 1 and holds item 2.
	attempt(t, b, AttemptSpec{RatingKey: "1"})
	if _, err := b.db.Exec(`INSERT INTO read_hold (server, section, rating_key) VALUES (?, ?, '2')`, srv, sec); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"1", "2"} {
		if _, err := a.BeginAttempt(context.Background(), auto(key)); !errors.Is(err, ErrHeld) {
			t.Errorf("item %s: err = %v, want ErrHeld", key, err)
		}
	}
	// A manual write is not refused.
	attempt(t, a, AttemptSpec{RatingKey: "1"})
	// A section that the run did not read refuses the item.
	other := auto("3")
	other.Sections = []string{"9"}
	if _, err := a.BeginAttempt(context.Background(), other); !errors.Is(err, ErrHeld) {
		t.Errorf("unread section: err = %v, want ErrHeld", err)
	}
	attempt(t, a, auto("3"))
	if n := countRows(t, a, "write_attempt"); n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
}

func TestBeginAttemptFollowUp(t *testing.T) {
	a, b := openPair(t)
	fixedClock(a, base)
	fixedClock(b, base)
	apply(t, a, Read{Items: []Item{item("1", Unmatched)}})
	match := attempt(t, a, AttemptSpec{RatingKey: "1", Action: ActionMatch, Automatic: true, Sections: []string{sec}})
	followUp := AttemptSpec{Server: srv, RatingKey: "1", Action: ActionRefresh, Automatic: true, Sections: []string{sec}, FollowsUp: match}

	// The live match attempt does not refuse its own follow-up refresh.
	attempt(t, a, followUp)

	// An attempt of the other connection refuses it, and the refusal saves a
	// hold that stays after the other attempt is rejected and deleted.
	other := attempt(t, b, AttemptSpec{RatingKey: "1"})
	if _, err := a.BeginAttempt(context.Background(), followUp); !errors.Is(err, ErrHeld) {
		t.Fatalf("err = %v, want ErrHeld", err)
	}
	if err := b.DropAttempt(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, a, "read_hold"); n != 1 {
		t.Errorf("holds = %d, want 1", n)
	}
	// The hold alone refuses it too.
	if _, err := a.BeginAttempt(context.Background(), followUp); !errors.Is(err, ErrHeld) {
		t.Errorf("with a hold: err = %v, want ErrHeld", err)
	}
}

func TestPruneAttempts(t *testing.T) {
	s := openTest(t)
	fixedClock(s, base)
	old := attempt(t, s, AttemptSpec{RatingKey: "1", Timeout: 30 * time.Second})
	_, ends := attemptTimes(t, s, old)
	end := time.Unix(ends, 0)

	gen := func() generations {
		t.Helper()
		st, err := s.ReadStart(srv, sec)
		if err != nil {
			t.Fatal(err)
		}
		return st.Mark.generations
	}
	before := gen()
	if n, err := s.PruneAttempts(end.Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("recent attempt: pruned %d, err %v", n, err)
	}
	if gen() != before {
		t.Error("a pruning that deleted nothing changed the generation")
	}
	if n, err := s.PruneAttempts(end.Add(25 * time.Hour)); err != nil || n != 1 {
		t.Fatalf("old attempt: pruned %d, err %v", n, err)
	}
	if got := gen(); got.all != before.all+1 || got.server != before.server {
		t.Errorf("generations = %+v, want only the shared one bumped from %+v", got, before)
	}
}

func TestPruningsBumpTheirGenerations(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("1", NoSummary)}})
	apply(t, s, Read{Server: "other", Section: "2", Items: []Item{{Server: "other", Section: "2", RatingKey: "5", Type: "movie", AddedAt: base, Reasons: NoSummary}}})
	gen := func(server string) generations {
		t.Helper()
		st, err := s.ReadStart(server, sec)
		if err != nil {
			t.Fatal(err)
		}
		return st.Mark.generations
	}
	start, startOther := gen(srv), gen("other")

	if _, err := s.PruneSections("other", nil); err != nil {
		t.Fatal(err)
	}
	if got := gen(srv); got != start {
		t.Errorf("pruning another server changed %+v to %+v", start, got)
	}
	if got := gen("other"); got.server != startOther.server+1 {
		t.Errorf("generation of the pruned server = %d, want %d", got.server, startOther.server+1)
	}

	if _, _, err := s.PruneHistory(base.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := gen(srv); got != start {
		t.Errorf("a history pruning that deleted nothing changed %+v to %+v", start, got)
	}
	if err := s.RecordRequest(Request{Server: srv, RatingKey: "1", At: base, Source: SourceAuto, Action: ActionRefresh}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PruneHistory(base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := gen(srv); got.all != start.all+1 {
		t.Errorf("shared generation = %d, want %d", got.all, start.all+1)
	}
}

func TestReadStartReportsHolds(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("1", NoSummary)}})
	st, err := s.ReadStart(srv, sec)
	if err != nil {
		t.Fatal(err)
	}
	if st.Held || st.LastRead.IsZero() || !st.Mark.fenced {
		t.Errorf("state = %+v, want read times, no hold, and a fenced mark", st)
	}
	if _, err := s.db.Exec(`INSERT INTO read_hold (server, section, rating_key) VALUES (?, ?, '1')`, srv, sec); err != nil {
		t.Fatal(err)
	}
	if st, err = s.ReadStart(srv, sec); err != nil || !st.Held {
		t.Errorf("held = %v, err = %v, want a hold", st.Held, err)
	}
}

func TestRollbackOfWriteAttempts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	// The rollback SQL of the README.
	if _, err := s.db.Exec(`BEGIN IMMEDIATE;
		DROP TABLE read_hold;
		DROP TABLE write_attempt;
		DROP TABLE prune_fence;
		DELETE FROM schema_migration WHERE version = 18;
		COMMIT;`); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	s.Close()
	if s, err = Open(file); err != nil {
		t.Fatalf("Open after the rollback: %v", err)
	}
	defer s.Close()
	if n := countRows(t, s, "prune_fence"); n != 1 {
		t.Errorf("fence rows = %d, want 1", n)
	}
}
