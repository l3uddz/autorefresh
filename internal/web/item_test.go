package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

type itemAnswer struct {
	Item     map[string]any   `json:"item"`
	Timeline []map[string]any `json:"timeline"`
}

// getItem sends GET /api/item with the query and decodes the answer.
func getItem(t *testing.T, s *Server, query string) (itemAnswer, string) {
	t.Helper()
	var got itemAnswer
	body := getJSON(t, s, "/api/item?"+query, &got)
	return got, body
}

func TestItemFromRowAndHistory(t *testing.T) {
	st := openStore(t)
	if err := st.SaveLibraries("Home", []store.LibraryInfo{{Section: "1", Title: "Movies", Type: "movie"}}); err != nil {
		t.Fatal(err)
	}
	alien := movie("101", "Alien", store.NoSummary, time.Time{})
	alien.Year, alien.Path = 1979, "/movies/Alien (1979)"
	readLibrary(t, st, "Home", "1", testNow.Add(-3*time.Hour), alien)
	requested := testNow.Add(-2 * time.Hour)
	err := st.RecordRequest(store.Request{Server: "Home", RatingKey: "101", At: requested, Source: store.SourceAuto,
		Action: store.ActionMatch, Accepted: true, Match: &store.MatchTarget{
			GUID: "plex://movie/abc", Title: "Alien", Year: 1979, Links: []string{"tmdb-348"},
			PathYear: 1979, TMDBYear: 1979,
		}})
	if err != nil {
		t.Fatal(err)
	}
	s := newAPIServer(st, 0, "Home")

	got, body := getItem(t, s, "srv=0&key=101")
	if got.Item["key"] != "101" || got.Item["title"] != "Alien" || got.Item["cached"] != true ||
		got.Item["library"] != "Movies" || got.Item["srv"] != 0.0 {
		t.Errorf("item = %v", got.Item)
	}
	if len(got.Timeline) != 1 {
		t.Fatalf("timeline = %v, want one request", got.Timeline)
	}
	req := got.Timeline[0]
	want := map[string]any{
		"kind": "request", "source": "auto", "action": "match", "accepted": true, "outcome": "match",
		"match":    map[string]any{"guid": "plex://movie/abc", "title": "Alien", "year": 1979.0},
		"links":    []any{"tmdb-348"},
		"pathYear": 1979.0, "tmdbYear": 1979.0, "searches": []any{},
	}
	for field, w := range want {
		if !reflect.DeepEqual(req[field], w) {
			t.Errorf("request %s = %#v, want %#v", field, req[field], w)
		}
	}
	if id, ok := req["id"].(float64); !ok || id <= 0 {
		t.Errorf("request id = %v", req["id"])
	}
	assertTime(t, req, "at", requested.Truncate(time.Second))
	assertAbsent(t, req, "error", "conflict", "episodeCount", "fixedAction", "fixedMatch", "fixedMatchEventId")
	if !strings.Contains(body, `"searches":[]`) {
		t.Errorf("body has no empty searches: %s", body)
	}

	// The read completes the item, so its row is gone.
	done := testNow.Add(-time.Hour)
	remastered := movie("101", "Alien Remastered", 0, time.Time{})
	readLibrary(t, st, "Home", "1", done, remastered)

	got, _ = getItem(t, s, "srv=0&key=101")
	if got.Item["cached"] != false || got.Item["title"] != "Alien Remastered" || got.Item["path"] != "/movies/Alien (1979)" {
		t.Errorf("completed item = %v", got.Item)
	}
	if len(got.Timeline) != 2 {
		t.Fatalf("timeline = %v, want completed and request", got.Timeline)
	}
	completed := got.Timeline[0]
	if completed["kind"] != "completed" || completed["fixedAction"] != "match" || completed["fixedMatch"] != "match" ||
		completed["fixedMatchEventId"] != req["id"] {
		t.Errorf("completed event = %v", completed)
	}
	assertTime(t, completed, "at", done.Truncate(time.Second))
	if got.Timeline[1]["id"] != req["id"] {
		t.Errorf("second event = %v, want the request", got.Timeline[1])
	}

	for _, tt := range []struct {
		query  string
		status int
		msg    string
	}{
		{"srv=0&key=999", http.StatusNotFound, "not found"},
		{"srv=0&key=1/2", http.StatusBadRequest, "invalid key"},
		{"srv=0&key=", http.StatusBadRequest, "invalid key"},
		{"srv=0&key=..", http.StatusBadRequest, "invalid key"},
		{"srv=0", http.StatusBadRequest, "invalid key"},
		{"key=101", http.StatusBadRequest, "invalid srv"},
		{"srv=1&key=101", http.StatusBadRequest, "invalid srv"},
		{"srv=-1&key=101", http.StatusBadRequest, "invalid srv"},
		{"srv=a&key=101", http.StatusBadRequest, "invalid srv"},
	} {
		t.Run(tt.query, func(t *testing.T) {
			rec := serve(s, httptest.NewRequest(http.MethodGet, "/api/item?"+tt.query, nil))
			assertJSONError(t, rec, tt.status, tt.msg)
		})
	}
}

func TestItemTimelineDropsUnsafeCandidateThumbs(t *testing.T) {
	st := openStore(t)
	readLibrary(t, st, "Home", "1", testNow, movie("101", "Alien", store.Unmatched, time.Time{}))
	err := st.RecordMatchDecision(store.MatchDecision{
		Server: "Home", RatingKey: "101", At: testNow.Add(-time.Minute), Source: store.SourceAuto,
		Outcome: store.DecisionNoMatch, Conflict: "years differ", PathYear: 1979, TMDBYear: 1980, EpisodeCount: 3,
		Searches: []store.MatchSearch{
			{Query: "tmdb-348", Source: "path", Candidates: []store.MatchCandidate{
				{GUID: "plex://movie/a", Name: "Alien", Year: 1979, Type: "movie", Summary: "In space.",
					Thumb: "http://h/x.jpg", Score: 90, Verdict: "year-mismatch"},
				{GUID: "plex://movie/b", Name: "Aliens", Thumb: "https://h/y.jpg", Verdict: "rejected"},
			}},
			{Query: "tmdb-349", Source: "tmdb", FromID: "imdb-tt0078748", Failed: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := newAPIServer(st, 0, "Home")

	got, body := getItem(t, s, "srv=0&key=101")
	if len(got.Timeline) != 1 {
		t.Fatalf("timeline = %v", got.Timeline)
	}
	e := got.Timeline[0]
	if e["kind"] != "match-decision" || e["outcome"] != "no-match" || e["conflict"] != "years differ" ||
		e["pathYear"] != 1979.0 || e["tmdbYear"] != 1980.0 || e["episodeCount"] != 3.0 || e["accepted"] != false {
		t.Errorf("event = %v", e)
	}
	assertAbsent(t, e, "match", "action", "links")
	searches, _ := e["searches"].([]any)
	if len(searches) != 2 {
		t.Fatalf("searches = %v", e["searches"])
	}
	first, _ := searches[0].(map[string]any)
	if first["query"] != "tmdb-348" || first["source"] != "path" || first["failed"] != false {
		t.Errorf("first search = %v", first)
	}
	assertAbsent(t, first, "fromId")
	candidates, _ := first["candidates"].([]any)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %v", first["candidates"])
	}
	unsafe, _ := candidates[0].(map[string]any)
	wantUnsafe := map[string]any{
		"guid": "plex://movie/a", "name": "Alien", "year": 1979.0, "type": "movie", "summary": "In space.",
		"score": 90.0, "verdict": "year-mismatch",
	}
	for field, w := range wantUnsafe {
		if !reflect.DeepEqual(unsafe[field], w) {
			t.Errorf("candidate %s = %#v, want %#v", field, unsafe[field], w)
		}
	}
	assertAbsent(t, unsafe, "thumb")
	if safe, _ := candidates[1].(map[string]any); safe["thumb"] != "https://h/y.jpg" {
		t.Errorf("safe candidate = %v", safe)
	}
	second, _ := searches[1].(map[string]any)
	if second["fromId"] != "imdb-tt0078748" || second["failed"] != true || second["source"] != "tmdb" {
		t.Errorf("second search = %v", second)
	}
	if !strings.Contains(body, `"candidates":[]`) || strings.Contains(body, "http://h/x.jpg") {
		t.Errorf("body = %s", body)
	}
}

func TestItemTimelineDropsCandidateThumbsWithSecrets(t *testing.T) {
	const secret = "sentinel-secret"
	thumbs := map[string]string{
		"query":          "https://img.example/p.jpg?sig=" + secret,
		"path":           "https://img.example/" + secret + "/p.jpg",
		"encoded-path":   "https://img.example/%73entinel-secret/p.jpg",
		"encoded-query":  "https://img.example/p.jpg?sig=%73entinel%2Dsecret",
		"clean":          "https://img.example/p.jpg?w=300",
		"encoded-clean":  "https://img.example/a%20b.jpg",
		"secret-in-host": "https://" + secret + ".example/p.jpg",
	}
	var candidates []store.MatchCandidate
	for name, thumb := range thumbs {
		candidates = append(candidates, store.MatchCandidate{GUID: "plex://movie/" + name, Thumb: thumb})
	}
	st := openStore(t)
	readLibrary(t, st, "Home", "1", testNow, movie("101", "Alien", store.Unmatched, time.Time{}))
	err := st.RecordMatchDecision(store.MatchDecision{
		Server: "Home", RatingKey: "101", At: testNow, Source: store.SourceAuto, Outcome: store.DecisionNoMatch,
		Searches: []store.MatchSearch{{Query: "tmdb-348", Source: "path", Candidates: candidates}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Servers: []PlexServer{{Name: "Home", Plex: &fakePlex{}}},
		Store:   st,
		Secrets: []string{"", secret},
	})

	got, body := getItem(t, s, "srv=0&key=101")
	if strings.Contains(body, secret) || strings.Contains(body, "entinel") {
		t.Errorf("body holds the secret: %s", body)
	}
	if len(got.Timeline) != 1 {
		t.Fatalf("timeline = %v", got.Timeline)
	}
	searches, _ := got.Timeline[0]["searches"].([]any)
	if len(searches) != 1 {
		t.Fatalf("searches = %v", got.Timeline[0]["searches"])
	}
	search, _ := searches[0].(map[string]any)
	gotThumbs := map[string]any{}
	for _, c := range search["candidates"].([]any) {
		c, _ := c.(map[string]any)
		name, _ := strings.CutPrefix(c["guid"].(string), "plex://movie/")
		gotThumbs[name] = c["thumb"]
	}
	want := map[string]any{"clean": thumbs["clean"], "encoded-clean": thumbs["encoded-clean"]}
	for name := range thumbs {
		if gotThumbs[name] != want[name] {
			t.Errorf("thumb of %s = %v, want %v", name, gotThumbs[name], want[name])
		}
	}
}

func TestItemTimelineEmptyIsArray(t *testing.T) {
	st := openStore(t)
	readLibrary(t, st, "Home", "1", testNow, movie("101", "Alien", store.NoSummary, time.Time{}))
	_, body := getItem(t, newAPIServer(st, 0, "Home"), "srv=0&key=101")
	if !strings.Contains(body, `"timeline":[]`) {
		t.Errorf("body = %s", body)
	}
}

func TestStoredErrorsAreRedacted(t *testing.T) {
	st := openStore(t)
	readLibrary(t, st, "Home", "1", testNow, movie("101", "Alien", store.NoSummary, time.Time{}))
	err := st.RecordRequest(store.Request{Server: "Home", RatingKey: "101", At: testNow, Source: store.SourceAuto,
		Action: store.ActionRefresh, Error: "GET /x?t=sentinel-token: 500"})
	if err != nil {
		t.Fatal(err)
	}
	recordRunError(t, st, "Home", "token sentinel-token rejected")
	s := New(Options{
		Servers: []PlexServer{{Name: "Home", Plex: &fakePlex{}}},
		Store:   st,
		Secrets: []string{"", "sentinel-token"},
	})

	got, body := getItem(t, s, "srv=0&key=101")
	if strings.Contains(body, "sentinel-token") {
		t.Errorf("item body holds the secret: %s", body)
	}
	if len(got.Timeline) != 1 || got.Timeline[0]["error"] != "GET /x?t=<redacted>: 500" {
		t.Errorf("timeline = %v", got.Timeline)
	}

	runs, body := getRuns(t, s, "")
	if strings.Contains(body, "sentinel-token") {
		t.Errorf("runs body holds the secret: %s", body)
	}
	if len(runs.Runs) != 1 || runs.Runs[0]["error"] != "token <redacted> rejected" {
		t.Errorf("runs = %v", runs.Runs)
	}
}

func TestRedactMergesOverlappingSecrets(t *testing.T) {
	st := openStore(t)
	recordRunError(t, st, "Home", "GET /gateway/sentinel-path/library/sections: 500")
	s := New(Options{
		Servers: []PlexServer{{Name: "Home", Plex: &fakePlex{}}},
		Store:   st,
		Secrets: []string{"/gateway", "/gateway/sentinel-path"},
	})
	runs, _ := getRuns(t, s, "")
	if len(runs.Runs) != 1 || runs.Runs[0]["error"] != "GET <redacted>/library/sections: 500" {
		t.Errorf("runs = %v", runs.Runs)
	}

	for _, tt := range []struct {
		secrets    []string
		text, want string
	}{
		{[]string{"abc", "bcd"}, "xabcdy", "x<redacted>y"},
		{[]string{"ab", "cd"}, "xabcdy", "x<redacted>y"},
		{[]string{"aa"}, "xaaay", "x<redacted>y"},
		{[]string{"b", "abc"}, "abc", "<redacted>"},
		{[]string{"a"}, "a-a", "<redacted>-<redacted>"},
		{[]string{""}, "text", "text"},
		{nil, "text", "text"},
		{[]string{"zz"}, "", ""},
	} {
		if got := New(Options{Secrets: tt.secrets}).redact(tt.text); got != tt.want {
			t.Errorf("redact(%q) with %q = %q, want %q", tt.text, tt.secrets, got, tt.want)
		}
	}
}

func TestRunErrorFromPrefixedPlexURLIsRedacted(t *testing.T) {
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ps.Close()
	base := mustParseURL(t, ps.URL+"/sentinel-path")
	client := plex.New("Home", base, "plex-token", time.Second)
	_, libErr := client.Libraries(context.Background())
	if libErr == nil {
		t.Fatal("Libraries: no error")
	}
	st := openStore(t)
	recordRunError(t, st, "Home", libErr.Error())
	s := New(Options{
		Servers: []PlexServer{{Name: "Home", Plex: &fakePlex{}}},
		Store:   st,
		Secrets: []string{client.PathPrefix()},
	})

	runs, body := getRuns(t, s, "")
	if strings.Contains(body, "sentinel-path") {
		t.Errorf("runs body holds the path prefix: %s", body)
	}
	if len(runs.Runs) != 1 {
		t.Fatalf("runs = %v", runs.Runs)
	}
	msg, _ := runs.Runs[0]["error"].(string)
	if !strings.Contains(msg, "GET <redacted>/library/sections") || !strings.Contains(msg, "500") {
		t.Errorf("error = %q", msg)
	}
}

type liveAnswer struct {
	Summary   string   `json:"summary"`
	GUID      string   `json:"guid"`
	Seasons   *int     `json:"seasons"`
	Episodes  *int     `json:"episodes"`
	Locations []string `json:"locations"`
	Files     []string `json:"files"`
	Path      string   `json:"path"`
}

// liveServer returns a Server for st with one server Home whose Plex
// answers ItemDetails with details.
func liveServer(st *store.Store, details func(context.Context, string) (plex.Details, error)) *Server {
	return newServerWith(st, 0, []PlexServer{{Name: "Home", Plex: &fakePlex{details: details}}})
}

// storedPath returns the path of the row of key, or false when no row
// exists.
func storedPath(t *testing.T, st *store.Store, key string) (string, bool) {
	t.Helper()
	it, found, err := st.ItemDetail(context.Background(), "Home", key, store.DueRules{})
	if err != nil {
		t.Fatal(err)
	}
	return it.Path, found && it.Cached
}

func TestItemLive(t *testing.T) {
	st := openStore(t)
	dark := movie("201", "Dark", store.NoSummary, time.Time{})
	dark.Type = "show"
	alien := movie("101", "Alien", store.NoSummary, time.Time{})
	alien.Path = "/movies/Alien (1979)/Alien.mkv"
	gone := movie("202", "Gone", store.NoSummary, time.Time{})
	gone.Type = "show"
	readLibrary(t, st, "Home", "1", testNow.Add(-time.Hour), dark, alien, gone)
	completedGone := movie("202", "Gone", 0, time.Time{})
	completedGone.Type = "show"
	readLibrary(t, st, "Home", "1", testNow, dark, alien, completedGone)

	answers := map[string]plex.Details{
		"201": {GUID: "plex://show/dark", Summary: "Time travel.", ChildCount: 3, LeafCount: 26,
			Paths: plex.Paths{Locations: []string{"/tv/Dark", "/tv2/Dark"}}},
		"101": {GUID: "plex://movie/alien", Summary: "In space.",
			Paths: plex.Paths{Files: []string{"/movies/Alien (1979)/Alien.mkv"}}},
		"202": {GUID: "plex://show/gone", Paths: plex.Paths{Locations: []string{"/tv/Gone"}}},
	}
	s := liveServer(st, func(_ context.Context, key string) (plex.Details, error) {
		return answers[key], nil
	})

	var show liveAnswer
	getJSON(t, s, "/api/item/live?srv=0&key=201", &show)
	if show.GUID != "plex://show/dark" || show.Summary != "Time travel." || show.Path != "/tv/Dark" ||
		show.Seasons == nil || *show.Seasons != 3 || show.Episodes == nil || *show.Episodes != 26 ||
		!reflect.DeepEqual(show.Locations, []string{"/tv/Dark", "/tv2/Dark"}) || len(show.Files) != 0 {
		t.Errorf("show = %+v", show)
	}
	if p, cached := storedPath(t, st, "201"); !cached || p != "/tv/Dark" {
		t.Errorf("show row path = %q (cached %v), want /tv/Dark", p, cached)
	}

	var film liveAnswer
	body := getJSON(t, s, "/api/item/live?srv=0&key=101", &film)
	if film.Path != "/movies/Alien (1979)/Alien.mkv" || film.Seasons != nil || film.Episodes != nil ||
		!reflect.DeepEqual(film.Files, []string{"/movies/Alien (1979)/Alien.mkv"}) {
		t.Errorf("movie = %+v", film)
	}
	if !strings.Contains(body, `"locations":[]`) {
		t.Errorf("movie body has no empty locations: %s", body)
	}

	var uncached liveAnswer
	getJSON(t, s, "/api/item/live?srv=0&key=202", &uncached)
	if uncached.Path != "/tv/Gone" {
		t.Errorf("uncached = %+v, want path /tv/Gone", uncached)
	}
	if _, cached := storedPath(t, st, "202"); cached {
		t.Error("the live view created a row for an uncached item")
	}

	var empty liveAnswer
	answers["201"] = plex.Details{}
	body = getJSON(t, s, "/api/item/live?srv=0&key=201", &empty)
	if !strings.Contains(body, `"locations":[]`) || !strings.Contains(body, `"files":[]`) {
		t.Errorf("empty details body = %s", body)
	}
}

func TestItemLiveKeepsPathSavedMeanwhile(t *testing.T) {
	st := openStore(t)
	dark := movie("201", "Dark", store.NoSummary, time.Time{})
	dark.Type = "show"
	readLibrary(t, st, "Home", "1", testNow, dark)
	s := liveServer(st, func(_ context.Context, key string) (plex.Details, error) {
		if err := st.SetPath("Home", key, "/run/path"); err != nil {
			return plex.Details{}, err
		}
		return plex.Details{Paths: plex.Paths{Locations: []string{"/tv/Other"}}}, nil
	})

	var got liveAnswer
	getJSON(t, s, "/api/item/live?srv=0&key=201", &got)
	if p, _ := storedPath(t, st, "201"); p != "/run/path" {
		t.Errorf("row path = %q, want /run/path", p)
	}
}

func TestItemLiveErrors(t *testing.T) {
	st := openStore(t)
	readLibrary(t, st, "Home", "1", testNow, movie("101", "Alien", store.NoSummary, time.Time{}))

	for _, tt := range []struct {
		err    error
		status int
		msg    string
	}{
		{plex.ErrNotFound, http.StatusNotFound, "not found in Plex"},
		{errors.Join(errors.New("401 at http://secret-host"), plex.ErrUnauthorized, plex.ErrUnavailable),
			http.StatusBadGateway, "Plex rejected the token"},
		{errors.Join(errors.New("503 at http://secret-host"), plex.ErrUnavailable), http.StatusBadGateway, "Plex unavailable"},
		{errors.New("bad body at http://secret-host"), http.StatusBadGateway, "Plex unavailable"},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			s := liveServer(st, func(context.Context, string) (plex.Details, error) {
				return plex.Details{}, tt.err
			})
			rec := serve(s, httptest.NewRequest(http.MethodGet, "/api/item/live?srv=0&key=101", nil))
			assertJSONError(t, rec, tt.status, tt.msg)
		})
	}

	calls := 0
	s := liveServer(st, func(context.Context, string) (plex.Details, error) {
		calls++
		return plex.Details{}, nil
	})
	for _, tt := range []struct {
		query  string
		status int
		msg    string
	}{
		{"srv=0&key=999", http.StatusNotFound, "not found"},
		{"srv=0&key=1/2", http.StatusBadRequest, "invalid key"},
		{"key=101", http.StatusBadRequest, "invalid srv"},
		{"srv=3&key=101", http.StatusBadRequest, "invalid srv"},
	} {
		rec := serve(s, httptest.NewRequest(http.MethodGet, "/api/item/live?"+tt.query, nil))
		assertJSONError(t, rec, tt.status, tt.msg)
	}
	if calls != 0 {
		t.Errorf("ItemDetails calls = %d, want 0 for an unknown or invalid item", calls)
	}
}

func TestItemLiveTimesOut(t *testing.T) {
	st := openStore(t)
	readLibrary(t, st, "Home", "1", testNow, movie("101", "Alien", store.NoSummary, time.Time{}))
	s := liveServer(st, func(ctx context.Context, _ string) (plex.Details, error) {
		select {
		case <-ctx.Done():
			return plex.Details{}, ctx.Err()
		case <-time.After(2 * time.Second):
			return plex.Details{}, errors.New("the context did not end")
		}
	})
	s.liveTimeout = 50 * time.Millisecond

	start := time.Now()
	rec := serve(s, httptest.NewRequest(http.MethodGet, "/api/item/live?srv=0&key=101", nil))
	assertJSONError(t, rec, http.StatusBadGateway, "Plex unavailable")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("answer took %v, want about 50ms", elapsed)
	}
}
