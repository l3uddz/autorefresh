package refresher

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestReadRecordsCompletionOnlyWhenNotDryRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dryRun bool
		want   int
	}{{"live run", false, 1}, {"dry run", true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePlex(moviesLib)
			fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
			f := newFixture(t, Server{Name: testServer, Plex: fp})
			f.run(t0)

			fp.lists["1?type=1&updatedAt>>="+unixString(t0.Add(-oversample))] = []plex.Item{completeItem("101")}
			f.r.opts.DryRun = tc.dryRun
			f.run(t0.Add(time.Hour))

			if rows := f.rows(testServer, "1"); len(rows) != 0 {
				t.Errorf("rows = %+v, want none", rows)
			}
			events, err := f.store.Timeline(testServer, "101")
			if err != nil {
				t.Fatal(err)
			}
			completed := 0
			for _, e := range events {
				if e.Kind == store.EventCompleted {
					completed++
				}
			}
			if completed != tc.want {
				t.Errorf("completed events = %d, want %d (events %+v)", completed, tc.want, events)
			}
		})
	}
}

// timeline returns the events of an item, newest first.
func (f *fixture) timeline(key string) []store.Event {
	f.t.Helper()
	events, err := f.store.Timeline(testServer, key)
	if err != nil {
		f.t.Fatal(err)
	}
	return events
}

// newestEvent returns the newest event of an item.
func (f *fixture) newestEvent(key string) store.Event {
	f.t.Helper()
	events := f.timeline(key)
	if len(events) == 0 {
		f.t.Fatalf("item %s has no events", key)
	}
	return events[0]
}

// matchFixture returns a refresh fixture with the unmatched movie 101,
// refreshed once, whose path ID finds the candidate plex://movie/m.
func matchFixture(t *testing.T) (*fixture, *fakePlex) {
	t.Helper()
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/m", Name: "Film", Year: 2020}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)
	return f, fp
}

func TestRefreshRecordsAcceptedRequestEvent(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	events := f.timeline("101")
	if len(events) != 1 {
		t.Fatalf("events = %+v, want 1", events)
	}
	e := events[0]
	if e.Kind != store.EventRequest || e.Source != store.SourceAuto || !e.At.Equal(t0) || e.Action != store.ActionRefresh ||
		!e.Accepted || e.Outcome != "refresh" || e.Error != "" || e.MatchGUID != "" || e.Title != "Title 101" {
		t.Errorf("event = %+v, want an accepted refresh at %v", e, t0)
	}
	if row, _ := f.row(testServer, "1", "101"); row.LastOutcome != "refresh" || row.Refreshes != 1 {
		t.Errorf("row = %+v, want LastOutcome refresh and 1 refresh", row)
	}
}

func TestFailedRefreshRecordsFailedEvent(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	fp.refreshErrs["101"] = errServer
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	events := f.timeline("101")
	if len(events) != 1 {
		t.Fatalf("events = %+v, want 1", events)
	}
	e := events[0]
	if e.Kind != store.EventRequest || e.Source != store.SourceAuto || e.Action != store.ActionRefresh || e.Accepted ||
		e.Outcome != "refresh-failed" || e.Error != errServer.Error() {
		t.Errorf("event = %+v, want a failed refresh with error %q", e, errServer)
	}
	if row, _ := f.row(testServer, "1", "101"); row.LastOutcome != "refresh-failed" || row.Refreshes != 0 {
		t.Errorf("row = %+v, want LastOutcome refresh-failed and no refresh", row)
	}
}

func TestFailedMatchRecordsItsTarget(t *testing.T) {
	f, fp := matchFixture(t)
	fp.matchErrs["101"] = errServer

	f.run(t0)

	e := f.newestEvent("101")
	if e.Kind != store.EventRequest || e.Source != store.SourceAuto || e.Action != store.ActionMatch || e.Accepted ||
		e.Outcome != "match-failed" || e.Error != errServer.Error() || e.MatchGUID != "plex://movie/m" {
		t.Errorf("event = %+v, want a failed match of plex://movie/m", e)
	}
	row, _ := f.row(testServer, "1", "101")
	if row.LastMatch != "" || row.LastMatchEventID != 0 || row.LastOutcome != "match-failed" {
		t.Errorf("row = %+v, want no match and LastOutcome match-failed", row)
	}
}

func TestNotFoundRecordsGoneEvent(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	fp.refreshErrs["101"] = errNotFound
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	if _, ok := f.row(testServer, "1", "101"); ok {
		t.Error("row of the gone item still exists")
	}
	events := f.timeline("101")
	if len(events) != 1 || events[0].Kind != store.EventGone || events[0].Source != store.SourceAuto ||
		events[0].Title != "Title 101" || !events[0].At.Equal(t0) {
		t.Errorf("events = %+v, want one gone event at %v", events, t0)
	}
}

func TestMatchRecordsTarget(t *testing.T) {
	f, _ := matchFixture(t)

	f.run(t0)

	e := f.newestEvent("101")
	wantLinks := []string{"https://www.themoviedb.org/movie/1"}
	if e.Kind != store.EventRequest || e.Source != store.SourceAuto || e.Action != store.ActionMatch || !e.Accepted ||
		e.Outcome != "match" || e.MatchGUID != "plex://movie/m" || e.MatchTitle != "Film" || e.MatchYear != 2020 ||
		!reflect.DeepEqual(e.Links, wantLinks) {
		t.Errorf("event = %+v, want an accepted match of plex://movie/m with links %v", e, wantLinks)
	}
	row, _ := f.row(testServer, "1", "101")
	if row.LastMatchEventID != e.ID || row.LastMatch != store.ActionMatch {
		t.Errorf("row = %+v, want LastMatchEventID %d", row, e.ID)
	}
}

// A dry run writes no request or gone event: not for a found match, a plain
// refresh, a failed search, or a search that Plex answers with 404.
func TestDryRunRecordsNoRequestEvents(t *testing.T) {
	fp := newFakePlex(moviesLib)
	for _, key := range []string{"102", "103", "104"} {
		fp.paths[key] = []string{"/m/Film (2020) {tmdb-" + key + "}/Film.mkv"}
	}
	fp.candidates["102 tmdb-102"] = []plex.Candidate{{GUID: "plex://movie/a", Name: "Film", Year: 2020}}
	fp.searchErrs["103"] = errServer
	fp.searchErrs["104"] = errNotFound
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.DryRun = true
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 1)
	for _, key := range []string{"102", "103", "104"} {
		f.seed(testServer, moviesLib, unmatchedItem(key), store.Unmatched, 1)
	}
	before := map[string]int{}
	for _, key := range []string{"101", "102", "103", "104"} {
		before[key] = len(f.timeline(key))
	}

	f.run(t0)

	if len(f.logLines("info", "Dry run")) != 2 {
		t.Fatalf("dry run lines = %v, want 2", f.logLines("info", "Dry run"))
	}
	for key, n := range before {
		if got := len(f.timeline(key)); got != n {
			t.Errorf("events of %s = %d, want %d", key, got, n)
		}
	}
}

func TestReadStoresImagesAndMovieFolder(t *testing.T) {
	fp := newFakePlex(moviesLib, showsLib)
	movie := incompleteItem("101")
	movie.Thumb, movie.Art = "/library/metadata/101/thumb/1700000000", "/library/metadata/101/art/1700000001"
	movie.Files = []string{"/movies/Film (2020)/Film.mkv", "/other/Film.part2.mkv"}
	fp.lists["1?type=1"] = []plex.Item{movie}
	series := incompleteItem("201")
	series.Thumb, series.Art = "/library/metadata/201/thumb/1700000000", "/library/metadata/201/art/1700000001"
	fp.lists["2?type=2"] = []plex.Item{series}
	f := newFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	row, _ := f.row(testServer, "1", "101")
	if row.Thumb != movie.Thumb || row.Art != movie.Art || row.Path != "/movies/Film (2020)" {
		t.Errorf("movie row = %+v, want its images and folder /movies/Film (2020)", row)
	}
	row, _ = f.row(testServer, "2", "201")
	if row.Thumb != series.Thumb || row.Art != series.Art || row.Path != "" {
		t.Errorf("series row = %+v, want its images and no path", row)
	}

	// A later read that lists the series only in an episode request also
	// keeps its images.
	at := t0.Add(time.Hour)
	_, unmatched := episodeKeys(at)
	listed := completeItem("201")
	listed.Thumb, listed.Art = "/library/metadata/201/thumb/1700000099", "/library/metadata/201/art/1700000098"
	fp.lists[unmatched] = []plex.Item{listed}
	f.run(at)

	row, _ = f.row(testServer, "2", "201")
	if row.Thumb != listed.Thumb || row.Art != listed.Art {
		t.Errorf("series row = %+v, want the images of the episode request", row)
	}
}

func TestReadDropsForeignImagePaths(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := incompleteItem("101")
	movie.Thumb, movie.Art = "https://x/y.jpg", "/library/metadata/999/art/1700000001"
	fp.lists["1?type=1"] = []plex.Item{movie}
	f := newFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	if row, _ := f.row(testServer, "1", "101"); row.Thumb != "" || row.Art != "" {
		t.Errorf("row = %+v, want no images", row)
	}
}

// The fake answers ItemPaths with locations, so the movie shows that only a
// series saves its folder.
// A show listing that carries a media file keeps the cached series
// folder: only a movie takes its path from its files.
func TestShowListingKeepsSeriesFolder(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.lists["2?type=2"] = []plex.Item{incompleteItem("201")}
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.DryRun = false
	f.run(t0)
	if err := f.store.SetPath(testServer, "201", "/tv/Show (2020)"); err != nil {
		t.Fatal(err)
	}

	// The next full read lists the series with a media file.
	series := incompleteItem("201")
	series.Files = []string{"/tv/Show (2020)/Season 01/Show S01E01.mkv"}
	fp.lists["2?type=2"] = []plex.Item{series}
	fp.calls = nil
	f.run(sunday0300)

	if !containsCall(fp.calls, "List 2?type=2") {
		t.Fatalf("calls = %v, want a full read of the show library", fp.calls)
	}
	if row, _ := f.row(testServer, "2", "201"); row.Path != "/tv/Show (2020)" {
		t.Errorf("series path = %q, want /tv/Show (2020)", row.Path)
	}
}

func TestSeriesPathSavedAfterItemPaths(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "live run", true: "dry run"}[dryRun], func(t *testing.T) {
			fp := newFakePlex(moviesLib, showsLib)
			fp.paths["101"] = []string{"/m/Film (2020)/Film.mkv"}
			fp.paths["201"] = []string{"/tv/Show (2020)", "/tv2/Show (2020)"}
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.r.opts.DryRun = dryRun
			f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)
			f.seed(testServer, showsLib, unmatchedItem("201"), store.Unmatched, 1)

			f.run(t0)

			wantCalls(t, matchReads(fp.calls), "ItemPaths 101", "ItemPaths 201")
			if row, _ := f.row(testServer, "2", "201"); row.Path != "/tv/Show (2020)" {
				t.Errorf("series path = %q, want /tv/Show (2020)", row.Path)
			}
			if row, _ := f.row(testServer, "1", "101"); row.Path != "" {
				t.Errorf("movie path = %q, want none", row.Path)
			}
		})
	}
}

// failingPath is a store whose path writes fail.
type failingPath struct{ *store.Store }

func (failingPath) SetPath(string, string, string) error { return errDisk }

// A failed path write is a store error: it stops the run and records no
// attempt.
func TestSeriesPathErrorStopsRun(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.paths["201"] = []string{"/tv/Show (2020)"}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, showsLib, unmatchedItem("201"), store.Unmatched, 1)
	f.r.store = failingPath{f.store}

	f.now = t0
	if err := f.r.Run(context.Background()); !errors.Is(err, errDisk) {
		t.Fatalf("Run = %v, want the store error", err)
	}
	if warnings := f.logLines("warn", "Match search failed"); len(warnings) != 0 {
		t.Errorf("got match search warnings %v, want none", warnings)
	}
	if row, _ := f.row(testServer, "2", "201"); !row.LastAttempt.Equal(seedAttempt) {
		t.Errorf("LastAttempt = %v, want %v", row.LastAttempt, seedAttempt)
	}
}

func TestEmptyItemPathsSavesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
	}{{"no locations", nil}, {"empty location", []string{""}}} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePlex(showsLib)
			fp.paths["201"] = tc.paths
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.seed(testServer, showsLib, unmatchedItem("201"), store.Unmatched, 1)
			if err := f.store.SetPath(testServer, "201", "/tv/Old"); err != nil {
				t.Fatal(err)
			}

			f.run(t0)

			wantCalls(t, matchReads(fp.calls), "ItemPaths 201")
			if row, _ := f.row(testServer, "2", "201"); row.Path != "/tv/Old" {
				t.Errorf("path = %q, want /tv/Old", row.Path)
			}
		})
	}
}

// libraryStore records the libraries that the refresher saves.
type libraryStore struct {
	*store.Store
	saved map[string][]store.LibraryInfo // by server
}

func (s *libraryStore) SaveLibraries(server string, libs []store.LibraryInfo) error {
	s.saved[server] = libs
	return s.Store.SaveLibraries(server, libs)
}

// The read fixture runs dry, so a dry run saves the libraries too.
func TestReadSavesLibraryMetadata(t *testing.T) {
	music := plex.Library{ID: "3", Title: "Music", Type: "artist", Agent: "tv.plex.agents.music", Language: "en-US"}
	fp := newFakePlex(moviesLib, showsLib, music)
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	st := &libraryStore{Store: f.store, saved: map[string][]store.LibraryInfo{}}
	f.r.store = st

	f.run(t0)

	want := []store.LibraryInfo{
		{Section: "1", Title: "Movies", Type: "movie", Agent: "tv.plex.agents.movie", Language: "en-US"},
		{Section: "2", Title: "TV", Type: "show", Agent: "tv.plex.agents.series", Language: "en-US"},
	}
	if got := st.saved[testServer]; !reflect.DeepEqual(got, want) {
		t.Errorf("saved libraries = %+v, want %+v", got, want)
	}
}

// A full read saves the folder of an unseen series row that Plex still
// answers, but only if the whole read applies. An unseen movie row saves
// no folder.
func TestFullReadSavesFolderOfUnseenSeries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		readFails  bool
		wantFolder string
	}{{"read applies", false, "/tv/Show (2020)"}, {"read fails", true, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePlex(moviesLib, showsLib)
			fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
			fp.lists["2?type=2"] = []plex.Item{incompleteItem("201")}
			f := newFixture(t, Server{Name: testServer, Plex: fp})
			f.run(t0)

			fp.lists["1?type=1"] = nil
			fp.lists["2?type=2"] = nil
			fp.paths["101"] = []string{"/m/Film (2020)/Film.mkv"}
			fp.paths["201"] = []string{"/tv/Show (2020)"}
			if tc.readFails {
				noSummary, _ := episodeKeys(sunday0300)
				fp.listErrs[noSummary] = errors.New("listing section 2: 500 Internal Server Error")
			}
			f.run(sunday0300)

			wantCalls(t, matchReads(fp.calls), "ItemPaths 101", "ItemPaths 201")
			if row, _ := f.row(testServer, "2", "201"); row.Path != tc.wantFolder {
				t.Errorf("series path = %q, want %q", row.Path, tc.wantFolder)
			}
			if row, _ := f.row(testServer, "1", "101"); row.Path != "" {
				t.Errorf("movie path = %q, want none", row.Path)
			}
		})
	}
}

func TestFolderOf(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{nil, ""},
		{[]string{"/movies/Film (2020)/Film.mkv", "/other/Film.mkv"}, "/movies/Film (2020)"},
		{[]string{`C:\Movies\Film (2020)\Film.mkv`}, `C:\Movies\Film (2020)`},
		{[]string{"Film.mkv"}, ""},
	} {
		if got := folderOf(tc.files); got != tc.want {
			t.Errorf("folderOf(%q) = %q, want %q", tc.files, got, tc.want)
		}
	}
}

// The events of a run name it: the request that it sent and the completion
// that its read found.
func TestRunLinksItsEvents(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1&updatedAt>>="+unixString(seedAt.Add(-oversample))] = []plex.Item{completeItem("102")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 0)

	f.run(t0)

	run := f.onlyRun()
	events, err := f.store.RunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		got = append(got, e.RatingKey+" "+string(e.Kind))
	}
	if want := []string{"102 completed", "101 request"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("run events = %v, want %v", got, want)
	}
}
