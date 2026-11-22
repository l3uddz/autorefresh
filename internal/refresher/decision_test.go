package refresher

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// decisions returns the match decisions of an item, newest first.
func (f *fixture) decisions(key string) []store.Event {
	f.t.Helper()
	var found []store.Event
	for _, e := range f.timeline(key) {
		if e.Kind == store.EventMatchDecision {
			found = append(found, e)
		}
	}
	return found
}

// decision returns the only match decision of an item.
func (f *fixture) decision(key string) store.Event {
	f.t.Helper()
	found := f.decisions(key)
	if len(found) != 1 {
		f.t.Fatalf("decisions of %s = %+v, want 1", key, found)
	}
	return found[0]
}

// describeSearches renders each search as "<source> <query>[ from
// <id>][ failed]:" and then " <guid>=<verdict>" for each candidate.
func describeSearches(searches []store.MatchSearch) []string {
	var out []string
	for _, m := range searches {
		var b strings.Builder
		b.WriteString(m.Source + " " + m.Query)
		if m.FromID != "" {
			b.WriteString(" from " + m.FromID)
		}
		if m.Failed {
			b.WriteString(" failed")
		}
		b.WriteString(":")
		for _, c := range m.Candidates {
			b.WriteString(" " + c.GUID + "=" + c.Verdict)
		}
		out = append(out, b.String())
	}
	return out
}

// wantDecision checks the source, the outcome, the conflict, and the
// searches of a decision.
func wantDecision(t *testing.T, e store.Event, outcome store.DecisionOutcome, conflict string, searches ...string) {
	t.Helper()
	if e.Source != store.SourceAuto || !e.At.Equal(t0) {
		t.Errorf("decision source %q at %v, want auto at %v", e.Source, e.At, t0)
	}
	if store.DecisionOutcome(e.Outcome) != outcome || e.Conflict != conflict {
		t.Errorf("decision outcome %q conflict %q, want %q and %q (error %q)", e.Outcome, e.Conflict, outcome, conflict, e.Error)
	}
	if got := describeSearches(e.Searches); !reflect.DeepEqual(got, searches) {
		t.Errorf("searches:\n got %q\nwant %q", got, searches)
	}
}

// wantYears checks the confirmation values of a decision.
func wantYears(t *testing.T, e store.Event, pathYear, tmdbYear, episodeCount int) {
	t.Helper()
	if e.PathYear != pathYear || e.TMDBYear != tmdbYear || e.EpisodeCount != episodeCount {
		t.Errorf("path year %d, tmdb year %d, episode count %d, want %d, %d, %d",
			e.PathYear, e.TMDBYear, e.EpisodeCount, pathYear, tmdbYear, episodeCount)
	}
}

// yearConflictFixture returns a refresh fixture with the unmatched show
// 201 of 2022, refreshed once, at the path "/tv/Show (2022) {tvdb-5}".
func yearConflictFixture(t *testing.T, fp *fakePlex, ft *fakeTMDB) *fixture {
	t.Helper()
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	if ft != nil {
		f.r.opts.TMDB = ft
	}
	it := unmatchedItem("201")
	it.Year = 2022
	fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5}"}
	f.seed(testServer, showsLib, it, store.Unmatched, 1)
	return f
}

func TestDecisionMatched(t *testing.T) {
	f, fp := matchFixture(t)
	cand := plex.Candidate{GUID: "plex://movie/m", Name: "Film", Year: 2020, Type: "movie", Summary: "A film.",
		Thumb: "https://image.example/m.jpg", Score: 100}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{cand}

	f.run(t0)

	e := f.decision("101")
	wantDecision(t, e, store.DecisionMatched, "", "path tmdb-1: plex://movie/m=picked")
	wantYears(t, e, 0, 0, 0)
	want := store.MatchCandidate{GUID: cand.GUID, Name: cand.Name, Year: cand.Year, Type: cand.Type,
		Summary: cand.Summary, Thumb: cand.Thumb, Score: cand.Score, Verdict: "picked"}
	if got := e.Searches[0].Candidates[0]; got != want {
		t.Errorf("candidate = %+v, want %+v", got, want)
	}
	if e.Error != "" {
		t.Errorf("error = %q, want none", e.Error)
	}
	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m")
	if row, _ := f.row(testServer, "1", "101"); row.LastMatchOutcome != "matched" {
		t.Errorf("LastMatchOutcome = %q, want matched", row.LastMatchOutcome)
	}
}

func TestDecisionNoIDs(t *testing.T) {
	fp := newFakePlex(showsLib)
	ft := &fakeTMDB{}
	f := newTMDBFixture(t, fp, ft)

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoIDs, "")
	wantCalls(t, ft.calls)
	wantCalls(t, matchReads(fp.calls), "ItemPaths 201")
	wantCalls(t, sends(fp.calls), "Refresh 201")
}

func TestDecisionSeveralCandidates(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show"}, {GUID: "plex://show/b", Name: "Show"}}
	f := newTMDBFixture(t, fp, nil, "tvdb-5")

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "several candidates",
		"path tvdb-5: plex://show/a=several candidates plex://show/b=several candidates")
	wantCalls(t, sends(fp.calls), "Refresh 201")
}

func TestDecisionCandidateWithoutGUID(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{Name: "Show", Year: 2020}}
	f := newTMDBFixture(t, fp, nil, "tvdb-5")

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "candidate without guid",
		"path tvdb-5: =candidate without guid")
}

func TestDecisionDifferentCandidates(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = show("plex://show/a")
	fp.candidates["201 imdb-tt1"] = show("plex://show/b")
	f := newTMDBFixture(t, fp, nil, "tvdb-5", "imdb-tt1")

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "different candidates",
		"path tvdb-5: plex://show/a=different candidates",
		"path imdb-tt1: plex://show/b=different candidates")
}

func TestDecisionYearDiffers(t *testing.T) {
	old := []plex.Candidate{{GUID: "plex://show/old", Name: "Show", Year: 1978}}
	t.Run("TMDB answered", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tvdb-5"] = old
		fp.candidates["201 tmdb-77"] = old
		ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}, years: map[string]int{"77 true": 1978}}
		f := yearConflictFixture(t, fp, ft)

		f.run(t0)

		e := f.decision("201")
		wantDecision(t, e, store.DecisionNoMatch, "year differs",
			"path tvdb-5: plex://show/old=year differs",
			"tie tmdb-77: plex://show/old=confirms")
		wantYears(t, e, 0, 1978, 0)
		wantCalls(t, sends(fp.calls), "Refresh 201")
	})
	t.Run("no TMDB", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tvdb-5"] = old
		f := yearConflictFixture(t, fp, nil)

		f.run(t0)

		e := f.decision("201")
		wantDecision(t, e, store.DecisionNoMatch, "year differs", "path tvdb-5: plex://show/old=year differs")
		wantYears(t, e, 0, 0, 0)
	})
}

func TestDecisionConfirmedByPathYear(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Year = 2000
	movie.Files = []string{"/m/Concert 2000 (2018) {imdb-tt1}/Concert 2000 (2018).mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	fp.candidates["101 imdb-tt1"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Concert 2000", Year: 2018}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, movie, store.Unmatched, 1)

	f.run(t0)

	e := f.decision("101")
	wantDecision(t, e, store.DecisionMatched, "", "path imdb-tt1: plex://movie/a=picked")
	wantYears(t, e, 2018, 0, 0)
}

func TestDecisionConfirmedByTMDBYear(t *testing.T) {
	fp := newFakePlex(showsLib)
	cand := []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 2017}}
	fp.candidates["201 tvdb-5"] = cand
	fp.candidates["201 tmdb-77"] = cand
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}, years: map[string]int{"77 true": 2022}}
	f := yearConflictFixture(t, fp, ft)

	f.run(t0)

	e := f.decision("201")
	wantDecision(t, e, store.DecisionMatched, "",
		"path tvdb-5: plex://show/s=picked",
		"tie tmdb-77: plex://show/s=confirms")
	wantYears(t, e, 0, 2022, 0)
	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s")
}

func TestDecisionConfirmedByEpisodes(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.paths["201"] = []string{"/tv/Show (2003) {tvdb-5}"}
	fp.episodeNumbers = map[string]map[int][]int{"201": {1: {1, 2, 3}}}
	cand := []plex.Candidate{{GUID: "plex://show/right", Name: "Show", Year: 2011}}
	fp.candidates["201 tvdb-5"] = cand
	fp.candidates["201 tmdb-77"] = cand
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.TMDB = &fakeTMDB{
		ids:     map[string]string{"tvdb 5 true": "77"},
		years:   map[string]int{"77 true": 2011},
		seasons: map[string][]int{"77 1": {1, 2, 3}},
	}
	item := unmatchedItem("201")
	item.Year = 2003
	f.seed(testServer, showsLib, item, store.Unmatched, 1)

	f.run(t0)

	e := f.decision("201")
	wantDecision(t, e, store.DecisionMatched, "",
		"path tvdb-5: plex://show/right=picked",
		"tie tmdb-77: plex://show/right=confirms")
	wantYears(t, e, 0, 2011, 3)
	wantCalls(t, sends(fp.calls), "Match 201 plex://show/right")
}

func TestDecisionTieWithDifferentGUID(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
	fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/b", Name: "Show", Year: 2022}}
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}, years: map[string]int{"77 true": 2022}}
	f := yearConflictFixture(t, fp, ft)

	f.run(t0)

	e := f.decision("201")
	wantDecision(t, e, store.DecisionNoMatch, "year differs",
		"path tvdb-5: plex://show/a=year differs",
		"tie tmdb-77: plex://show/b=different candidate")
	wantYears(t, e, 0, 0, 0)
}

func TestDecisionKeepsFirstPassWithTMDB(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tmdb-77"] = show("plex://show/s")
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
	f := newTMDBFixture(t, fp, ft, "tvdb-5")

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionMatched, "",
		"path tvdb-5:",
		"tmdb tmdb-77 from tvdb-5: plex://show/s=picked")
}

func TestSwallowedTMDBFailureRecordsFailedAndRefreshes(t *testing.T) {
	t.Run("TMDB ID lookup", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		ft := &fakeTMDB{errs: map[string]error{"tvdb 5 true": errors.New("GET /3/find/5: 500 Internal Server Error")}}
		f := newTMDBFixture(t, fp, ft, "tvdb-5")

		f.run(t0)

		e := f.decision("201")
		wantDecision(t, e, store.DecisionFailed, "", "path tvdb-5:")
		if want := "tmdb lookup: tvdb-5: GET /3/find/5: 500 Internal Server Error"; e.Error != want {
			t.Errorf("error = %q, want %q", e.Error, want)
		}
		wantCalls(t, sends(fp.calls), "Refresh 201")
	})
	t.Run("TMDB year lookup", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		cand := []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
		fp.candidates["201 tvdb-5"] = cand
		fp.candidates["201 tmdb-77"] = cand
		ft := &fakeTMDB{
			ids:      map[string]string{"tvdb 5 true": "77"},
			yearErrs: map[string]error{"77 true": errors.New("GET /3/tv/77: 500 Internal Server Error")},
		}
		f := yearConflictFixture(t, fp, ft)

		f.run(t0)

		e := f.decision("201")
		// A handled failure leaves every verdict empty.
		wantDecision(t, e, store.DecisionFailed, "", "path tvdb-5: plex://show/a=", "tie tmdb-77: plex://show/a=")
		if want := "tmdb lookup: tmdb-77: GET /3/tv/77: 500 Internal Server Error"; e.Error != want {
			t.Errorf("error = %q, want %q", e.Error, want)
		}
		wantYears(t, e, 0, 0, 0)
		wantCalls(t, sends(fp.calls), "Refresh 201")
	})
}

func TestAcceptedEmptySearchFailureIsMarkedFailed(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020) {imdb-tt1}/Film.mkv"}
	fp.searchErrs["101 imdb-tt1"] = errServer
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)

	f.run(t0)

	e := f.decision("101")
	wantDecision(t, e, store.DecisionNoMatch, "", "path imdb-tt1 failed:")
	if e.Error != "" {
		t.Errorf("error = %q, want none", e.Error)
	}
	wantCalls(t, sends(fp.calls), "Refresh 101")
}

// failingDecision is a store whose match decision writes fail.
type failingDecision struct{ *store.Store }

func (failingDecision) RecordMatchDecision(store.MatchDecision) error { return errDisk }

func TestCanceledOrCacheErrorRecordsNoDecision(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		f := newTMDBFixture(t, fp, nil, "tvdb-5")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.r.opts.TMDB = cancelTMDB{cancel}

		f.now = t0
		if err := f.r.Run(ctx); err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
		if got := f.decisions("201"); len(got) != 0 {
			t.Errorf("decisions = %+v, want none", got)
		}
	})
	t.Run("TMDB ID cache error", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		f := newTMDBFixture(t, fp, &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}, "tvdb-5")
		f.r.store = failingCache{f.store}

		f.now = t0
		if err := f.r.Run(context.Background()); !errors.Is(err, errDisk) {
			t.Fatalf("Run = %v, want the store error", err)
		}
		if got := f.decisions("201"); len(got) != 0 {
			t.Errorf("decisions = %+v, want none", got)
		}
	})
	t.Run("decision write error", func(t *testing.T) {
		f, fp := matchFixture(t)
		f.r.store = failingDecision{f.store}

		f.now = t0
		err := f.r.Run(context.Background())
		if !errors.Is(err, errDisk) || !errors.Is(err, errCache) {
			t.Fatalf("Run = %v, want the store error as a cache error", err)
		}
		wantCalls(t, sends(fp.calls))
	})
}

func TestDryRunRecordsNoDecision(t *testing.T) {
	f, fp := matchFixture(t)
	f.r.opts.DryRun = true

	f.run(t0)

	if got := f.decisions("101"); len(got) != 0 {
		t.Errorf("decisions = %+v, want none", got)
	}
	if len(f.logLines("info", "Dry run")) != 1 {
		t.Errorf("dry run lines = %v, want 1", f.logLines("info", "Dry run"))
	}
	wantCalls(t, sends(fp.calls))
}

func TestPathLookupErrorRecordsFailed(t *testing.T) {
	t.Run("500", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.pathErrs["201"] = errServer
		f := newTMDBFixture(t, fp, &fakeTMDB{}, "tvdb-5")

		f.run(t0)

		e := f.decision("201")
		wantDecision(t, e, store.DecisionFailed, "")
		if e.Error != errServer.Error() {
			t.Errorf("error = %q, want %q", e.Error, errServer)
		}
	})
	t.Run("404 records a gone event instead", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.pathErrs["201"] = errNotFound
		f := newTMDBFixture(t, fp, &fakeTMDB{}, "tvdb-5")

		f.run(t0)

		events := f.timeline("201")
		if len(events) == 0 || events[0].Kind != store.EventGone {
			t.Fatalf("events = %+v, want a gone event first", events)
		}
		if got := f.decisions("201"); len(got) != 0 {
			t.Errorf("decisions = %+v, want none", got)
		}
	})
}

func TestAcceptedSearchFailureOutcome(t *testing.T) {
	t.Run("only search failed", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.searchErrs["201 tvdb-5"] = errServer
		f := newTMDBFixture(t, fp, nil, "tvdb-5")

		f.run(t0)

		wantDecision(t, f.decision("201"), store.DecisionNoMatch, "", "path tvdb-5 failed:")
		wantCalls(t, sends(fp.calls), "Refresh 201")
	})
	t.Run("another search matches", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.searchErrs["201 tvdb-5"] = errServer
		fp.candidates["201 imdb-tt1"] = show("plex://show/s")
		f := newTMDBFixture(t, fp, nil, "tvdb-5", "imdb-tt1")

		f.run(t0)

		wantDecision(t, f.decision("201"), store.DecisionMatched, "",
			"path tvdb-5 failed:",
			"path imdb-tt1: plex://show/s=picked")
		wantCalls(t, sends(fp.calls), "Match 201 plex://show/s")
	})
	t.Run("a tmdb search failure is not accepted", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.searchErrs["201 tmdb-5"] = errServer
		f := newTMDBFixture(t, fp, nil, "tmdb-5")

		f.run(t0)

		e := f.decision("201")
		wantDecision(t, e, store.DecisionFailed, "", "path tmdb-5 failed:")
		if e.Error != errServer.Error() {
			t.Errorf("error = %q, want %q", e.Error, errServer)
		}
		wantCalls(t, sends(fp.calls))
	})
}

func TestAcceptedSearchFailureThenTMDBMatch(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.searchErrs["201 tvdb-5"] = errServer
	fp.candidates["201 tmdb-77"] = show("plex://show/s")
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
	f := newTMDBFixture(t, fp, ft, "tvdb-5")

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionMatched, "",
		"path tvdb-5 failed:",
		"tmdb tmdb-77 from tvdb-5: plex://show/s=picked")
	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s")
}

func TestMixedResultsVerdicts(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show"}, {GUID: "plex://show/b", Name: "Show"}}
	fp.candidates["201 imdb-tt1"] = show("plex://show/a")
	f := newTMDBFixture(t, fp, nil, "tvdb-5", "imdb-tt1")

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "several candidates",
		"path tvdb-5: plex://show/a=several candidates plex://show/b=several candidates",
		"path imdb-tt1: plex://show/a=not compared")
}

func TestTMDBFallbackKeepsPositions(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show"}, {GUID: "plex://show/b", Name: "Show"}}
	fp.candidates["201 tmdb-78"] = show("plex://show/a")
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77", "imdb tt1 true": "78"}}
	f := newTMDBFixture(t, fp, ft, "tvdb-5", "imdb-tt1")

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "several candidates",
		"path tvdb-5:",
		"path imdb-tt1:",
		"tmdb tmdb-77 from tvdb-5: plex://show/a=several candidates plex://show/b=several candidates",
		"tmdb tmdb-78 from imdb-tt1: plex://show/a=not compared")
}
