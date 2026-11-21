package refresher

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// cached reports for each "<server>/<section>" whether the library has read
// times or item rows.
func (f *fixture) cached(scopes ...string) map[string]bool {
	f.t.Helper()
	got := map[string]bool{}
	for _, scope := range scopes {
		server, section, _ := strings.Cut(scope, "/")
		got[scope] = !f.library(server, section).LastRead.IsZero() || len(f.rows(server, section)) > 0
	}
	return got
}

func TestRunPrunesRemovedServer(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.seed("old", moviesLib, incompleteItem("101"), store.NoSummary, 2)
	f.seed(testServer, moviesLib, incompleteItem("102"), store.NoSummary, 0)

	f.run(t0)

	want := map[string]bool{"old/1": false, testServer + "/1": true}
	if got := f.cached("old/1", testServer+"/1"); !reflect.DeepEqual(got, want) {
		t.Errorf("cached = %v, want %v", got, want)
	}
	lines := f.logLines("info", "Removed cache of library")
	if len(lines) != 1 {
		t.Fatalf("got %d removed cache lines, want 1", len(lines))
	}
	wantField(t, lines[0], "server", "old")
	wantField(t, lines[0], "section", "1")
	wantField(t, lines[0], "items", float64(1))
	if _, ok := lines[0]["library"]; ok {
		t.Errorf("removed server line has a library field: %v", lines[0])
	}
}

func TestRunPrunesUnselectedLibraries(t *testing.T) {
	gone := plex.Library{ID: "9", Title: "Old", Type: "movie", Agent: "tv.plex.agents.movie"}
	tests := []struct {
		name      string
		libraries []string // the configured library titles
		want      map[string]bool
		titles    map[string]any // the library field of each removed section
	}{
		{
			name:      "unselected and deleted libraries",
			libraries: []string{"Movies"},
			want:      map[string]bool{"home/1": true, "home/2": false, "home/9": false},
			titles:    map[string]any{"2": "TV", "9": nil},
		},
		{
			name:      "no selected library",
			libraries: []string{"Anime"},
			want:      map[string]bool{"home/1": false, "home/2": false, "home/9": false},
			titles:    map[string]any{"1": "Movies", "2": "TV", "9": nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := newFakePlex(moviesLib, showsLib)
			f := newFixture(t, Server{Name: testServer, Plex: fp, Libraries: tt.libraries})
			f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 1)
			f.seed(testServer, showsLib, incompleteItem("201"), store.NoSummary, 1)
			f.seed(testServer, gone, incompleteItem("901"), store.NoSummary, 1)

			f.run(t0)

			if got := f.cached("home/1", "home/2", "home/9"); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("cached = %v, want %v", got, tt.want)
			}
			titles := map[string]any{}
			for _, ev := range f.logLines("info", "Removed cache of library") {
				wantField(t, ev, "server", testServer)
				wantField(t, ev, "items", float64(1))
				titles[ev["section"].(string)] = ev["library"]
			}
			if !reflect.DeepEqual(titles, tt.titles) {
				t.Errorf("removed sections = %v, want %v", titles, tt.titles)
			}
			wantField(t, f.lastSummary(testServer), "completed_items", float64(0))
		})
	}
}

func TestRunKeepsCacheWithoutLibraryListing(t *testing.T) {
	tests := []struct {
		name   string
		fail   func(*fakePlex)
		failed bool // the server counts as unavailable in the run
	}{
		{"listing failed", func(fp *fakePlex) {
			fp.librariesErr = errors.New("libraries: 500 Internal Server Error")
		}, true},
		{"no library listed", func(fp *fakePlex) { fp.libraries = nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := newFakePlex(showsLib)
			tt.fail(fp)
			f := newFixture(t, Server{Name: testServer, Plex: fp})
			f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 1)

			if tt.failed {
				f.runUnavailable(t0)
			} else {
				f.run(t0)
			}

			if got := f.cached("home/1"); !got["home/1"] {
				t.Error("cache of home/1 was removed")
			}
			if lines := f.logLines("info", "Removed cache of library"); len(lines) != 0 {
				t.Errorf("got removed cache lines %v, want none", lines)
			}

			// The next listing succeeds without the library, so the run
			// removes its cache.
			*fp = *newFakePlex(showsLib)
			f.run(t0.Add(time.Hour))

			if got := f.cached("home/1"); got["home/1"] {
				t.Error("cache of home/1 stayed after a listing without it")
			}
		})
	}
}

func TestRunCanceledBeforeStartDoesNotPrune(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.seed("old", moviesLib, incompleteItem("101"), store.NoSummary, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if got := f.cached("old/1"); !got["old/1"] {
		t.Error("canceled Run pruned old/1")
	}
}
