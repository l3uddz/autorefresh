package refresher

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

// matchRequest returns the newest match request event of an item. The
// refresh that follows an accepted match is newer.
func (f *fixture) matchRequest(key string) store.Event {
	f.t.Helper()
	for _, e := range f.timeline(key) {
		if e.Kind == store.EventRequest && slices.Contains([]store.Action{store.ActionMatch, store.ActionTMDBMatch, store.ActionTitleMatch}, e.Action) {
			return e
		}
	}
	f.t.Fatalf("item %s has no match request event", key)
	return store.Event{}
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

func wantEvidence(t *testing.T, e store.Event, want store.MatchEvidence) {
	t.Helper()
	if !reflect.DeepEqual(e.MatchEvidence, want) {
		t.Errorf("evidence = %+v, want %+v", e.MatchEvidence, want)
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
	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
	if row, _ := f.row(testServer, "1", "101"); row.LastMatchOutcome != "matched" {
		t.Errorf("LastMatchOutcome = %q, want matched", row.LastMatchOutcome)
	}
}

// Without TMDB, a series without folder IDs gets no title search, so
// nothing is searched.
func TestDecisionNoIDs(t *testing.T) {
	fp := newFakePlex(showsLib)
	f := newTMDBFixture(t, fp, nil)

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoIDs, "")
	wantCalls(t, matchReads(fp.calls), "ItemPaths 201")
	wantCalls(t, sends(fp.calls), "Refresh 201")
}

// With TMDB, a series without folder IDs gets a title search. One that
// finds nothing is a decision without a match.
func TestDecisionNoIDsTitleSearchFindsNothing(t *testing.T) {
	fp := newFakePlex(showsLib)
	ft := &fakeTMDB{}
	f := newTMDBFixture(t, fp, ft)

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "", "title Title 201:")
	wantCalls(t, ft.calls)
	wantCalls(t, matchReads(fp.calls), "ItemPaths 201", "SearchMatches 201 Title 201 tv.plex.agents.series en-US")
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
		wantYears(t, e, 2022, 1978, 0)
		wantEvidence(t, e, store.MatchEvidence{TMDBID: "77"})
		wantCalls(t, sends(fp.calls), "Refresh 201")
	})
	t.Run("episode numbers differ", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tvdb-5"] = old
		fp.candidates["201 tmdb-77"] = old
		fp.episodeNumbers = map[string]map[int][]int{"201": {1: {1, 2, 3}, 2: {1, 2}}}
		ft := &fakeTMDB{
			ids:     map[string]string{"tvdb 5 true": "77"},
			years:   map[string]int{"77 true": 1978},
			seasons: map[string][]int{"77 1": {1, 2, 3}, "77 2": {1, 2, 3, 4}},
		}
		f := yearConflictFixture(t, fp, ft)

		f.run(t0)

		e := f.decision("201")
		wantDecision(t, e, store.DecisionNoMatch, "year differs",
			"path tvdb-5: plex://show/old=year differs",
			"tie tmdb-77: plex://show/old=confirms")
		wantYears(t, e, 2022, 1978, 0)
		wantEvidence(t, e, store.MatchEvidence{TMDBID: "77", EpisodeDiff: store.EpisodeDiff{Season: 2, Plex: 2, TMDB: 4, Missing: []int{3, 4}}})
	})
	t.Run("no TMDB", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tvdb-5"] = old
		f := yearConflictFixture(t, fp, nil)

		f.run(t0)

		e := f.decision("201")
		wantDecision(t, e, store.DecisionNoMatch, "year differs", "path tvdb-5: plex://show/old=year differs")
		wantYears(t, e, 2022, 0, 0)
		wantEvidence(t, e, store.MatchEvidence{TMDBSkip: store.TMDBOff})
	})
}

// A search can return the same candidate with another year. Only the row
// whose year differs gets the conflict.
func TestDecisionYearVerdictPerRow(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 2022}}
	fp.candidates["201 imdb-tt1"] = []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 1978}}
	f := yearConflictFixture(t, fp, nil)
	// The fixture sets the path, so the second ID comes after it.
	fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5} {imdb-tt1}"}

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "year differs",
		"path tvdb-5: plex://show/s=year agrees",
		"path imdb-tt1: plex://show/s=year differs")
}

// TMDB answered without a year: the decision has the TMDB ID and no year.
func TestDecisionTMDBWithoutYear(t *testing.T) {
	fp := newFakePlex(showsLib)
	old := []plex.Candidate{{GUID: "plex://show/old", Name: "Show", Year: 1978}}
	fp.candidates["201 tvdb-5"] = old
	fp.candidates["201 tmdb-77"] = old
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
	f := yearConflictFixture(t, fp, ft)

	f.run(t0)

	e := f.decision("201")
	wantDecision(t, e, store.DecisionNoMatch, "year differs",
		"path tvdb-5: plex://show/old=year differs", "tie tmdb-77: plex://show/old=confirms")
	wantYears(t, e, 2022, 0, 0)
	wantEvidence(t, e, store.MatchEvidence{TMDBID: "77"})
}

// A row without a year was not compared.
func TestDecisionYearVerdictWithoutYear(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/s", Name: "Show"}}
	fp.candidates["201 imdb-tt1"] = []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 1978}}
	f := yearConflictFixture(t, fp, nil)
	fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5} {imdb-tt1}"}

	f.run(t0)

	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "year differs",
		"path tvdb-5: plex://show/s=not compared",
		"path imdb-tt1: plex://show/s=year differs")
}

// A Plex failure of the episode lookup keeps the years compared before it.
func TestDecisionKeepsYearsOfAPlexEpisodeFailure(t *testing.T) {
	fp := newFakePlex(showsLib)
	old := []plex.Candidate{{GUID: "plex://show/old", Name: "Show", Year: 1978}}
	fp.candidates["201 tvdb-5"] = old
	fp.candidates["201 tmdb-77"] = old
	fp.episodeReadErr = errServer
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}, years: map[string]int{"77 true": 1978}}
	f := yearConflictFixture(t, fp, ft)

	f.run(t0)

	e := f.decision("201")
	if e.Outcome != string(store.DecisionFailed) {
		t.Fatalf("outcome = %q, want failed", e.Outcome)
	}
	wantYears(t, e, 2022, 1978, 0)
}

// The year check logs a path ID that TMDB has no single entry for.
func TestDecisionYearCheckWithoutTMDBEntry(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/old", Name: "Show", Year: 1978}}
	ft := &fakeTMDB{}
	f := yearConflictFixture(t, fp, ft)

	f.run(t0)

	e := f.decision("201")
	wantDecision(t, e, store.DecisionNoMatch, "year differs", "path tvdb-5: plex://show/old=year differs")
	wantEvidence(t, e, store.MatchEvidence{TMDBMissing: []string{"tvdb-5"}})
	wantCalls(t, ft.calls, "Find tvdb 5 true")
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
	wantYears(t, f.matchRequest("101"), 2018, 0, 0)
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
	wantYears(t, e, 2022, 2022, 0)
	wantEvidence(t, e, store.MatchEvidence{TMDBID: "77"})
	wantYears(t, f.matchRequest("201"), 0, 2022, 0)
	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s", "Refresh 201")
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
	wantYears(t, e, 2003, 2011, 3)
	wantYears(t, f.matchRequest("201"), 0, 2011, 3)
	wantCalls(t, sends(fp.calls), "Match 201 plex://show/right", "Refresh 201")
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
	wantYears(t, e, 2022, 0, 0)
	wantEvidence(t, e, store.MatchEvidence{})
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
		// A handled failure leaves every verdict empty, and keeps the year
		// conflict that started the year check.
		wantDecision(t, e, store.DecisionFailed, "year differs", "path tvdb-5: plex://show/a=", "tie tmdb-77: plex://show/a=")
		if want := "tmdb lookup: tmdb-77: GET /3/tv/77: 500 Internal Server Error"; e.Error != want {
			t.Errorf("error = %q, want %q", e.Error, want)
		}
		// The folder year stays with the failure. TMDB never answered, so
		// the decision has no TMDB ID.
		wantYears(t, e, 2022, 0, 0)
		wantEvidence(t, e, store.MatchEvidence{})
		wantCalls(t, sends(fp.calls), "Refresh 201")
	})
	t.Run("TMDB episode lookup", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		cand := []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
		fp.candidates["201 tvdb-5"] = cand
		fp.candidates["201 tmdb-77"] = cand
		fp.episodeNumbers = map[string]map[int][]int{"201": {1: {1, 2}}}
		ft := &fakeTMDB{
			ids:        map[string]string{"tvdb 5 true": "77"},
			years:      map[string]int{"77 true": 2017},
			seasonErrs: map[string]error{"77 1": errors.New("GET /3/tv/77/season/1: 500 Internal Server Error")},
		}
		f := yearConflictFixture(t, fp, ft)

		f.run(t0)

		e := f.decision("201")
		if e.Outcome != string(store.DecisionFailed) {
			t.Fatalf("outcome = %q, want failed", e.Outcome)
		}
		// The years that the evaluation compared stay with the failure.
		wantYears(t, e, 2022, 2017, 0)
		wantEvidence(t, e, store.MatchEvidence{TMDBID: "77"})
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
		wantCalls(t, sends(fp.calls), "Match 201 plex://show/s", "Refresh 201")
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
	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s", "Refresh 201")
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

func TestFailedTMDBSearchRecordsFailed(t *testing.T) {
	t.Run("TMDB fallback search", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.searchErrs["201 tmdb-77"] = errServer
		ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
		f := newTMDBFixture(t, fp, ft, "tvdb-5")

		f.run(t0)

		e := f.decision("201")
		wantDecision(t, e, store.DecisionFailed, "", "path tvdb-5:", "tmdb tmdb-77 from tvdb-5 failed:")
		if e.Error != errServer.Error() {
			t.Errorf("error = %q, want %q", e.Error, errServer)
		}
		wantCalls(t, sends(fp.calls))
	})
	t.Run("tie search", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
		fp.searchErrs["201 tmdb-77"] = errServer
		ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}, years: map[string]int{"77 true": 2022}}
		f := yearConflictFixture(t, fp, ft)

		f.run(t0)

		e := f.decision("201")
		// The failure keeps the year conflict that started the year check.
		wantDecision(t, e, store.DecisionFailed, "year differs", "path tvdb-5: plex://show/a=", "tie tmdb-77 failed:")
		if e.Error != errServer.Error() {
			t.Errorf("error = %q, want %q", e.Error, errServer)
		}
		wantCalls(t, sends(fp.calls))
	})
}

// runMatchFixture returns a refresh fixture whose run matches the unmatched
// movie 101 to plex://movie/m. Each check of the item gets the next answer.
func runMatchFixture(t *testing.T, answers ...checkAnswer) (*fixture, *fakePlex, *int) {
	t.Helper()
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/m", Name: "Film", Year: 2020}}
	checks := new(int)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: checkedMetadata{fakePlex: fp, answers: answers, checks: checks}})
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)
	return f, fp, checks
}

func TestRunMatchSettlesLikeAManualMatch(t *testing.T) {
	f, fp, checks := runMatchFixture(t, checkAnswer{item: unmatchedItem("101")}, checkAnswer{item: completeItem("101")})

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
	if *checks != 2 {
		t.Errorf("checks = %d, want 2", *checks)
	}
	if _, found := f.row(testServer, "1", "101"); found {
		t.Error("complete item still in the cache")
	}
	var refresh, done store.Event
	for _, e := range f.timeline("101") {
		switch {
		case e.Kind == store.EventRequest && refresh.ID == 0:
			refresh = e
		case e.Kind == store.EventCompleted:
			done = e
		}
	}
	if refresh.Action != store.ActionRefresh || !refresh.Accepted || refresh.Source != store.SourceAuto || refresh.RunID == 0 {
		t.Errorf("newest request = %+v, want the accepted refresh of the run", refresh)
	}
	if done.FixedAction != store.ActionRefresh || done.FixedMatch != store.ActionMatch || done.RunID != refresh.RunID {
		t.Errorf("completion = %+v, want one of run %d credited to the refresh after the match", done, refresh.RunID)
	}
	summary := f.lastSummary(testServer)
	for field, want := range map[string]float64{"completed_items": 1, "completed_after_request": 1, "completed_after_match": 1, "refresh_requests": 1} {
		wantField(t, summary, field, want)
	}
}

// A 404 on the follow-up refresh records the matched item as gone, and the
// run then refreshes its next item.
func TestRunMatchStopsSettlingWhenTheItemIsGone(t *testing.T) {
	f, fp, checks := runMatchFixture(t, checkAnswer{item: completeItem("101")})
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)
	fp.refreshErrs["101"] = plex.ErrNotFound

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101", "Refresh 102")
	if *checks != 0 {
		t.Errorf("checks = %d, want none", *checks)
	}
	if _, found := f.row(testServer, "1", "101"); found {
		t.Error("gone item still in the cache")
	}
	if e := f.timeline("101")[0]; e.Kind != store.EventGone || e.Source != store.SourceAuto || e.RunID == 0 {
		t.Errorf("newest event = %+v, want a gone event of the run", e)
	}
	if got := f.onlyRun(); got.Status != store.RunFinished {
		t.Errorf("run = %+v, want a finished run", got)
	}
}

// A server that becomes unavailable after it accepted the match of a run
// ends the run of that server, as a failed request does: the follow-up asks
// it nothing more, and the run sends no other request.
func TestRunMatchStopsTheServerWhenPlexBecomesUnavailable(t *testing.T) {
	f, fp, checks := runMatchFixture(t, checkAnswer{item: completeItem("101")})
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)
	fp.refreshErrs["101"] = fmt.Errorf("refresh: %w", plex.ErrUnavailable)
	p := withProgress(f)

	f.runUnavailable(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
	if *checks != 0 {
		t.Errorf("checks = %d, want none", *checks)
	}
	if calls := p.itemCalls(); len(calls) != 2 || !strings.HasPrefix(calls[1], "ItemDone home 101 ") {
		t.Errorf("item progress = %q, want 101 started and done", calls)
	}
	runs := f.runs()
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want 1", runs)
	}
	if r := runs[0]; r.Status != store.RunFailed || r.Matched != 1 || r.Refreshed != 1 || r.Failed != 1 {
		t.Errorf("run = %+v, want failed with 1 match, 1 refresh, and 1 failure", r)
	}
}

// The end of the run during the wait before the first check of a follow-up
// ends the run as canceled, as the end of the run in a request does: the
// matched item is done, and the run waits and sends nothing more.
func TestRunMatchFollowUpCanceledEndsTheRun(t *testing.T) {
	f, fp, checks := runMatchFixture(t, checkAnswer{item: completeItem("101")})
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)
	p := withProgress(f)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waits := 0
	f.r.sleep = func(ctx context.Context, _ time.Duration) error {
		waits++
		cancel()
		return ctx.Err()
	}

	f.now = t0
	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
	// A run that went on after the follow-up would wait again before item 102.
	if waits != 1 || *checks != 0 {
		t.Errorf("waits = %d, checks = %d, want 1 wait and no check", waits, *checks)
	}
	wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 match")
	if got := f.onlyRun(); got.Status != store.RunCanceled || got.Matched != 1 || got.Refreshed != 1 {
		t.Errorf("run = %+v, want a canceled run with 1 match and 1 refresh", got)
	}
}

// After a 500 on the follow-up refresh, the run logs the failure and still
// checks the matched item. Then it refreshes its next item.
func TestRunMatchFollowUpServerErrorKeepsTheRunGoing(t *testing.T) {
	f, fp, checks := runMatchFixture(t, checkAnswer{item: completeItem("101")})
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)
	fp.refreshErrs["101"] = errServer

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101", "Refresh 102")
	if *checks != 1 {
		t.Errorf("checks = %d, want 1", *checks)
	}
	warnings := f.logLines("warn", "Following up a request failed")
	if len(warnings) != 1 {
		t.Fatalf("got %d follow-up warnings, want 1", len(warnings))
	}
	wantField(t, warnings[0], "step", "refresh")
	wantField(t, warnings[0], "error", errServer.Error())
	if _, found := f.row(testServer, "1", "101"); found {
		t.Error("complete item still in the cache")
	}
	if got := f.onlyRun(); got.Status != store.RunFinished {
		t.Errorf("run = %+v, want a finished run", got)
	}
}

// failedFollowUp fails a store write of a match follow-up: the record of
// the follow-up refresh, or the check that applies the metadata of the item.
type failedFollowUp struct {
	Store
	refresh, check bool
}

func (s failedFollowUp) RecordRequest(r store.Request) error {
	if s.refresh && r.Action == store.ActionRefresh {
		return errDisk
	}
	return s.Store.RecordRequest(r)
}

func (s failedFollowUp) ApplyCheck(server string, it store.Item, at time.Time, runID int64) (bool, error) {
	if s.check {
		return false, errDisk
	}
	return s.Store.ApplyCheck(server, it, at, runID)
}

// A store error in the follow-up of a run's match ends the run as a store
// error of the run does: the run reports no item done, sends no other
// request, and records its failure.
func TestRunMatchFollowUpStoreErrorEndsTheRun(t *testing.T) {
	tests := []struct {
		name   string
		fail   failedFollowUp
		checks int
	}{
		{"refresh record", failedFollowUp{refresh: true}, 0},
		{"check", failedFollowUp{check: true}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, fp, checks := runMatchFixture(t, checkAnswer{item: completeItem("101")})
			f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)
			tt.fail.Store = f.store
			f.r.store = tt.fail
			p := withProgress(f)

			f.now = t0
			err := f.r.Run(t.Context())

			if !errors.Is(err, ErrStore) || !errors.Is(err, errDisk) {
				t.Errorf("Run = %v, want a store error with %v", err, errDisk)
			}
			wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
			if *checks != tt.checks {
				t.Errorf("checks = %d, want %d", *checks, tt.checks)
			}
			wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101")
			if got := f.onlyRun(); got.Status != store.RunFailed || !strings.Contains(got.Error, errDisk.Error()) {
				t.Errorf("run = %+v, want a failed run with %v", got, errDisk)
			}
		})
	}
}
