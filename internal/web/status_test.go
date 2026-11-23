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
	st.ItemStarted("two", refresher.ProgressItem{RatingKey: "1", Title: "Alien"})
	inWork := st.Snapshot()
	// The counts come from the refresher and are not derived from the
	// outcome: a failed refresh and a 404 answer count as sent refreshes.
	st.ItemDone("two", "1", "refresh", refresher.Counts{Refreshed: 1})
	st.ItemDone("two", "2", "match", refresher.Counts{Refreshed: 1, Matched: 1})
	st.ItemDone("two", "3", "refresh-failed", refresher.Counts{Refreshed: 2, Matched: 1, Failed: 1})
	st.ItemDone("two", "4", "search-failed", refresher.Counts{Refreshed: 2, Matched: 1, Failed: 2})
	st.ItemDone("two", "5", refresher.OutcomeGone, refresher.Counts{Refreshed: 3, Matched: 1, Failed: 2})
	st.ItemStarted("two", refresher.ProgressItem{RatingKey: "6", Title: "Brazil"})
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
	// The last item is Brazil, whose dry run ended the run so far.
	last := got.Servers[1].Run.Last
	if last == nil || last.Key != "6" || last.Title != "Brazil" || last.Outcome != refresher.OutcomeDryRun {
		t.Errorf("last = %+v, want Brazil with the dry-run outcome", last)
	}
	want.Last = last
	if got.Servers[1].Run == nil || !reflect.DeepEqual(*got.Servers[1].Run, want) {
		t.Fatalf("run = %+v, want %+v", got.Servers[1].Run, want)
	}
	if run := inWork.Servers[1].Run; run.Current == nil || run.Current.Title != "Alien" || run.Done != 0 {
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
	st.ItemStarted("b", refresher.ProgressItem{RatingKey: "1", Title: "Alien"})

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
	// Three changes: the check of a, the start of the run, and its item.
	if got.Boot == "" || got.Seq != 3 {
		t.Errorf("boot, seq = %q, %d, want a boot and seq 3", got.Boot, got.Seq)
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
	s.ItemStarted("a", refresher.ProgressItem{RatingKey: "1", Title: "Current"})
	s.RunFinished("a", store.RunResult{Status: store.RunCanceled}, nil, true)
	got := s.Snapshot().Servers[0]
	if got.Run != nil || got.Last == nil || got.Last.ID != 42 || got.Last.Current != nil || !got.Last.Recorded || got.Last.Status != store.RunCanceled {
		t.Fatalf("finished status = %+v, last = %+v", got, got.Last)
	}
}

func TestStatusNextRun(t *testing.T) {
	st := NewStatus([]string{"a"}, false)
	before := st.Snapshot()
	raw, err := json.Marshal(before)
	if err != nil || strings.Contains(string(raw), "nextRun") {
		t.Fatalf("status JSON without a schedule = %s, %v", raw, err)
	}

	next := time.Date(2026, 11, 22, 23, 0, 0, 0, time.UTC)
	st.SetNextRun(next)
	after := st.Snapshot()
	if !after.NextRun.Equal(next) || after.Seq <= before.Seq {
		t.Fatalf("after SetNextRun: next %v seq %d, want %v and a seq above %d", after.NextRun, after.Seq, next, before.Seq)
	}
	// The same time again is no change, so no snapshot replaces another.
	st.SetNextRun(next)
	if again := st.Snapshot(); again.Seq != after.Seq {
		t.Fatalf("seq after the same next run = %d, want %d", again.Seq, after.Seq)
	}
	// A late callback of an earlier tick never moves the time back.
	st.SetNextRun(next.Add(-time.Hour))
	if late := st.Snapshot(); !late.NextRun.Equal(next) || late.Seq != after.Seq {
		t.Fatalf("after an older time: next %v seq %d, want %v and %d", late.NextRun, late.Seq, next, after.Seq)
	}
}

// fixedStatus returns a status of the servers "one" and "two" whose clock
// stands at now.
func fixedStatus(now time.Time) *Status {
	st := NewStatus([]string{"one", "two"}, false)
	st.now = func() time.Time { return now }
	st.RunStarted(1, store.RunStart{Server: "two", StartedAt: now})
	return st
}

// The run of server two at index 1 shows the next item while it waits,
// then the item in work, and then the item as the last one.
func TestStatusShowsNextCurrentAndLast(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	st := fixedStatus(now)
	item := refresher.ProgressItem{RatingKey: "1", Title: "Alien", Type: "movie", Library: "Movies", Year: 1979,
		Thumb: "/library/metadata/1/thumb/170", Reasons: store.NoSummary | store.NoPoster}
	wantItem := LiveItem{Key: "1", Title: "Alien", Year: 1979, Type: "movie", Library: "Movies",
		Reasons: reasonNames(store.NoSummary | store.NoPoster), Thumb: imageURL("thumb", 1, "1", "/library/metadata/1/thumb/170")}
	at := now.Add(5 * time.Second)

	st.ItemNext("two", item, at)
	run := st.Snapshot().Servers[1].Run
	if run.Current != nil || run.Next == nil || !reflect.DeepEqual(*run.Next, wantItem) || !run.NextAt.Equal(at) {
		t.Fatalf("waiting run = %+v, next %+v, want next %+v at %v", run, run.Next, wantItem, at)
	}

	st.ItemStarted("two", item)
	run = st.Snapshot().Servers[1].Run
	if run.Next != nil || !run.NextAt.IsZero() || run.Current == nil || !reflect.DeepEqual(*run.Current, wantItem) {
		t.Fatalf("working run = %+v, current %+v, want current %+v and no next", run, run.Current, wantItem)
	}

	st.ItemDone("two", "1", "refresh", refresher.Counts{Refreshed: 1})
	run = st.Snapshot().Servers[1].Run
	wantLast := wantItem
	wantLast.Outcome, wantLast.FinishedAt = "refresh", now
	if run.Current != nil || run.Last == nil || !reflect.DeepEqual(*run.Last, wantLast) {
		t.Fatalf("done run = %+v, last %+v, want last %+v", run, run.Last, wantLast)
	}
}

// ItemDone without an ItemStarted of that item keeps its key and outcome.
func TestStatusLastWithoutStart(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	st := fixedStatus(now)
	st.ItemStarted("two", refresher.ProgressItem{RatingKey: "4", Title: "Other"})

	st.ItemDone("two", "5", refresher.OutcomeGone, refresher.Counts{})

	want := LiveItem{Key: "5", Outcome: refresher.OutcomeGone, FinishedAt: now}
	if last := st.Snapshot().Servers[1].Run.Last; last == nil || !reflect.DeepEqual(*last, want) {
		t.Errorf("last = %+v, want %+v", last, want)
	}
}

// An item without a thumb or reasons has no thumb URL and no reasons in
// the JSON.
func TestStatusItemWithoutThumb(t *testing.T) {
	st := fixedStatus(time.Now())
	st.ItemStarted("two", refresher.ProgressItem{RatingKey: "1", Title: "Alien"})

	current := st.Snapshot().Servers[1].Run.Current
	if current == nil || current.Thumb != "" || current.Reasons != nil {
		t.Fatalf("current = %+v, want no thumb and no reasons", current)
	}
	body, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(body); strings.Contains(s, "thumb") || strings.Contains(s, "reasons") || strings.Contains(s, "finishedAt") {
		t.Errorf("json = %s, want no thumb, reasons, or finishedAt", s)
	}
}

// A snapshot is a deep copy, also of the items of a run.
func TestStatusSnapshotCopiesItems(t *testing.T) {
	st := fixedStatus(time.Now())
	item := refresher.ProgressItem{RatingKey: "1", Title: "Alien", Reasons: store.NoSummary}
	st.ItemNext("two", item, time.Now())
	st.ItemStarted("two", item)
	st.ItemDone("two", "1", "refresh", refresher.Counts{})
	st.ItemNext("two", item, time.Now())

	first := st.Snapshot().Servers[1].Run
	first.Last.Title = "Changed"
	first.Last.Reasons[0] = "changed"
	first.Next.Title = "Changed"

	again := st.Snapshot().Servers[1].Run
	if again.Last.Title != "Alien" || again.Last.Reasons[0] == "changed" || again.Next.Title != "Alien" {
		t.Errorf("snapshot after a change of an earlier copy = last %+v, next %+v", again.Last, again.Next)
	}
}

// RunFinished keeps the last item of the run and drops the current and the
// next item.
func TestStatusFinishedRunKeepsLastItem(t *testing.T) {
	st := fixedStatus(time.Now())
	item := refresher.ProgressItem{RatingKey: "1", Title: "Alien"}
	st.ItemStarted("two", item)
	st.ItemDone("two", "1", "refresh", refresher.Counts{})
	st.ItemNext("two", refresher.ProgressItem{RatingKey: "2", Title: "Brazil"}, time.Now())

	st.RunFinished("two", store.RunResult{Status: store.RunCanceled}, nil, true)

	last := st.Snapshot().Servers[1].Last
	if last == nil || last.Current != nil || last.Next != nil || !last.NextAt.IsZero() || last.Last == nil || last.Last.Title != "Alien" {
		t.Errorf("finished run = %+v, want the last item Alien and no current or next", last)
	}
}
