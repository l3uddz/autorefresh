package refresher

import (
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// fullListing is the listing key of a full read of the movie library.
var fullListing = listKey(moviesLib.ID, plex.TypeMovie)

// held reports whether the library section of the test server has a hold.
func (f *fixture) held(section string) bool {
	f.t.Helper()
	st, err := f.store.ReadStart(testServer, section)
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Held
}

// holdFixture caches movie 101 with a hold that another process saved, and
// returns the fixture, its Plex fake, and the other process.
func holdFixture(t *testing.T, dryRun bool) (*fixture, *fakePlex, *store.Store) {
	t.Helper()
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.DryRun = dryRun
	f.now = t0
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	other := otherProcess(f)
	if err := other.AddHold(t.Context(), testServer, "101"); err != nil {
		t.Fatal(err)
	}
	return f, fp, other
}

func TestHoldMakesTheNextReadFull(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		f, fp, other := holdFixture(t, dryRun)
		// The attempt of the hold ended before the read.
		other.SetClock(func() time.Time { return t0.Add(-time.Hour) })
		if _, err := other.BeginAttempt(t.Context(), store.AttemptSpec{Server: testServer, RatingKey: "101", Action: store.ActionRefresh}); err != nil {
			t.Fatal(err)
		}
		fp.lists[fullListing] = []plex.Item{completeItem("101")}

		f.run(t0)

		if listed := listedCalls(fp.calls); len(listed) != 1 || listed[0] != "List "+fullListing {
			t.Errorf("dry run %v: lists = %q, want one full listing", dryRun, listed)
		}
		if _, found := f.row(testServer, moviesLib.ID, "101"); found {
			t.Errorf("dry run %v: the full read did not apply the item", dryRun)
		}
		if f.held(moviesLib.ID) {
			t.Errorf("dry run %v: the full read kept the hold", dryRun)
		}
	}
}

func TestLiveAttemptKeepsTheHold(t *testing.T) {
	f, fp, other := holdFixture(t, true)
	if _, err := other.BeginAttempt(t.Context(), store.AttemptSpec{Server: testServer, RatingKey: "101", Action: store.ActionRefresh}); err != nil {
		t.Fatal(err)
	}
	fp.lists[fullListing] = []plex.Item{completeItem("101")}

	f.run(t0)

	if row, found := f.row(testServer, moviesLib.ID, "101"); !found || row.Reasons != store.NoSummary {
		t.Errorf("row = %+v, found %v, want it kept", row, found)
	}
	if !f.held(moviesLib.ID) {
		t.Error("the hold was cleared while the attempt is live")
	}
}

// A held movie that the full listing omits keeps its row and its hold when
// its 404 check fails or answers with the item, and the latter logs a
// warning. A failed full read keeps the hold too.
func TestHeldItemThatTheReadCannotReconcile(t *testing.T) {
	tests := map[string]func(fp *fakePlex){
		"404 check fails":         func(fp *fakePlex) { fp.pathErrs["101"] = errServer },
		"404 check has the item":  func(*fakePlex) {},
		"full listing fails":      func(fp *fakePlex) { fp.listErrs[fullListing] = errServer },
		"full listing has others": func(fp *fakePlex) { fp.lists[fullListing] = []plex.Item{completeItem("102")} },
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			f, fp, _ := holdFixture(t, false)
			setup(fp)

			f.run(t0)

			if _, found := f.row(testServer, moviesLib.ID, "101"); !found {
				t.Error("the row was removed")
			}
			if !f.held(moviesLib.ID) {
				t.Error("the hold was cleared")
			}
			if sent := sends(fp.calls); len(sent) != 0 {
				t.Errorf("sends = %q, want none to a held item", sent)
			}
			warned := len(f.logLines("warn", "Held item not in listing")) == 1
			if want := name != "404 check fails" && name != "full listing fails"; warned != want {
				t.Errorf("warned = %v, want %v", warned, want)
			}
		})
	}
}

func TestDryRunSavesAHold(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.now = t0
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	other := otherProcess(f)
	if _, err := other.BeginAttempt(t.Context(), store.AttemptSpec{Server: testServer, RatingKey: "101", Action: store.ActionRefresh}); err != nil {
		t.Fatal(err)
	}
	fp.lists["1?type=1&updatedAt>>="+unixString(seedAt.Add(-oversample))] = []plex.Item{completeItem("101")}

	f.run(t0)

	if _, found := f.row(testServer, moviesLib.ID, "101"); !found || !f.held(moviesLib.ID) {
		t.Errorf("found %v, held %v, want the row kept with a hold", found, f.held(moviesLib.ID))
	}
}

// staleFixture caches movie 101 in the movie library, with one refresh in
// its history, and series 201 in the show library, and makes another
// process prune that history while the movie library is listed.
func staleFixture(t *testing.T) (*fixture, *fakePlex) {
	t.Helper()
	fp := newFakePlex(moviesLib, showsLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.now = t0
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 1)
	f.seed(testServer, showsLib, incompleteItem("201"), store.NoSummary, 0)
	other := otherProcess(f)
	pruned := false
	fp.onCall = func(call string) {
		if pruned || !strings.HasPrefix(call, "List 1?") {
			return
		}
		pruned = true
		if n, _, err := other.PruneHistory(t0); err != nil || n != 1 {
			t.Errorf("pruned %d events, err %v", n, err)
		}
	}
	return f, fp
}

func TestStaleReadOfANormalRun(t *testing.T) {
	f, fp := staleFixture(t)
	lastRead := f.library(testServer, moviesLib.ID).LastRead

	f.run(t0)

	if len(f.logLines("warn", "Read is older than pruning")) != 1 {
		t.Error("no warning for the stale read")
	}
	if got := f.library(testServer, moviesLib.ID).LastRead; !got.Equal(lastRead) {
		t.Errorf("last read = %v, want it kept at %v", got, lastRead)
	}
	if !f.library(testServer, showsLib.ID).LastRead.Equal(t0) {
		t.Error("the next library was not read")
	}
	wantCalls(t, sends(fp.calls), "Refresh 201")
}

func TestStaleReadOfAFullRunFailsTheServer(t *testing.T) {
	f, fp := staleFixture(t)
	jobs := NewJobs(f.r)
	t.Cleanup(jobs.Close)
	if _, err := jobs.RunNow(nil, true); err != nil {
		t.Fatal(err)
	}
	jobs.wg.Wait()

	if got := f.onlyRun(); got.Status != store.RunFailed {
		t.Errorf("run = %+v, want a failed run", got)
	}
	if sent := sends(fp.calls); len(sent) != 0 {
		t.Errorf("sends = %q, want none", sent)
	}
}

func TestFailedLibraryReadSendsNothingToItsItems(t *testing.T) {
	fp := newFakePlex(moviesLib, showsLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.now = t0
	f.seed(testServer, moviesLib, incompleteItem("101"), store.NoSummary, 0)
	f.seed(testServer, showsLib, incompleteItem("201"), store.NoSummary, 0)
	fp.listErrs["1?type=1&updatedAt>>="+unixString(seedAt.Add(-oversample))] = errServer

	f.run(t0)

	if len(f.logLines("warn", "Reading library failed")) != 1 {
		t.Error("no warning for the failed read")
	}
	wantCalls(t, sends(fp.calls), "Refresh 201")
}

// listedCalls returns the recorded List calls.
func listedCalls(calls []string) []string {
	var got []string
	for _, c := range calls {
		if strings.HasPrefix(c, "List ") {
			got = append(got, c)
		}
	}
	return got
}
