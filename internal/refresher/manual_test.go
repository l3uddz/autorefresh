package refresher

import (
	"testing"

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
