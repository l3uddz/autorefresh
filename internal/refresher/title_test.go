package refresher

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
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
	if year := fp.searchYears[titleSearch]; year != 2020 {
		t.Errorf("title search year = %d, want the item year 2020", year)
	}
	e := f.decision("201")
	wantDecision(t, e, store.DecisionMatched, "", titleVerdicts)
	wantYears(t, e, 2020, 0, 3)
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
	wantYears(t, e, 2020, 0, 0)
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

// Every TMDB ID of the folder names must be the provider TMDB ID, also
// when one of them is: the folder then names two series.
func TestTitleMatchAnyFolderTMDBIDDiffers(t *testing.T) {
	for name, setup := range map[string]func(*fakeTMDB) []string{
		"two tmdb IDs": func(*fakeTMDB) []string { return []string{"tmdb-88", "tmdb-99"} },
		"a tmdb ID and a translated ID": func(ft *fakeTMDB) []string {
			ft.ids = map[string]string{"imdb tt1 true": "99"}
			return []string{"tmdb-88", "imdb-tt1"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			fp := newFakePlex(showsLib)
			fp.candidates[titleSearch] = titleCandidates()
			fp.series = map[string]plex.Series{"plex://show/t": {TMDBID: "88"}}
			fp.episodeNumbers = map[string]map[int][]int{"201": {1: {1, 2, 3}}}
			ft := &fakeTMDB{seasons: map[string][]int{"88 1": {1, 2, 3}}}
			f := newTMDBFixture(t, fp, ft, setup(ft)...)

			f.run(t0)

			wantCalls(t, sends(fp.calls), "Refresh 201")
			if e := f.decision("201"); e.Conflict != "folder id differs" {
				t.Errorf("conflict = %q, want folder id differs", e.Conflict)
			}
		})
	}
}

// A folder ID that TMDB has several entries for names no single series,
// so it vetoes the title match, also when the TMDB fallback asked first.
func TestTitleMatchFolderIDWithSeveralTMDBEntries(t *testing.T) {
	f, fp, ft := titleFixture(t, "tvdb-5")
	ft.errs = map[string]error{"tvdb 5 true": tmdb.ErrSeveral}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	wantCalls(t, ft.calls, "Find tvdb 5 true")
	e := f.decision("201")
	if e.Conflict != "folder id differs" || store.DecisionOutcome(e.Outcome) != store.DecisionNoMatch {
		t.Errorf("decision = %q · %q, want no-match · folder id differs", e.Outcome, e.Conflict)
	}
	wantEvidence(t, e, store.MatchEvidence{TMDBID: "88", TMDBMissing: []string{"tvdb-5"}})
}

// A title candidate whose year differs from the year of the series folder
// is another series, such as a remake.
func TestTitleMatchFolderYearDiffers(t *testing.T) {
	f, fp, _ := titleFixture(t)
	fp.paths["201"] = []string{"/tv/Title 201 (2012)"}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	e := f.decision("201")
	wantDecision(t, e, store.DecisionNoMatch, "",
		"title Title 201: plex://show/t=other title plex://show/o=other title plex://show/y=other title")
	wantYears(t, e, 2012, 0, 0)
}

func TestTitleAgrees(t *testing.T) {
	it := store.Item{Title: "Limitless With Chris Hemsworth", Year: 2022}
	for _, c := range []struct {
		name string
		year int
		want bool
	}{
		{"Limitless with Chris Hemsworth", 2022, true},
		{"Limitless: with Chris-Hemsworth!", 2023, true},
		{"Limitless", 2022, false},
		{"Limitless with Chris Hemsworth", 2025, false},
		{"Limitless with Chris Hemsworth", 0, false},
	} {
		if got := titleAgrees(it, 0, c.name, c.year); got != c.want {
			t.Errorf("titleAgrees(%q, %d) = %v, want %v", c.name, c.year, got, c.want)
		}
	}
	// Vowel signs are marks, and they tell these titles apart.
	if titleAgrees(store.Item{Title: "कला", Year: 2020}, 0, "कली", 2020) {
		t.Error("titles that differ in a vowel sign agree")
	}
	if titleAgrees(store.Item{Title: "!!!", Year: 2020}, 0, "???", 2020) {
		t.Error("titles without letters or digits agree")
	}
	// A combining mark alone is no letter.
	if titleAgrees(store.Item{Title: "\u0301", Year: 2020}, 0, "\u0301", 2020) {
		t.Error("titles of marks alone agree")
	}
	// Symbols tell titles apart.
	if titleAgrees(store.Item{Title: "Example+", Year: 2020}, 0, "Example", 2020) {
		t.Error("Example+ agrees with Example")
	}
	// The folder year must agree too.
	if titleAgrees(store.Item{Title: "Example", Year: 2022}, 2012, "Example", 2022) {
		t.Error("a candidate of 2022 agrees with the folder year 2012")
	}
	if !titleAgrees(store.Item{Title: "Example", Year: 2022}, 2022, "Example", 2022) {
		t.Error("a candidate of 2022 does not agree with the folder year 2022")
	}
}

// A TMDB failure in the episode check is a failed decision with a plain
// refresh. It turns TMDB off for the rest of the run, so the next series
// gets no title search.
func TestTitleMatchTMDBFailureTurnsTitleSearchOff(t *testing.T) {
	f, fp, ft := titleFixture(t)
	ft.seasonErrs = map[string]error{"88 1": fmt.Errorf("GET /3/tv/88/season/1: 500 Internal Server Error")}
	second := unmatchedItem("202")
	second.AddedAt = addedAt.Add(time.Hour)
	f.seed(testServer, showsLib, second, store.Unmatched, 1)
	fp.candidates["202 Title 202"] = []plex.Candidate{{GUID: "plex://show/t2", Name: "Title 202", Year: 2020}}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201", "Refresh 202")
	if e := f.decision("201"); store.DecisionOutcome(e.Outcome) != store.DecisionFailed || e.Error == "" {
		t.Errorf("decision of 201 = %q (%q), want failed with the error", e.Outcome, e.Error)
	}
	if slices.ContainsFunc(fp.calls, func(c string) bool { return strings.HasPrefix(c, "SearchMatches 202 Title 202") }) {
		t.Errorf("calls = %q, want no title search of 202", fp.calls)
	}
	if run := f.onlyRun(); run.Status != store.RunFinished {
		t.Errorf("run = %+v, want a finished run", run)
	}
}

// A failed title search is a handled failure: before the title fallback,
// the item got a plain refresh, so it still gets one, and the run goes on
// to the next item.
func TestTitleSearchFailureSendsPlainRefresh(t *testing.T) {
	f, fp, _ := titleFixture(t)
	fp.searchErrs[titleSearch] = fmt.Errorf("504 Gateway Timeout: %w", plex.ErrUnavailable)
	second := unmatchedItem("202")
	second.AddedAt = addedAt.Add(time.Hour)
	f.seed(testServer, showsLib, second, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201", "Refresh 202")
	e := f.decision("201")
	if store.DecisionOutcome(e.Outcome) != store.DecisionFailed || !strings.Contains(e.Error, "504") {
		t.Errorf("decision = %q (%q), want failed with the search error", e.Outcome, e.Error)
	}
	wantDecision(t, e, store.DecisionFailed, "", "title Title 201 failed:")
	if run := f.onlyRun(); run.Status != store.RunFinished || run.Failed != 1 {
		t.Errorf("run = %+v, want a finished run with 1 failure", run)
	}
}

// Folders with different years, such as two volumes of one item, name no
// single series, so the title fallback does not run.
func TestTitleFallbackSkipsFoldersWithDifferentYears(t *testing.T) {
	f, fp, _ := titleFixture(t)
	fp.paths["201"] = []string{"/tv/Title 201 (2012)", "/tv/Title 201 (2020)"}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	wantCalls(t, matchReads(fp.calls), "ItemPaths 201")
	wantDecision(t, f.decision("201"), store.DecisionNoIDs, "")
}

// A failed episode read of the title fallback, such as a 504 for a large
// series, is a handled failure too.
func TestTitleEpisodeReadFailureSendsPlainRefresh(t *testing.T) {
	f, fp, _ := titleFixture(t)
	fp.episodeReadErr = fmt.Errorf("504 Gateway Timeout: %w", plex.ErrUnavailable)
	second := unmatchedItem("202")
	second.AddedAt = addedAt.Add(time.Hour)
	f.seed(testServer, showsLib, second, store.Unmatched, 1)
	fp.candidates["202 Title 202"] = []plex.Candidate{{GUID: "plex://show/t2", Name: "Title 202", Year: 2020}}
	fp.series["plex://show/t2"] = plex.Series{TMDBID: "89"}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201", "Refresh 202")
	for _, key := range []string{"201", "202"} {
		if e := f.decision(key); store.DecisionOutcome(e.Outcome) != store.DecisionFailed || !strings.Contains(e.Error, "504") {
			t.Errorf("decision of %s = %q (%q), want failed with the read error", key, e.Outcome, e.Error)
		}
	}
	if run := f.onlyRun(); run.Status != store.RunFinished || run.Failed != 2 {
		t.Errorf("run = %+v, want a finished run with 2 failures", run)
	}
}
