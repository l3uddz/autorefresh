package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
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

func TestMatchRejectsUnsafeOrInvalidRequests(t *testing.T) {
	actions := &refreshActions{}
	s := New(Options{Actions: actions, Servers: []PlexServer{{Name: "home"}}})
	for _, tc := range []struct {
		name, body, site string
		status           int
	}{
		{"cross site", `{"guid":"id"}`, "cross-site", 403},
		{"large body", `{"guid":"` + strings.Repeat("x", maxBodyBytes) + `"}`, "same-origin", 413},
		{"null", "null", "same-origin", 400},
		{"extra value", `{"guid":"id"} {}`, "same-origin", 400},
		{"unknown field", `{"guid":"id","extra":true}`, "same-origin", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/item/match?srv=0&key=1", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", tc.site)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Errorf("status %d, want %d, body %s", w.Code, tc.status, w.Body)
			}
		})
	}
}

type successfulPlexRefresh struct{ refresher.Plex }

func (successfulPlexRefresh) Refresh(context.Context, string) error { return nil }

type pendingManualRecord struct {
	*store.Store
	entered, release, recorded chan struct{}
}

func (s pendingManualRecord) RecordManual(m store.ManualRecord) error {
	close(s.entered)
	<-s.release
	err := s.Store.RecordManual(m)
	close(s.recorded)
	return err
}

func TestForcedHTTPStopPreservesInFlightManualRecording(t *testing.T) {
	db := openStore(t)
	_, err := db.ApplyRead(store.Read{Server: "home", Section: "1", At: testNow, Items: []store.Item{movie("1", "One", store.NoSummary, testNow)}})
	if err != nil {
		t.Fatal(err)
	}
	st := pendingManualRecord{db, make(chan struct{}), make(chan struct{}), make(chan struct{})}
	r := refresher.New([]refresher.Server{{Name: "home", Plex: successfulPlexRefresh{}}}, st, refresher.Options{})
	jobs := refresher.NewJobs(r)
	s := New(Options{Actions: r, Jobs: jobs, Store: db, Servers: []PlexServer{{Name: "home"}}})
	s.forceWait = time.Millisecond
	base := startServer(t, s)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/api/item/refresh?srv=0&key=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		if response, err := http.DefaultClient.Do(req); err == nil {
			response.Body.Close()
		}
	}()
	waitEntered(t, st.entered)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stopErr := s.Stop(ctx)
	if !errors.Is(stopErr, context.Canceled) {
		t.Errorf("forced stop = %v", stopErr)
	}
	select {
	case <-st.recorded:
		t.Error("recording finished before the barrier was released")
	default:
	}
	closed := make(chan struct{})
	go func() {
		jobs.Close()
		close(closed)
	}()
	close(st.release)
	waitEntered(t, closed)
	waitEntered(t, requestDone)
	events, err := db.Timeline("home", "1")
	if err != nil || len(events) != 1 || !events[0].Accepted {
		t.Fatalf("events = %+v, %v", events, err)
	}
}
