package refresher

import (
	"fmt"
	"slices"
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// The title search of the unmatched show 201 ("Title 201", 2020) and the
// candidates that it finds: the series itself, one with another title, and
// one with the same title from another year.
const titleSearch = "201 Title 201"

func titleCandidates() []plex.Candidate {
	return []plex.Candidate{
		{GUID: "plex://show/t", Name: "Title 201", Year: 2020, Type: "show"},
		{GUID: "plex://show/o", Name: "Title 2010", Year: 2020, Type: "show"},
		{GUID: "plex://show/y", Name: "title-201", Year: 2015, Type: "show"},
	}
}

// titleFixture returns the show 201 with the path IDs ids, the title search
// candidates of titleCandidates, a provider TMDB ID 88 for plex://show/t,
// and local and TMDB episodes 1 to 3 of season 1.
func titleFixture(t *testing.T, ids ...string) (*fixture, *fakePlex, *fakeTMDB) {
	t.Helper()
	fp := newFakePlex(showsLib)
	fp.candidates[titleSearch] = titleCandidates()
	fp.series = map[string]plex.Series{"plex://show/t": {TMDBID: "88"}}
	fp.episodeNumbers = map[string]map[int][]int{"201": {1: {1, 2, 3}}}
	ft := &fakeTMDB{seasons: map[string][]int{"88 1": {1, 2, 3}}}
	return newTMDBFixture(t, fp, ft, ids...), fp, ft
}

const titleVerdicts = "title Title 201: plex://show/t=picked plex://show/o=other title plex://show/y=other title"

func TestTitleMatchWithoutFolderIDs(t *testing.T) {
	f, fp, ft := titleFixture(t)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 201 plex://show/t", "Refresh 201")
	wantCalls(t, ft.calls, "SeasonEpisodes 88 1")
	e := f.decision("201")
	wantDecision(t, e, store.DecisionMatched, "", titleVerdicts)
	wantYears(t, e, 0, 0, 3)
	wantEvidence(t, e, store.MatchEvidence{TMDBID: "88"})
	sent := f.logLines("info", "Sent request")
	if len(sent) == 0 {
		t.Fatal("no Sent request line")
	}
	wantMatchFields(t, sent[0], "title-match", "Title 201", 2020, "https://www.themoviedb.org/tv/88")
	if req := f.matchRequest("201"); req.Action != store.ActionTitleMatch || !req.Accepted {
		t.Errorf("match request = %+v, want an accepted title-match", req)
	}
}

func TestTitleMatchAfterFolderIDsFindNothing(t *testing.T) {
	f, fp, ft := titleFixture(t, "imdb-tt1")

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 201 plex://show/t", "Refresh 201")
	wantCalls(t, ft.calls, "Find imdb tt1 true", "SeasonEpisodes 88 1")
	e := f.decision("201")
	wantDecision(t, e, store.DecisionMatched, "", "path imdb-tt1:", titleVerdicts)
	wantEvidence(t, e, store.MatchEvidence{TMDBID: "88", TMDBMissing: []string{"imdb-tt1"}})
}

func TestTitleMatchEpisodesDiffer(t *testing.T) {
	f, fp, ft := titleFixture(t)
	ft.seasons["88 1"] = []int{1, 2}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	e := f.decision("201")
	wantDecision(t, e, store.DecisionNoMatch, "not confirmed",
		"title Title 201: plex://show/t=not confirmed plex://show/o=other title plex://show/y=other title")
	wantYears(t, e, 0, 0, 0)
	wantEvidence(t, e, store.MatchEvidence{TMDBID: "88",
		EpisodeDiff: store.EpisodeDiff{Season: 1, Plex: 3, TMDB: 2, Extra: []int{3}}})
}

func TestTitleMatchSeveralWithTheSameTitle(t *testing.T) {
	f, fp, _ := titleFixture(t)
	fp.candidates[titleSearch] = append(titleCandidates(), plex.Candidate{GUID: "plex://show/u", Name: "Title 201", Year: 2021})

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	if slices.ContainsFunc(fp.calls, func(c string) bool { return c == "ProviderSeries plex://show/t" }) {
		t.Errorf("calls = %q, want no provider lookup", fp.calls)
	}
	wantDecision(t, f.decision("201"), store.DecisionNoMatch, "several candidates",
		"title Title 201: plex://show/t=several candidates plex://show/o=other title plex://show/y=other title plex://show/u=several candidates")
}

func TestTitleMatchFolderTMDBIDDiffers(t *testing.T) {
	f, fp, ft := titleFixture(t, "tmdb-99")

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	wantCalls(t, ft.calls)
	e := f.decision("201")
	wantDecision(t, e, store.DecisionNoMatch, "folder id differs", "path tmdb-99:",
		"title Title 201: plex://show/t=folder id differs plex://show/o=other title plex://show/y=other title")
	wantEvidence(t, e, store.MatchEvidence{TMDBID: "88", TMDBSkip: store.TMDBHasID})
}

func TestTitleMatchProviderWithoutTMDBID(t *testing.T) {
	for name, series := range map[string]map[string]plex.Series{
		"unknown GUID": {},
		"no TMDB ID":   {"plex://show/t": {}},
	} {
		t.Run(name, func(t *testing.T) {
			f, fp, ft := titleFixture(t)
			fp.series = series

			f.run(t0)

			wantCalls(t, sends(fp.calls), "Refresh 201")
			wantCalls(t, ft.calls)
			wantDecision(t, f.decision("201"), store.DecisionNoMatch, "not confirmed",
				"title Title 201: plex://show/t=not confirmed plex://show/o=other title plex://show/y=other title")
			if _, cached := f.row(testServer, "2", "201"); !cached {
				t.Error("item left the cache: an unknown GUID is not a gone item")
			}
		})
	}
}

// A failed provider lookup is a handled failure: Plex itself answered, so
// the run goes on and the item gets a plain refresh.
func TestTitleMatchProviderFailure(t *testing.T) {
	f, fp, _ := titleFixture(t)
	fp.seriesErrs = map[string]error{"plex://show/t": fmt.Errorf("provider series: %w", plex.ErrUnavailable)}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	e := f.decision("201")
	if store.DecisionOutcome(e.Outcome) != store.DecisionFailed || e.Error == "" {
		t.Errorf("decision = %q (%q), want failed with the error", e.Outcome, e.Error)
	}
	if run := f.onlyRun(); run.Status != store.RunFinished || run.Failed != 1 {
		t.Errorf("run = %+v, want a finished run with 1 failure", run)
	}
}

func TestTitleFallbackSkipped(t *testing.T) {
	cases := map[string]func(*fixture, *fakePlex){
		"TMDB off": func(f *fixture, _ *fakePlex) { f.r.opts.TMDB = nil },
		"no item year": func(f *fixture, fp *fakePlex) {
			it := unmatchedItem("201")
			it.Year = 0
			f.seed(testServer, showsLib, it, store.Unmatched, 1)
		},
		"an ID search found a candidate": func(_ *fixture, fp *fakePlex) {
			fp.paths["201"] = []string{"/tv/Show (2020) {tvdb-5}"}
			fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "A"}, {GUID: "plex://show/b", Name: "B"}}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f, fp, _ := titleFixture(t)
			setup(f, fp)

			f.run(t0)

			wantCalls(t, sends(fp.calls), "Refresh 201")
			if slices.Contains(fp.calls, "SearchMatches 201 Title 201 tv.plex.agents.series en-US") {
				t.Errorf("calls = %q, want no title search", fp.calls)
			}
		})
	}
}

func TestTitleFallbackSkipsMovies(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020)/Film.mkv"}
	fp.candidates["101 Title 101"] = []plex.Candidate{{GUID: "plex://movie/m", Name: "Title 101", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.TMDB = &fakeTMDB{}
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, matchReads(fp.calls), "ItemPaths 101")
	wantDecision(t, f.decision("101"), store.DecisionNoIDs, "")
}

func TestPreviewTitleMatchRecordsNothing(t *testing.T) {
	f, fp, _ := titleFixture(t)
	f.saveLibrary(showsLib)
	before := len(f.timeline("201"))

	dec, err := f.r.PreviewMatch(t.Context(), testServer, "201")
	if err != nil || dec.Outcome != store.DecisionMatched {
		t.Fatalf("decision = %+v, %v", dec, err)
	}
	if got := describeSearches(dec.Searches); !slices.Equal(got, []string{titleVerdicts}) {
		t.Errorf("searches = %q", got)
	}
	wantCalls(t, sends(fp.calls))
	if events := f.timeline("201"); len(events) != before {
		t.Errorf("events = %+v, want only the %d seeded", events, before)
	}
}
