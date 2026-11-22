package refresher

import (
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestReadRecordsCompletionOnlyWhenNotDryRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dryRun bool
		want   int
	}{{"live run", false, 1}, {"dry run", true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePlex(moviesLib)
			fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
			f := newFixture(t, Server{Name: testServer, Plex: fp})
			f.run(t0)

			fp.lists["1?type=1&updatedAt>>="+unixString(t0.Add(-oversample))] = []plex.Item{completeItem("101")}
			f.r.opts.DryRun = tc.dryRun
			f.run(t0.Add(time.Hour))

			if rows := f.rows(testServer, "1"); len(rows) != 0 {
				t.Errorf("rows = %+v, want none", rows)
			}
			events, err := f.store.Timeline(testServer, "101")
			if err != nil {
				t.Fatal(err)
			}
			completed := 0
			for _, e := range events {
				if e.Kind == store.EventCompleted {
					completed++
				}
			}
			if completed != tc.want {
				t.Errorf("completed events = %d, want %d (events %+v)", completed, tc.want, events)
			}
		})
	}
}
