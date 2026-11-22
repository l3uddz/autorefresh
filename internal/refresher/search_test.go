package refresher

import (
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestManualSearchRecognizesTypedID(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	result, err := f.r.SearchMatches(t.Context(), "home", "1", MatchQuery{Text: "imdb-tt123"})
	if err != nil || len(result.Searches) != 1 || result.Searches[0].Source != "id" || result.Searches[0].Query != "imdb-tt123" {
		t.Fatalf("search = %+v, %v", result, err)
	}
	if events, err := f.store.Timeline("home", "1"); err != nil || len(events) != 0 {
		t.Fatalf("events = %+v, %v", events, err)
	}
}
