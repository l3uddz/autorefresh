package tmdb

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
)

func TestMain(m *testing.M) {
	log.Logger = zerolog.Nop()
	os.Exit(m.Run())
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldLogger, oldLevel := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() {
		log.Logger = oldLogger
		zerolog.SetGlobalLevel(oldLevel)
	})
	return &buf
}

const token = "secret-token"

// serve returns a client for a server that checks the token and answers each
// request with handler.
func serve(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q, want the bearer token", got)
		}
		if strings.Contains(r.URL.String(), token) {
			t.Errorf("URL %q holds the token", r.URL)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New(token, 5*time.Second)
	c.base = mustURL(t, srv.URL)
	c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return c
}

// mustURL parses a test server URL.
func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// file answers with a file of testdata.
func file(t *testing.T, name string) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

func TestFindSeries(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/find/81189" || r.URL.Query().Get("external_source") != "tvdb_id" {
			t.Errorf("request = %s, want /3/find/81189 with external_source=tvdb_id", r.URL)
		}
		file(t, "find_tv.json")(w, r)
	})

	id, ok, err := c.Find(context.Background(), "tvdb", "81189", true)
	if err != nil || !ok || id != "1396" {
		t.Fatalf("Find = %q, %v, %v, want 1396", id, ok, err)
	}
}

func TestFindMovieByIMDb(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/find/tt0133093" || r.URL.Query().Get("external_source") != "imdb_id" {
			t.Errorf("request = %s, want /3/find/tt0133093 with external_source=imdb_id", r.URL)
		}
		file(t, "find_movie.json")(w, r)
	})

	id, ok, err := c.Find(context.Background(), "imdb", "tt0133093", false)
	if err != nil || !ok || id != "603" {
		t.Fatalf("Find = %q, %v, %v, want 603", id, ok, err)
	}
}

func TestFindNothing(t *testing.T) {
	tests := []struct {
		name, file string
		show       bool
	}{
		{"no entry", "find_empty.json", true},
		{"series for a movie", "find_tv.json", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := serve(t, file(t, tt.file))
			id, ok, err := c.Find(context.Background(), "tvdb", "81189", tt.show)
			if err != nil || ok || id != "" {
				t.Fatalf("Find = %q, %v, %v, want nothing", id, ok, err)
			}
		})
	}
}

func TestFindSeveral(t *testing.T) {
	c := serve(t, file(t, "find_two_tv.json"))
	id, ok, err := c.Find(context.Background(), "tvdb", "81189", true)
	if !errors.Is(err, ErrSeveral) || ok || id != "" {
		t.Fatalf("Find = %q, %v, %v, want ErrSeveral", id, ok, err)
	}
}

func TestFindErrors(t *testing.T) {
	status := func(code int) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
	}
	tests := []struct {
		name         string
		handler      func(http.ResponseWriter, *http.Request)
		unauthorized bool
	}{
		{"401", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			file(t, "unauthorized.json")(w, r)
		}, true},
		{"404", status(http.StatusNotFound), false},
		{"429", status(http.StatusTooManyRequests), false},
		{"500", status(http.StatusInternalServerError), false},
		{"bad JSON", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{")) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := serve(t, tt.handler)
			_, _, err := c.Find(context.Background(), "tvdb", "81189", true)
			checkError(t, err, tt.unauthorized)
		})
	}

	t.Run("closed server", func(t *testing.T) {
		c := serve(t, nil)
		c.base = mustURL(t, "http://127.0.0.1:1")
		_, _, err := c.Find(context.Background(), "tvdb", "81189", true)
		checkError(t, err, false)
	})
}

// checkError fails unless err is a TMDB error that the refresher cannot take
// for a Plex error, and that does not show the token.
func checkError(t *testing.T, err error, unauthorized bool) {
	t.Helper()
	switch {
	case err == nil:
		t.Fatal("err = nil, want an error")
	case errors.Is(err, ErrUnauthorized) != unauthorized:
		t.Errorf("errors.Is(%v, ErrUnauthorized) = %v, want %v", err, !unauthorized, unauthorized)
	case errors.Is(err, plex.ErrUnavailable), errors.Is(err, plex.ErrNotFound):
		t.Errorf("err %v wraps a Plex error", err)
	case strings.Contains(err.Error(), token):
		t.Errorf("err %v holds the token", err)
	}
}

func TestCheck(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// A request without a query has no "?".
		if r.RequestURI != "/3/authentication" {
			t.Errorf("request URI = %s, want /3/authentication", r.RequestURI)
		}
		w.Write([]byte(`{"success":true}`))
	})
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}

	c = serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		file(t, "unauthorized.json")(w, r)
	})
	if err := c.Check(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Check = %v, want ErrUnauthorized", err)
	}
}

func TestFindRejectsInvalidID(t *testing.T) {
	var hits atomic.Int32
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) })
	tests := []struct {
		source, id string
	}{
		{"tvdb", ""},
		{"tvdb", ".."},
		{"tvdb", "1/2"},
		{"tvdb", "tt123"},
		{"imdb", ""},
		{"imdb", ".."},
		{"imdb", "12345"},
		{"imdb", "tt"},
		{"unknown", "123"},
	}
	for _, tt := range tests {
		t.Run(tt.source+"_"+tt.id, func(t *testing.T) {
			if _, _, err := c.Find(context.Background(), tt.source, tt.id, true); err == nil {
				t.Errorf("Find(%q, %q) returned no error", tt.source, tt.id)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("server received %d requests, want 0", n)
	}
}

func TestFindIgnoresNonPositiveID(t *testing.T) {
	for _, body := range []string{`{"tv_results":[{}]}`, `{"tv_results":[{"id":0}]}`, `{"tv_results":[{"id":-5}]}`} {
		t.Run(body, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
			id, ok, err := c.Find(context.Background(), "tvdb", "81189", true)
			if err != nil || ok || id != "" {
				t.Fatalf("Find = %q, %v, %v, want nothing", id, ok, err)
			}
		})
	}
}

func TestRefusesCrossOriginRedirect(t *testing.T) {
	var other atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other.Add(1) }))
	t.Cleanup(target.Close)
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	})

	err := c.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("Check error = %v, want redirect refused", err)
	}
	if other.Load() != 0 {
		t.Errorf("other origin received %d requests, want 0", other.Load())
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error %q holds the token", err)
	}
}

func TestTraceLogsRequestsWithoutToken(t *testing.T) {
	buf := captureLog(t)
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"success":true}`))
	})

	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.base = mustURL(t, "http://127.0.0.1:1")
	_ = c.Check(context.Background())

	out := buf.String()
	if strings.Contains(out, token) {
		t.Fatalf("trace log holds the token: %s", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d trace lines, want 2: %s", len(lines), out)
	}
	for _, want := range []string{
		`"level":"trace"`,
		`"method":"GET","path":"/3/authentication"`,
	} {
		for i, line := range lines {
			if !strings.Contains(line, want) {
				t.Errorf("line %d missing %s: %s", i, want, line)
			}
		}
	}
	if !strings.Contains(lines[0], `"status":200`) {
		t.Errorf("line 0 = %s, want status 200", lines[0])
	}
	if !strings.Contains(lines[1], `"error":`) {
		t.Errorf("line 1 = %s, want error field", lines[1])
	}
}

func TestTraceLogsRedirectHopsSeparately(t *testing.T) {
	buf := captureLog(t)
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/authentication" {
			http.Redirect(w, r, "/3/authentication/v2", http.StatusTemporaryRedirect)
			return
		}
		w.Write([]byte(`{"success":true}`))
	})

	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	out := buf.String()
	if strings.Contains(out, token) {
		t.Fatalf("trace log holds the token: %s", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d trace lines, want 2: %s", len(lines), out)
	}
	if !strings.Contains(lines[0], `"path":"/3/authentication"`) || !strings.Contains(lines[0], `"status":307`) {
		t.Errorf("line 0 = %s, want /3/authentication with status 307", lines[0])
	}
	if !strings.Contains(lines[1], `"path":"/3/authentication/v2"`) || !strings.Contains(lines[1], `"status":200`) {
		t.Errorf("line 1 = %s, want /3/authentication/v2 with status 200", lines[1])
	}
}

func TestYearSeries(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/tv/75367" || r.URL.RawQuery != "" {
			t.Errorf("request = %s, want /3/tv/75367", r.URL)
		}
		file(t, "tv_seasons.json")(w, r)
	})

	year, ok, err := c.Year(context.Background(), "75367", true)
	if err != nil || !ok || year != 2022 {
		t.Fatalf("Year = %d, %v, %v, want 2022 (ignoring Season 0 and null dates)", year, ok, err)
	}
}

func TestPremiereYear(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/tv/75367" || r.URL.RawQuery != "" {
			t.Errorf("request = %s, want /3/tv/75367", r.URL)
		}
		file(t, "tv_seasons.json")(w, r)
	})

	year, ok, err := c.PremiereYear(context.Background(), "75367")
	if err != nil || !ok || year != 2022 {
		t.Fatalf("PremiereYear = %d, %v, %v, want 2022 of season 1, ignoring Season 0", year, ok, err)
	}
}

// Only season 1 gives the premiere year: a later season's date never
// stands in for a missing one.
func TestPremiereYearNothing(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{"only specials", `{"seasons":[{"season_number":0,"air_date":"2017-11-27"}]}`},
		{"first season undated", `{"seasons":[{"season_number":2,"air_date":"2022-01-01"},{"season_number":1,"air_date":null}]}`},
		{"no season 1", `{"seasons":[{"season_number":3,"air_date":"2021-01-01"},{"season_number":2,"air_date":"2019-01-01"}]}`},
		{"bad date format", `{"seasons":[{"season_number":1,"air_date":"20220101"}]}`},
		{"invalid date", `{"seasons":[{"season_number":1,"air_date":"2022-99-99"}]}`},
		{"year 0", `{"seasons":[{"season_number":1,"air_date":"0000-01-01"}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(tt.body)) })
			year, ok, err := c.PremiereYear(context.Background(), "1")
			if err != nil || ok || year != 0 {
				t.Fatalf("PremiereYear = %d, %v, %v, want nothing", year, ok, err)
			}
		})
	}
	t.Run("season 1 between specials and a later season", func(t *testing.T) {
		c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"seasons":[{"season_number":0,"air_date":"2016-01-01"},{"season_number":2,"air_date":"2020-01-01"},{"season_number":1,"air_date":"2019-01-01"}]}`))
		})
		if year, ok, err := c.PremiereYear(context.Background(), "1"); err != nil || !ok || year != 2019 {
			t.Fatalf("PremiereYear = %d, %v, %v, want 2019 of season 1", year, ok, err)
		}
	})
	t.Run("404", func(t *testing.T) {
		c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		if year, ok, err := c.PremiereYear(context.Background(), "999999"); err != nil || ok || year != 0 {
			t.Fatalf("PremiereYear on 404 = %d, %v, %v, want nothing without error", year, ok, err)
		}
	})
	t.Run("invalid id", func(t *testing.T) {
		if _, _, err := noRequests(t).PremiereYear(context.Background(), "1/2"); err == nil {
			t.Error("PremiereYear(1/2) returned no error")
		}
	})
	t.Run("500", func(t *testing.T) {
		c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
		_, _, err := c.PremiereYear(context.Background(), "75367")
		checkError(t, err, false)
	})
}

// noRequests returns a client whose server fails the test on any request.
func noRequests(t *testing.T) *Client {
	t.Helper()
	return serve(t, func(_ http.ResponseWriter, r *http.Request) { t.Errorf("unexpected request %s", r.URL) })
}

func TestYearMovie(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/272165/release_dates" || r.URL.RawQuery != "" {
			t.Errorf("request = %s, want /3/movie/272165/release_dates", r.URL)
		}
		file(t, "movie_release_dates.json")(w, r)
	})

	year, ok, err := c.Year(context.Background(), "272165", false)
	if err != nil || !ok || year != 1984 {
		t.Fatalf("Year = %d, %v, %v, want earliest release year 1984", year, ok, err)
	}
}

func TestSeasonEpisodes(t *testing.T) {
	fixture := file(t, "tv_season_episodes.json")
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/tv/77/season/1":
			fixture(w, r)
		case "/3/tv/77/season/2":
			w.WriteHeader(http.StatusNotFound)
		case "/3/tv/77/season/3":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})
	got, err := c.SeasonEpisodes(context.Background(), "77", 1)
	if err != nil || !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("SeasonEpisodes = %v, %v, want [1 2]", got, err)
	}
	got, err = c.SeasonEpisodes(context.Background(), "77", 2)
	if err != nil || len(got) != 0 {
		t.Fatalf("SeasonEpisodes on 404 = %v, %v, want no evidence", got, err)
	}
	_, err = c.SeasonEpisodes(context.Background(), "77", 3)
	checkError(t, err, true)
}

func TestSeasonEpisodesRejectsInvalidParameters(t *testing.T) {
	c := serve(t, func(http.ResponseWriter, *http.Request) { t.Error("sent a request for invalid parameters") })
	for _, tc := range []struct {
		id     string
		season int
	}{{"1/2", 1}, {"77", 0}, {"77", -1}} {
		if _, err := c.SeasonEpisodes(context.Background(), tc.id, tc.season); err == nil {
			t.Errorf("SeasonEpisodes(%q, %d) returned no error", tc.id, tc.season)
		}
	}
}

func TestEpisodeGroups(t *testing.T) {
	fixture := file(t, "tv_episode_groups.json")
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/tv/1399/episode_groups":
			fixture(w, r)
		case "/3/tv/78/episode_groups":
			w.WriteHeader(http.StatusNotFound)
		case "/3/tv/79/episode_groups":
			w.WriteHeader(http.StatusUnauthorized)
		case "/3/tv/81/episode_groups":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/3/tv/80/episode_groups":
			w.Write([]byte(`{"results":[{"id":"../bad","name":"Bad","type":1,"episode_count":10},{"id":"5e9077d2e640d600151f32bd","name":"Good","type":1,"episode_count":10}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})

	got, err := c.EpisodeGroups(context.Background(), "1399")
	want := []EpisodeGroup{
		{ID: "5e9077d2e640d600151f32bd", Name: "Aired Order", Type: EpisodeGroupOriginalAirDate, EpisodeCount: 102},
		{ID: "5acf93e60e0a26346d0000ce", Name: "DVD Order", Type: EpisodeGroupDVD, EpisodeCount: 73},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("EpisodeGroups = %+v, %v, want %+v", got, err, want)
	}
	got, err = c.EpisodeGroups(context.Background(), "78")
	if err != nil || len(got) != 0 {
		t.Fatalf("EpisodeGroups on 404 = %+v, %v, want empty", got, err)
	}
	_, err = c.EpisodeGroups(context.Background(), "79")
	checkError(t, err, true)
	_, err = c.EpisodeGroups(context.Background(), "81")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("EpisodeGroups on 429 = %v, want ErrRateLimited", err)
	}
	got, err = c.EpisodeGroups(context.Background(), "80")
	if err != nil || len(got) != 1 || got[0].ID != "5e9077d2e640d600151f32bd" {
		t.Fatalf("EpisodeGroups with bad id = %+v, %v, want only valid entry", got, err)
	}
}

func TestEpisodeGroupsRejectsInvalidID(t *testing.T) {
	c := serve(t, func(http.ResponseWriter, *http.Request) { t.Error("sent a request for invalid id") })
	for _, id := range []string{"", "..", "1/2", "tt123"} {
		if _, err := c.EpisodeGroups(context.Background(), id); err == nil {
			t.Errorf("EpisodeGroups(%q) returned no error", id)
		}
	}
}

func TestEpisodeGroupSeasons(t *testing.T) {
	details := file(t, "tv_episode_group_details.json")
	dvd := file(t, "tv_episode_group_dvd.json")
	abs := file(t, "tv_episode_group_absolute.json")
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/tv/episode_group/5acf93e60e0a26346d0000ce":
			details(w, r)
		case "/3/tv/episode_group/5b829bf6c3a368469d0034a7":
			dvd(w, r)
		case "/3/tv/episode_group/5eb7353b0cb3350020ce402e":
			abs(w, r)
		case "/3/tv/episode_group/000000000000000000000404":
			w.WriteHeader(http.StatusNotFound)
		case "/3/tv/episode_group/000000000000000000000401":
			w.WriteHeader(http.StatusUnauthorized)
		case "/3/tv/episode_group/000000000000000000000429":
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})

	// Published TMDB example without specials (explicit 0-based episode order).
	got, err := c.EpisodeGroupSeasons(context.Background(), "5acf93e60e0a26346d0000ce")
	if err != nil || len(got) != 2 || !slices.Equal(got[1], []int{1, 2, 3}) || !slices.Equal(got[2], []int{1}) {
		t.Fatalf("details = %+v, %v, want {1:[1 2 3], 2:[1]}", got, err)
	}
	// DVD order with explicit Specials (order 0) and a season gap (1, 3).
	got, err = c.EpisodeGroupSeasons(context.Background(), "5b829bf6c3a368469d0034a7")
	if err != nil || len(got) != 2 || !slices.Equal(got[1], []int{1, 2}) || !slices.Equal(got[3], []int{1}) {
		t.Fatalf("dvd = %+v, %v, want {1:[1 2], 3:[1]}", got, err)
	}
	// Absolute order with single group order 1 -> Plex season 1.
	got, err = c.EpisodeGroupSeasons(context.Background(), "5eb7353b0cb3350020ce402e")
	if err != nil || len(got) != 1 || !slices.Equal(got[1], []int{1, 2, 3}) {
		t.Fatalf("absolute = %+v, %v, want {1:[1 2 3]}", got, err)
	}
	got, err = c.EpisodeGroupSeasons(context.Background(), "000000000000000000000404")
	if err != nil || len(got) != 0 {
		t.Fatalf("404 = %+v, %v, want nil", got, err)
	}
	_, err = c.EpisodeGroupSeasons(context.Background(), "000000000000000000000401")
	checkError(t, err, true)
	_, err = c.EpisodeGroupSeasons(context.Background(), "000000000000000000000429")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("429 = %v, want ErrRateLimited", err)
	}
}

func TestEpisodeGroupSeasonsRejectsAmbiguousOrders(t *testing.T) {
	cases := map[string]string{
		"missing episode order":   `{"groups":[{"order":1,"episodes":[{}]}]}`,
		"null episode order":      `{"groups":[{"order":1,"episodes":[{"order":null}]}]}`,
		"negative episode order":  `{"groups":[{"order":1,"episodes":[{"order":-1}]}]}`,
		"duplicate episode order": `{"groups":[{"order":1,"episodes":[{"order":0},{"order":0}]}]}`,
		"missing subgroup order":  `{"groups":[{"episodes":[{"order":0}]},{"order":1,"episodes":[{"order":0}]}]}`,
		"null subgroup order":     `{"groups":[{"order":null,"episodes":[{"order":0}]},{"order":1,"episodes":[{"order":0}]}]}`,
		"negative subgroup order": `{"groups":[{"order":-1,"episodes":[{"order":0}]},{"order":1,"episodes":[{"order":0}]}]}`,
		"duplicate group order":   `{"groups":[{"order":1,"episodes":[{"order":0}]},{"order":1,"episodes":[{"order":0}]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
			got, err := c.EpisodeGroupSeasons(context.Background(), "5e9077d2e640d600151f32bd")
			if err != nil || len(got) != 0 {
				t.Fatalf("EpisodeGroupSeasons = %+v, %v, want nil", got, err)
			}
		})
	}
}

func TestRateLimitBackoffRetry(t *testing.T) {
	t.Run("retries transient 429 with exponential backoff and succeeds", func(t *testing.T) {
		var attempts atomic.Int32
		c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			if attempts.Add(1) <= 2 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Write([]byte(`{"episodes":[{"episode_number":1},{"episode_number":2}]}`))
		})
		var waits []time.Duration
		c.sleep = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}

		got, err := c.SeasonEpisodes(t.Context(), "77", 1)
		if err != nil || !slices.Equal(got, []int{1, 2}) {
			t.Fatalf("SeasonEpisodes = %v, %v, want [1 2]", got, err)
		}
		wantWaits := []time.Duration{500 * time.Millisecond, 1 * time.Second}
		if !slices.Equal(waits, wantWaits) {
			t.Fatalf("waits = %v, want %v", waits, wantWaits)
		}
	})

	t.Run("honors Retry-After seconds and HTTP-date clamped to max wait", func(t *testing.T) {
		now := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
		var attempts atomic.Int32
		c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			switch attempts.Add(1) {
			case 1:
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(http.StatusTooManyRequests)
			case 2:
				w.Header().Set("Retry-After", "10000000000")
				w.WriteHeader(http.StatusTooManyRequests)
			case 3:
				w.Header().Set("Retry-After", now.Add(4*time.Second).Format(http.TimeFormat))
				w.WriteHeader(http.StatusTooManyRequests)
			default:
				w.WriteHeader(http.StatusTooManyRequests)
			}
		})
		c.now = func() time.Time { return now }
		var waits []time.Duration
		c.sleep = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}

		_, err := c.SeasonEpisodes(t.Context(), "77", 1)
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("err = %v, want ErrRateLimited", err)
		}
		wantWaits := []time.Duration{3 * time.Second, maxRateLimitWait, 4 * time.Second}
		if !slices.Equal(waits, wantWaits) {
			t.Fatalf("waits = %v, want %v", waits, wantWaits)
		}
		if got := attempts.Load(); got != maxRateLimitRetries+1 {
			t.Fatalf("attempts = %d, want %d", got, maxRateLimitRetries+1)
		}
	})

	t.Run("stops retry when context cancels during backoff", func(t *testing.T) {
		var attempts atomic.Int32
		c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
		})
		ctx, cancel := context.WithCancel(t.Context())
		c.sleep = func(ctx context.Context, _ time.Duration) error {
			cancel()
			return sleepContext(ctx, time.Second)
		}

		_, err := c.SeasonEpisodes(ctx, "77", 1)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if got := attempts.Load(); got != 1 {
			t.Fatalf("attempts = %d, want 1", got)
		}
	})
}

func TestEpisodeGroupSeasonsRejectsInvalidID(t *testing.T) {
	c := serve(t, func(http.ResponseWriter, *http.Request) { t.Error("sent a request for invalid group id") })
	for _, id := range []string{"", "..", "5e90/bad", "not-hex"} {
		if _, err := c.EpisodeGroupSeasons(context.Background(), id); err == nil {
			t.Errorf("EpisodeGroupSeasons(%q) returned no error", id)
		}
	}
}

func TestYearNothing(t *testing.T) {
	tests := []struct {
		name string
		show bool
		body string
	}{
		{"only specials", true, `{"seasons":[{"season_number":0,"air_date":"2017-11-27"}]}`},
		{"null season dates", true, `{"seasons":[{"season_number":1,"air_date":null}]}`},
		{"bad date format", true, `{"seasons":[{"season_number":1,"air_date":"20220101"}]}`},
		{"empty movie releases", false, `{"results":[]}`},
		{"blank movie release date", false, `{"results":[{"release_dates":[{"release_date":""}]}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(tt.body)) })
			year, ok, err := c.Year(context.Background(), "1", tt.show)
			if err != nil || ok || year != 0 {
				t.Fatalf("Year = %d, %v, %v, want nothing", year, ok, err)
			}
		})
	}
	for _, show := range []bool{true, false} {
		name := "movie 404"
		if show {
			name = "series 404"
		}
		t.Run(name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
			year, ok, err := c.Year(context.Background(), "999999", show)
			if err != nil || ok || year != 0 {
				t.Fatalf("Year on 404 = %d, %v, %v, want nothing without error", year, ok, err)
			}
		})
	}
}

func TestYearRejectsInvalidID(t *testing.T) {
	var hits atomic.Int32
	c := serve(t, func(http.ResponseWriter, *http.Request) { hits.Add(1) })
	for _, id := range []string{"", "..", "1/2", "tt123"} {
		if _, _, err := c.Year(context.Background(), id, true); err == nil {
			t.Errorf("Year(%q) returned no error", id)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("server received %d requests, want 0", n)
	}
}

func TestYearErrors(t *testing.T) {
	for _, tt := range []struct {
		name         string
		status       int
		show         bool
		unauthorized bool
	}{
		{"series 401", http.StatusUnauthorized, true, true},
		{"movie 500", http.StatusInternalServerError, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) })
			_, _, err := c.Year(context.Background(), "75367", tt.show)
			checkError(t, err, tt.unauthorized)
		})
	}
}

func TestClientTimeoutPreservesDeadlineWithoutTransportDetails(t *testing.T) {
	c := serve(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	c.http.Timeout = 50 * time.Millisecond
	ctx := t.Context()
	err := c.Check(ctx)
	if ctx.Err() != nil {
		t.Fatalf("caller context ended: %v", ctx.Err())
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if strings.Contains(err.Error(), c.base.Host) || strings.Contains(err.Error(), token) {
		t.Fatalf("timeout exposed transport details: %v", err)
	}
}

func TestBodyReadErrorsSanitizedAndBounded(t *testing.T) {
	t.Run("timeout after headers", func(t *testing.T) {
		release := make(chan struct{})
		c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-release
		})
		t.Cleanup(func() { close(release) })
		c.http.Timeout = 50 * time.Millisecond

		_, _, err := c.Find(t.Context(), "tvdb", "1", true)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
		if strings.Contains(err.Error(), c.base.Host) || strings.Contains(err.Error(), token) {
			t.Fatalf("error exposed transport details: %v", err)
		}
	})

	t.Run("oversized response rejected", func(t *testing.T) {
		old := maxJSONBytes
		maxJSONBytes = 32
		t.Cleanup(func() { maxJSONBytes = old })

		c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"tv_results":[{"id":1,"pad":"` + strings.Repeat("a", 64) + `"}]}`))
		})
		_, _, err := c.Find(t.Context(), "tvdb", "1", true)
		if err == nil || !strings.Contains(err.Error(), "response too large") {
			t.Fatalf("err = %v, want response too large", err)
		}
	})
}
