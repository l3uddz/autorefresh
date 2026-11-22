package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

// errPlexUnauthorized is the error of a Plex 401, wrapped as the plex client
// wraps it.
var errPlexUnauthorized = fmt.Errorf("401 Unauthorized: %w: %w", plex.ErrUnauthorized, plex.ErrUnavailable)

func TestStatusTracksRun(t *testing.T) {
	st := NewStatus([]string{"one", "two"}, false)
	started := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	st.RunStarted(0, store.RunStart{Server: "two", StartedAt: started})
	st.LibraryRead("two", "Movies")
	st.LibraryRead("two", "Shows")
	st.Batch("two", 8, 12)
	st.ItemStarted("two", "1", "Alien")
	inWork := st.Snapshot()
	// The counts come from the refresher and are not derived from the
	// outcome: a failed refresh and a 404 answer count as sent refreshes.
	st.ItemDone("two", "1", "refresh", refresher.Counts{Refreshed: 1})
	st.ItemDone("two", "2", "match", refresher.Counts{Refreshed: 1, Matched: 1})
	st.ItemDone("two", "3", "refresh-failed", refresher.Counts{Refreshed: 2, Matched: 1, Failed: 1})
	st.ItemDone("two", "4", "search-failed", refresher.Counts{Refreshed: 2, Matched: 1, Failed: 2})
	st.ItemDone("two", "5", refresher.OutcomeGone, refresher.Counts{Refreshed: 3, Matched: 1, Failed: 2})
	st.ItemStarted("two", "6", "Brazil")
	st.ItemDone("two", "6", refresher.OutcomeDryRun, refresher.Counts{Refreshed: 3, Matched: 1, Failed: 2})
	st.ItemDone("unknown", "7", "refresh", refresher.Counts{Refreshed: 9})

	got := st.Snapshot()
	want := RunProgress{
		Status:        store.RunRunning,
		StartedAt:     started,
		LibrariesRead: 2,
		Library:       "Shows",
		Selected:      8,
		Due:           12,
		Done:          6,
		Refreshed:     3,
		Matched:       1,
		Failed:        2,
	}
	if got.Servers[0].Run != nil {
		t.Errorf("idle server run = %+v, want nil", got.Servers[0].Run)
	}
	if got.Servers[1].Run == nil || *got.Servers[1].Run != want {
		t.Fatalf("run = %+v, want %+v", got.Servers[1].Run, want)
	}
	if run := inWork.Servers[1].Run; run.Current != "Alien" || run.Done != 0 {
		t.Errorf("earlier snapshot = %+v, want Current Alien and Done 0", run)
	}

	st.RunFinished("two", store.RunResult{Status: store.RunFinished}, nil, false)
	end := st.Snapshot().Servers[1]
	if end.Run != nil {
		t.Errorf("run after RunFinished = %+v, want nil", end.Run)
	}
	if end.Health != HealthOK {
		t.Errorf("health after RunFinished = %q, want ok", end.Health)
	}
	if end.Srv != 1 || end.Name != "two" {
		t.Errorf("server = %d %q, want 1 two", end.Srv, end.Name)
	}
}

func TestStatusHealth(t *testing.T) {
	finished := store.RunResult{Status: store.RunFinished}
	failed := store.RunResult{Status: store.RunFailed}
	canceled := store.RunResult{Status: store.RunCanceled}
	tests := []struct {
		name  string
		prior error // the error of the start check
		act   func(*Status)
		want  Health
	}{
		{"unchecked", nil, func(*Status) {}, HealthUnknown},
		{"check ok", nil, func(s *Status) { s.ServerChecked("a", nil) }, HealthOK},
		{"check unauthorized", nil, func(s *Status) { s.ServerChecked("a", errPlexUnauthorized) }, HealthUnauthorized},
		{"check unreachable", nil, func(s *Status) { s.ServerChecked("a", errors.New("dial tcp: refused")) }, HealthUnreachable},
		{"run unauthorized", errors.New("x"), func(s *Status) { s.RunFinished("a", failed, errPlexUnauthorized, false) }, HealthUnauthorized},
		{"run unavailable", nil, func(s *Status) {
			s.ServerChecked("a", nil)
			s.RunFinished("a", failed, fmt.Errorf("read: %w", plex.ErrUnavailable), false)
		}, HealthUnreachable},
		{"run finished", nil, func(s *Status) {
			s.ServerChecked("a", errPlexUnauthorized)
			s.RunFinished("a", finished, nil, false)
		}, HealthOK},
		{"run failed keeps", nil, func(s *Status) {
			s.ServerChecked("a", errPlexUnauthorized)
			s.RunFinished("a", failed, errors.New("database is locked"), false)
		}, HealthUnauthorized},
		{"run canceled keeps", nil, func(s *Status) {
			s.ServerChecked("a", errors.New("dial tcp: refused"))
			s.RunFinished("a", canceled, context.Canceled, false)
		}, HealthUnreachable},
		{"unknown server ignored", nil, func(s *Status) { s.ServerChecked("b", nil) }, HealthUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewStatus([]string{"a"}, false)
			tt.act(st)
			if got := st.Snapshot().Servers[0].Health; got != tt.want {
				t.Errorf("health = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusTMDB(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		act     func(*Status)
		want    string
	}{
		{"off", false, func(*Status) {}, "off"},
		{"off ignores checks", false, func(s *Status) { s.TMDBChecked(nil) }, "off"},
		{"unknown", true, func(*Status) {}, "unknown"},
		{"ok", true, func(s *Status) { s.TMDBChecked(nil) }, "ok"},
		{"unauthorized", true, func(s *Status) { s.TMDBChecked(fmt.Errorf("check: %w", tmdb.ErrUnauthorized)) }, "unauthorized"},
		{"unreachable", true, func(s *Status) { s.TMDBChecked(errors.New("dial tcp: refused")) }, "unreachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewStatus(nil, tt.enabled)
			tt.act(st)
			if got := st.Snapshot().TMDB; got != tt.want {
				t.Errorf("tmdb = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusNotifyNeverBlocks(t *testing.T) {
	st := NewStatus([]string{"a"}, false)
	signals, unsubscribe := st.subscribe()
	defer unsubscribe()
	st.RunStarted(0, store.RunStart{Server: "a", StartedAt: time.Now()})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10000 {
			st.ItemDone("a", "1", "refresh", refresher.Counts{Refreshed: 1})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ItemDone blocked on a subscriber that never reads")
	}
	if n := len(signals); n != 1 {
		t.Errorf("pending signals = %d, want 1", n)
	}
}

func TestStatusEndpoint(t *testing.T) {
	st := NewStatus([]string{"a", "b"}, true)
	st.ServerChecked("a", nil)
	st.RunStarted(0, store.RunStart{Server: "b", StartedAt: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)})
	st.ItemStarted("b", "1", "Alien")

	rec := serve(New(Options{Status: st}), httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var got StatusView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := st.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("body = %+v, want %+v", got, want)
	}

	empty := serve(New(Options{}), httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if body := empty.Body.String(); !strings.Contains(body, `"servers":[]`) || !strings.Contains(body, `"tmdb":"off"`) {
		t.Errorf("body without a status = %s, want no servers and tmdb off", body)
	}
}

func TestFinishedStatusRetainsRunIdentity(t *testing.T) {
	s := NewStatus([]string{"a"}, false)
	start := store.RunStart{Server: "a", Kind: store.RunKindRun, Source: store.RunSourceManual, StartedAt: time.Now()}
	s.RunStarted(42, start)
	s.ItemStarted("a", "1", "Current")
	s.RunFinished("a", store.RunResult{Status: store.RunCanceled}, nil, true)
	got := s.Snapshot().Servers[0]
	if got.Run != nil || got.Last == nil || got.Last.ID != 42 || got.Last.Current != "" || !got.Last.Recorded || got.Last.Status != store.RunCanceled {
		t.Fatalf("finished status = %+v, last = %+v", got, got.Last)
	}
}
