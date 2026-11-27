package refresher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/store"
)

// upkeepStore counts the space upkeep calls and can fail them. Reclaim
// waits for release when it is set.
type upkeepStore struct {
	*store.Store
	reclaims, optimizes int
	fail                error
	release             chan struct{}
}

func (s *upkeepStore) Reclaim(ctx context.Context) (store.ReclaimResult, error) {
	s.reclaims++
	if s.release != nil {
		<-s.release
	}
	if s.fail != nil {
		return store.ReclaimResult{}, s.fail
	}
	return s.Store.Reclaim(ctx)
}

func (s *upkeepStore) Optimize(ctx context.Context) error {
	s.optimizes++
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
	if st.reclaims != 1 || st.optimizes != 1 {
		t.Errorf("Reclaim and Optimize calls = %d and %d, want 1 each for a run of two servers", st.reclaims, st.optimizes)
	}
}

func TestDryRunDoesNoSpaceUpkeep(t *testing.T) {
	f := newFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)}) // a dry run
	st := &upkeepStore{Store: f.store}
	f.r.store = st

	f.run(t0)

	if st.reclaims != 0 || st.optimizes != 0 {
		t.Errorf("Reclaim and Optimize calls = %d and %d in a dry run, want none", st.reclaims, st.optimizes)
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
	if st.reclaims != 1 || st.optimizes != 1 {
		t.Errorf("Reclaim and Optimize calls = %d and %d, want 1 each", st.reclaims, st.optimizes)
	}
}
