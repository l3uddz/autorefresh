package refresher

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

const movieFolder = "/movies/Title (2020) "

func TestCompareMovieIDs(t *testing.T) {
	tests := []struct {
		name  string
		guid  string
		files []string
		ids   []string
		want  WrongMatch
	}{
		{"tmdb differs", "plex://movie/1", []string{movieFolder + "{tmdb-100}/Title.mkv"}, []string{"imdb://tt1", "tmdb://200"},
			WrongMatch{WrongMatchMismatched, []ComparedSource{{"tmdb", "100", []string{"200"}}}}},
		{"one source agrees", "plex://movie/1", []string{movieFolder + "{tmdb-100} {imdb-tt5}/Title.mkv"}, []string{"tmdb://100", "imdb://tt6"},
			WrongMatch{WrongMatchAgrees, []ComparedSource{{"tmdb", "100", []string{"100"}}, {"imdb", "tt5", []string{"tt6"}}}}},
		{"numbers written differently", "plex://movie/1", []string{movieFolder + "{tmdb-0100}/Title.mkv"}, []string{"tmdb://100"},
			WrongMatch{WrongMatchAgrees, []ComparedSource{{"tmdb", "100", []string{"100"}}}}},
		{"imdb written differently", "plex://movie/1", []string{movieFolder + "[IMDB-TT0115097]/Title.mkv"}, []string{"imdb://tt0115097"},
			WrongMatch{WrongMatchAgrees, []ComparedSource{{"imdb", "tt0115097", []string{"tt0115097"}}}}},
		{"no path ID", "plex://movie/1", []string{"/movies/Title (2020)/Title.mkv"}, []string{"tmdb://200"},
			WrongMatch{Result: WrongMatchNoPathIDs}},
		{"unmatched", "local://1", []string{movieFolder + "{tmdb-100}/Title.mkv"}, nil,
			WrongMatch{Result: WrongMatchUnmatched}},
		{"no files", "plex://movie/1", nil, []string{"tmdb://200"},
			WrongMatch{Result: WrongMatchUndecided}},
		{"plex lists no ID of the path source", "plex://movie/1", []string{movieFolder + "{tvdb-7}/Title.mkv"}, []string{"imdb://tt1", "tmdb://2"},
			WrongMatch{Result: WrongMatchUndecided}},
		{"two IDs of one source", "plex://movie/1", []string{movieFolder + "{tmdb-1}/A.mkv", movieFolder + "{tmdb-2}/B.mkv"}, []string{"tmdb://3"},
			WrongMatch{Result: WrongMatchUndecided}},
		{"other plex schemes", "plex://movie/1", []string{movieFolder + "{imdb-tt1} {tmdb-100}/Title.mkv"},
			[]string{"plex://movie/5d77", "imdbid://tt9", "tt9", "tmdb://200"},
			WrongMatch{WrongMatchMismatched, []ComparedSource{{"tmdb", "100", []string{"200"}}}}},
		{"malformed plex IDs", "plex://movie/1", []string{movieFolder + "{tmdb-100}/Title.mkv"}, []string{"tmdb://-100", "tmdb:///100"},
			WrongMatch{Result: WrongMatchUndecided}},
		{"duplicate plex IDs", "plex://movie/1", []string{movieFolder + "{tmdb-100}/Title.mkv"}, []string{"TMDB://0200", "tmdb://200"},
			WrongMatch{WrongMatchMismatched, []ComparedSource{{"tmdb", "100", []string{"200"}}}}},
		{"fixed source order", "plex://movie/1", []string{movieFolder + "{imdb-tt5} {tvdb-9} {tmdb-100}/Title.mkv"},
			[]string{"tvdb://8", "imdb://tt6", "tmdb://200"},
			WrongMatch{WrongMatchMismatched, []ComparedSource{
				{"tmdb", "100", []string{"200"}}, {"imdb", "tt5", []string{"tt6"}}, {"tvdb", "9", []string{"8"}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompareMovieIDs(tt.guid, tt.files, tt.ids); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CompareMovieIDs = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// movie returns a complete matched movie with one file in a folder that
// names folderID, and the external IDs.
func movie(key, folderID string, ids ...string) plex.Item {
	it := completeItem(key)
	it.GUID = "plex://movie/" + key
	it.Files = []string{movieFolder + folderID + "/Title.mkv"}
	it.ExternalIDs = ids
	return it
}

func TestReadFlagsWrongMovieMatch(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{
		movie("101", "{tmdb-100}", "tmdb://200"),
		movie("102", "{tmdb-300}", "tmdb://300"),
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	if got := f.reasons(moviesLib.ID); !reflect.DeepEqual(got, map[string]store.Reasons{"101": store.Mismatched}) {
		t.Fatalf("reasons = %v, want only 101 mismatched", got)
	}
	if got := sends(fp.calls); len(got) != 0 {
		t.Errorf("sends = %v, want none for a wrong match", got)
	}
	lines := f.logLines("debug", "Wrong match")
	if len(lines) != 1 {
		t.Fatalf("got %d Wrong match lines, want 1", len(lines))
	}
	wantField(t, lines[0], "title", "Title 101")
	if got := lines[0]["path_ids"]; !reflect.DeepEqual(got, []any{"tmdb-100"}) {
		t.Errorf("path_ids = %v, want [tmdb-100]", got)
	}
	if got := lines[0]["plex_ids"]; !reflect.DeepEqual(got, []any{"tmdb-200"}) {
		t.Errorf("plex_ids = %v, want [tmdb-200]", got)
	}
}

// listUpdated lists items as updated in the incremental read at t0 after a
// seed.
func listUpdated(f *fixture, fp *fakePlex, items ...plex.Item) {
	since := unixString(seedAt.Add(-f.r.opts.Oversample))
	fp.lists["1?type=1&updatedAt>>="+since] = items
}

func TestReadKeepsUndecidedWrongMatch(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, completeItem("101"), store.Mismatched, 0)
	listUpdated(f, fp, movie("101", "{tvdb-7}", "imdb://tt1", "tmdb://2"))

	f.run(t0)

	if got := f.reasons(moviesLib.ID); got["101"] != store.Mismatched {
		t.Errorf("reasons = %v, want 101 kept mismatched", got)
	}
}

func TestReadClearsCorrectedWrongMatch(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.seed(testServer, moviesLib, completeItem("101"), store.Mismatched, 0)
	listUpdated(f, fp, movie("101", "{tmdb-100}", "tmdb://100"))

	f.run(t0)

	if _, ok := f.row(testServer, moviesLib.ID, "101"); ok {
		t.Error("row of a corrected match stays, want it removed as complete")
	}
	if !slices.Contains(fp.calls, "List 1?type=1&updatedAt>>="+unixString(seedAt.Add(-f.r.opts.Oversample))) {
		t.Errorf("calls = %v, want the incremental listing", fp.calls)
	}
}

func TestManualMatchCorrectsWrongMatch(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	answer := movie("1", "{tmdb-100}", "tmdb://100")
	checks := new(int)
	f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: newFakePlex(lib), answers: []checkAnswer{{item: answer}}, checks: checks}})
	f.now = t0
	f.seed("home", lib, completeItem("1"), store.Mismatched, 0)

	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Title 1", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}

	if _, ok := f.row("home", "1", "1"); ok {
		t.Error("row stays after the match corrected it")
	}
	if *checks != 1 {
		t.Errorf("checks = %d, want 1", *checks)
	}
}

func TestRefreshKeepsWrongMatch(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	answer := movie("1", "{tmdb-100}", "tmdb://200")
	f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: newFakePlex(lib), answers: []checkAnswer{{item: answer}}, checks: new(int)}})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.Mismatched|store.NoSummary, 0)

	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}

	if row, ok := f.row("home", "1", "1"); !ok || row.Reasons != store.Mismatched {
		t.Errorf("row = %+v, %v, want only mismatched", row, ok)
	}
}

// A check whose answer cannot be compared keeps the stored reason, also
// when the answer has no Guid list at all, so it cannot move the movie to
// Fixed.
func TestCheckKeepsUndecidedWrongMatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer plex.Item
	}{
		{"no ID of the path source", movie("1", "{tmdb-100}", "imdb://tt1")},
		{"no Guid", movie("1", "{tmdb-100}")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := plex.Library{ID: "1", Type: "movie"}
			f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: newFakePlex(lib), answers: []checkAnswer{{item: tc.answer}}, checks: new(int)}})
			f.now = t0
			f.seed("home", lib, completeItem("1"), store.Mismatched, 0)

			if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
				t.Fatal(err)
			}

			if row, ok := f.row("home", "1", "1"); !ok || row.Reasons != store.Mismatched {
				t.Errorf("row = %+v, %v, want mismatched kept", row, ok)
			}
			lines := f.logLines("info", "Checked item")
			if len(lines) == 0 || lines[len(lines)-1]["reasons"] != "mismatched" {
				t.Errorf("Checked item lines = %v, want the kept mismatched reason", lines)
			}
		})
	}
}

// A queued movie that only has a wrong match left before its turn gets no
// request.
func TestRecheckSkipsRowWithOnlyWrongMatchLeft(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	fp := newFakePlex(lib)
	f := newRefreshFixture(t, Server{Name: "home", Plex: fp})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.Mismatched|store.NoSummary, 0)
	batch, _ := f.row("home", "1", "1")
	left := batch
	left.Reasons = store.Mismatched
	if _, err := f.store.ApplyCheck("home", left, t0, 0, store.Observation{}); err != nil {
		t.Fatal(err)
	}

	run := &serverRun{server: f.r.servers[0], source: store.SourceAuto}
	if sent, err := f.r.runItem(t.Context(), run, batch); err != nil || sent || run.skipped != 1 {
		t.Fatalf("runItem = sent %v, skipped %d, %v; want a skip", sent, run.skipped, err)
	}
	if got := sends(fp.calls); len(got) != 0 {
		t.Errorf("sends = %v, want none", got)
	}
}

func TestRunSendsNothingForWrongMatchOnly(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{movie("101", "{tmdb-100}", "tmdb://200")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)
	f.run(t0.Add(48 * time.Hour))

	if got := sends(fp.calls); len(got) != 0 {
		t.Errorf("sends = %v, want none", got)
	}
	if slices.Contains(fp.calls, "Item 101") {
		t.Errorf("calls = %v, want no item read", fp.calls)
	}
}
