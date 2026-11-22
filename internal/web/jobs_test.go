package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
)

type testJobs struct {
	Jobs
	keys []string
}

func (j *testJobs) BulkRefresh(_ string, keys []string) (int64, error) {
	j.keys = keys
	return 42, nil
}

func TestBulkRefreshDeduplicatesExplicitKeys(t *testing.T) {
	j := &testJobs{}
	s := New(Options{Jobs: j, Servers: []PlexServer{{Name: "home"}}})
	req := httptest.NewRequest(http.MethodPost, "/api/items/refresh?srv=0", strings.NewReader(`{"keys":["2","1","2"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 202 || !reflect.DeepEqual(j.keys, []string{"2", "1"}) {
		t.Fatalf("status %d, keys %v, response %s", w.Code, j.keys, w.Body)
	}
}

func TestBulkRefreshRejectsInvalidSelection(t *testing.T) {
	for _, tc := range []struct {
		name, query, body, message string
	}{
		{"missing server", "", `{"keys":["1"]}`, "invalid srv"},
		{"tab override", "srv=0&tab=fixed", `{"keys":["1"]}`, "invalid tab"},
		{"invalid key", "srv=0", `{"keys":["../refresh"]}`, "invalid key"},
		{"empty keys", "srv=0", `{"keys":[]}`, "invalid body"},
		{"mixed modes", "srv=0", `{"keys":["1"],"matching":true}`, "invalid body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs := &testJobs{}
			s := New(Options{Jobs: jobs, Servers: []PlexServer{{Name: "home"}}})
			req := httptest.NewRequest(http.MethodPost, "/api/items/refresh?"+tc.query, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != 400 || !strings.Contains(w.Body.String(), tc.message) || jobs.keys != nil {
				t.Fatalf("status %d, body %s, admitted keys %v", w.Code, w.Body, jobs.keys)
			}
		})
	}
}

type matchingTimeoutStore struct{ Store }

func (matchingTimeoutStore) ListItems(ctx context.Context, _ store.ListQuery) ([]store.ListItem, *store.Cursor, error) {
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

func TestBulkStopMatchingTimeoutReturns504(t *testing.T) {
	s := New(Options{Actions: &refreshActions{}, Store: matchingTimeoutStore{}, Servers: []PlexServer{{Name: "home"}}})
	s.actionTimeout = 0
	req := httptest.NewRequest(http.MethodPost, "/api/items/stop?srv=0", strings.NewReader(`{"matching":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 504 || !strings.Contains(w.Body.String(), "timed out") {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

type testStoppingActions struct {
	Actions
	calls []string
}

func (a *testStoppingActions) SetStopped(_ context.Context, _ string, key string, stop bool) (bool, error) {
	if stop {
		return false, errors.New("expected resume")
	}
	a.calls = append(a.calls, key)
	switch key {
	case "1":
		return true, nil
	case "2":
		return false, refresher.ErrItemBusy
	case "3":
		return false, refresher.ErrNotCached
	case "4":
		return false, nil
	default:
		return false, context.DeadlineExceeded
	}
}

func TestBulkResumeReportsSkippedAndStopsAfterTimeout(t *testing.T) {
	a := &testStoppingActions{}
	s := New(Options{Actions: a, Servers: []PlexServer{{Name: "home"}}})
	req := httptest.NewRequest(http.MethodPost, "/api/items/resume?srv=0", strings.NewReader(`{"keys":["1","2","3","4","5","6"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var got struct {
		Changed int
		Skipped []bulkSkip
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []bulkSkip{{"2", "busy"}, {"3", "not-cached"}, {"4", "unchanged"}, {"5", "timeout"}, {"6", "timeout"}}
	if w.Code != 200 || got.Changed != 1 || !reflect.DeepEqual(got.Skipped, want) || !reflect.DeepEqual(a.calls, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("status %d, response %+v, calls %v", w.Code, got, a.calls)
	}
}

func TestRunItemsRedactsStoredErrors(t *testing.T) {
	db := openStore(t)
	start := store.RunStart{Server: "home", Kind: store.RunKindBulkRefresh, Source: store.RunSourceManual, StartedAt: testNow}
	id, err := db.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "fake-private-token"
	if _, err := db.FinishJob(id, start, store.RunResult{Status: store.RunFailed, FinishedAt: testNow}, []store.RunItem{
		{RatingKey: "1", Outcome: store.ItemFailed, Error: "Plex error " + secret},
	}); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: db, Secrets: []string{secret}, Servers: []PlexServer{{Name: "home"}}})
	var got struct{ Items []runItemJSON }
	body := getJSON(t, s, fmt.Sprintf("/api/runs/items?id=%d", id), &got)
	if len(got.Items) != 1 || got.Items[0].Error != "Plex error "+redacted || strings.Contains(body, secret) {
		t.Fatalf("items = %+v", got.Items)
	}
}

func TestBulkMatchingPagesAndAppliesFilter(t *testing.T) {
	db := openStore(t)
	items := make([]store.Item, 0, maxPageLimit+2)
	for i := 1; i <= maxPageLimit+1; i++ {
		items = append(items, movie(fmt.Sprint(i), "Selected", store.NoSummary, testNow.Add(-time.Hour)))
	}
	items = append(items, movie("9000", "Excluded", store.NoSummary, testNow.Add(-time.Hour)))
	readLibrary(t, db, "home", "1", testNow, items...)
	jobs := &testJobs{}
	s := New(Options{Store: db, Jobs: jobs, Servers: []PlexServer{{Name: "home"}}})
	// The dashboard reads "no items" as a server without matching items and
	// goes on to the next server, so the message is part of the API.
	for _, tc := range []struct {
		filter, body string
		status       int
	}{
		{"Selected", `"count":501`, 202},
		{"Missing", `{"error":"no items"}`, 400},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/items/refresh?srv=0&q="+tc.filter, strings.NewReader(`{"matching":true}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.body) {
			t.Fatalf("filter %s: status %d, body %s", tc.filter, w.Code, w.Body)
		}
	}
	if len(jobs.keys) != maxPageLimit+1 || slices.Contains(jobs.keys, "9000") {
		t.Fatalf("selected keys = %d, includes excluded = %v", len(jobs.keys), slices.Contains(jobs.keys, "9000"))
	}
}
