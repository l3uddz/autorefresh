package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
