package refresher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

const refreshDelay = 5 * time.Second

var (
	// seedAt is the time of the seeded read. A run at t0 reads incrementally.
	seedAt = t0.Add(-24 * time.Hour)
	// seedAttempt is the time of the seeded refreshes. The row is due at t0.
	seedAttempt    = t0.Add(-48 * time.Hour)
	errServer      = errors.New("500 Internal Server Error")
	errUnavailable = fmt.Errorf("refresh: %w", plex.ErrUnavailable)
	errNotFound    = fmt.Errorf("refresh: %w", plex.ErrNotFound)
)

// newRefreshFixture returns a fixture that sends refresh and match requests
// with a refresh delay.
func newRefreshFixture(t *testing.T, servers ...Server) *fixture {
	t.Helper()
	f := newFixture(t, servers...)
	f.r.opts.DryRun = false
	f.r.opts.RefreshDelay = refreshDelay
	return f
}

// seed stores a row of lib, read at seedAt, with refreshes successful
// refreshes at seedAttempt. A reason outside store.ItemReasons must be
// store.EpisodeUnmatched.
func (f *fixture) seed(server string, lib plex.Library, it plex.Item, reasons store.Reasons, refreshes int) {
	f.t.Helper()
	row := storeItem(server, lib, it, reasons&store.ItemReasons)
	rd := store.Read{
		Server: server, Section: lib.ID, At: seedAt, Full: true, Shows: lib.Type == "show",
		Items: []store.Item{row},
	}
	if reasons&store.EpisodeUnmatched != 0 {
		rd.EpisodeUnmatched = []store.Item{row}
	}
	if _, err := f.store.ApplyRead(rd); err != nil {
		f.t.Fatalf("seed store: %v", err)
	}
	for range refreshes {
		err := f.store.RecordRequest(store.Request{Server: server, RatingKey: it.RatingKey, At: seedAttempt,
			Source: store.SourceAuto, Action: store.ActionRefresh, Accepted: true})
		if err != nil {
			f.t.Fatalf("seed attempt: %v", err)
		}
	}
}

// row returns the stored row of a rating key, or false if there is none.
func (f *fixture) row(server, section, key string) (store.Item, bool) {
	f.t.Helper()
	for _, it := range f.rows(server, section) {
		if it.RatingKey == key {
			return it, true
		}
	}
	return store.Item{}, false
}

func unmatchedItem(key string) plex.Item {
	it := completeItem(key)
	it.GUID = "local://" + key
	return it
}

// sends returns the recorded Refresh and Match calls.
func sends(calls []string) []string {
	var got []string
	for _, c := range calls {
		if strings.HasPrefix(c, "Refresh ") || strings.HasPrefix(c, "Match ") {
			got = append(got, c)
		}
	}
	return got
}

// matchReads returns the recorded ItemPaths and SearchMatches calls.
func matchReads(calls []string) []string {
	var got []string
	for _, c := range calls {
		if strings.HasPrefix(c, "ItemPaths ") || strings.HasPrefix(c, "SearchMatches ") {
			got = append(got, c)
		}
	}
	return got
}

// wantMatchFields checks the action and the match fields of a request line.
func wantMatchFields(t *testing.T, ev map[string]any, action, name string, year int, links ...string) {
	t.Helper()
	wantField(t, ev, "action", action)
	wantField(t, ev, "item_year", float64(2020))
	wantField(t, ev, "candidate", name)
	wantField(t, ev, "candidate_year", float64(year))
	var want []any
	for _, l := range links {
		want = append(want, l)
	}
	if got, _ := ev["links"].([]any); !reflect.DeepEqual(got, want) {
		t.Errorf("links = %v, want %v", ev["links"], want)
	}
	if _, ok := ev["guid"]; ok {
		t.Errorf("request line has a guid: %v", ev)
	}
}

// lastSummary returns the last summary line of the server.
func (f *fixture) lastSummary(server string) map[string]any {
	f.t.Helper()
	var last map[string]any
	for _, ev := range f.logLines("info", "Run summary") {
		if ev["server"] == server {
			last = ev
		}
	}
	if last == nil {
		f.t.Fatalf("no summary line for server %s", server)
	}
	return last
}

func TestRefreshOrderAndDelay(t *testing.T) {
	first, second := incompleteItem("103"), incompleteItem("101")
	first.AddedAt = addedAt.Add(-2 * time.Hour)
	second.AddedAt = addedAt.Add(-time.Hour)
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("102"), second, first}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	wantCalls(t, fp.calls, "Libraries", "List 1?type=1", "Refresh 103", "Refresh 101", "Refresh 102")
	if want := []time.Duration{refreshDelay, refreshDelay}; !reflect.DeepEqual(f.sleeps, want) {
		t.Errorf("sleeps = %v, want %v", f.sleeps, want)
	}
}

func TestMaxRefreshesPerRun(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102"), incompleteItem("103")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.MaxRefreshesPerRun = 2

	f.run(t0)
	wantCalls(t, sends(fp.calls), "Refresh 101", "Refresh 102")
	summary := f.lastSummary(testServer)
	wantField(t, summary, "due_items", float64(3))
	wantField(t, summary, "remaining_items", float64(1))

	fp.calls = nil
	f.run(t0.Add(24 * time.Hour))
	wantCalls(t, sends(fp.calls), "Refresh 103", "Refresh 101")
}

func TestRefreshBacksOffAfterRefreshes(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.MaxRefreshInterval = 7 * 24 * time.Hour
	// At t0, the last refresh of each row is 48h old. Two refreshes wait
	// 48h, and three refreshes wait 96h.
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 2)
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 3)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 101")
}

func TestIgnoredReasonsGetNoRequest(t *testing.T) {
	noArt := completeItem("201")
	noArt.Art = ""
	fp := newFakePlex(showsLib)
	fp.lists["2?type=2"] = []plex.Item{noArt, incompleteItem("202")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.IgnoreReasons = store.NoArt | store.EpisodeNoSummary | store.EpisodeUnmatched

	f.run(t0)

	// No episode request is sent, because both episode reasons are ignored.
	wantCalls(t, fp.calls, "Libraries", "List 2?type=2", "Refresh 202")
	wantField(t, f.lastSummary(testServer), "incomplete_items", float64(1))
}

func TestWarnAtLongestWait(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	// The waits are 24h, 24h, 48h, and then 96h from the third refresh on.
	f.r.opts.MaxRefreshInterval = 96 * time.Hour
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 1)
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 101", "Refresh 102")
	lines := f.logLines("warn", "Item reached the longest wait")
	if len(lines) != 1 {
		t.Fatalf("got %d longest wait lines, want 1", len(lines))
	}
	wantField(t, lines[0], "server", testServer)
	wantField(t, lines[0], "library", "Movies")
	wantField(t, lines[0], "title", "Title 102")
	wantField(t, lines[0], "reasons", "no-summary")
	wantField(t, lines[0], "refreshes", float64(3))
	// zerolog writes a duration in milliseconds.
	wantField(t, lines[0], "wait", float64((96 * time.Hour).Milliseconds()))
}

func TestNoLongestWaitWarningWithoutBackoff(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.MaxRefreshInterval = f.r.opts.RefreshInterval
	// 101 gets its first refresh, and 102 its third.
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 2)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 101", "Refresh 102")
	if lines := f.logLines("warn", "Item reached the longest wait"); len(lines) != 0 {
		t.Errorf("got longest wait lines %v, want none", lines)
	}
}

func TestIgnoredEpisodeReasonKeepsCachedRow(t *testing.T) {
	fp := newFakePlex(showsLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, showsLib, completeItem("201"), store.EpisodeUnmatched, 1)
	f.r.opts.IgnoreReasons = store.EpisodeUnmatched

	f.run(t0)

	if row, ok := f.row(testServer, "2", "201"); !ok || row.Reasons != store.EpisodeUnmatched || row.Refreshes != 1 {
		t.Errorf("row 201 = %+v, %v, want EpisodeUnmatched with 1 refresh", row, ok)
	}
	if lines := f.logLines("info", "Item complete"); len(lines) != 0 {
		t.Errorf("got complete lines %v, want none", lines)
	}
	wantCalls(t, sends(fp.calls))
}

func TestRefreshSuccessRecordsRefresh(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	row, _ := f.row(testServer, "1", "101")
	if row.Refreshes != 1 || !row.LastRefresh.Equal(t0) || !row.LastAttempt.Equal(t0) {
		t.Errorf("row = %+v, want 1 refresh and both times %v", row, t0)
	}
	if row.LastAction != store.ActionRefresh || row.LastMatch != "" {
		t.Errorf("actions = %q, %q, want refresh and no match", row.LastAction, row.LastMatch)
	}
	lines := f.logLines("info", "Sent request")
	if len(lines) != 1 {
		t.Fatalf("got %d sent lines, want 1", len(lines))
	}
	wantField(t, lines[0], "server", testServer)
	wantField(t, lines[0], "library", "Movies")
	wantField(t, lines[0], "title", "Title 101")
	wantField(t, lines[0], "reasons", "no-summary")
	wantField(t, lines[0], "action", "refresh")
}

func TestRefreshFailureRecordsAttemptOnly(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	fp.refreshErrs["101"] = errServer
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	row, _ := f.row(testServer, "1", "101")
	if row.Refreshes != 0 || !row.LastRefresh.IsZero() || !row.LastAttempt.Equal(t0) || row.LastAction != "" {
		t.Errorf("row = %+v, want no refresh, no action, and LastAttempt %v", row, t0)
	}
	warnings := f.logLines("warn", "Request failed")
	if len(warnings) != 1 {
		t.Fatalf("got %d request failure warnings, want 1", len(warnings))
	}
	wantField(t, warnings[0], "title", "Title 101")
	wantField(t, warnings[0], "action", "refresh")

	fp.calls = nil
	f.run(t0.Add(23 * time.Hour))
	wantCalls(t, sends(fp.calls))

	f.run(t0.Add(24 * time.Hour))
	wantCalls(t, sends(fp.calls), "Refresh 101")
}

func TestRefreshNotFoundDeletesRow(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	fp.refreshErrs["101"] = errNotFound
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	if rows := f.rows(testServer, "1"); len(rows) != 0 {
		t.Errorf("rows = %+v, want none", rows)
	}
	wantField(t, f.lastSummary(testServer), "failed_requests", float64(0))
}

func TestRefreshUnavailableStopsServer(t *testing.T) {
	down := newFakePlex(moviesLib)
	down.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	down.refreshErrs["101"] = errUnavailable
	up := newFakePlex(moviesLib)
	up.lists["1?type=1"] = []plex.Item{incompleteItem("201")}
	f := newRefreshFixture(t, Server{Name: "down", Plex: down}, Server{Name: "up", Plex: up})

	f.runUnavailable(t0)

	wantCalls(t, sends(down.calls), "Refresh 101")
	wantCalls(t, sends(up.calls), "Refresh 201")
	errs := f.logLines("error", "Plex server unavailable")
	if len(errs) != 1 {
		t.Fatalf("got %d unavailable errors, want 1", len(errs))
	}
	wantField(t, errs[0], "server", "down")
	warnings := f.logLines("warn", "Request failed")
	if len(warnings) != 1 {
		t.Fatalf("got %d request failure warnings, want 1", len(warnings))
	}
	wantField(t, warnings[0], "server", "down")
	wantField(t, warnings[0], "library", "Movies")
	wantField(t, warnings[0], "title", "Title 101")
	wantField(t, warnings[0], "reasons", "no-summary")
	wantField(t, warnings[0], "action", "refresh")
	// Plex can apply a refresh before the response fails, so the failed
	// request counts as an attempt.
	if row, _ := f.row("down", "1", "101"); row.Refreshes != 0 || !row.LastAttempt.Equal(t0) {
		t.Errorf("row 101 = %+v, want no refresh and LastAttempt %v", row, t0)
	}
	if row, _ := f.row("down", "1", "102"); !row.LastAttempt.IsZero() {
		t.Errorf("row 102 = %+v, want no attempt", row)
	}
	summary := f.lastSummary("down")
	wantField(t, summary, "refresh_requests", float64(1))
	wantField(t, summary, "failed_requests", float64(1))
	wantField(t, summary, "remaining_items", float64(1))
	if got := len(f.logLines("info", "Run summary")); got != 2 {
		t.Errorf("got %d summary lines, want 2", got)
	}

	down.refreshErrs = map[string]error{}
	down.calls, up.calls = nil, nil
	f.run(t0.Add(23 * time.Hour))
	wantCalls(t, sends(down.calls), "Refresh 102")
	wantCalls(t, sends(up.calls))

	down.calls = nil
	f.run(t0.Add(24 * time.Hour))
	wantCalls(t, sends(down.calls), "Refresh 101")
}

func TestUnmatchedFirstAttemptIsPlainRefresh(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Files = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.lists["1?type=1"] = []plex.Item{movie}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 101")
	wantCalls(t, matchReads(fp.calls))
}

func TestFixMatchMovie(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Files = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, movie, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, matchReads(fp.calls), "SearchMatches 101 tmdb-1 tv.plex.agents.movie en-US")
	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/a", "Refresh 101")
	row, _ := f.row(testServer, "1", "101")
	// The refresh that follows the accepted match is the last action.
	if row.Refreshes != 3 || row.LastAction != store.ActionRefresh || row.LastMatch != store.ActionMatch {
		t.Errorf("row = %+v, want 3 refreshes, the refresh as the last action, and the match as the last match", row)
	}
	summary := f.lastSummary(testServer)
	wantField(t, summary, "match_requests", float64(1))
	wantField(t, summary, "refresh_requests", float64(1))
	// A tmdb ID from the path is a plain match, not a TMDB match.
	sent := f.logLines("info", "Sent request")
	if len(sent) != 2 {
		t.Fatalf("got %d sent lines, want 2: the match and the refresh after it", len(sent))
	}
	wantMatchFields(t, sent[0], "match", "Film", 2020, "https://www.themoviedb.org/movie/1")
	results := f.logLines("debug", "Match search result")
	if len(results) != 1 {
		t.Fatalf("got %d search result lines, want 1", len(results))
	}
	wantField(t, results[0], "id", "tmdb-1")
	wantField(t, results[0], "candidates", float64(1))
}

// An ignored unmatched reason gets no fix match. The item is still due for
// its other reason, so it gets a plain refresh.
func TestIgnoredUnmatchedGetsNoFixMatch(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Summary = ""
	movie.Files = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.IgnoreReasons = store.Unmatched
	f.seed(testServer, moviesLib, movie, store.Unmatched|store.NoSummary, 1)

	f.run(t0)

	wantCalls(t, matchReads(fp.calls))
	wantCalls(t, sends(fp.calls), "Refresh 101")
}

func TestFixMatchMovieWithoutListingUsesItemPaths(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, matchReads(fp.calls), "ItemPaths 101", "SearchMatches 101 tmdb-1 tv.plex.agents.movie en-US")
	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/a", "Refresh 101")
}

func TestFixMatchFileMapIsPerServer(t *testing.T) {
	a := newFakePlex(moviesLib)
	listed := unmatchedItem("1001")
	listed.Files = []string{"/a/Film (2020) {tmdb-1}/Film.mkv"}
	a.lists["1?type=1&unmatched=1"] = []plex.Item{listed}
	b := newFakePlex(moviesLib)
	b.paths["1001"] = []string{"/b/Other (2020) {tmdb-2}/Other.mkv"}
	f := newRefreshFixture(t, Server{Name: "a", Plex: a}, Server{Name: "b", Plex: b})
	f.seed("a", moviesLib, listed, store.Unmatched, 1)
	f.seed("b", moviesLib, unmatchedItem("1001"), store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, matchReads(a.calls), "SearchMatches 1001 tmdb-1 tv.plex.agents.movie en-US")
	wantCalls(t, matchReads(b.calls), "ItemPaths 1001", "SearchMatches 1001 tmdb-2 tv.plex.agents.movie en-US")
}

func TestFixMatchSeriesUsesItemPaths(t *testing.T) {
	fp := newFakePlex(showsLib)
	series := unmatchedItem("201")
	fp.lists["2?type=2&unmatched=1"] = []plex.Item{series}
	fp.paths["201"] = []string{"/tv/Show (2020) {tvdb-5}"}
	fp.candidates["201 tvdb-5"] = []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, showsLib, series, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, matchReads(fp.calls), "ItemPaths 201", "SearchMatches 201 tvdb-5 tv.plex.agents.series en-US")
	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s", "Refresh 201")
}

func TestFixMatchConflictSendsPlainRefresh(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020) {tmdb-1} [imdb-tt2]/Film.mkv"}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	fp.candidates["101 imdb-tt2"] = []plex.Candidate{{GUID: "plex://movie/b", Name: "Film", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 101")
	warnings := f.logLines("warn", "Match conflict")
	if len(warnings) != 1 {
		t.Fatalf("got %d conflict warnings, want 1", len(warnings))
	}
	wantField(t, warnings[0], "title", "Title 101")
	wantField(t, warnings[0], "conflict", "different candidates")
	wantField(t, warnings[0], "item_year", float64(2020))
	want := []any{
		"Film (2020) via https://www.themoviedb.org/movie/1",
		"Film (2020) via https://www.imdb.com/title/tt2",
	}
	if got, _ := warnings[0]["candidates"].([]any); !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %v, want %v", warnings[0]["candidates"], want)
	}
}

func TestFixMatchWithoutIDsSendsPlainRefresh(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020)/Film.mkv"}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, matchReads(fp.calls), "ItemPaths 101")
	wantCalls(t, sends(fp.calls), "Refresh 101")
}

func TestFixMatchReadErrors(t *testing.T) {
	tests := []struct {
		name      string
		id        string // the path ID of 201; "" means tmdb-5
		fail      func(*fakePlex)
		wantSends []string
		wantRow   string // "deleted", "attempted" (no request), or "refreshed"
		wantStop  bool   // the error stops the server
	}{
		{"item paths unavailable", "", func(fp *fakePlex) { fp.pathErrs["201"] = errUnavailable }, nil, "attempted", true},
		{"item paths not found", "", func(fp *fakePlex) { fp.pathErrs["201"] = errNotFound }, []string{"Refresh 202"}, "deleted", false},
		{"item paths 500", "", func(fp *fakePlex) { fp.pathErrs["201"] = errServer }, []string{"Refresh 202"}, "attempted", false},
		{"search unavailable", "", func(fp *fakePlex) { fp.searchErrs["201"] = errUnavailable }, nil, "attempted", true},
		{"search not found", "", func(fp *fakePlex) { fp.searchErrs["201"] = errNotFound }, []string{"Refresh 202"}, "deleted", false},
		{"search 500", "", func(fp *fakePlex) { fp.searchErrs["201"] = errServer }, []string{"Refresh 202"}, "attempted", false},
		// A failed tvdb search counts as no candidate, unless the error
		// stops the server or says that the item is gone.
		{"tvdb search unavailable", "tvdb-5", func(fp *fakePlex) { fp.searchErrs["201"] = errUnavailable }, nil, "attempted", true},
		{"tvdb search not found", "tvdb-5", func(fp *fakePlex) { fp.searchErrs["201"] = errNotFound }, []string{"Refresh 202"}, "deleted", false},
		{"tvdb search 500", "tvdb-5", func(fp *fakePlex) { fp.searchErrs["201"] = errServer }, []string{"Refresh 201", "Refresh 202"}, "refreshed", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.id
			if id == "" {
				id = "tmdb-5"
			}
			fp := newFakePlex(showsLib)
			fp.paths["201"] = []string{"/tv/Show (2020) {" + id + "}"}
			fp.candidates["201 "+id] = []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 2020}}
			tt.fail(fp)
			second := incompleteItem("202")
			second.AddedAt = addedAt.Add(time.Hour)
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.seed(testServer, showsLib, unmatchedItem("201"), store.Unmatched, 1)
			f.seed(testServer, showsLib, second, store.NoSummary, 1)

			if tt.wantStop {
				f.runUnavailable(t0)
			} else {
				f.run(t0)
			}

			wantCalls(t, sends(fp.calls), tt.wantSends...)
			row, exists := f.row(testServer, "2", "201")
			switch tt.wantRow {
			case "deleted":
				if exists {
					t.Errorf("row = %+v, want none", row)
				}
			case "attempted":
				if !exists || row.Refreshes != 1 || !row.LastAttempt.Equal(t0) {
					t.Errorf("row = %+v (exists %v), want 1 refresh and LastAttempt %v", row, exists, t0)
				}
			case "refreshed":
				if !exists || row.Refreshes != 2 || !row.LastAttempt.Equal(t0) {
					t.Errorf("row = %+v (exists %v), want 2 refreshes and LastAttempt %v", row, exists, t0)
				}
			}
			if !tt.wantStop {
				if tt.wantRow == "attempted" {
					if warnings := f.logLines("warn", "Match search failed"); len(warnings) != 1 {
						t.Errorf("got %d search failure warnings, want 1", len(warnings))
					}
				}
				return
			}
			if errs := f.logLines("error", "Plex server unavailable"); len(errs) != 1 {
				t.Errorf("got %d unavailable errors, want 1", len(errs))
			}
			logs := f.logs.String()
			errAt := strings.Index(logs, `"message":"Plex server unavailable"`)
			summaryAt := strings.Index(logs, `"message":"Run summary"`)
			if errAt < 0 || summaryAt < errAt {
				t.Errorf("the summary line does not follow the error line:\n%s", logs)
			}
			wantField(t, f.lastSummary(testServer), "failed_requests", float64(1))
		})
	}
}

func TestFixMatchIMDbSearchErrorUsesOtherID(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.paths["201"] = []string{"/tv/Show (2020) {imdb-tt1} {tmdb-2}"}
	fp.searchErrs["201 imdb-tt1"] = errServer
	fp.candidates["201 tmdb-2"] = []plex.Candidate{{GUID: "plex://show/s", Name: "Show", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, showsLib, unmatchedItem("201"), store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 201 plex://show/s", "Refresh 201")
	if warnings := f.logLines("warn", "Match search failed"); len(warnings) != 0 {
		t.Errorf("got search failure warnings %v, want none", warnings)
	}
}

func TestDryRunUnavailableSearchRecordsNothing(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.paths["201"] = []string{"/tv/Show (2020) {tvdb-5}"}
	fp.searchErrs["201"] = errUnavailable
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.DryRun = true
	f.seed(testServer, showsLib, unmatchedItem("201"), store.Unmatched, 1)

	f.runUnavailable(t0)

	wantCalls(t, sends(fp.calls))
	if row, exists := f.row(testServer, "2", "201"); !exists || row.Refreshes != 1 || !row.LastAttempt.Equal(seedAttempt) {
		t.Errorf("row = %+v (exists %v), want it unchanged", row, exists)
	}
	if errs := f.logLines("error", "Plex server unavailable"); len(errs) != 1 {
		t.Errorf("got %d unavailable errors, want 1", len(errs))
	}
}

// A failed match search sends nothing, but the item counts toward the limit,
// so failing searches cannot run without bound. The failed attempt starts
// the wait of the item, so the next run reaches an item with more refreshes,
// which the queue puts after it.
func TestFailedSearchSendsNothing(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.searchErrs["101"] = errServer
	second := incompleteItem("102")
	second.AddedAt = addedAt.Add(time.Hour)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.MaxRefreshesPerRun = 1
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)
	f.seed(testServer, moviesLib, second, store.NoSummary, 2)

	f.run(t0)

	wantCalls(t, sends(fp.calls))
	if row, _ := f.row(testServer, "1", "101"); row.Refreshes != 1 || !row.LastAttempt.Equal(t0) {
		t.Errorf("row = %+v, want 1 refresh and LastAttempt %v", row, t0)
	}
	wantField(t, f.lastSummary(testServer), "remaining_items", float64(1))

	// The failed item waits, so the next run refreshes the other item.
	fp.calls = nil
	f.run(t0.Add(time.Minute))
	wantCalls(t, sends(fp.calls), "Refresh 102")
}

func TestEpisodeReasonGetsPlainRefresh(t *testing.T) {
	fp := newFakePlex(showsLib)
	_, unmatched := episodeKeys(t0)
	fp.lists[unmatched] = []plex.Item{completeItem("201")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, showsLib, completeItem("201"), store.EpisodeUnmatched, 3)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	wantCalls(t, matchReads(fp.calls))
}

func TestDryRunSendsAndRecordsNothing(t *testing.T) {
	fp := newFakePlex(moviesLib)
	for _, key := range []string{"102", "103", "104"} {
		fp.paths[key] = []string{"/m/Film (2020) {tmdb-" + key + "}/Film.mkv"}
	}
	fp.candidates["102 tmdb-102"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	fp.searchErrs["103"] = errServer
	fp.searchErrs["104"] = errNotFound
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{incompleteItem("101")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.DryRun = true
	for _, key := range []string{"102", "103", "104"} {
		f.seed(testServer, moviesLib, unmatchedItem(key), store.Unmatched, 1)
	}

	f.run(t0)

	wantCalls(t, sends(fp.calls))
	if len(f.sleeps) != 0 {
		t.Errorf("sleeps = %v, want none", f.sleeps)
	}
	want := map[string]time.Time{"101": {}, "102": seedAttempt, "103": seedAttempt, "104": seedAttempt}
	for key, attempt := range want {
		row, exists := f.row(testServer, "1", key)
		if !exists || !row.LastAttempt.Equal(attempt) || row.LastMatch != "" {
			t.Errorf("row %s = %+v (exists %v), want LastAttempt %v and no match", key, row, exists, attempt)
		}
	}
	lines := f.logLines("info", "Dry run")
	if len(lines) != 2 {
		t.Fatalf("got %d dry run lines, want 2", len(lines))
	}
	wantField(t, lines[0], "title", "Title 101")
	wantField(t, lines[0], "reasons", "no-summary")
	wantField(t, lines[0], "action", "refresh")
	wantField(t, lines[1], "title", "Title 102")
	wantField(t, lines[1], "reasons", "unmatched")
	wantMatchFields(t, lines[1], "match", "Film", 2020, "https://www.themoviedb.org/movie/102")
	if _, ok := lines[0]["candidate"]; ok {
		t.Errorf("a refresh line has match fields: %v", lines[0])
	}
	if warnings := f.logLines("warn", "Match search failed"); len(warnings) != 2 {
		t.Errorf("got %d search failure warnings, want 2", len(warnings))
	}
	if lib := f.library(testServer, "1"); !lib.LastRead.Equal(t0) {
		t.Errorf("LastRead = %v, want %v", lib.LastRead, t0)
	}
}

func TestDryRunAppliesLimit(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102"), incompleteItem("103")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.DryRun = true
	f.r.opts.MaxRefreshesPerRun = 1

	f.run(t0)

	lines := f.logLines("info", "Dry run")
	if len(lines) != 1 {
		t.Fatalf("got %d dry run lines, want 1", len(lines))
	}
	wantField(t, lines[0], "title", "Title 101")
}

func TestSummaryLine(t *testing.T) {
	// moviesLib has a full read at seedAt, so it gets an incremental read.
	// newLib has no full read yet, so it gets a full read.
	newLib := plex.Library{ID: "3", Title: "New Movies", Type: "movie", Agent: "tv.plex.agents.movie", Language: "en-US"}
	fp := newFakePlex(moviesLib, newLib)
	fp.lists["3?type=1"] = []plex.Item{completeItem("301"), completeItem("302")}
	since := unixString(seedAt.Add(-oversample))
	fp.lists["1?type=1&updatedAt>>="+since] = []plex.Item{completeItem("101")}
	fp.paths["103"] = []string{"/m/Film (2020) {tmdb-3}/Film.mkv"}
	fp.candidates["103 tmdb-3"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	fp.paths["104"] = []string{"/m/Film (2020) {tmdb-4}/Film.mkv"}
	fp.searchErrs["104"] = errServer
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 1)
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 1)
	f.seed(testServer, moviesLib, unmatchedItem("103"), store.Unmatched, 1)
	f.seed(testServer, moviesLib, unmatchedItem("104"), store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 102", "Match 103 plex://movie/a", "Refresh 103")
	summary := f.lastSummary(testServer)
	for field, want := range map[string]float64{
		"full_read_items": 2, "incremental_read_items": 1, "incomplete_items": 3, "due_items": 3,
		"refresh_requests": 2, "match_requests": 1, "failed_requests": 1, "remaining_items": 0,
		"completed_items": 1, "completed_after_request": 1, "completed_after_match": 0,
	} {
		wantField(t, summary, field, want)
	}
	if _, ok := summary["duration"]; !ok {
		t.Error("summary line has no duration")
	}
}

func TestCanceledDelayStopsRunQuietly(t *testing.T) {
	first := newFakePlex(moviesLib)
	first.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	f := newRefreshFixture(t, Server{Name: "first", Plex: first})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.r.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	f.now = t0
	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	wantCalls(t, sends(first.calls), "Refresh 101")
	if strings.Contains(f.logs.String(), `"level":"error"`) {
		t.Errorf("got error lines, want none:\n%s", f.logs)
	}
	if lines := f.logLines("info", "Run summary"); len(lines) != 0 {
		t.Errorf("got summary lines %v, want none", lines)
	}
}

func TestCanceledSendRecordsAttempt(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	fp.refreshErrs["101"] = fmt.Errorf("refresh: %w: %w", plex.ErrUnavailable, context.Canceled)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fp.onCall = func(call string) {
		if call == "Refresh 101" {
			cancel()
		}
	}

	f.now = t0
	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	wantCalls(t, sends(fp.calls), "Refresh 101")
	if row, _ := f.row(testServer, "1", "101"); row.Refreshes != 0 || !row.LastAttempt.Equal(t0) {
		t.Errorf("row = %+v, want no refresh and LastAttempt %v", row, t0)
	}
	if strings.Contains(f.logs.String(), `"level":"error"`) {
		t.Errorf("got error lines, want none:\n%s", f.logs)
	}
	if lines := f.logLines("info", "Run summary"); len(lines) != 0 {
		t.Errorf("got summary lines %v, want none", lines)
	}
}

func TestMatchFailureRecordsAttemptOnly(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Files = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	fp.matchErrs["101"] = errServer
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, movie, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/a")
	if row, _ := f.row(testServer, "1", "101"); row.Refreshes != 1 || !row.LastAttempt.Equal(t0) {
		t.Errorf("row = %+v, want 1 refresh and LastAttempt %v", row, t0)
	}
	warnings := f.logLines("warn", "Request failed")
	if len(warnings) != 1 {
		t.Fatalf("got %d request failure warnings, want 1", len(warnings))
	}
	wantField(t, warnings[0], "title", "Title 101")
	wantField(t, warnings[0], "action", "match")
	summary := f.lastSummary(testServer)
	wantField(t, summary, "match_requests", float64(1))
	wantField(t, summary, "failed_requests", float64(1))
}

func TestAttemptTimeIsRequestTime(t *testing.T) {
	fp := newFakePlex(moviesLib)
	since := unixString(seedAt.Add(-oversample))
	fp.lists["1?type=1&updatedAt>>="+since] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	fp.paths["103"] = []string{"/m/Film (2020) {tmdb-3}/Film.mkv"}
	fp.searchErrs["103"] = errServer
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, unmatchedItem("103"), store.Unmatched, 1)
	// The clock moves forward by each delay.
	f.r.sleep = func(_ context.Context, d time.Duration) error {
		f.now = f.now.Add(d)
		return nil
	}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 101", "Refresh 102")
	later, last := t0.Add(refreshDelay), t0.Add(2*refreshDelay)
	want := map[string]time.Time{"101": t0, "102": later, "103": last}
	for key, at := range want {
		if row, _ := f.row(testServer, "1", key); !row.LastAttempt.Equal(at) {
			t.Errorf("row %s = %+v, want LastAttempt %v", key, row, at)
		}
	}
	if row, _ := f.row(testServer, "1", "102"); !row.LastRefresh.Equal(later) {
		t.Errorf("row 102 = %+v, want LastRefresh %v", row, later)
	}
	// zerolog writes a duration in milliseconds.
	wantField(t, f.lastSummary(testServer), "duration", float64(2*refreshDelay.Milliseconds()))
}

func TestSummaryDurationRoundsToMilliseconds(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.sleep = func(_ context.Context, d time.Duration) error {
		f.now = f.now.Add(d + 400*time.Microsecond)
		return nil
	}

	f.run(t0)

	// The tests keep the zerolog default, a float of milliseconds.
	wantField(t, f.lastSummary(testServer), "duration", float64(refreshDelay.Milliseconds()))
}

// countFails is a store whose CountIncomplete fails. If wait is set, it
// waits for wait to close first.
type countFails struct {
	Store
	wait chan struct{}
}

var errCount = errors.New("count items: database is locked")

func (c countFails) CountIncomplete(string, []string, store.Reasons) (int, error) {
	if c.wait != nil {
		select {
		case <-c.wait:
		case <-time.After(5 * time.Second):
		}
	}
	return 0, errCount
}

// signalWriter closes done after it writes a line that contains match.
type signalWriter struct {
	match string
	done  chan struct{}
	once  sync.Once
}

func (w *signalWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(w.match)) {
		w.once.Do(func() { close(w.done) })
	}
	return len(p), nil
}

// Run keeps the error of an unavailable server, also when a store error of
// another server ends the run.
func TestRunKeepsUnavailableAndStoreErrors(t *testing.T) {
	down := newFakePlex(moviesLib)
	down.librariesErr = fmt.Errorf("libraries: %w", plex.ErrUnavailable)
	broken := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: "down", Plex: down}, Server{Name: "broken", Plex: broken})
	// The store error waits until the unavailable server logged its error,
	// so the cancel of the store error cannot stop that server first.
	signal := &signalWriter{match: "Plex server unavailable", done: make(chan struct{})}
	log.Logger = zerolog.New(zerolog.SyncWriter(io.MultiWriter(f.logs, signal)))
	f.r.store = countFails{Store: f.store, wait: signal.done}

	f.now = t0
	err := f.r.Run(context.Background())
	if !errors.Is(err, errCount) || !errors.Is(err, plex.ErrUnavailable) {
		t.Fatalf("Run error = %v, want the store error and the unavailable server", err)
	}
}

// A store error after an unavailable server is the error of the run, so the
// caller does not take it for an unavailable server only.
func TestStoreErrorAfterUnavailableServer(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	fp.refreshErrs["101"] = errUnavailable
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.store = countFails{Store: f.store}

	f.now = t0
	err := f.r.Run(context.Background())
	if !errors.Is(err, errCount) || errors.Is(err, plex.ErrUnavailable) {
		t.Fatalf("Run error = %v, want only the store error", err)
	}
	if errs := f.logLines("error", "Plex server unavailable"); len(errs) != 1 {
		t.Errorf("got %d unavailable errors, want 1", len(errs))
	}
}

func TestSleepWaitsOrStops(t *testing.T) {
	if err := sleep(context.Background(), time.Millisecond); err != nil {
		t.Errorf("sleep = %v, want nil", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("sleep after cancel = %v, want context.Canceled", err)
	}
}

// An item that keeps failing must not keep the only slot of each run. Runs
// come more often than the wait of the items here, so both items are due in
// every run. The failing item starts 4 refreshes behind the other item. Each
// attempt counts in the order, also a failed one, so the failing item gives
// up the slot after 4 failed attempts.
func TestFailingItemGivesUpTheSlot(t *testing.T) {
	tests := []struct {
		name    string
		reasons store.Reasons
		item    plex.Item
		fail    func(fp *fakePlex)
	}{
		{"failed search", store.Unmatched, unmatchedItem("101"), func(fp *fakePlex) {
			fp.paths["101"] = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
			fp.searchErrs["101"] = errServer
		}},
		{"failed request", store.NoSummary, incompleteItem("101"), func(fp *fakePlex) {
			fp.refreshErrs["101"] = errServer
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := newFakePlex(moviesLib)
			tt.fail(fp)
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.r.opts.MaxRefreshesPerRun = 1
			f.r.opts.RefreshInterval = time.Minute
			f.r.opts.MaxRefreshInterval = time.Minute
			f.seed(testServer, moviesLib, tt.item, tt.reasons, 1)
			f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 5)

			var got []string
			for run := range 6 {
				fp.calls = nil
				f.run(t0.Add(time.Duration(run) * 2 * time.Minute))
				if row, _ := f.row(testServer, "1", "102"); row.LastAttempt.Equal(t0.Add(time.Duration(run) * 2 * time.Minute)) {
					got = append(got, "102")
				} else {
					got = append(got, "101")
				}
			}
			if want := []string{"101", "101", "101", "101", "102", "101"}; !reflect.DeepEqual(got, want) {
				t.Errorf("slot by run = %v, want %v", got, want)
			}
		})
	}
}
