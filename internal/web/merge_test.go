package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
)

// mergedStore caches movie 7 of library 1 of server home with an accepted
// match to plex://movie/7.
func mergedStore(t *testing.T) *store.Store {
	t.Helper()
	db := openStore(t)
	if err := db.SaveLibraries("home", []store.LibraryInfo{{Section: "1", Title: "Movies", Type: "movie"}}); err != nil {
		t.Fatal(err)
	}
	readLibrary(t, db, "home", "1", testNow.Add(-time.Hour), movie("7", "Harbor", store.NoSummary, testNow.Add(-2*time.Hour)))
	err := db.RecordRequest(store.Request{Server: "home", RatingKey: "7", At: testNow.Add(-time.Minute), Source: store.SourceAuto,
		Action: store.ActionMatch, Accepted: true, Match: &store.MatchTarget{GUID: "plex://movie/7", Title: "Harbor"}})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// The item timeline and the run details give the kept rating key of a
// merged completion.
func TestMergedCompletionGivesTheMergedKey(t *testing.T) {
	db := mergedStore(t)
	start := store.RunStart{Server: "home", Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: testNow}
	id, err := db.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.RecordGone("home", "7", testNow, store.SourceAuto, store.ActionRefresh, id, store.AttemptResult{}, "564849"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinishRun(id, start, store.RunResult{FinishedAt: testNow, Status: store.RunFinished}); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: db, Servers: []PlexServer{{Name: "home"}}})

	got, _ := getItem(t, s, "srv=0&key=7")
	if len(got.Timeline) == 0 {
		t.Fatal("empty timeline")
	}
	if e := got.Timeline[0]; e["kind"] != "completed" || e["mergedKey"] != "564849" || e["fixedMatch"] != "match" {
		t.Errorf("newest event = %v, want the merged completion", e)
	}
	assertAbsent(t, got.Timeline[1], "mergedKey")

	var detail runDetailAnswer
	getJSON(t, s, fmt.Sprintf("/api/runs/detail?id=%d", id), &detail)
	if len(detail.Events) != 1 || detail.Events[0]["kind"] != "completed" || detail.Events[0]["mergedKey"] != "564849" {
		t.Errorf("run events = %v, want the merged completion", detail.Events)
	}
}

// mergedActions records that Plex merged the item after the match that
// the refresh applied, as the refresher does after a 404 of the refresh
// after the match.
type mergedActions struct {
	Actions
	db    *store.Store
	match *store.MatchTarget
}

func (a mergedActions) RefreshItem(_ context.Context, server, key string) (refresher.RefreshResult, error) {
	req := store.Request{Server: server, RatingKey: key, At: testNow, Source: store.SourceManual, Action: store.ActionRefresh}
	_, err := a.db.RecordManual(store.ManualRecord{Request: req, Cached: true, Gone: true, Merged: "564849"})
	return refresher.RefreshResult{Match: a.match, Checked: true}, err
}

func TestRefreshAnswersAMerge(t *testing.T) {
	db := mergedStore(t)
	match := &store.MatchTarget{GUID: "plex://movie/7", Title: "Harbor"}
	s := New(Options{Store: db, Actions: mergedActions{db: db, match: match}, Servers: []PlexServer{{Name: "home"}}})
	s.now = func() time.Time { return testNow }
	req := httptest.NewRequest(http.MethodPost, "/api/item/refresh?srv=0&key=7", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	var got struct {
		Checked  bool             `json:"checked"`
		Timeline []map[string]any `json:"timeline"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Checked || len(got.Timeline) == 0 || got.Timeline[0]["kind"] != "completed" || got.Timeline[0]["mergedKey"] != "564849" {
		t.Fatalf("answer = %s, want checked with the merged completion", w.Body)
	}
}
