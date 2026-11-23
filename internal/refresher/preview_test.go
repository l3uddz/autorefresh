package refresher

import (
	"errors"
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// saveLibrary stores the metadata of lib, so a preview searches with its
// agent and language.
func (f *fixture) saveLibrary(lib plex.Library) {
	f.t.Helper()
	info := store.LibraryInfo{Section: lib.ID, Title: lib.Title, Type: lib.Type, Agent: lib.Agent, Language: lib.Language}
	if err := f.store.SaveLibraries(testServer, []store.LibraryInfo{info}); err != nil {
		f.t.Fatal(err)
	}
}

func TestPreviewMatchDecidesLikeARunAndRecordsNothing(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/m", Name: "Film", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.now = t0
	// A matched item that was never refreshed gets no fix match in a run,
	// but a preview evaluates any item.
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	f.saveLibrary(moviesLib)

	dec, err := f.r.PreviewMatch(t.Context(), testServer, "101")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Outcome != store.DecisionMatched || dec.Conflict != "" || !dec.At.Equal(t0) {
		t.Errorf("decision = %+v, want matched at %v", dec, t0)
	}
	if got := describeSearches(dec.Searches); len(got) != 1 || got[0] != "path tmdb-1: plex://movie/m=picked" {
		t.Errorf("searches = %q", got)
	}
	wantCalls(t, matchReads(fp.calls), "ItemPaths 101", "SearchMatches 101 tmdb-1 tv.plex.agents.movie en-US")
	wantCalls(t, sends(fp.calls))
	if events := f.timeline("101"); len(events) != 0 {
		t.Errorf("events = %+v, want none", events)
	}
	if row, _ := f.row(testServer, "1", "101"); row.LastMatchOutcome != "" {
		t.Errorf("LastMatchOutcome = %q, want none", row.LastMatchOutcome)
	}
}

func TestPreviewMatchWritesNoCache(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tmdb-77"] = show("plex://show/s")
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
	f := newTMDBFixture(t, fp, ft, "tvdb-5")

	dec, err := f.r.PreviewMatch(t.Context(), testServer, "201")
	if err != nil || dec.Outcome != store.DecisionMatched {
		t.Fatalf("decision = %+v, %v", dec, err)
	}
	if _, found, err := f.store.TMDBID(testServer, "201", "tvdb", "5"); err != nil || found {
		t.Errorf("TMDB ID cached = %v, %v", found, err)
	}
	if row, _ := f.row(testServer, "2", "201"); row.Path != "" {
		t.Errorf("path = %q, want none", row.Path)
	}
}

func TestPreviewMatchKeepsAFailureInTheDecision(t *testing.T) {
	f, fp := matchFixture(t)
	fp.searchErrs["101"] = plex.ErrUnavailable

	dec, err := f.r.PreviewMatch(t.Context(), testServer, "101")
	if err != nil || dec.Outcome != store.DecisionFailed || dec.Error == "" {
		t.Fatalf("decision = %+v, %v, want a failed decision", dec, err)
	}
}

func TestPreviewMatchErrors(t *testing.T) {
	t.Run("unknown item", func(t *testing.T) {
		f, _ := matchFixture(t)
		if _, err := f.r.PreviewMatch(t.Context(), testServer, "999"); !errors.Is(err, ErrUnknownItem) {
			t.Errorf("err = %v, want ErrUnknownItem", err)
		}
	})
	t.Run("gone from Plex", func(t *testing.T) {
		f, fp := matchFixture(t)
		fp.pathErrs["101"] = plex.ErrNotFound
		if _, err := f.r.PreviewMatch(t.Context(), testServer, "101"); !errors.Is(err, plex.ErrNotFound) {
			t.Errorf("err = %v, want plex.ErrNotFound", err)
		}
	})
	t.Run("closed", func(t *testing.T) {
		f, _ := matchFixture(t)
		f.r.Close()
		if _, err := f.r.PreviewMatch(t.Context(), testServer, "101"); !errors.Is(err, ErrClosed) {
			t.Errorf("err = %v, want ErrClosed", err)
		}
	})
}

// A preview keeps the evidence of a run: the item year, the folder year,
// the TMDB ID of the year check, and the season whose episodes differ.
func TestPreviewMatchKeepsTheEvidence(t *testing.T) {
	fp := newFakePlex(showsLib)
	old := []plex.Candidate{{GUID: "plex://show/old", Name: "Show", Year: 1978}}
	fp.candidates["201 tvdb-5"] = old
	fp.candidates["201 tmdb-77"] = old
	fp.episodeNumbers = map[string]map[int][]int{"201": {1: {1, 2}}}
	ft := &fakeTMDB{
		ids:     map[string]string{"tvdb 5 true": "77"},
		years:   map[string]int{"77 true": 1978},
		seasons: map[string][]int{"77 1": {1, 2, 3}},
	}
	f := yearConflictFixture(t, fp, ft)
	f.saveLibrary(showsLib)

	dec, err := f.r.PreviewMatch(t.Context(), testServer, "201")
	if err != nil {
		t.Fatal(err)
	}
	if dec.ItemYear != 2022 || dec.PathYear != 2022 || dec.TMDBYear != 1978 {
		t.Errorf("years = item %d, path %d, tmdb %d, want 2022, 2022, 1978", dec.ItemYear, dec.PathYear, dec.TMDBYear)
	}
	want := store.MatchEvidence{TMDBID: "77", EpisodeDiff: store.EpisodeDiff{Season: 1, Plex: 2, TMDB: 3}}
	wantEvidence(t, store.Event{MatchEvidence: dec.MatchEvidence}, want)
}
