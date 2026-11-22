package refresher

import (
	"errors"
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
