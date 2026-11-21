package plex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestLibrariesDecodesSections(t *testing.T) {
	rec, c := newServer(t, serveFixture(t, "sections.json"))

	got, err := c.Libraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := []Library{
		{ID: "1", Title: "Fixture Movies", Type: "movie", Agent: "tv.plex.agents.movie", Language: "en-GB"},
		{ID: "2", Title: "Fixture Shows", Type: "show", Agent: "tv.plex.agents.series", Language: "en-GB"},
		{ID: "3", Title: "Fixture Other", Type: "movie", Agent: "tv.plex.agents.none", Language: "en-GB"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("libraries = %+v, want %+v", got, want)
	}
	if r := rec.all()[0]; r.Method != http.MethodGet || r.URL.Path != "/library/sections" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
}

func TestListDecodesRecordedShapes(t *testing.T) {
	const moviesDir = "/fixture/movies/"
	tests := []struct {
		fixture string
		typ     int
		want    []Item
	}{
		{"movies_matched.json", TypeMovie, []Item{
			{
				RatingKey: "1001", GUID: "plex://movie/000000000000000000000001", Title: "Fixture Movie One",
				Year: 2020, Summary: "A neutral summary.",
				Thumb: "/library/metadata/1001/thumb/1700000050", Art: "/library/metadata/1001/art/1700000050",
				AddedAt: time.Unix(1700000000, 0),
				Files: []string{
					moviesDir + "Fixture Movie One (2020) {tmdb-100001}/Fixture Movie One (2020).mkv",
					moviesDir + "Fixture Movie One (2020) {tmdb-100001}/Fixture Movie One (2020) - version2.mkv",
					moviesDir + "Fixture Movie One (2020) {tmdb-100001}/Fixture Movie One (2020) - version3.mkv",
				},
			},
			{
				RatingKey: "1002", GUID: "plex://movie/000000000000000000000002", Title: "Fixture Movie Two",
				Year: 2020, Summary: "A neutral summary.",
				Thumb: "/library/metadata/1002/thumb/1700000150", Art: "/library/metadata/1002/art/1700000150",
				AddedAt: time.Unix(1700000100, 0),
				Files: []string{
					moviesDir + "Fixture Movie Two (2020) {tmdb-100002}/Fixture Movie Two (2020).mkv",
					moviesDir + "Fixture Movie Two (2020) {tmdb-100002}/Fixture Movie Two (2020) - version2.mkv",
					moviesDir + "Fixture Movie Two (2020) {tmdb-100002}/Fixture Movie Two (2020) - version3.mkv",
				},
			},
		}},
		{"movies_unmatched.json", TypeMovie, []Item{{
			RatingKey: "1003", GUID: "local://1003", Title: "Fixture Movie Three", Year: 2024, Summary: "",
			Thumb: "/library/metadata/1003/thumb/1700000250", Art: "/library/metadata/1003/art/1700000250",
			AddedAt: time.Unix(1700000200, 0),
			Files:   []string{moviesDir + "Fixture Movie Three (2024) {tmdb-100003}/Fixture Movie Three (2024).mkv"},
		}}},
		{"shows_unmatched.json", TypeShow, []Item{{
			RatingKey: "1004", GUID: "local://1004", Title: "Fixture Series One",
			AddedAt: time.Unix(1700000300, 0),
		}}},
		{"shows_episode_summary.json", TypeShow, []Item{
			{
				RatingKey: "1005", GUID: "plex://show/000000000000000000000002", Title: "Fixture Series Two",
				Year: 2018, Summary: "A neutral summary.",
				Thumb: "/library/metadata/1005/thumb/1700000450", Art: "/library/metadata/1005/art/1700000450",
				AddedAt: time.Unix(1700000400, 0),
			},
			{
				RatingKey: "1006", GUID: "plex://show/000000000000000000000003", Title: "Fixture Series Three",
				Year: 2022, Summary: "A neutral summary.",
				Thumb: "/library/metadata/1006/thumb/1700000550", Art: "/library/metadata/1006/art/1700000550",
				AddedAt: time.Unix(1700000500, 0),
			},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			_, c := newServer(t, serveOnePage(t, tt.fixture))

			got, err := c.List(context.Background(), "1", tt.typ)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("items = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// serveOnePage serves a recorded listing with totalSize set to the page
// size, so List reads exactly one page.
func serveOnePage(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	var doc map[string]map[string]any
	if err := json.Unmarshal(fixture(t, name), &doc); err != nil {
		t.Fatal(err)
	}
	doc["MediaContainer"]["totalSize"] = doc["MediaContainer"]["size"]
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }
}

// pagedServer answers each listing with the page at X-Plex-Container-Start
// of total items, with at most size items. The pages have totalSize only if
// withTotal is set.
func pagedServer(t *testing.T, total, size int, withTotal bool) (*recorder, *Client) {
	t.Helper()
	return newServer(t, func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
		n := max(0, min(size, total-start))
		w.Write([]byte(`{"MediaContainer":{`))
		if withTotal {
			w.Write([]byte(`"totalSize":` + strconv.Itoa(total) + `,`))
		}
		w.Write([]byte(`"Metadata":[`))
		for i := 0; i < n; i++ {
			if i > 0 {
				w.Write([]byte(","))
			}
			w.Write([]byte(`{"ratingKey":"` + strconv.Itoa(start+i) + `"}`))
		}
		w.Write([]byte(`]}}`))
	})
}

func TestListReadsEveryPage(t *testing.T) {
	tests := []struct {
		name   string
		total  int
		size   int // the most items in one answer
		starts []string
	}{
		{"full pages", 1001, 500, []string{"0", "500", "1000"}},
		{"empty library", 0, 500, []string{"0"}},
		{"short pages", 450, 200, []string{"0", "200", "400"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, c := pagedServer(t, tt.total, tt.size, true)

			items, err := c.List(context.Background(), "1", TypeMovie)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != tt.total {
				t.Errorf("got %d items, want %d", len(items), tt.total)
			}
			for i, it := range items {
				if it.RatingKey != strconv.Itoa(i) {
					t.Fatalf("item %d has rating key %s", i, it.RatingKey)
				}
			}

			var starts []string
			for _, r := range rec.all() {
				starts = append(starts, r.URL.Query().Get("X-Plex-Container-Start"))
			}
			if !reflect.DeepEqual(starts, tt.starts) {
				t.Errorf("starts = %v, want %v", starts, tt.starts)
			}
		})
	}
}

// Without totalSize, the end of a listing is unknown, so the listing fails
// after one request instead of returning part of the library.
func TestListRejectsPageWithoutTotalSize(t *testing.T) {
	rec, c := pagedServer(t, 1001, 200, false)

	if _, err := c.List(context.Background(), "1", TypeMovie); err == nil {
		t.Fatal("no error")
	}
	if n := len(rec.all()); n != 1 {
		t.Errorf("got %d requests, want 1", n)
	}
}

func TestListStopsOnEmptyPage(t *testing.T) {
	rec, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"totalSize":10,"Metadata":[]}}`))
	})

	if _, err := c.List(context.Background(), "1", TypeMovie); err == nil {
		t.Fatal("no error")
	}
	if n := len(rec.all()); n != 1 {
		t.Errorf("got %d requests, want 1", n)
	}
}

func TestListEncodesFilters(t *testing.T) {
	tests := []struct {
		name    string
		typ     int
		filters []Filter
		want    string
	}{
		{
			"show", TypeShow,
			[]Filter{{"addedAt>>", "100"}, {"episode.summary", ""}},
			"X-Plex-Container-Size=500&X-Plex-Container-Start=0&addedAt%3E%3E=100&episode.summary=" +
				"&excludeElements=Media%2CRole%2CDirector%2CWriter%2CGenre%2CCountry&sort=addedAt&type=2",
		},
		{
			"movie", TypeMovie, nil,
			"X-Plex-Container-Size=500&X-Plex-Container-Start=0" +
				"&excludeElements=Role%2CDirector%2CWriter%2CGenre%2CCountry&sort=addedAt&type=1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"MediaContainer":{"totalSize":0}}`))
			})

			if _, err := c.List(context.Background(), "2", tt.typ, tt.filters...); err != nil {
				t.Fatal(err)
			}

			r := rec.all()[0]
			if r.Method != http.MethodGet || r.URL.Path != "/library/sections/2/all" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			if r.URL.RawQuery != tt.want {
				t.Errorf("query =\n%s\nwant\n%s", r.URL.RawQuery, tt.want)
			}
		})
	}
}

func TestItemPathsReadsLocationsAndFiles(t *testing.T) {
	tests := []struct {
		fixture string
		want    []string
	}{
		{"item_show.json", []string{"/fixture/tv/Fixture Series One (2021) {tvdb-200004}"}},
		{"item_movie.json", []string{
			"/fixture/movies/Fixture Movie One (2020) {tmdb-100001}/Fixture Movie One (2020).mkv",
			"/fixture/movies/Fixture Movie One (2020) {tmdb-100001}/Fixture Movie One (2020) - version2.mkv",
			"/fixture/movies/Fixture Movie One (2020) {tmdb-100001}/Fixture Movie One (2020) - version3.mkv",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			rec, c := newServer(t, serveFixture(t, tt.fixture))

			got, err := c.ItemPaths(context.Background(), "1001")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("paths = %v, want %v", got, tt.want)
			}
			if r := rec.all()[0]; r.Method != http.MethodGet || r.URL.Path != "/library/metadata/1001" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
		})
	}
}

func TestItemPathsEmptyResponseIsNotNotFound(t *testing.T) {
	_, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{}}`))
	})

	_, err := c.ItemPaths(context.Background(), "1001")
	if err == nil {
		t.Fatal("no error")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
		t.Errorf("error %q wraps ErrNotFound or ErrUnavailable, want a plain error", err)
	}
}
