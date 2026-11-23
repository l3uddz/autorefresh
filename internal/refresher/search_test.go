package refresher

import (
	"context"
	"reflect"
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestManualSearchRecognizesTypedID(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	result, err := f.r.SearchMatches(t.Context(), "home", "1", MatchQuery{Text: "imdb-tt123"})
	if err != nil || len(result.Searches) != 1 || result.Searches[0].Source != "id" || result.Searches[0].Query != "imdb-tt123" {
		t.Fatalf("search = %+v, %v", result, err)
	}
	if events, err := f.store.Timeline("home", "1"); err != nil || len(events) != 0 {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

type tmdbFindHook struct {
	*fakeTMDB
	onFind func()
}

func (h tmdbFindHook) Find(ctx context.Context, source, id string, show bool) (string, bool, error) {
	h.onFind()
	return h.fakeTMDB.Find(ctx, source, id, show)
}

func TestHistorySearchDoesNotCacheTMDBForRowCreatedMidSearch(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	p := newFakePlex(lib)
	p.paths["1"] = []string{"/movies/One {imdb-tt123}"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: p})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	row, _ := f.row("home", "1", "1")
	row.Reasons = 0
	if _, err := f.store.ApplyCheck("home", row, t0, 0); err != nil {
		t.Fatal(err)
	}
	f.r.opts.TMDB = tmdbFindHook{fakeTMDB: &fakeTMDB{ids: map[string]string{"imdb tt123 false": "77"}}, onFind: func() {
		f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	}}
	result, err := f.r.SearchMatches(t.Context(), "home", "1", MatchQuery{})
	if err != nil || len(result.Searches) != 3 || result.Searches[2].Query != "tmdb-77" || result.Searches[2].FromID != "imdb-tt123" {
		t.Fatalf("opening searches = %+v, %v", result, err)
	}
	if _, found, err := f.store.TMDBID("home", "1", "imdb", "tt123"); err != nil || found {
		t.Fatalf("TMDB ID cached = %v, %v", found, err)
	}
	if current, found := f.row("home", "1", "1"); !found || current.Path != "" {
		t.Fatalf("new row = %+v, found %v", current, found)
	}
}

func TestEpisodeCheckComparesNumbers(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "show"}
	p := newFakePlex(lib)
	p.episodeNumbers = map[string]map[int][]int{"1": {1: {1, 2}}}
	f := newRefreshFixture(t, Server{Name: "home", Plex: p})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	f.r.opts.TMDB = &fakeTMDB{seasons: map[string][]int{"77 1": {1, 2}, "78 1": {1, 3, 4}, "79 1": {1, 3}}}
	for _, tc := range []struct {
		id   string
		want EpisodeResult
	}{
		{"77", EpisodeResult{Match: true, Episodes: 2}},
		{"78", EpisodeResult{Diff: store.EpisodeDiff{Season: 1, Plex: 2, TMDB: 3}, Missing: []int{3, 4}, Extra: []int{2}}},
		{"79", EpisodeResult{Diff: store.EpisodeDiff{Season: 1, Plex: 2, TMDB: 2}, Missing: []int{3}, Extra: []int{2}}},
	} {
		if got, err := f.r.EpisodeCheck(t.Context(), "home", "1", tc.id); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, %v, want %+v", tc.id, got, err, tc.want)
		}
	}
}
