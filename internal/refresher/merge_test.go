package refresher

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// guidKey is the fake listing key of the FindByGUID request for guid in
// the section of lib.
func guidKey(lib plex.Library, guid string) string {
	return listKey(lib.ID, itemType(lib.Type), plex.Filter{Key: "guid", Value: guid})
}

// keptItem is the item 900 that Plex kept with the GUID guid.
func keptItem(guid string) []plex.Item {
	return []plex.Item{{RatingKey: "900", GUID: guid}}
}

// lookups returns the recorded FindByGUID calls.
func lookups(calls []string) []string {
	var got []string
	for _, c := range calls {
		if strings.HasPrefix(c, "FindByGUID ") {
			got = append(got, c)
		}
	}
	return got
}

// wantMerged fails unless key left the cache with a merged completion into
// item 900 that credits its accepted match request, and has no gone event.
func (f *fixture) wantMerged(server, section, key string, action store.Action) store.Event {
	f.t.Helper()
	if _, found := f.row(server, section, key); found {
		f.t.Error("merged item still in the cache")
	}
	var match store.Event
	for _, e := range f.timeline(key) {
		if e.Kind == store.EventGone {
			f.t.Errorf("gone event %+v, want none", e)
		}
		if e.Kind == store.EventRequest && e.Action == action && match.ID == 0 {
			match = e
		}
	}
	e := f.newestEvent(key)
	if e.Kind != store.EventCompleted || e.Source != store.SourceAuto || e.MergedKey != "900" ||
		e.FixedAction != action || e.FixedMatch != action || e.FixedMatchEventID != match.ID || match.ID == 0 {
		f.t.Errorf("newest event = %+v, want a completion merged into 900 by match %d", e, match.ID)
	}
	return e
}

// Refresh now applies the automatic match, Plex merges the item into the
// item that already had the match, and the refresh after it gets 404: the
// item is fixed, and the action makes no check.
func TestRefreshItemRecordsAMerge(t *testing.T) {
	f, fp := refreshNowFixture(t)
	fp.refreshErrs["101"] = plex.ErrNotFound
	fp.lists[guidKey(moviesLib, "plex://movie/m")] = keptItem("plex://movie/m")

	res, err := f.r.RefreshItem(t.Context(), testServer, "101")
	if err != nil || res.Match == nil || !res.Checked {
		t.Fatalf("result = %+v, %v, want an applied match that checked", res, err)
	}
	wantCalls(t, lookups(fp.calls), "FindByGUID 1?type=1&guid=plex://movie/m")
	if len(f.sleeps) != 0 {
		t.Errorf("sleeps = %v, want no check", f.sleeps)
	}
	match := f.matchRequest("101")
	if e := f.wantMerged(testServer, "1", "101", match.Action); e.RunID != 0 {
		t.Errorf("run id = %d, want none", e.RunID)
	}
	if lines := f.logLines("info", "Plex merged the item"); len(lines) != 1 || lines[0]["merged_key"] != "900" {
		t.Errorf("merge log = %v", lines)
	}
}

// A run counts a merge as a completion after a match, and the item keeps
// the outcome of the accepted match.
func TestRunMatchRecordsAMerge(t *testing.T) {
	for name, answer := range map[string]checkAnswer{
		"refresh": {item: completeItem("101")},
		"check":   {err: plex.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			f, fp, checks := runMatchFixture(t, answer)
			if name == "refresh" {
				fp.refreshErrs["101"] = plex.ErrNotFound
			}
			fp.lists[guidKey(moviesLib, "plex://movie/m")] = keptItem("plex://movie/m")
			p := withProgress(f)

			f.run(t0)

			wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
			wantCalls(t, lookups(fp.calls), "FindByGUID 1?type=1&guid=plex://movie/m")
			if want := map[string]int{"refresh": 0, "check": 1}[name]; *checks != want {
				t.Errorf("checks = %d, want %d", *checks, want)
			}
			if e := f.wantMerged(testServer, "1", "101", store.ActionMatch); e.RunID == 0 {
				t.Error("the completion has no run")
			}
			summary := f.lastSummary(testServer)
			for field, want := range map[string]float64{"completed_items": 1, "completed_after_request": 1, "completed_after_match": 1} {
				wantField(t, summary, field, want)
			}
			wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 match")
			if got := f.onlyRun(); got.Status != store.RunFinished || got.Completed != 1 || got.Fixed != 1 {
				t.Errorf("run = %+v, want a finished run with 1 completion", got)
			}
		})
	}
}

// A check after a manual match that gets 404 finds the merge.
func TestApplyMatchCheckRecordsAMerge(t *testing.T) {
	f, fake, checks := matchCheckFixture(t, checkAnswer{err: plex.ErrNotFound})
	lib := plex.Library{ID: "1", Type: "movie"}
	fake.lists[guidKey(lib, "plex://movie/1")] = keptItem("plex://movie/1")
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}

	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	if *checks != 1 {
		t.Errorf("checks = %d, want 1", *checks)
	}
	f.wantMerged("home", "1", "1", store.ActionMatch)
}

// The lookup of a series asks for the items of the series library.
func TestApplyMatchRecordsAMergeOfASeries(t *testing.T) {
	fake := newFakePlex(showsLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: checkedMetadata{fakePlex: fake,
		answers: []checkAnswer{{item: completeItem("1")}}, checks: new(int)}})
	f.now = t0
	f.seed(testServer, showsLib, unmatchedItem("1"), store.Unmatched, 0)
	fake.refreshErrs["1"] = plex.ErrNotFound
	fake.lists[guidKey(showsLib, "plex://show/1")] = keptItem("plex://show/1")

	if err := f.r.ApplyMatch(t.Context(), testServer, "1", plex.Candidate{GUID: "plex://show/1", Name: "Show"}); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, lookups(fake.calls), "FindByGUID 2?type=2&guid=plex://show/1")
	f.wantMerged(testServer, "2", "1", store.ActionMatch)
}

// Without another item that has the chosen GUID, or when the lookup fails,
// the item is gone.
func TestApplyMatchWithoutAMergeIsGone(t *testing.T) {
	const guid = "plex://movie/1"
	tests := map[string]struct {
		items []plex.Item
		err   error
	}{
		"empty answer":  {},
		"other guid":    {items: []plex.Item{{RatingKey: "900", GUID: "plex://movie/2"}}},
		"own key":       {items: []plex.Item{{RatingKey: "1", GUID: guid}}},
		"invalid key":   {items: []plex.Item{{RatingKey: "../9", GUID: guid}}},
		"failed lookup": {err: errors.New("bad answer")},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f, fake, checks := matchCheckFixture(t, checkAnswer{item: completeItem("1")})
			fake.refreshErrs["1"] = plex.ErrNotFound
			key := guidKey(plex.Library{ID: "1", Type: "movie"}, guid)
			fake.lists[key], fake.listErrs[key] = tt.items, tt.err

			if err := f.r.ApplyMatch(t.Context(), "home", "1", plex.Candidate{GUID: guid, Name: "Match"}); err != nil {
				t.Fatal(err)
			}
			if *checks != 0 {
				t.Errorf("checks = %d, want none", *checks)
			}
			if e := f.newestEvent("1"); e.Kind != store.EventGone || e.Action != store.ActionRefresh || e.MergedKey != "" {
				t.Errorf("newest event = %+v, want gone by the refresh", e)
			}
			if failed := len(f.logLines("warn", "Merge lookup failed")) == 1; failed != (tt.err != nil) {
				t.Errorf("lookup failure logged = %v, want %v", failed, tt.err != nil)
			}
		})
	}
}

// An unavailable server during the lookup records the item gone and ends
// the run of the server, with no hold for the removed item.
func TestRunMergeLookupUnavailableEndsTheRun(t *testing.T) {
	for name, answer := range map[string]checkAnswer{
		"refresh": {item: completeItem("101")},
		"check":   {err: plex.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			f, fp, _ := runMatchFixture(t, answer)
			f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)
			if name == "refresh" {
				fp.refreshErrs["101"] = plex.ErrNotFound
			}
			fp.listErrs[guidKey(moviesLib, "plex://movie/m")] = fmt.Errorf("find: %w", plex.ErrUnavailable)

			f.runUnavailable(t0)

			wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
			if e := f.newestEvent("101"); e.Kind != store.EventGone || e.MergedKey != "" {
				t.Errorf("newest event = %+v, want gone", e)
			}
			if _, found := f.row(testServer, "1", "101"); found {
				t.Error("gone item still in the cache")
			}
			if held, err := f.store.Held(testServer, "101", t0); err != nil || held {
				t.Errorf("held = %v, %v, want no hold", held, err)
			}
			if got := f.onlyRun(); got.Status != store.RunFailed {
				t.Errorf("run = %+v, want a failed run", got)
			}
			// A check that recorded the item gone ends the follow-up as
			// settled: only the lookup logs its failure.
			if n := len(f.logLines("warn", "Following up a request failed")); name == "check" && n != 0 {
				t.Errorf("follow-up failures logged = %d, want none after the gone check", n)
			}
		})
	}
}

// A 404 after a plain refresh, and a 404 of the match of an item with only
// history, look up no merge.
func TestNoMergeLookupWithoutAMatchOrACacheRow(t *testing.T) {
	t.Run("plain refresh", func(t *testing.T) {
		lib := plex.Library{ID: "1", Type: "movie"}
		p := manualMetadata{fakePlex: newFakePlex(lib), err: plex.ErrNotFound}
		f := newRefreshFixture(t, Server{Name: "home", Plex: p})
		f.now = t0
		f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
		if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
			t.Fatal(err)
		}
		if got := lookups(p.calls); len(got) != 0 {
			t.Errorf("lookups = %v, want none", got)
		}
		if e := f.newestEvent("1"); e.Kind != store.EventGone {
			t.Errorf("newest event = %+v, want gone", e)
		}
	})
	t.Run("history only", func(t *testing.T) {
		f, fake, _ := matchCheckFixture(t, checkAnswer{item: completeItem("1")})
		if _, _, err := f.store.RecordGone("home", "1", t0, store.SourceAuto, "", 0, store.AttemptResult{}, ""); err != nil {
			t.Fatal(err)
		}
		fake.refreshErrs["1"] = plex.ErrNotFound
		fake.lists[guidKey(plex.Library{ID: "1", Type: "movie"}, "plex://movie/1")] = keptItem("plex://movie/1")

		if err := f.r.ApplyMatch(t.Context(), "home", "1", plex.Candidate{GUID: "plex://movie/1", Name: "Match"}); err != nil {
			t.Fatal(err)
		}
		if got := lookups(fake.calls); len(got) != 0 {
			t.Errorf("lookups = %v, want none", got)
		}
		events := f.timeline("1")
		if slices.ContainsFunc(events, func(e store.Event) bool { return e.MergedKey != "" }) {
			t.Errorf("events = %+v, want no merge", events)
		}
	})
}

// removingCheck answers each check as checkedMetadata does, after it
// removes the row, as a run that completes or removes the item during the
// check does.
type removingCheck struct {
	checkedMetadata
	remove func()
}

func (p removingCheck) Item(ctx context.Context, ratingKey string) (plex.Item, error) {
	p.remove()
	return p.checkedMetadata.Item(ctx, ratingKey)
}

// A row that leaves the cache while a check reads Plex settles the
// follow-up: one check, no failure, and no merge, after a plain refresh and
// after a match, whose gone check looks up the merge and finds no row.
func TestCheckSettlesWhenTheRowLeavesDuringTheCheck(t *testing.T) {
	answers := map[string]checkAnswer{"complete": {item: completeItem("1")}, "gone": {err: plex.ErrNotFound}}
	for _, action := range []string{"refresh", "match"} {
		for name, answer := range answers {
			t.Run(action+" "+name, func(t *testing.T) {
				lib := plex.Library{ID: "1", Type: "movie"}
				fake, checks := newFakePlex(lib), new(int)
				var f *fixture
				p := removingCheck{checkedMetadata: checkedMetadata{fakePlex: fake, answers: []checkAnswer{answer}, checks: checks}, remove: func() {
					if _, _, err := f.store.RecordGone("home", "1", t0, store.SourceAuto, "", 0, store.AttemptResult{}, ""); err != nil {
						t.Error(err)
					}
				}}
				f = newRefreshFixture(t, Server{Name: "home", Plex: p})
				f.now = t0
				f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
				fake.lists[guidKey(lib, "plex://movie/1")] = keptItem("plex://movie/1")

				if action == "refresh" {
					res, err := f.r.RefreshItem(t.Context(), "home", "1")
					if err != nil || !res.Checked {
						t.Fatalf("result = %+v, %v, want checked", res, err)
					}
				} else if err := f.r.ApplyMatch(t.Context(), "home", "1", plex.Candidate{GUID: "plex://movie/1", Name: "Match"}); err != nil {
					t.Fatal(err)
				}
				if *checks != 1 || len(f.sleeps) != 1 {
					t.Errorf("checks %d, sleeps %v, want one", *checks, f.sleeps)
				}
				if n := len(f.logLines("warn", "Following up a request failed")); n != 0 {
					t.Errorf("follow-up failures logged = %d, want none", n)
				}
				if n := len(f.logLines("info", "Checked item")); n != 0 {
					t.Errorf("checked items logged = %d, want none for a row that was not saved", n)
				}
				for _, e := range f.timeline("1") {
					if e.MergedKey != "" {
						t.Errorf("event %+v, want no merge for a row that left the cache", e)
					}
				}
			})
		}
	}
}

// yieldingGone yields every gone check, as the store does when another
// write can still change the item.
type yieldingGone struct{ Store }

func (yieldingGone) ApplyCheckGone(string, string, time.Time, store.Source, int64, store.Observation, string) (store.Item, bool, error) {
	return store.Item{}, false, store.ErrCheckYielded
}

// A gone check that yields after a lookup on an unavailable server still
// ends the run of the server.
func TestRunMergeLookupUnavailableEndsTheRunAfterAYield(t *testing.T) {
	f, fp, _ := runMatchFixture(t, checkAnswer{err: plex.ErrNotFound})
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)
	fp.listErrs[guidKey(moviesLib, "plex://movie/m")] = fmt.Errorf("find: %w", plex.ErrUnavailable)
	f.r.store = yieldingGone{f.r.store}

	f.runUnavailable(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
	if got := f.onlyRun(); got.Status != store.RunFailed {
		t.Errorf("run = %+v, want a failed run", got)
	}
}
