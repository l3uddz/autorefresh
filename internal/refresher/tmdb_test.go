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

// fakeTMDB records each Find call as "Find <source> <id> <show>" and
// answers from ids and errs, both by "<source> <id> <show>".
type fakeTMDB struct {
	ids   map[string]string
	errs  map[string]error
	calls []string
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

// cancelTMDB cancels the run during its Find call, and Find then fails.
type cancelTMDB struct{ cancel context.CancelFunc }

func (c cancelTMDB) Find(context.Context, string, string, bool) (string, bool, error) {
	c.cancel()
	return "", false, fmt.Errorf("GET /3/find/5: %w", context.Canceled)
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
