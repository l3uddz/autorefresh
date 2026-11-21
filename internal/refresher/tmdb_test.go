package refresher

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

var _ TMDB = (*tmdb.Client)(nil)

// fakeTMDB records each Find and Year call and answers from its maps.
// Find keys are "<source> <id> <show>"; Year keys are "<id> <show>".
type fakeTMDB struct {
	ids      map[string]string
	errs     map[string]error
	years    map[string]int
	yearErrs map[string]error
	calls    []string
}

func (f *fakeTMDB) Find(_ context.Context, source, id string, show bool) (string, bool, error) {
	key := fmt.Sprintf("%s %s %v", source, id, show)
	f.calls = append(f.calls, "Find "+key)
	if err := f.errs[key]; err != nil {
		return "", false, err
	}
	tmdbID, ok := f.ids[key]
	return tmdbID, ok, nil
}

func (f *fakeTMDB) Year(_ context.Context, id string, show bool) (int, bool, error) {
	key := fmt.Sprintf("%s %v", id, show)
	f.calls = append(f.calls, "Year "+key)
	if err := f.yearErrs[key]; err != nil {
		return 0, false, err
	}
	y, ok := f.years[key]
	return y, ok, nil
}

// newTMDBFixture returns a refresh fixture with the fake TMDB, and the
// unmatched show 201 with one refresh and the path ID id.
func newTMDBFixture(t *testing.T, fp *fakePlex, ft *fakeTMDB, ids ...string) *fixture {
	t.Helper()
	path := "/tv/Show (2020)"
	for _, id := range ids {
		path += " {" + id + "}"
	}
	fp.paths["201"] = []string{path}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	if ft != nil {
		f.r.opts.TMDB = ft
	}
	f.seed(testServer, showsLib, unmatchedItem("201"), store.Unmatched, 1)
	return f
}

func show(guid string) []plex.Candidate {
	return []plex.Candidate{{GUID: guid, Name: "Show", Year: 2020}}
}

func TestTMDBFallbackMatchesSeries(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tmdb-77"] = show("plex://show/s")
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
	f := newTMDBFixture(t, fp, ft, "tvdb-5")

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s")
	wantCalls(t, ft.calls, "Find tvdb 5 true")
	sent := f.logLines("info", "Sent request")
	if len(sent) != 1 {
		t.Fatalf("got %d sent lines, want 1", len(sent))
	}
	wantMatchFields(t, sent[0], "tmdb-match", "Show", 2020, "https://www.themoviedb.org/tv/77")
	if row, _ := f.row(testServer, "2", "201"); row.LastMatch != store.ActionTMDBMatch {
		t.Errorf("LastMatch = %q, want tmdb-match", row.LastMatch)
	}
	if id, ok, err := f.store.TMDBID(testServer, "201", "tvdb", "5"); err != nil || !ok || id != "77" {
		t.Errorf("cached TMDB ID = %q, %v, %v, want 77", id, ok, err)
	}
	var from []any
	for _, ev := range f.logLines("debug", "Match search result") {
		if ev["id"] == "tmdb-77" {
			from = append(from, ev["from"])
		}
	}
	if len(from) != 1 || from[0] != "tvdb-5" {
		t.Errorf("from fields of the tmdb-77 search = %v, want [tvdb-5]", from)
	}
}

func TestTMDBFallbackUsesCache(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tmdb-77"] = show("plex://show/s")
	ft := &fakeTMDB{}
	f := newTMDBFixture(t, fp, ft, "tvdb-5")
	if err := f.store.SaveTMDBID(testServer, "201", "tvdb", "5", "77"); err != nil {
		t.Fatal(err)
	}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s")
	wantCalls(t, ft.calls)
}

func TestTMDBFallbackMatchesMovieByIMDb(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Files = []string{"/m/Film (2020) {imdb-tt1}/Film.mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	fp.candidates["101 tmdb-9"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	ft := &fakeTMDB{ids: map[string]string{"imdb tt1 false": "9"}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.TMDB = ft
	f.seed(testServer, moviesLib, movie, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/a")
	wantCalls(t, ft.calls, "Find imdb tt1 false")
}

func TestTMDBFallbackSkipped(t *testing.T) {
	t.Run("no TMDB token", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		f := newTMDBFixture(t, fp, nil, "tvdb-5")
		f.run(t0)
		wantCalls(t, sends(fp.calls), "Refresh 201")
	})
	t.Run("a normal search found a candidate", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tvdb-5"] = show("plex://show/s")
		ft := &fakeTMDB{ids: map[string]string{"imdb tt1 true": "78"}}
		f := newTMDBFixture(t, fp, ft, "tvdb-5", "imdb-tt1")
		f.run(t0)
		wantCalls(t, sends(fp.calls), "Match 201 plex://show/s")
		wantCalls(t, ft.calls)
	})
	t.Run("the path has a tmdb ID", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
		f := newTMDBFixture(t, fp, ft, "tvdb-5", "tmdb-2")
		f.run(t0)
		wantCalls(t, sends(fp.calls), "Refresh 201")
		wantCalls(t, ft.calls)
	})
	t.Run("a tvdb ID of a movie", func(t *testing.T) {
		fp := newFakePlex(moviesLib)
		movie := unmatchedItem("101")
		movie.Files = []string{"/m/Film (2020) {tvdb-5}/Film.mkv"}
		fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
		ft := &fakeTMDB{ids: map[string]string{"tvdb 5 false": "77"}}
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		f.r.opts.TMDB = ft
		f.seed(testServer, moviesLib, movie, store.Unmatched, 1)
		f.run(t0)
		wantCalls(t, sends(fp.calls), "Refresh 101")
		wantCalls(t, ft.calls)
	})
}

func TestTMDBFallbackFindsNothing(t *testing.T) {
	fp := newFakePlex(showsLib)
	ft := &fakeTMDB{}
	f := newTMDBFixture(t, fp, ft, "tvdb-5")

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	wantCalls(t, ft.calls, "Find tvdb 5 true")
	if _, ok, err := f.store.TMDBID(testServer, "201", "tvdb", "5"); err != nil || ok {
		t.Errorf("cached TMDB ID found = %v, %v, want none", ok, err)
	}
}

func TestTMDBFallbackConflict(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tmdb-77"] = show("plex://show/a")
	fp.candidates["201 tmdb-78"] = show("plex://show/b")
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77", "imdb tt1 true": "78"}}
	f := newTMDBFixture(t, fp, ft, "tvdb-5", "imdb-tt1")

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	if warnings := f.logLines("warn", "Match conflict"); len(warnings) != 1 {
		t.Errorf("got %d conflict warnings, want 1", len(warnings))
	}
}

// A failed TMDB lookup must not block the plain refresh that the item gets
// without a TMDB client. The rest of the run then asks TMDB nothing, so a
// rejected token sends one failing request per run, not one per item.
func TestTMDBFailureSendsPlainRefresh(t *testing.T) {
	for _, tmdbErr := range []error{
		errors.New("GET /3/find/5: 500 Internal Server Error"),
		fmt.Errorf("GET /3/find/5: 401 Unauthorized: %w", tmdb.ErrUnauthorized),
		errors.New("GET /3/find/5: 404 Not Found"),
	} {
		t.Run(tmdbErr.Error(), func(t *testing.T) {
			fp := newFakePlex(showsLib)
			ft := &fakeTMDB{
				ids:  map[string]string{"tvdb 6 true": "78"},
				errs: map[string]error{"tvdb 5 true": tmdbErr},
			}
			f := newTMDBFixture(t, fp, ft, "tvdb-5")
			second := unmatchedItem("202")
			second.AddedAt = addedAt.Add(time.Hour)
			fp.paths["202"] = []string{"/tv/Other (2020) {tvdb-6}"}
			f.seed(testServer, showsLib, second, store.Unmatched, 1)

			f.run(t0)

			wantCalls(t, ft.calls, "Find tvdb 5 true")
			wantCalls(t, sends(fp.calls), "Refresh 201", "Refresh 202")
			if row, _ := f.row(testServer, "2", "201"); row.Refreshes != 2 || !row.LastAttempt.Equal(t0) {
				t.Errorf("row = %+v, want 2 refreshes and LastAttempt %v", row, t0)
			}
			warnings := f.logLines("warn", "TMDB lookup failed")
			if len(warnings) != 1 {
				t.Fatalf("got %d TMDB warnings, want 1", len(warnings))
			}
			wantField(t, warnings[0], "title", "Title 201")
			wantField(t, f.lastSummary(testServer), "failed_requests", float64(1))

			// The next run asks TMDB again.
			ft.calls = nil
			f.run(t0.Add(48 * time.Hour))
			wantCalls(t, ft.calls, "Find tvdb 5 true")
		})
	}
}

// failingCache is a store whose TMDB ID reads fail.
type failingCache struct{ *store.Store }

var errDisk = errors.New("disk failed")

func (failingCache) TMDBID(string, string, string, string) (string, bool, error) {
	return "", false, errDisk
}

func TestTMDBCacheErrorStopsRun(t *testing.T) {
	fp := newFakePlex(showsLib)
	ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
	f := newTMDBFixture(t, fp, ft, "tvdb-5")
	f.r.store = failingCache{f.store}

	f.now = t0
	if err := f.r.Run(context.Background()); !errors.Is(err, errDisk) {
		t.Fatalf("Run = %v, want the store error", err)
	}
	if warnings := f.logLines("warn", "TMDB lookup failed"); len(warnings) != 0 {
		t.Errorf("got TMDB warnings %v, want none", warnings)
	}
	wantCalls(t, ft.calls)
}

func TestTMDBFallbackDryRun(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tmdb-77"] = show("plex://show/s")
		ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}}
		f := newTMDBFixture(t, fp, ft, "tvdb-5")
		f.r.opts.DryRun = true

		f.run(t0)

		wantCalls(t, sends(fp.calls))
		wantCalls(t, ft.calls, "Find tvdb 5 true")
		lines := f.logLines("info", "Dry run")
		if len(lines) != 1 {
			t.Fatalf("got %d dry run lines, want 1", len(lines))
		}
		wantMatchFields(t, lines[0], "tmdb-match", "Show", 2020, "https://www.themoviedb.org/tv/77")
		// A dry run caches the TMDB ID, as its reads update the cache.
		if id, ok, err := f.store.TMDBID(testServer, "201", "tvdb", "5"); err != nil || !ok || id != "77" {
			t.Errorf("cached TMDB ID = %q, %v, %v, want 77", id, ok, err)
		}
		if row, _ := f.row(testServer, "2", "201"); row.LastAttempt.Equal(t0) || row.Refreshes != 1 || row.LastMatch != "" {
			t.Errorf("row = %+v, want no new attempt and no match", row)
		}
	})
	t.Run("failed", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		ft := &fakeTMDB{errs: map[string]error{"tvdb 5 true": errors.New("GET /3/find/5: 500 Internal Server Error")}}
		f := newTMDBFixture(t, fp, ft, "tvdb-5")
		f.r.opts.DryRun = true

		f.run(t0)

		if warnings := f.logLines("warn", "TMDB lookup failed"); len(warnings) != 1 {
			t.Errorf("got %d TMDB warnings, want 1", len(warnings))
		}
		lines := f.logLines("info", "Dry run")
		if len(lines) != 1 {
			t.Fatalf("got %d dry run lines, want 1", len(lines))
		}
		wantField(t, lines[0], "action", "refresh")
		if row, _ := f.row(testServer, "2", "201"); row.LastAttempt.Equal(t0) || row.Refreshes != 1 {
			t.Errorf("row = %+v, want no new attempt", row)
		}
	})
}

// cancelTMDB cancels the run during its Find or Year call, which then fails.
type cancelTMDB struct{ cancel context.CancelFunc }

func (c cancelTMDB) Find(context.Context, string, string, bool) (string, bool, error) {
	c.cancel()
	return "", false, fmt.Errorf("GET /3/find/5: %w", context.Canceled)
}

func (c cancelTMDB) Year(context.Context, string, bool) (int, bool, error) {
	c.cancel()
	return 0, false, fmt.Errorf("GET /3/tv/77: %w", context.Canceled)
}

func TestTMDBFindCanceled(t *testing.T) {
	fp := newFakePlex(showsLib)
	f := newTMDBFixture(t, fp, nil, "tvdb-5")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.r.opts.TMDB = cancelTMDB{cancel}

	f.now = t0
	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	wantCalls(t, sends(fp.calls))
	if warnings := f.logLines("warn", "TMDB lookup failed"); len(warnings) != 0 {
		t.Errorf("got TMDB warnings %v, want none", warnings)
	}
	if lines := f.logLines("info", "Run summary"); len(lines) != 0 {
		t.Errorf("got summary lines %v, want none", lines)
	}
	if row, _ := f.row(testServer, "2", "201"); row.LastAttempt.Equal(t0) || row.Refreshes != 1 {
		t.Errorf("row = %+v, want no new attempt", row)
	}
}

func TestYearConfirmSeriesViaTMDB(t *testing.T) {
	fp := newFakePlex(showsLib)
	cand := []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 2017}}
	fp.candidates["201 tvdb-5"] = cand
	fp.candidates["201 tmdb-77"] = cand
	ft := &fakeTMDB{
		ids:   map[string]string{"tvdb 5 true": "77"},
		years: map[string]int{"77 true": 2022},
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.TMDB = ft
	it := unmatchedItem("201")
	it.Year = 2022
	fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5}"}
	f.seed(testServer, showsLib, it, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s")
	wantCalls(t, ft.calls, "Find tvdb 5 true", "Year 77 true")
	sent := f.logLines("info", "Sent request")
	if len(sent) != 1 {
		t.Fatalf("got %d sent lines, want 1", len(sent))
	}
	wantField(t, sent[0], "action", "match")
	wantField(t, sent[0], "item_year", float64(2022))
	wantField(t, sent[0], "candidate_year", float64(2017))
	wantField(t, sent[0], "tmdb_year", float64(2022))
	wantLinks := []any{"https://thetvdb.com/dereferrer/series/5", "https://www.themoviedb.org/tv/77"}
	if got, _ := sent[0]["links"].([]any); fmt.Sprint(got) != fmt.Sprint(wantLinks) {
		t.Errorf("links = %v, want %v", got, wantLinks)
	}
}

func TestYearConfirmMovieViaTMDBFallback(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Year = 1984
	movie.Files = []string{"/m/Film (1984)/Film (1984) {imdb-tt1}.mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	fp.candidates["101 tmdb-9"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 1991}}
	ft := &fakeTMDB{
		ids:   map[string]string{"imdb tt1 false": "9"},
		years: map[string]int{"9 false": 1984},
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.TMDB = ft
	f.seed(testServer, moviesLib, movie, store.Unmatched, 1)

	f.run(t0)

	// The fallback already searched tmdb-9, so year confirmation sends no
	// duplicate SearchMatches call and keeps action as tmdb-match.
	wantCalls(t, matchReads(fp.calls),
		"SearchMatches 101 imdb-tt1 tv.plex.agents.movie en-US",
		"SearchMatches 101 tmdb-9 tv.plex.agents.movie en-US",
	)
	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/a")
	wantCalls(t, ft.calls, "Find imdb tt1 false", "Year 9 false")
	sent := f.logLines("info", "Sent request")
	if len(sent) != 1 {
		t.Fatalf("got %d sent lines, want 1", len(sent))
	}
	wantField(t, sent[0], "action", "tmdb-match")
	wantField(t, sent[0], "tmdb_year", float64(1984))
	wantLinks := []any{"https://www.themoviedb.org/movie/9"}
	if got, _ := sent[0]["links"].([]any); fmt.Sprint(got) != fmt.Sprint(wantLinks) {
		t.Errorf("links = %v, want %v", got, wantLinks)
	}
}

// When Plex misparses a bare year in a title (e.g. "Concert 2000 (2018)"),
// the unambiguous parenthesized path year confirms the match without TMDB.
func TestYearConfirmViaPathYearWithoutTMDB(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Year = 2000
	movie.Files = []string{"/m/Concert 2000 (2018) {imdb-tt1}/Concert 2000 (2018).mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	fp.candidates["101 imdb-tt1"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Concert 2000", Year: 2018}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, movie, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/a")
	sent := f.logLines("info", "Sent request")
	if len(sent) != 1 {
		t.Fatalf("got %d sent lines, want 1", len(sent))
	}
	wantField(t, sent[0], "action", "match")
	wantField(t, sent[0], "item_year", float64(2000))
	wantField(t, sent[0], "path_year", float64(2018))
	wantField(t, sent[0], "candidate_year", float64(2018))
}

// When the path year disagrees with the candidate (2018 vs 2016) and the item
// year was misparsed (2000), TMDB confirming the path year (2018) still
// accepts the match after GUID verification.
func TestYearConfirmPathYearMatchesTMDB(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Year = 2000
	movie.Files = []string{"/m/Concert 2000 (2018) {imdb-tt1}/Concert 2000 (2018).mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	cand := []plex.Candidate{{GUID: "plex://movie/a", Name: "Concert 2000", Year: 2016}}
	fp.candidates["101 imdb-tt1"] = cand
	fp.candidates["101 tmdb-9"] = cand
	ft := &fakeTMDB{
		ids:   map[string]string{"imdb tt1 false": "9"},
		years: map[string]int{"9 false": 2018},
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.TMDB = ft
	f.seed(testServer, moviesLib, movie, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/a")
	wantCalls(t, ft.calls, "Find imdb tt1 false", "Year 9 false")
	sent := f.logLines("info", "Sent request")
	if len(sent) != 1 {
		t.Fatalf("got %d sent lines, want 1", len(sent))
	}
	wantField(t, sent[0], "action", "match")
	wantField(t, sent[0], "tmdb_year", float64(2018))
}

func TestYearConfirmRejectsUnconfirmedConflicts(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(fp *fakePlex, ft *fakeTMDB)
		noTMDB       bool
		wantConflict string
		wantTMDBYear float64
		wantTMDBNone bool
	}{
		{
			name: "remake year still differs",
			setup: func(fp *fakePlex, ft *fakeTMDB) {
				cand := []plex.Candidate{{GUID: "plex://show/old", Name: "Show", Year: 1978}}
				fp.candidates["201 tvdb-5"] = cand
				fp.candidates["201 tmdb-77"] = cand
				ft.ids["tvdb 5 true"] = "77"
				ft.years["77 true"] = 1978
			},
			wantConflict: "year differs",
			wantTMDBYear: 1978,
		},
		{
			name: "tmdb plex search returns different guid",
			setup: func(fp *fakePlex, ft *fakeTMDB) {
				fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
				fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/b", Name: "Show", Year: 2022}}
				ft.ids["tvdb 5 true"] = "77"
				ft.years["77 true"] = 2022
			},
			wantConflict: "year differs",
		},
		{
			name: "tmdb plex search returns no candidate",
			setup: func(fp *fakePlex, ft *fakeTMDB) {
				fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
				ft.ids["tvdb 5 true"] = "77"
				ft.years["77 true"] = 2022
			},
			wantConflict: "year differs",
		},
		{
			name: "no tmdb token",
			setup: func(fp *fakePlex, _ *fakeTMDB) {
				fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
			},
			noTMDB:       true,
			wantConflict: "year differs",
			wantTMDBNone: true,
		},
		{
			name: "multi-id different guids skips tmdb confirmation",
			setup: func(fp *fakePlex, ft *fakeTMDB) {
				fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5} {imdb-tt1}"}
				fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
				fp.candidates["201 imdb-tt1"] = []plex.Candidate{{GUID: "plex://show/b", Name: "Other", Year: 2022}}
				ft.ids["tvdb 5 true"] = "77"
				ft.years["77 true"] = 2022
			},
			wantConflict: "different candidates",
			wantTMDBNone: true,
		},
		{
			name: "stale tmdb path id disagrees with translated tvdb id",
			setup: func(fp *fakePlex, ft *fakeTMDB) {
				fp.paths["201"] = []string{"/tv/Show (2022) {tmdb-99} {tvdb-5}"}
				fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
				ft.ids["tvdb 5 true"] = "77"
				ft.years["77 true"] = 2022
			},
			wantConflict: "year differs",
		},
		{
			name: "two different matched tmdb ids stay unconfirmed",
			setup: func(fp *fakePlex, ft *fakeTMDB) {
				fp.paths["201"] = []string{"/tv/Show (2022) {tmdb-77} {tmdb-78}"}
				cand := []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
				fp.candidates["201 tmdb-77"] = cand
				fp.candidates["201 tmdb-78"] = cand
				ft.years["77 true"] = 2022
				ft.years["78 true"] = 2022
			},
			wantConflict: "year differs",
			wantTMDBNone: true,
		},
		{
			name: "already searched empty tmdb id is not searched again",
			setup: func(fp *fakePlex, ft *fakeTMDB) {
				fp.paths["201"] = []string{"/tv/Show (2022) {tmdb-77} {tvdb-5}"}
				fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
				ft.ids["tvdb 5 true"] = "77"
				ft.years["77 true"] = 2022
			},
			wantConflict: "year differs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := newFakePlex(showsLib)
			ft := &fakeTMDB{ids: map[string]string{}, years: map[string]int{}}
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			if !tt.noTMDB {
				f.r.opts.TMDB = ft
			}
			it := unmatchedItem("201")
			it.Year = 2022
			fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5}"}
			f.seed(testServer, showsLib, it, store.Unmatched, 1)
			tt.setup(fp, ft)

			f.run(t0)

			wantCalls(t, sends(fp.calls), "Refresh 201")
			if tt.wantTMDBNone {
				wantCalls(t, ft.calls)
			}
			warnings := f.logLines("warn", "Match conflict")
			if len(warnings) != 1 {
				t.Fatalf("got %d conflict warnings, want 1", len(warnings))
			}
			wantField(t, warnings[0], "conflict", tt.wantConflict)
			if tt.wantTMDBYear != 0 {
				wantField(t, warnings[0], "tmdb_year", tt.wantTMDBYear)
			}
		})
	}
}

func TestYearConfirmTMDBYearFailureDisablesTMDB(t *testing.T) {
	fp := newFakePlex(showsLib)
	cand1 := []plex.Candidate{{GUID: "plex://show/a", Name: "Show 1", Year: 2017}}
	cand2 := []plex.Candidate{{GUID: "plex://show/b", Name: "Show 2", Year: 2017}}
	fp.candidates["201 tvdb-5"] = cand1
	fp.candidates["201 tmdb-77"] = cand1
	fp.candidates["202 tvdb-6"] = cand2
	fp.candidates["202 tmdb-78"] = cand2
	ft := &fakeTMDB{
		ids:      map[string]string{"tvdb 5 true": "77", "tvdb 6 true": "78"},
		years:    map[string]int{"78 true": 2022},
		yearErrs: map[string]error{"77 true": errors.New("GET /3/tv/77: 500 Internal Server Error")},
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.TMDB = ft
	first := unmatchedItem("201")
	first.Year = 2022
	fp.paths["201"] = []string{"/tv/Show 1 (2022) {tvdb-5}"}
	f.seed(testServer, showsLib, first, store.Unmatched, 1)

	second := unmatchedItem("202")
	second.Year = 2022
	second.AddedAt = addedAt.Add(time.Hour)
	fp.paths["202"] = []string{"/tv/Show 2 (2022) {tvdb-6}"}
	f.seed(testServer, showsLib, second, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, ft.calls, "Find tvdb 5 true", "Year 77 true")
	wantCalls(t, sends(fp.calls), "Refresh 201", "Refresh 202")
	if warnings := f.logLines("warn", "TMDB lookup failed"); len(warnings) != 1 {
		t.Errorf("got %d TMDB warnings, want 1", len(warnings))
	}
	wantField(t, f.lastSummary(testServer), "failed_requests", float64(1))
}

func TestYearConfirmCanceledDuringTMDBYearStopsQuietly(t *testing.T) {
	fp := newFakePlex(showsLib)
	cand := []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 2017}}
	fp.candidates["201 tmdb-77"] = cand
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	it := unmatchedItem("201")
	it.Year = 2022
	fp.paths["201"] = []string{"/tv/Show (2022) {tmdb-77}"}
	f.seed(testServer, showsLib, it, store.Unmatched, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.r.opts.TMDB = cancelTMDB{cancel}

	f.now = t0
	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	wantCalls(t, sends(fp.calls))
	if warnings := f.logLines("warn", "TMDB lookup failed"); len(warnings) != 0 {
		t.Errorf("got TMDB warnings %v, want none", warnings)
	}
	if lines := f.logLines("info", "Run summary"); len(lines) != 0 {
		t.Errorf("got summary lines %v, want none", lines)
	}
}

// A failed Plex verification search during year confirmation propagates as a
// failed match search: no request is sent and refreshes do not increase.
func TestYearConfirmPlexVerificationErrorFailsSearch(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
	fp.searchErrs["201 tmdb-77"] = errServer
	ft := &fakeTMDB{
		ids:   map[string]string{"tvdb 5 true": "77"},
		years: map[string]int{"77 true": 2022},
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.TMDB = ft
	it := unmatchedItem("201")
	it.Year = 2022
	fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5}"}
	f.seed(testServer, showsLib, it, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls))
	if row, _ := f.row(testServer, "2", "201"); row.Refreshes != 1 || !row.LastAttempt.Equal(t0) {
		t.Errorf("row = %+v, want 1 refresh and LastAttempt %v", row, t0)
	}
	if warnings := f.logLines("warn", "Match search failed"); len(warnings) != 1 {
		t.Errorf("got %d search failure warnings, want 1", len(warnings))
	}
	wantField(t, f.lastSummary(testServer), "failed_requests", float64(1))
}

func TestYearConfirmFindFailureAndCacheError(t *testing.T) {
	t.Run("find failure disables TMDB and sends plain refresh", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
		ft := &fakeTMDB{errs: map[string]error{"tvdb 5 true": errors.New("GET /3/find/5: 500 Internal Server Error")}}
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		f.r.opts.TMDB = ft
		it := unmatchedItem("201")
		it.Year = 2022
		fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5}"}
		f.seed(testServer, showsLib, it, store.Unmatched, 1)

		f.run(t0)

		wantCalls(t, sends(fp.calls), "Refresh 201")
		if warnings := f.logLines("warn", "TMDB lookup failed"); len(warnings) != 1 {
			t.Errorf("got %d TMDB warnings, want 1", len(warnings))
		}
		wantField(t, f.lastSummary(testServer), "failed_requests", float64(1))
	})

	t.Run("cache error stops run", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/a", Name: "Show", Year: 2017}}
		ft := &fakeTMDB{ids: map[string]string{"tvdb 5 true": "77"}, years: map[string]int{"77 true": 2022}}
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		f.r.opts.TMDB = ft
		it := unmatchedItem("201")
		it.Year = 2022
		fp.paths["201"] = []string{"/tv/Show (2022) {tvdb-5}"}
		f.seed(testServer, showsLib, it, store.Unmatched, 1)
		f.r.store = failingCache{f.store}

		f.now = t0
		if err := f.r.Run(context.Background()); !errors.Is(err, errDisk) {
			t.Fatalf("Run = %v, want store error", err)
		}
		wantCalls(t, ft.calls)
	})
}
