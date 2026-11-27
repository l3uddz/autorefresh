package refresher

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// upkeepStore counts the space upkeep calls and can fail them. Reclaim
// waits for release when it is set, or until its context ends; then it
// signals cancelled, if set, and waits for finish.
type upkeepStore struct {
	*store.Store
	mu                  sync.Mutex // the server runs call the store at once
	reclaims, optimizes int
	fail                error
	entered             chan struct{} // gets a value when Reclaim starts, if set
	release             chan struct{}
	cancelled           chan struct{} // closed when Reclaim sees its context end, if set
	finish              chan struct{} // Reclaim returns after a cancel once it is closed
	calls               []string      // the order of the removals and the upkeep
}

func (s *upkeepStore) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
	switch call {
	case "Reclaim":
		s.reclaims++
	case "Optimize":
		s.optimizes++
	}
}

func (s *upkeepStore) counts() (reclaims, optimizes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reclaims, s.optimizes
}

func (s *upkeepStore) PruneServers(names []string) ([]store.Pruned, error) {
	s.record("PruneServers")
	return s.Store.PruneServers(names)
}

func (s *upkeepStore) PruneSections(server string, keep []string) ([]store.Pruned, error) {
	s.record("PruneSections")
	return s.Store.PruneSections(server, keep)
}

func (s *upkeepStore) Reclaim(ctx context.Context) (store.ReclaimResult, error) {
	s.record("Reclaim")
	if s.entered != nil {
		s.entered <- struct{}{}
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			if s.cancelled != nil {
				close(s.cancelled)
				<-s.finish
			}
			return store.ReclaimResult{}, ctx.Err()
		}
	}
	if s.fail != nil {
		return store.ReclaimResult{}, s.fail
	}
	return s.Store.Reclaim(ctx)
}

func (s *upkeepStore) Optimize(ctx context.Context) error {
	s.record("Optimize")
	if s.fail != nil {
		return s.fail
	}
	return s.Store.Optimize(ctx)
}

func TestRunDoesSpaceUpkeepOnce(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)}, Server{Name: "other", Plex: newFakePlex(moviesLib)})
	st := &upkeepStore{Store: f.store}
	f.r.store = st

	if err := f.r.Run(context.Background()); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if reclaims, optimizes := st.counts(); reclaims != 1 || optimizes != 1 {
		t.Errorf("Reclaim and Optimize calls = %d and %d, want 1 each for a run of two servers", reclaims, optimizes)
	}
}

// The upkeep releases the pages of every removal of the run: the cache of
// servers that are not configured before the servers run, and the cache of
// removed libraries while they run.
func TestSpaceUpkeepFollowsTheRemovalsOfTheRun(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
	st := &upkeepStore{Store: f.store}
	f.r.store = st

	if err := f.r.Run(context.Background()); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if want := []string{"PruneServers", "PruneSections", "Reclaim", "Optimize"}; !slices.Equal(st.calls, want) {
		t.Errorf("calls = %v, want %v", st.calls, want)
	}
}

func TestDryRunDoesNoSpaceUpkeep(t *testing.T) {
	f := newFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)}) // a dry run
	st := &upkeepStore{Store: f.store}
	f.r.store = st

	f.run(t0)

	if reclaims, optimizes := st.counts(); reclaims != 0 || optimizes != 0 {
		t.Errorf("Reclaim and Optimize calls = %d and %d in a dry run, want none", reclaims, optimizes)
	}
}

func TestFailedSpaceUpkeepOnlyWarns(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
	st := &upkeepStore{Store: f.store, fail: errors.New("database is locked")}
	f.r.store = st

	if err := f.r.Run(context.Background()); err != nil {
		t.Fatalf("Run = %v, want nil despite the failed upkeep", err)
	}
	if len(f.logLines("warn", "Reclaiming database space failed")) != 1 ||
		len(f.logLines("warn", "Updating database statistics failed")) != 1 {
		t.Error("want one warning for each failed upkeep step")
	}
	if run := f.onlyRun(); run.Status != store.RunFinished {
		t.Errorf("run status = %q, want %q", run.Status, store.RunFinished)
	}
}

// Run now answers when the job starts, so the upkeep runs in the job.
func TestRunNowDoesSpaceUpkeepInTheJob(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
	st := &upkeepStore{Store: f.store, release: make(chan struct{})}
	f.r.store = st
	jobs := NewJobs(f.r)
	t.Cleanup(jobs.Close)

	// The gate opens also when the test fails, so the job can end.
	var once sync.Once
	open := func() { once.Do(func() { close(st.release) }) }
	t.Cleanup(open)
	errs := make(chan error, 1)
	go func() {
		_, err := jobs.RunNow(nil, false)
		errs <- err
	}()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunNow waited for the upkeep, want it to answer when the job starts")
	}
	// RunNow returned while Reclaim still waits.
	open()
	jobs.wg.Wait()
	if reclaims, optimizes := st.counts(); reclaims != 1 || optimizes != 1 {
		t.Errorf("Reclaim and Optimize calls = %d and %d, want 1 each", reclaims, optimizes)
	}
}

// The upkeep runs after the run lock is free again. The dashboard offers
// Run now once no server runs, so a click while Reclaim waits must start a
// run instead of answering that a run is busy.
func TestRunNowIsAdmittedDuringTheUpkeep(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
	st := &upkeepStore{Store: f.store, entered: make(chan struct{}, 2), release: make(chan struct{})}
	f.r.store = st
	jobs := NewJobs(f.r)
	t.Cleanup(jobs.Close)
	var once sync.Once
	open := func() { once.Do(func() { close(st.release) }) }
	t.Cleanup(open)

	if _, err := jobs.RunNow(nil, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-st.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the upkeep did not start")
	}
	// The first run has ended, and its Reclaim waits.
	if _, err := jobs.RunNow(nil, false); err != nil {
		t.Errorf("RunNow during the upkeep = %v, want a new run", err)
	}
	open()
	jobs.wg.Wait()
}

// Close stops the upkeep of a Run now job and waits for it, so the store is
// never closed under it.
func TestJobsCloseCancelsAndWaitsForTheUpkeep(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
	st := &upkeepStore{Store: f.store, entered: make(chan struct{}, 1), release: make(chan struct{}),
		cancelled: make(chan struct{}), finish: make(chan struct{})}
	f.r.store = st
	jobs := NewJobs(f.r)
	var once sync.Once
	finish := func() { once.Do(func() { close(st.finish) }) }
	t.Cleanup(finish)
	// The release gate opens on a failure too, so a Reclaim that never sees
	// the cancel can still return, and Close with it.
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(st.release) }) })

	if _, err := jobs.RunNow(nil, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-st.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the upkeep did not start")
	}
	closed := make(chan struct{})
	go func() {
		jobs.Close()
		close(closed)
	}()
	select {
	case <-st.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the upkeep")
	}
	// Reclaim saw the cancel and has not returned yet.
	select {
	case <-closed:
		t.Fatal("Close returned before the upkeep returned")
	case <-time.After(100 * time.Millisecond):
	}
	finish()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the upkeep returned")
	}
	if _, optimizes := st.counts(); optimizes != 0 {
		t.Errorf("Optimize calls = %d after a cancelled upkeep, want none", optimizes)
	}
}

// A run whose context ended before its upkeep does none.
func TestCancelledRunDoesNoSpaceUpkeep(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
	st := &upkeepStore{Store: f.store}
	f.r.store = st
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if reclaims, optimizes := st.counts(); reclaims != 0 || optimizes != 0 {
		t.Errorf("Reclaim and Optimize calls = %d and %d, want none", reclaims, optimizes)
	}
}

// A bulk refresh removes nothing, so it does no space upkeep.
func TestBulkRefreshDoesNoSpaceUpkeep(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	st := &upkeepStore{Store: f.store}
	f.r.store = st
	jobs := NewJobs(f.r)
	t.Cleanup(jobs.Close)

	if _, err := jobs.BulkRefresh("home", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	jobs.wg.Wait()
	if reclaims, optimizes := st.counts(); reclaims != 0 || optimizes != 0 {
		t.Errorf("Reclaim and Optimize calls = %d and %d, want none", reclaims, optimizes)
	}
}
