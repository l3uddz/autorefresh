package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/store"
)

type runsAnswer struct {
	Runs []map[string]any `json:"runs"`
	Next string           `json:"next"`
}

// getRuns sends GET /api/runs with the query and decodes the answer.
func getRuns(t *testing.T, s *Server, query string) (runsAnswer, string) {
	t.Helper()
	var got runsAnswer
	body := getJSON(t, s, "/api/runs?"+query, &got)
	return got, body
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// recordRunAt stores a finished run of server that started at started,
// with the error text errText, and returns its ID.
func recordRunAt(t *testing.T, st *store.Store, server string, started time.Time, errText string) int64 {
	t.Helper()
	start := store.RunStart{Server: server, Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: started}
	id, err := st.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	res := store.RunResult{FinishedAt: started.Add(time.Minute), Status: store.RunFinished, Error: errText,
		ReadItems: 1, Refreshed: 2, Matched: 3, Failed: 4, Completed: 5, Fixed: 6, Incomplete: 7, Due: 8, Selected: 9,
		FullReads: 10, IncrementalReads: 11}
	if _, err := st.FinishRun(id, start, res); err != nil {
		t.Fatal(err)
	}
	return id
}

// recordRunError stores a finished run of server with the error text.
func recordRunError(t *testing.T, st *store.Store, server, text string) {
	t.Helper()
	recordRunAt(t, st, server, testNow.Add(-time.Hour), text)
}

func runIDs(t *testing.T, runs []map[string]any) []int64 {
	t.Helper()
	out := []int64{}
	for _, r := range runs {
		id, ok := r["id"].(float64)
		if !ok {
			t.Fatalf("run without id: %v", r)
		}
		out = append(out, int64(id))
	}
	return out
}

// runPages follows the pages of query with limit runs each. It returns the
// IDs of each page and checks that only the last page has no next.
func runPages(t *testing.T, s *Server, query url.Values, limit string) [][]int64 {
	t.Helper()
	query.Set("limit", limit)
	var out [][]int64
	for range 20 {
		got, body := getRuns(t, s, query.Encode())
		out = append(out, runIDs(t, got.Runs))
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

func flatten(pages [][]int64) []int64 {
	out := []int64{}
	for _, p := range pages {
		out = append(out, p...)
	}
	return out
}

func TestRunsEndpoint(t *testing.T) {
	st := openStore(t)
	early, tie, late := testNow.Add(-3*time.Hour), testNow.Add(-2*time.Hour), testNow.Add(-time.Hour)
	h1 := recordRunAt(t, st, "Home", early, "")
	h2 := recordRunAt(t, st, "Home", tie, "")
	h3 := recordRunAt(t, st, "Home", tie, "")
	h4 := recordRunAt(t, st, "Home", tie, "")
	recordRunAt(t, st, "Gone", late, "")
	h5 := recordRunAt(t, st, "Home", late, "")
	a1 := recordRunAt(t, st, "Away", early, "")
	a2, err := st.StartRun(store.RunStart{Server: "Away", Kind: store.RunKindRun, Source: store.RunSourceManual, StartedAt: late})
	if err != nil {
		t.Fatal(err)
	}
	s := newAPIServer(st, 0, "Home", "Away")

	got, _ := getRuns(t, s, "")
	wantAll := []int64{a2, h5, h4, h3, h2, a1, h1}
	if ids := runIDs(t, got.Runs); !reflect.DeepEqual(ids, wantAll) {
		t.Fatalf("ids = %v, want %v", ids, wantAll)
	}
	if got.Next != "" {
		t.Errorf("next = %q, want none", got.Next)
	}

	finished := got.Runs[1]
	want := map[string]any{
		"id": float64(h5), "srv": 0.0, "server": "Home", "kind": "run", "source": "schedule", "status": "finished",
		"readItems": 1.0, "refreshed": 2.0, "matched": 3.0, "failed": 4.0, "completed": 5.0, "fixed": 6.0,
		"incomplete": 7.0, "due": 8.0, "selected": 9.0, "fullReads": 10.0, "incrementalReads": 11.0,
	}
	for field, w := range want {
		if !reflect.DeepEqual(finished[field], w) {
			t.Errorf("run %s = %#v, want %#v", field, finished[field], w)
		}
	}
	assertTime(t, finished, "startedAt", late)
	assertTime(t, finished, "finishedAt", late.Add(time.Minute))
	assertAbsent(t, finished, "error")

	running := got.Runs[0]
	if running["srv"] != 1.0 || running["server"] != "Away" || running["status"] != "running" ||
		running["source"] != "manual" || running["readItems"] != 0.0 {
		t.Errorf("running run = %v", running)
	}
	assertAbsent(t, running, "finishedAt")

	away, _ := getRuns(t, s, "srv=1")
	if ids := runIDs(t, away.Runs); !reflect.DeepEqual(ids, []int64{a2, a1}) {
		t.Errorf("srv=1: ids = %v", ids)
	}

	for _, tt := range []struct {
		query url.Values
		limit string
		sizes []int
		want  []int64
	}{
		{url.Values{}, "2", []int{2, 2, 2, 1}, wantAll},
		{url.Values{"srv": {"0"}}, "1", []int{1, 1, 1, 1, 1}, []int64{h5, h4, h3, h2, h1}},
		{url.Values{"srv": {"0"}}, "5", []int{5}, []int64{h5, h4, h3, h2, h1}},
		{url.Values{"srv": {"1"}}, "1", []int{1, 1}, []int64{a2, a1}},
	} {
		pages := runPages(t, s, tt.query, tt.limit)
		sizes := []int{}
		for _, p := range pages {
			sizes = append(sizes, len(p))
		}
		if !reflect.DeepEqual(sizes, tt.sizes) || !reflect.DeepEqual(flatten(pages), tt.want) {
			t.Errorf("%s limit %s: pages = %v, want sizes %v of %v", tt.query.Encode(), tt.limit, pages, tt.sizes, tt.want)
		}
	}
}

func TestRunsRejectsInvalidParams(t *testing.T) {
	st := openStore(t)
	for range 3 {
		recordRunError(t, st, "Home", "")
	}
	s := newAPIServer(st, 0, "Home", "Away")
	next := func(query string) string {
		t.Helper()
		got, _ := getRuns(t, s, query)
		if got.Next == "" {
			t.Fatalf("%s: no next", query)
		}
		return got.Next
	}
	plain := next("limit=1")
	onSrv0 := next("srv=0&limit=1")

	// A cursor fits another limit of the same request.
	if got, _ := getRuns(t, s, "limit=5&cursor="+plain); len(got.Runs) != 2 {
		t.Errorf("cursor with another limit: %v", runIDs(t, got.Runs))
	}

	tests := []struct{ query, msg string }{
		{"srv=-1", "invalid srv"},
		{"srv=2", "invalid srv"},
		{"srv=a", "invalid srv"},
		{"limit=0", "invalid limit"},
		{"limit=501", "invalid limit"},
		{"limit=a", "invalid limit"},
		{"cursor=!!!", "invalid cursor"},
		{"cursor=" + plain[:len(plain)/2], "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { f["v"] = 2 }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "v") }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "scope") }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "a") }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { delete(f, "i") }), "invalid cursor"},
		{"cursor=" + editCursor(t, plain, func(f map[string]any) { f["i"] = "x" }), "invalid cursor"},
		{"srv=1&cursor=" + onSrv0, "invalid cursor"},
		{"cursor=" + onSrv0, "invalid cursor"},
		{"srv=0&cursor=" + plain, "invalid cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := serve(s, httptest.NewRequest(http.MethodGet, "/api/runs?"+tt.query, nil))
			assertJSONError(t, rec, http.StatusBadRequest, tt.msg)
		})
	}
}

// The store reads an empty server list as every server, so a server
// without configured servers lists no run.
func TestRunsWithoutServersListsNothing(t *testing.T) {
	st := openStore(t)
	recordRunError(t, st, "Home", "")
	s := newServerWith(st, 0, nil)

	got, body := getRuns(t, s, "")
	if len(got.Runs) != 0 || got.Next != "" || !strings.Contains(body, `"runs":[]`) {
		t.Errorf("answer = %s, want no runs", body)
	}
}

func TestRunsEmptyIsArray(t *testing.T) {
	_, body := getRuns(t, newAPIServer(openStore(t), 0, "Home"), "")
	if !strings.Contains(body, `"runs":[]`) || strings.Contains(body, `"next"`) {
		t.Errorf("body = %s", body)
	}
}

type runDetailAnswer struct {
	Run    map[string]any   `json:"run"`
	Events []map[string]any `json:"events"`
	Items  []map[string]any `json:"items"`
}

func TestRunDetailListsEventsOfRun(t *testing.T) {
	db := openStore(t)
	one := movie("1", "One", store.NoSummary, testNow)
	one.Thumb = "/library/metadata/1/thumb/1700000000"
	if err := db.SaveLibraries("home", []store.LibraryInfo{{Section: "1", Title: "Movies"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyRead(store.Read{Server: "home", Section: "1", At: testNow, Items: []store.Item{one, movie("2", "Two", store.NoSummary, testNow)}}); err != nil {
		t.Fatal(err)
	}
	start := store.RunStart{Server: "home", Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: testNow}
	id, err := db.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "fake-private-token"
	at := testNow.Add(time.Minute)
	for _, r := range []store.Request{
		{Server: "home", RatingKey: "1", At: at, Source: store.SourceAuto, Action: store.ActionRefresh, Error: "Plex error " + secret, RunID: id},
		{Server: "home", RatingKey: "2", At: at, Source: store.SourceManual, Action: store.ActionRefresh, Accepted: true},
	} {
		if err := db.RecordRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.FinishRun(id, start, store.RunResult{FinishedAt: at, Status: store.RunFinished, Failed: 1}); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: db, Secrets: []string{secret}, Servers: []PlexServer{{Name: "home"}}})

	var got runDetailAnswer
	body := getJSON(t, s, fmt.Sprintf("/api/runs/detail?id=%d", id), &got)
	if got.Run["id"] != float64(id) || got.Run["failed"] != 1.0 || len(got.Items) != 0 {
		t.Errorf("run = %v, items = %v", got.Run, got.Items)
	}
	if len(got.Events) != 1 {
		t.Fatalf("events = %v, want only the event of the run", got.Events)
	}
	e := got.Events[0]
	want := map[string]any{"key": "1", "title": "One", "type": "movie", "library": "Movies", "kind": "request", "action": "refresh",
		"accepted": false, "error": "Plex error " + redacted}
	for field, w := range want {
		if !reflect.DeepEqual(e[field], w) {
			t.Errorf("event %s = %#v, want %#v", field, e[field], w)
		}
	}
	if thumb, _ := e["thumb"].(string); !strings.HasPrefix(thumb, "api/items/thumb?") {
		t.Errorf("thumb = %q, want the image endpoint", thumb)
	}
	if strings.Contains(body, secret) {
		t.Errorf("body holds the secret: %s", body)
	}
}

func TestRunDetailListsItemsOfBulkJob(t *testing.T) {
	db := openStore(t)
	start := store.RunStart{Server: "home", Kind: store.RunKindBulkRefresh, Source: store.RunSourceManual, StartedAt: testNow}
	id, err := db.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveLibraries("home", []store.LibraryInfo{{Section: "1", Title: "Movies"}}); err != nil {
		t.Fatal(err)
	}
	one := movie("1", "One", store.NoSummary, testNow)
	one.Thumb = "/library/metadata/1/thumb/1700000000"
	if _, err := db.ApplyRead(store.Read{Server: "home", Section: "1", At: testNow, Items: []store.Item{one}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinishJob(id, start, store.RunResult{Status: store.RunFinished, FinishedAt: testNow}, []store.RunItem{
		{RatingKey: "1", Title: "One", Outcome: store.ItemAccepted},
	}); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: db, Servers: []PlexServer{{Name: "home"}}})
	var got runDetailAnswer
	getJSON(t, s, fmt.Sprintf("/api/runs/detail?id=%d", id), &got)
	if len(got.Items) != 1 || len(got.Events) != 0 {
		t.Fatalf("detail = %+v", got)
	}
	item := got.Items[0]
	want := map[string]any{"key": "1", "title": "One", "outcome": store.ItemAccepted, "type": "movie", "library": "Movies"}
	for field, w := range want {
		if !reflect.DeepEqual(item[field], w) {
			t.Errorf("item %s = %#v, want %#v", field, item[field], w)
		}
	}
	if thumb, _ := item["thumb"].(string); !strings.HasPrefix(thumb, "api/items/thumb?") {
		t.Errorf("thumb = %q, want the image endpoint", thumb)
	}
}

func TestRunDetailErrors(t *testing.T) {
	db := openStore(t)
	away, err := db.StartRun(store.RunStart{Server: "away", Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: testNow})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: db, Servers: []PlexServer{{Name: "home"}}})
	tests := []struct {
		query string
		code  int
	}{
		{fmt.Sprintf("id=%d", away+1), http.StatusNotFound},
		{fmt.Sprintf("id=%d", away), http.StatusNotFound}, // a server that is not configured
		{"id=x", http.StatusBadRequest},
		{"id=0", http.StatusBadRequest},
		{"", http.StatusBadRequest},
		{"id=%zz", http.StatusBadRequest},
	}
	for _, tt := range tests {
		if rec := serve(s, httptest.NewRequest(http.MethodGet, "/api/runs/detail?"+tt.query, nil)); rec.Code != tt.code {
			t.Errorf("%s: code %d, want %d", tt.query, rec.Code, tt.code)
		}
	}
}
