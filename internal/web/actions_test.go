package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
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

type rejectedRefresh struct {
	Actions
	err error
}

func (a rejectedRefresh) RefreshItem(context.Context, string, string) error { return a.err }

func TestWriteActionErrorsMapToHTTP(t *testing.T) {
	for _, tc := range []struct {
		err     error
		status  int
		message string
	}{
		{refresher.ErrItemBusy, 409, "item busy"},
		{refresher.ErrNotCached, 409, "item not cached"},
		{refresher.ErrClosed, 503, "shutting down"},
		{fmt.Errorf("%w: %w", refresher.ErrTMDB, tmdb.ErrUnauthorized), 502, "TMDB rejected the token"},
	} {
		s := New(Options{Actions: rejectedRefresh{err: tc.err}, Servers: []PlexServer{{Name: "home"}}})
		req := httptest.NewRequest(http.MethodPost, "/api/item/refresh?srv=0&key=1", nil)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.message) {
			t.Errorf("%v: status %d, body %s", tc.err, w.Code, w.Body)
		}
	}
}

func TestDisconnectedStoreFailureIsLoggedAndRedacted(t *testing.T) {
	const secret = "fake-private-token"
	logs := captureLog(t)
	s := New(Options{Secrets: []string{secret}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/item/refresh", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	s.writeActionError(w, req, fmt.Errorf("%w: disk failure %s", refresher.ErrStore, secret))
	if w.Body.Len() != 0 || !strings.Contains(logs.String(), `"level":"error"`) ||
		!strings.Contains(logs.String(), redacted) || strings.Contains(logs.String(), secret) {
		t.Fatalf("body %q, log %q", w.Body, logs)
	}
}

type fakeMatchSearch struct {
	Actions
	result refresher.MatchResult
}

func (s fakeMatchSearch) SearchMatches(context.Context, string, string, refresher.MatchQuery) (refresher.MatchResult, error) {
	return s.result, nil
}

func TestMatchSearchOmitsCredentialFromCandidateThumb(t *testing.T) {
	const secret = "fake-private-token"
	actions := fakeMatchSearch{result: refresher.MatchResult{Searches: []store.MatchSearch{{Candidates: []store.MatchCandidate{
		{GUID: "plex://movie/1", Thumb: "https://example.invalid/1?sig=" + secret},
		{GUID: "plex://movie/2", Thumb: "https://example.invalid/2"},
	}}}}}
	server := New(Options{Actions: actions, Credentials: []string{secret}, Servers: []PlexServer{{Name: "home"}}})
	var got matchesJSON
	body := getJSON(t, server, "/api/item/matches?srv=0&key=1", &got)
	if strings.Contains(body, secret) || len(got.Searches) != 1 || len(got.Searches[0].Candidates) != 2 {
		t.Fatalf("matches response = %s", body)
	}
	candidates := got.Searches[0].Candidates
	if candidates[0].Thumb != "" || candidates[1].Thumb != "https://example.invalid/2" {
		t.Fatalf("candidate thumbs = %+v", candidates)
	}
}

func TestServerErrorLogRedactsSecrets(t *testing.T) {
	const secret = "fake-private-token"
	logs := captureLog(t)
	s := New(Options{Secrets: []string{secret}})
	s.srv.ErrorLog.Print("server failure " + secret)
	if !strings.Contains(logs.String(), "UI server error") || !strings.Contains(logs.String(), redacted) || strings.Contains(logs.String(), secret) {
		t.Fatalf("server log = %q", logs)
	}
}

// goneActions records a check of an item that Plex no longer has, as the
// refresher does after a 404.
type goneActions struct {
	Actions
	db *store.Store
}

func (a goneActions) CheckItem(_ context.Context, server, key string) error {
	req := store.Request{Server: server, RatingKey: key, At: testNow, Source: store.SourceManual}
	return a.db.RecordManual(store.ManualRecord{Request: req, Cached: true, Gone: true})
}

// The dashboard tells a removed item from a complete one by this answer:
// the item is no longer cached and its newest event is gone.
func TestCheckOfGoneItemAnswersUncachedWithGoneEvent(t *testing.T) {
	db := openStore(t)
	readLibrary(t, db, "home", "1", testNow.Add(-time.Hour), movie("7", "Harbor", store.NoSummary, testNow.Add(-2*time.Hour)))
	s := New(Options{Store: db, Actions: goneActions{db: db}, Servers: []PlexServer{{Name: "home"}}})
	s.now = func() time.Time { return testNow }
	req := httptest.NewRequest(http.MethodPost, "/api/item/check?srv=0&key=7", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	var got struct {
		Item struct {
			Cached  bool     `json:"cached"`
			Reasons []string `json:"reasons"`
		} `json:"item"`
		Timeline []struct {
			Kind string `json:"kind"`
		} `json:"timeline"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Item.Cached || len(got.Timeline) == 0 || got.Timeline[0].Kind != "gone" {
		t.Fatalf("cached %v, timeline %+v", got.Item.Cached, got.Timeline)
	}
}
