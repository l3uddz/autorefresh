package refresher

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestManualRefreshRecordsAttempt(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	if err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	item, found := f.row("home", "1", "1")
	if !found || item.Attempts != 1 || item.Refreshes != 1 {
		t.Fatalf("item = %+v, found %v", item, found)
	}
	events, err := f.store.Timeline("home", "1")
	if err != nil || len(events) != 1 || events[0].Source != store.SourceManual || !events[0].Accepted {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestRunRechecksSameSecondManualAttempt(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	if err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	batch, found := f.row("home", "1", "1")
	if !found {
		t.Fatal("missing batch row")
	}
	if err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	run := &serverRun{server: f.r.servers[0], source: store.SourceAuto}
	if err := f.r.runItem(t.Context(), run, batch); err != nil {
		t.Fatal(err)
	}
	current, _ := f.row("home", "1", "1")
	if run.skipped != 1 || current.Attempts != 2 || !current.LastAttempt.Equal(batch.LastAttempt) {
		t.Fatalf("skipped %d, attempts %d, timestamps %v and %v", run.skipped, current.Attempts, batch.LastAttempt, current.LastAttempt)
	}
}

func TestManualRefreshOfHistoryOnlyItem(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	row, _ := f.row("home", "1", "1")
	row.Reasons = 0
	if _, err := f.store.ApplyCheck("home", row, t0); err != nil {
		t.Fatal(err)
	}
	if err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	if _, found := f.row("home", "1", "1"); found {
		t.Fatal("manual refresh inserted a cache row")
	}
	latest := f.newestEvent("1")
	if latest.Kind != store.EventRequest || latest.Source != store.SourceManual || !latest.Accepted {
		t.Fatalf("latest event = %+v", latest)
	}
}

type recordingBarrier struct {
	Store
	entered chan struct{}
	release chan struct{}
}

func (s recordingBarrier) RecordManual(m store.ManualRecord) error {
	close(s.entered)
	<-s.release
	return s.Store.RecordManual(m)
}

func TestCloseDrainsManualRecording(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	f.r.now = func() time.Time { return t0 }
	barrier := recordingBarrier{Store: f.store, entered: make(chan struct{}), release: make(chan struct{})}
	f.r.store = barrier
	done := make(chan error, 1)
	go func() { done <- f.r.RefreshItem(t.Context(), "home", "1") }()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("manual request did not reach recording")
	}
	released := false
	defer func() {
		if !released {
			close(barrier.release)
		}
	}()
	closed := make(chan struct{})
	go func() {
		f.r.Close()
		close(closed)
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
waitForGate:
	for {
		err := f.r.RefreshItem(t.Context(), "home", "1")
		if errors.Is(err, ErrClosed) {
			break waitForGate
		}
		if !errors.Is(err, ErrItemBusy) {
			t.Fatalf("action while recording = %v", err)
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("action gate did not close")
		}
	}
	select {
	case <-closed:
		t.Fatal("close returned before recording")
	default:
	}
	close(barrier.release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not drain")
	}
	if event := f.newestEvent("1"); !event.Accepted || event.Source != store.SourceManual {
		t.Fatalf("event = %+v", event)
	}
}

type manualMetadata struct {
	*fakePlex
	live plex.Item
	err  error
}

func (p manualMetadata) Item(context.Context, string) (plex.Item, error) { return p.live, p.err }

func TestCheckItemValidatesImagesAgainstRequestedKey(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	const thumb = "/library/metadata/1/thumb/1700000050"
	p := manualMetadata{fakePlex: newFakePlex(lib), live: plex.Item{RatingKey: "2", Title: "Updated", Thumb: thumb, Art: "/library/metadata/2/art/1700000050"}}
	f := newRefreshFixture(t, Server{Name: "home", Plex: p})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	if err := f.r.CheckItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	row, found := f.row("home", "1", "1")
	if !found || row.Thumb != thumb || row.Art != "" || row.Title != "Updated" || row.Attempts != 0 {
		t.Fatalf("checked item = %+v, found %v", row, found)
	}
}

func TestCheckGoneItemRemovesRowAndRecordsManualEvent(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	p := manualMetadata{fakePlex: newFakePlex(lib), err: plex.ErrNotFound}
	f := newRefreshFixture(t, Server{Name: "home", Plex: p})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	if err := f.r.CheckItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	if _, found := f.row("home", "1", "1"); found {
		t.Fatal("gone item still cached")
	}
	if event := f.newestEvent("1"); event.Kind != store.EventGone || event.Source != store.SourceManual {
		t.Fatalf("gone event = %+v", event)
	}
	if err := f.r.CheckItem(t.Context(), "home", "1"); !errors.Is(err, ErrNotCached) {
		t.Fatalf("history check = %v", err)
	}
	if _, err := f.r.SetStopped(t.Context(), "home", "1", true); !errors.Is(err, ErrNotCached) {
		t.Fatalf("history stop = %v", err)
	}
}

func TestManualMatchRecordsTargetAndStoppedRunSkipsItem(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: newFakePlex(lib), answers: []checkAnswer{{item: incompleteItem("1")}}, checks: new(int)}})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	batch, _ := f.row("home", "1", "1")
	if changed, err := f.r.SetStopped(t.Context(), "home", "1", true); err != nil || !changed {
		t.Fatalf("stop = %v, %v", changed, err)
	}
	run := &serverRun{server: f.r.servers[0], source: store.SourceAuto}
	if err := f.r.runItem(t.Context(), run, batch); err != nil || run.skipped != 1 {
		t.Fatalf("scheduled skip = %d, %v", run.skipped, err)
	}
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	row, _ := f.row("home", "1", "1")
	// The match and the refresh after it are two attempts.
	if row.StoppedAt.IsZero() || row.Attempts != 2 {
		t.Fatalf("item after match = %+v", row)
	}
	e := f.matchEvent("1")
	if e.MatchGUID != cand.GUID || e.MatchTitle != cand.Name || e.MatchYear != cand.Year || e.Source != store.SourceManual {
		t.Fatalf("match event = %+v", e)
	}
}

// matchEvent returns the newest match request event of key.
func (f *fixture) matchEvent(key string) store.Event {
	f.t.Helper()
	for _, e := range f.timeline(key) {
		if e.Kind == store.EventRequest && e.Action == store.ActionMatch {
			return e
		}
	}
	f.t.Fatalf("item %s has no match event", key)
	return store.Event{}
}

// checkedMetadata answers each Item call with the next result of answers,
// and repeats the last one.
type checkedMetadata struct {
	*fakePlex
	answers []checkAnswer
	checks  *int
}

type checkAnswer struct {
	item plex.Item
	err  error
}

func (p checkedMetadata) Item(context.Context, string) (plex.Item, error) {
	a := p.answers[min(*p.checks, len(p.answers)-1)]
	*p.checks++
	return a.item, a.err
}

func matchCheckFixture(t *testing.T, answers ...checkAnswer) (*fixture, *fakePlex, *int) {
	t.Helper()
	lib := plex.Library{ID: "1", Type: "movie"}
	fake, checks := newFakePlex(lib), new(int)
	f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: fake, answers: answers, checks: checks}})
	f.now = t0
	unmatched := incompleteItem("1")
	unmatched.GUID = "local://1"
	f.seed("home", lib, unmatched, store.Unmatched|store.NoSummary, 0)
	return f, fake, checks
}

func TestApplyMatchRefreshesAndChecksUntilComplete(t *testing.T) {
	still := incompleteItem("1")
	f, fake, checks := matchCheckFixture(t, checkAnswer{item: still}, checkAnswer{item: completeItem("1")})
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Match 1 plex://movie/1", "Refresh 1"}; !reflect.DeepEqual(fake.calls, want) {
		t.Errorf("calls = %v, want %v", fake.calls, want)
	}
	if want := []time.Duration{time.Second, 2 * time.Second}; *checks != 2 || !reflect.DeepEqual(f.sleeps, want) {
		t.Errorf("checks %d, sleeps %v, want 2 and %v", *checks, f.sleeps, want)
	}
	if _, found := f.row("home", "1", "1"); found {
		t.Error("complete item still in the cache")
	}
	if e := f.newestEvent("1"); e.Kind != store.EventCompleted || e.FixedMatch != store.ActionMatch {
		t.Errorf("newest event = %+v, want a completion credited to the match", e)
	}
}

func TestApplyMatchCheckFailureKeepsMatch(t *testing.T) {
	f, _, checks := matchCheckFixture(t, checkAnswer{err: errors.New("plex unavailable")})
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatalf("match = %v, want nil after an accepted match", err)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}; *checks != 3 || !reflect.DeepEqual(f.sleeps, want) {
		t.Errorf("checks %d, sleeps %v, want 3 and %v", *checks, f.sleeps, want)
	}
	row, found := f.row("home", "1", "1")
	if !found || row.Reasons != store.Unmatched|store.NoSummary || row.LastMatch != store.ActionMatch {
		t.Errorf("row = %+v, found %v", row, found)
	}
	if e := f.matchEvent("1"); !e.Accepted {
		t.Errorf("match event = %+v", e)
	}
}

func TestApplyMatchFailedSkipsRefreshAndCheck(t *testing.T) {
	f, fake, checks := matchCheckFixture(t, checkAnswer{item: completeItem("1")})
	fake.matchErrs["1"] = errors.New("plex refused")
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err == nil {
		t.Fatal("failed match returned nil")
	}
	if want := []string{"Match 1 plex://movie/1"}; !reflect.DeepEqual(fake.calls, want) || *checks != 0 || len(f.sleeps) != 0 {
		t.Errorf("calls %v, checks %d, sleeps %v", fake.calls, *checks, f.sleeps)
	}
}
