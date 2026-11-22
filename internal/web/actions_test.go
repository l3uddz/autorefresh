package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type refreshActions struct {
	Actions
	calls int
}

func (a *refreshActions) RefreshItem(context.Context, string, string) error {
	a.calls++
	return nil
}

func TestRefreshRequiresJSONAndEmptyBody(t *testing.T) {
	actions := &refreshActions{}
	s := New(Options{Actions: actions, Servers: []PlexServer{{Name: "home"}}})
	for _, tc := range []struct {
		contentType, body string
		status            int
	}{
		{"text/plain", "", 415},
		{"application/json", "{}", 400},
		{"application/json; charset=utf-8", "", 200},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/item/refresh?srv=0&key=1", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.contentType)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Errorf("content type %q body %q: got %d, want %d", tc.contentType, tc.body, w.Code, tc.status)
		}
	}
	if actions.calls != 1 {
		t.Fatalf("refresh called %d times", actions.calls)
	}
}
