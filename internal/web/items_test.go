package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/store"
)

type itemsAnswer struct {
	Items  []map[string]any `json:"items"`
	Next   string           `json:"next"`
	Counts struct {
		Attention int `json:"attention"`
		Fixed     int `json:"fixed"`
		Stopped   int `json:"stopped"`
	} `json:"counts"`
}

// getItems sends GET /api/items with the query and decodes the answer.
func getItems(t *testing.T, s *Server, query string) (itemsAnswer, string) {
	t.Helper()
	var got itemsAnswer
	body := getJSON(t, s, "/api/items?"+query, &got)
	return got, body
}

// movie returns an item with reasons, a title, and an added time.
func movie(key, title string, reasons store.Reasons, added time.Time) store.Item {
	return store.Item{RatingKey: key, Type: "movie", Title: title, Reasons: reasons, AddedAt: added}
}

// accept records an accepted refresh of key at the time at.
func accept(t *testing.T, st *store.Store, server, key string, at time.Time) {
	t.Helper()
	err := st.RecordRequest(store.Request{Server: server, RatingKey: key, At: at, Source: store.SourceAuto,
		Action: store.ActionRefresh, Accepted: true})
	if err != nil {
		t.Fatal(err)
	}
}

// byKey returns the listed items by their key.
func byKey(t *testing.T, items []map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, it := range items {
		key, _ := it["key"].(string)
		if _, dup := out[key]; dup {
			t.Fatalf("key %q is listed twice", key)
		}
		out[key] = it
	}
	return out
}

func keysOf(items []map[string]any) []string {
	out := []string{}
	for _, it := range items {
		key, _ := it["key"].(string)
		out = append(out, key)
	}
	return out
}

// assertTime checks that field of it is an RFC 3339 time equal to want.
func assertTime(t *testing.T, it map[string]any, field string, want time.Time) {
	t.Helper()
	raw, ok := it[field].(string)
	if !ok {
		t.Errorf("%s = %v, want %v", field, it[field], want)
		return
	}
	got, err := time.Parse(time.RFC3339, raw)
	if err != nil || !got.Equal(want) {
		t.Errorf("%s = %q, want %v", field, raw, want)
	}
}

func assertAbsent(t *testing.T, it map[string]any, fields ...string) {
	t.Helper()
	for _, f := range fields {
		if v, ok := it[f]; ok {
			t.Errorf("%s = %v, want no key", f, v)
		}
	}
}

func TestItemsAttention(t *testing.T) {
	st := openStore(t)
	old := testNow.Add(-48 * time.Hour).Truncate(time.Second)
	if err := st.SaveLibraries("Home", []store.LibraryInfo{{Section: "1", Title: "Movies", Type: "movie"}}); err != nil {
		t.Fatal(err)
	}
	alien := movie("101", "Alien", store.NoSummary|store.NoPoster, old)
	alien.Year, alien.Path = 1979, "/movies/Alien (1979)"
	alien.Thumb, alien.Art = "/library/metadata/101/thumb/1700000000", "/library/metadata/101/art/1700000001"
	foreign := movie("103", "Brazil", store.NoSummary, old)
	foreign.Thumb = "/library/metadata/999/thumb/1"
	readLibrary(t, st, "Home", "1", testNow, alien, movie("102", "Ignored", store.NoArt, old), foreign)
	show := movie("201", "Dark", store.Unmatched, old)
	show.Type = "show"
	readLibrary(t, st, "Away", "2", testNow, show)
	attempt := testNow.Add(-10 * time.Minute)
	accept(t, st, "Home", "103", attempt)
	s := newAPIServer(st, store.NoArt, "Home", "Away")

	got, _ := getItems(t, s, "")
	items := byKey(t, got.Items)
	if want := []string{"101", "103", "201"}; !reflect.DeepEqual(sortedStrings(keysOf(got.Items)), want) {
		t.Fatalf("keys = %v, want %v", keysOf(got.Items), want)
	}
	if got.Counts.Attention != 3 || got.Counts.Fixed != 0 || got.Counts.Stopped != 0 {
		t.Errorf("counts = %+v, want 3/0/0", got.Counts)
	}

	a := items["101"]
	want := map[string]any{
		"srv": 0.0, "server": "Home", "section": "1", "library": "Movies", "key": "101", "type": "movie",
		"title": "Alien", "year": 1979.0, "path": "/movies/Alien (1979)", "cached": true,
		"reasons": []any{"no-summary", "no-poster"}, "state": "due", "attempts": 0.0, "refreshes": 0.0,
		"thumb": "api/items/thumb?key=101&srv=0&v=1700000000",
		"art":   "api/items/art?key=101&srv=0&v=1700000001",
	}
	for field, w := range want {
		if !reflect.DeepEqual(a[field], w) {
			t.Errorf("101 %s = %#v, want %#v", field, a[field], w)
		}
	}
	assertTime(t, a, "addedAt", old)
	assertAbsent(t, a, "nextDue", "lastAttempt", "stoppedAt", "lastAction", "lastOutcome", "lastMatchOutcome",
		"completedAt", "fixedAction", "fixedMatch")

	b := items["103"]
	if b["state"] != "waiting" || b["attempts"] != 1.0 || b["refreshes"] != 1.0 || b["lastAction"] != "refresh" ||
		b["lastOutcome"] != "refresh" {
		t.Errorf("103 = %v", b)
	}
	assertTime(t, b, "lastAttempt", attempt.Truncate(time.Second))
	assertTime(t, b, "nextDue", attempt.Truncate(time.Second).Add(time.Hour))
	assertAbsent(t, b, "thumb", "art", "year", "path")

	c := items["201"]
	if c["srv"] != 1.0 || c["server"] != "Away" || c["type"] != "show" || !reflect.DeepEqual(c["reasons"], []any{"unmatched"}) {
		t.Errorf("201 = %v", c)
	}
	assertAbsent(t, c, "library")

	all, _ := getItems(t, s, "all=1")
	if ignored := byKey(t, all.Items)["102"]; ignored == nil || ignored["state"] != "ignored" {
		t.Errorf("all=1: 102 = %v, want state ignored", ignored)
	}
	if all.Counts.Attention != 4 {
		t.Errorf("all=1: attention count = %d, want 4", all.Counts.Attention)
	}

	for query, wantKeys := range map[string][]string{
		"srv=1":            {"201"},
		"reason=unmatched": {"201"},
		"type=show":        {"201"},
		"q=+alien+":        {"101"},
		"srv=0&library=1":  {"101", "103"},
		"srv=1&library=1":  {},
	} {
		got, _ := getItems(t, s, query)
		if keys := sortedStrings(keysOf(got.Items)); !reflect.DeepEqual(keys, wantKeys) {
			t.Errorf("%s: keys = %v, want %v", query, keys, wantKeys)
		}
		if got.Counts.Attention != len(wantKeys) {
			t.Errorf("%s: attention count = %d, want %d", query, got.Counts.Attention, len(wantKeys))
		}
	}
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

// seedMovies stores n movies on Home with distinct titles, years, and added
// times. The keys are "1" to "n".
func seedMovies(t *testing.T, st *store.Store, n int) {
	t.Helper()
	items := make([]store.Item, n)
	for i := range items {
		key := string(rune('1' + i))
		items[i] = movie(key, "Title "+string(rune('E'-i)), store.NoSummary, testNow.Add(-time.Duration(48+i)*time.Hour))
		items[i].Year = 2000 + i
	}
	readLibrary(t, st, "Home", "1", testNow, items...)
}

// pages follows the pages of query with limit items each. It returns the
// keys of each page and checks that only the last page has no next.
func pages(t *testing.T, s *Server, query url.Values, limit string) [][]string {
	t.Helper()
	query.Set("limit", limit)
	var out [][]string
	for range 10 {
		got, body := getItems(t, s, query.Encode())
		out = append(out, keysOf(got.Items))
		if got.Next == "" {
			if strings.Contains(body, `"next"`) {
				t.Errorf("%s: body has an empty next: %s", query.Encode(), body)
			}
			return out
		}
		query.Set("cursor", got.Next)
	}
	t.Fatalf("%s: too many pages", query.Encode())
	return nil
}

func TestItemsPages(t *testing.T) {
	st := openStore(t)
	seedMovies(t, st, 5)
	s := newAPIServer(st, 0, "Home")

	for _, sort := range []string{"", "title", "-title", "year", "-added", "due"} {
		t.Run(sort, func(t *testing.T) {
			q := url.Values{}
			if sort != "" {
				q.Set("sort", sort)
			}
			got := pages(t, s, q, "2")
			sizes := []int{}
			seen := map[string]bool{}
			for _, page := range got {
				sizes = append(sizes, len(page))
				for _, key := range page {
					if seen[key] {
						t.Errorf("key %s repeats in %v", key, got)
					}
					seen[key] = true
				}
			}
			if !reflect.DeepEqual(sizes, []int{2, 2, 1}) || len(seen) != 5 {
				t.Errorf("pages = %v, want sizes 2, 2, 1 of 5 keys", got)
			}
		})
	}

	// The scope holds the default sort, so a cursor of the default order
	// also fits the same order spelled out.
	first, _ := getItems(t, s, "limit=2")
	second, _ := getItems(t, s, "sort=-tries&limit=3&cursor="+first.Next)
	if len(second.Items) != 3 {
		t.Errorf("cursor of the default sort with sort=-tries: %v", keysOf(second.Items))
	}
}

func TestItemsLastPageExactlyLimit(t *testing.T) {
	st := openStore(t)
	seedMovies(t, st, 4)
	s := newAPIServer(st, 0, "Home")

	got := pages(t, s, url.Values{}, "2")
	if len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 2 {
		t.Errorf("pages = %v, want two pages of 2", got)
	}
}

func TestItemsAcceptsEverySort(t *testing.T) {
	st := openStore(t)
	seedMovies(t, st, 3)
	s := newAPIServer(st, 0, "Home")

	queue := []string{"title", "year", "added", "due", "tries", "last-attempt", "stopped"}
	for tab, keys := range map[string][]string{
		"attention": queue,
		"stopped":   queue,
		"fixed":     {"title", "year", "completed"},
	} {
		for _, key := range keys {
			for _, sort := range []string{key, "-" + key} {
				q := url.Values{"tab": {tab}, "sort": {sort}}
				got := pages(t, s, q, "1")
				if tab == "attention" && len(got) != 3 {
					t.Errorf("%s: pages = %v, want 3", q.Encode(), got)
				}
			}
		}
	}
}

func TestItemsFixedTab(t *testing.T) {
	st := openStore(t)
	readLibrary(t, st, "Home", "1", testNow.Add(-2*time.Hour),
		movie("301", "Requested", store.NoSummary, time.Time{}),
		movie("302", "Unrequested", store.NoSummary, time.Time{}),
		movie("303", "Open", store.NoSummary, time.Time{}))
	accept(t, st, "Home", "301", testNow.Add(-90*time.Minute))
	done := testNow.Add(-time.Hour)
	readLibrary(t, st, "Home", "1", done,
		movie("301", "Requested", 0, time.Time{}),
		movie("302", "Unrequested", 0, time.Time{}),
		movie("303", "Open", store.NoSummary, time.Time{}))
	s := newAPIServer(st, 0, "Home")

	got, _ := getItems(t, s, "tab=fixed")
	if len(got.Items) != 1 {
		t.Fatalf("items = %v, want only 301", got.Items)
	}
	it := got.Items[0]
	if it["key"] != "301" || it["fixedAction"] != "refresh" || it["cached"] != false || it["title"] != "Requested" ||
		it["attempts"] != 0.0 || it["refreshes"] != 0.0 {
		t.Errorf("item = %v", it)
	}
	assertTime(t, it, "completedAt", done.Truncate(time.Second))
	assertAbsent(t, it, "reasons", "state", "addedAt", "fixedMatch")
	if got.Counts.Attention != 1 || got.Counts.Fixed != 1 {
		t.Errorf("counts = %+v, want attention 1, fixed 1", got.Counts)
	}

	all, _ := getItems(t, s, "tab=fixed&all=1")
	if keys := sortedStrings(keysOf(all.Items)); !reflect.DeepEqual(keys, []string{"301", "302"}) {
		t.Errorf("all=1: keys = %v, want 301, 302", keys)
	}
	if all.Counts.Fixed != 2 || all.Counts.Attention != 1 {
		t.Errorf("all=1: counts = %+v, want attention 1, fixed 2", all.Counts)
	}

	// all=1 applies only to the chosen tab.
	attention, _ := getItems(t, s, "tab=attention&all=1")
	if attention.Counts.Fixed != 1 {
		t.Errorf("tab=attention&all=1: fixed count = %d, want 1", attention.Counts.Fixed)
	}
}

func TestItemsEmptyListsAreArrays(t *testing.T) {
	s := newAPIServer(openStore(t), 0, "Home")

	_, body := getItems(t, s, "")
	if !strings.Contains(body, `"items":[]`) || strings.Contains(body, `"next"`) {
		t.Errorf("items body = %s", body)
	}
	var servers serversAnswer
	body = getJSON(t, s, "/api/servers", &servers)
	for _, want := range []string{`"libraries":[]`, `"ignoreReasons":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("servers body has no %s: %s", want, body)
		}
	}
}

func TestItemsHidesRemovedServers(t *testing.T) {
	st := openStore(t)
	readLibrary(t, st, "Home", "1", testNow, movie("1", "Kept", store.NoSummary, time.Time{}))
	readLibrary(t, st, "Gone", "1", testNow.Add(-time.Hour),
		movie("2", "Removed", store.NoSummary, time.Time{}), movie("3", "Removed fixed", store.NoSummary, time.Time{}))
	readLibrary(t, st, "Gone", "1", testNow,
		movie("2", "Removed", store.NoSummary, time.Time{}), movie("3", "Removed fixed", 0, time.Time{}))
	s := newAPIServer(st, 0, "Home")

	got, _ := getItems(t, s, "")
	if keys := keysOf(got.Items); !reflect.DeepEqual(keys, []string{"1"}) || got.Counts.Attention != 1 {
		t.Errorf("keys = %v, counts = %+v, want only 1", keys, got.Counts)
	}
	fixed, _ := getItems(t, s, "tab=fixed&all=1")
	if len(fixed.Items) != 0 || fixed.Counts.Fixed != 0 {
		t.Errorf("fixed = %v, counts = %+v, want none", fixed.Items, fixed.Counts)
	}
}

// The store reads an empty server list as every server, so a server
// without configured servers lists nothing.
func TestItemsWithoutServersListsNothing(t *testing.T) {
	st := openStore(t)
	seedMovies(t, st, 2)
	s := newServerWith(st, 0, nil)

	got, body := getItems(t, s, "")
	if len(got.Items) != 0 || got.Next != "" || got.Counts.Attention != 0 || got.Counts.Fixed != 0 || got.Counts.Stopped != 0 {
		t.Errorf("answer = %s, want no items and zero counts", body)
	}
	if !strings.Contains(body, `"items":[]`) {
		t.Errorf("body = %s, want an empty items array", body)
	}
}

// editCursor decodes cursor, applies edit to its JSON fields, and encodes
// it again.
func editCursor(t *testing.T, cursor string, edit func(map[string]any)) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	edit(fields)
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestItemsRejectsInvalidParams(t *testing.T) {
	st := openStore(t)
	seedMovies(t, st, 3)
	s := newAPIServer(st, 0, "Home", "Away")

	next := func(query string) string {
		t.Helper()
		got, _ := getItems(t, s, query)
		if got.Next == "" {
			t.Fatalf("%s: no next", query)
		}
		return got.Next
	}
	plain := next("limit=1")
	byYear := next("sort=year&limit=1")
	byTitle := next("sort=title&limit=1")
	onSrv0 := next("srv=0&limit=1")
	withQ := next("q=title&limit=1")

	// A cursor fits another limit of the same request.
	if got, _ := getItems(t, s, "limit=5&cursor="+plain); len(got.Items) != 2 {
		t.Errorf("cursor with another limit: %v", keysOf(got.Items))
	}

	tests := []struct{ query, msg string }{
		{"tab=x", "invalid tab"},
		{"tab=Attention", "invalid tab"},
		{"srv=-1", "invalid srv"},
		{"srv=2", "invalid srv"},
		{"srv=9", "invalid srv"},
		{"srv=a", "invalid srv"},
		{"library=1", "library needs srv"},
		{"reason=x", "invalid reason"},
		{"reason=no-art,no-poster", "invalid reason"},
		{"type=episode", "invalid type"},
		{"q=" + strings.Repeat("a", 201), "invalid q"},
		{"sort=x", "invalid sort"},
		{"sort=--title", "invalid sort"},
		{"tab=attention&sort=completed", "invalid sort"},
		{"tab=stopped&sort=completed", "invalid sort"},
		{"tab=fixed&sort=due", "invalid sort"},
		{"tab=fixed&sort=due&cursor=" + plain, "invalid sort"},
		{"all=0", "invalid all"},
		{"all=true", "invalid all"},
		{"limit=0", "invalid limit"},
		{"limit=501", "invalid limit"},
		{"limit=a", "invalid limit"},
		{"cursor=!!!", "invalid cursor"},
		{"cursor=" + plain[:len(plain)/2], "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { f["v"] = 2 }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "v") }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "k") }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "s") }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "n") }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "scope") }), "invalid cursor"},
		{"sort=title&cursor=" + editCursor(t, byTitle, func(f map[string]any) { delete(f, "t") }), "invalid cursor"},
		{"sort=added&cursor=" + byYear, "invalid cursor"},
		{"sort=year&cursor=" + byTitle, "invalid cursor"},
		{"srv=1&cursor=" + onSrv0, "invalid cursor"},
		{"cursor=" + onSrv0, "invalid cursor"},
		{"q=other&cursor=" + withQ, "invalid cursor"},
		{"tab=stopped&cursor=" + plain, "invalid cursor"},
		{"all=1&cursor=" + plain, "invalid cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := serve(s, httptest.NewRequest(http.MethodGet, "/api/items?"+tt.query, nil))
			assertJSONError(t, rec, http.StatusBadRequest, tt.msg)
		})
	}
}

// TestAPIRejectsMalformedQuery checks that a query with a bad percent
// escape is a 400, so a dropped pair never widens or resets a request.
func TestAPIRejectsMalformedQuery(t *testing.T) {
	st := openStore(t)
	seedMovies(t, st, 3)
	s := newAPIServer(st, 0, "Home", "Away")

	for _, path := range []string{
		"/api/items?cursor=%ZZ",
		"/api/items?srv=%ZZ",
		"/api/runs?cursor=%ZZ",
		"/api/runs?srv=%ZZ",
		"/api/item?srv=0&key=1&x=%ZZ",
		"/api/item/live?srv=0&key=1&x=%ZZ",
		"/api/items/thumb?srv=0&key=1&size=%ZZ",
	} {
		t.Run(path, func(t *testing.T) {
			rec := serve(s, httptest.NewRequest(http.MethodGet, path, nil))
			assertJSONError(t, rec, http.StatusBadRequest, "invalid query")
		})
	}
}

func TestAPIStoreFailureIs500(t *testing.T) {
	st := openStore(t)
	s := newAPIServer(st, 0, "Home")
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/items", "/api/servers", "/api/runs", "/api/item?srv=0&key=1", "/api/item/live?srv=0&key=1"} {
		rec := serve(s, httptest.NewRequest(http.MethodGet, path, nil))
		assertJSONError(t, rec, http.StatusInternalServerError, "internal error")
	}
}

func TestItemsManualState(t *testing.T) {
	st := openStore(t)
	old, recent := testNow.Add(-48*time.Hour), testNow.Add(-time.Hour)
	readLibrary(t, st, "Home", "1", testNow,
		movie("301", "Only wrong", store.Mismatched, old),
		movie("302", "New wrong", store.Mismatched, recent),
		movie("303", "Wrong and bare", store.Mismatched|store.NoArt, old),
		movie("304", "Bare", store.NoSummary, old))
	s := newServerWith(st, 0, []PlexServer{{Name: "Home", Plex: &fakePlex{id: "id-Home"}}})
	s.rules.MinimumAge = 24 * time.Hour

	got, _ := getItems(t, s, "")
	if got.Counts.Attention != 4 {
		t.Errorf("attention count = %d, want 4", got.Counts.Attention)
	}
	items := byKey(t, got.Items)
	for key, state := range map[string]string{"301": "manual", "302": "manual", "303": "due", "304": "due"} {
		if items[key]["state"] != state {
			t.Errorf("%s state = %v, want %s", key, items[key]["state"], state)
		}
	}
	assertAbsent(t, items["301"], "nextDue")
	assertAbsent(t, items["302"], "nextDue")
	if !reflect.DeepEqual(items["303"]["reasons"], []any{"no-art", "mismatched"}) {
		t.Errorf("303 reasons = %v", items["303"]["reasons"])
	}

	detail, _ := getItem(t, s, "srv=0&key=302")
	if detail.Item["state"] != "manual" {
		t.Errorf("detail state = %v, want manual", detail.Item["state"])
	}
	assertAbsent(t, detail.Item, "nextDue")

	filtered, _ := getItems(t, s, "reason=mismatched")
	if keys := sortedStrings(keysOf(filtered.Items)); !reflect.DeepEqual(keys, []string{"301", "302", "303"}) {
		t.Errorf("reason=mismatched keys = %v", keys)
	}
	if filtered.Counts.Attention != 3 {
		t.Errorf("reason=mismatched attention count = %d, want 3", filtered.Counts.Attention)
	}
}
