package web

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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
