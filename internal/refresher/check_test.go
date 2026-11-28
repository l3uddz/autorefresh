package refresher

import (
	"context"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// oldMatch is item key still showing its old match with complete metadata.
func oldMatch(key string) checkAnswer {
	it := completeItem(key)
	it.GUID = "plex://movie/old"
	return checkAnswer{item: it, keepGUID: true}
}

var newMatch = plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}

func TestCheckWaitsForTheGUIDOfTheMatch(t *testing.T) {
	f, _, checks := matchCheckFixture(t, oldMatch("1"), checkAnswer{item: completeItem("1")})
	if err := f.r.ApplyMatch(t.Context(), "home", "1", newMatch); err != nil {
		t.Fatal(err)
	}
	if *checks != 2 {
		t.Errorf("checks = %d, want 2", *checks)
	}
	if _, found := f.row("home", "1", "1"); found {
		t.Error("the check of the new GUID did not complete the item")
	}
	if f.held("1") {
		t.Error("the confirmed match left a hold")
	}
}

func TestCheckHoldsAMatchNotShownInTime(t *testing.T) {
	f, _, checks := matchCheckFixture(t, oldMatch("1"))
	before, _ := f.row("home", "1", "1")
	if err := f.r.ApplyMatch(t.Context(), "home", "1", newMatch); err != nil {
		t.Fatal(err)
	}
	if *checks != len(matchCheckDelays) {
		t.Errorf("checks = %d, want %d", *checks, len(matchCheckDelays))
	}
	if row, found := f.row("home", "1", "1"); !found || row.Reasons != before.Reasons {
		t.Errorf("row = %+v, found %v, want it unchanged", row, found)
	}
	if !f.held("1") {
		t.Error("no hold after the waits ran out")
	}
}

func TestCheckOfAMatchToTheCurrentGUID(t *testing.T) {
	f, _, _ := matchCheckFixture(t, checkAnswer{item: completeItem("1")})
	current := plex.Candidate{GUID: completeItem("1").GUID, Name: "Title 1", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", current); err != nil {
		t.Fatal(err)
	}
	if _, found := f.row("home", "1", "1"); found || f.held("1") {
		t.Errorf("found %v, held %v, want the item fixed without a hold", found, f.held("1"))
	}
}

// A newer write attempt of another process yields the check of Refresh now,
// also its 404, and leaves a hold.
func TestCheckYieldsToANewerAttempt(t *testing.T) {
	for _, answer := range []checkAnswer{{item: completeItem("1")}, {err: plex.ErrNotFound}} {
		fake := newFakePlex(moviesLib)
		f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: fake, answers: []checkAnswer{answer}, checks: new(int)}})
		f.now = t0
		f.seed("home", moviesLib, incompleteItem("1"), store.NoSummary, 0)
		other := otherProcess(f)
		l := logAttempts(f, fake)
		l.after = func(ctx context.Context, _ store.AttemptSpec) {
			if _, err := other.BeginAttempt(ctx, store.AttemptSpec{Server: "home", RatingKey: "1", Action: store.ActionRefresh}); err != nil {
				t.Error(err)
			}
		}

		res, err := f.r.RefreshItem(t.Context(), "home", "1")
		if err != nil {
			t.Fatal(err)
		}
		if res.Checked {
			t.Errorf("%v: checked, want the reasons reported as not current", answer.err)
		}
		if row, found := f.row("home", "1", "1"); !found || row.Reasons != store.NoSummary {
			t.Errorf("%v: row = %+v, found %v, want it kept", answer.err, row, found)
		}
		if !f.held("1") {
			t.Errorf("%v: no hold", answer.err)
		}
	}
}

// Refresh now and then Fix match settles the item at once, because Plex
// answered the refresh before the match began.
func TestRefreshNowThenFixMatch(t *testing.T) {
	f, _, _ := matchCheckFixture(t, checkAnswer{item: incompleteItem("1")}, checkAnswer{item: incompleteItem("1")},
		checkAnswer{item: incompleteItem("1")}, checkAnswer{item: completeItem("1")})
	f.r.opts.IgnoreReasons = store.Unmatched // a plain Refresh now
	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	f.r.opts.IgnoreReasons = 0
	if err := f.r.ApplyMatch(t.Context(), "home", "1", newMatch); err != nil {
		t.Fatal(err)
	}
	if _, found := f.row("home", "1", "1"); found || f.held("1") {
		t.Errorf("found %v, held %v, want the item fixed without a hold", found, f.held("1"))
	}
}

// Fix match and then Refresh now within the protection of the match: the
// checks of Refresh now yield to the match, also when Plex shows the item
// complete, which leaves a hold.
func TestFixMatchThenRefreshNow(t *testing.T) {
	still := checkAnswer{item: incompleteItem("1")}
	f, _, _ := matchCheckFixture(t, still, still, still, checkAnswer{item: completeItem("1")})
	if err := f.r.ApplyMatch(t.Context(), "home", "1", newMatch); err != nil {
		t.Fatal(err)
	}
	if f.held("1") {
		t.Fatal("the confirmed match left a hold")
	}
	f.r.opts.IgnoreReasons = store.Unmatched // a plain Refresh now
	res, err := f.r.RefreshItem(t.Context(), "home", "1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked || !f.held("1") {
		t.Errorf("checked %v, held %v, want the reasons not current and a hold", res.Checked, f.held("1"))
	}
	if _, found := f.row("home", "1", "1"); !found {
		t.Error("the yielded check moved the item to Fixed")
	}
	if got := f.queueState("1"); got != store.StateWaiting {
		t.Errorf("state = %s, want waiting", got)
	}
}

// A check that yields after an earlier check of the same action saved
// reports the reasons as not current.
func TestCheckYieldsAfterASave(t *testing.T) {
	fake, checks := newFakePlex(moviesLib), new(int)
	f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: fake,
		answers: []checkAnswer{{item: incompleteItem("1")}}, checks: checks}})
	f.now = t0
	f.seed("home", moviesLib, incompleteItem("1"), store.NoSummary, 0)
	other := otherProcess(f)
	sleeps := 0
	f.r.sleep = func(ctx context.Context, _ time.Duration) error {
		if sleeps++; sleeps == 2 {
			if _, err := other.BeginAttempt(ctx, store.AttemptSpec{Server: "home", RatingKey: "1", Action: store.ActionRefresh}); err != nil {
				t.Error(err)
			}
		}
		return nil
	}

	res, err := f.r.RefreshItem(t.Context(), "home", "1")
	if err != nil {
		t.Fatal(err)
	}
	if *checks != 2 || res.Checked || !f.held("1") {
		t.Errorf("checks %d, checked %v, held %v, want 2, false, and a hold", *checks, res.Checked, f.held("1"))
	}
}
