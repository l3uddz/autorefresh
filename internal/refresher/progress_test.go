package refresher

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// recordingProgress records each progress call as a line, for example
// "ItemDone home 101 refresh". It keeps the counts of each ItemDone call and
// the result and the error of each RunFinished call apart.
type recordingProgress struct {
	mu      sync.Mutex
	calls   []string
	counts  []Counts
	results []store.RunResult
	errs    []error
	started []ProgressItem // the item of each ItemStarted call
}

func (p *recordingProgress) add(parts ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, strings.Join(parts, " "))
}

func (p *recordingProgress) RunStarted(_ int64, start store.RunStart) {
	p.add("RunStarted", start.Server, strconv.FormatInt(start.StartedAt.Unix(), 10))
}

func (p *recordingProgress) LibraryRead(server, library string) {
	p.add("LibraryRead", server, library)
}

func (p *recordingProgress) Batch(server string, selected, due int) {
	p.add("Batch", server, strconv.Itoa(selected), strconv.Itoa(due))
}

func (p *recordingProgress) ItemStarted(server string, item ProgressItem) {
	p.mu.Lock()
	p.started = append(p.started, item)
	p.mu.Unlock()
	p.add("ItemStarted", server, item.RatingKey, item.Title)
}

func (p *recordingProgress) ItemNext(server string, item ProgressItem, at time.Time) {
	p.add("ItemNext", server, item.RatingKey, at.UTC().Format(time.RFC3339))
}

func (p *recordingProgress) ItemDone(server, ratingKey, outcome string, counts Counts) {
	p.mu.Lock()
	p.counts = append(p.counts, counts)
	p.mu.Unlock()
	p.add("ItemDone", server, ratingKey, outcome)
}

func (p *recordingProgress) RunFinished(server string, res store.RunResult, err error, _ bool) {
	p.mu.Lock()
	p.results = append(p.results, res)
	p.errs = append(p.errs, err)
	p.mu.Unlock()
	p.add("RunFinished", server, string(res.Status))
}

// withProgress sets a recording progress on the fixture.
func withProgress(f *fixture) *recordingProgress {
	p := &recordingProgress{}
	f.r.opts.Progress = p
	return p
}

// finished returns the result and the error of the only RunFinished call.
func (p *recordingProgress) finished(t *testing.T) (store.RunResult, error) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.results) != 1 {
		t.Fatalf("RunFinished calls = %d, want 1 (calls %q)", len(p.results), p.calls)
	}
	return p.results[0], p.errs[0]
}

// itemCalls returns the ItemStarted and ItemDone calls.
func (p *recordingProgress) itemCalls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, c := range p.calls {
		if strings.HasPrefix(c, "ItemStarted ") || strings.HasPrefix(c, "ItemDone ") {
			out = append(out, c)
		}
	}
	return out
}

// nextCalls returns the ItemNext calls.
func (p *recordingProgress) nextCalls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, c := range p.calls {
		if strings.HasPrefix(c, "ItemNext ") {
			out = append(out, c)
		}
	}
	return out
}

// startedItems returns the item of each ItemStarted call.
func (p *recordingProgress) startedItems() []ProgressItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.started)
}

// wantStoredCounts checks that the counts of the last ItemDone call are want
// and equal the counts of the only stored run.
func (p *recordingProgress) wantStoredCounts(t *testing.T, f *fixture, want Counts) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.counts) == 0 {
		t.Fatalf("no ItemDone call (calls %q)", p.calls)
	}
	got := p.counts[len(p.counts)-1]
	run := f.onlyRun()
	stored := Counts{Refreshed: run.Refreshed, Matched: run.Matched, Failed: run.Failed}
	if got != want || got != stored {
		t.Errorf("last ItemDone counts = %+v, want %+v, stored run %+v", got, want, stored)
	}
}

func wantProgress(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("progress calls\n got %q\nwant %q", got, want)
	}
}

func TestProgressReportsRunSteps(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102"), incompleteItem("103")}
	fp.refreshErrs["102"] = errServer
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.MaxRefreshesPerRun = 2
	p := withProgress(f)

	f.run(t0)

	at := strconv.FormatInt(t0.Unix(), 10)
	wantProgress(t, p.calls,
		"RunStarted home "+at,
		"LibraryRead home Movies",
		"Batch home 2 3",
		"ItemStarted home 101 Title 101",
		"ItemDone home 101 refresh",
		"ItemNext home 102 "+t0.Add(refreshDelay).UTC().Format(time.RFC3339),
		"ItemStarted home 102 Title 102",
		"ItemDone home 102 refresh-failed",
		"RunFinished home finished",
	)
	res, err := p.finished(t)
	if err != nil {
		t.Errorf("RunFinished error = %v, want nil", err)
	}
	if res.Refreshed != 2 || res.Failed != 1 || res.Due != 3 || res.Selected != 2 {
		t.Errorf("RunFinished result = %+v, want 2 refreshed, 1 failed, 3 due, 2 selected", res)
	}
	if want := []Counts{{Refreshed: 1}, {Refreshed: 2, Failed: 1}}; !reflect.DeepEqual(p.counts, want) {
		t.Errorf("ItemDone counts = %+v, want %+v", p.counts, want)
	}
}

// The counts of each ItemDone call are the counts that the run row stores:
// a sent request counts also when it fails or Plex answers 404, and a
// handled TMDB failure counts at the next ItemDone.
func TestProgressCountsEqualStoredRun(t *testing.T) {
	t.Run("failed refresh", func(t *testing.T) {
		fp := newFakePlex(moviesLib)
		fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
		fp.refreshErrs["101"] = errServer
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		p := withProgress(f)

		f.run(t0)

		p.wantStoredCounts(t, f, Counts{Refreshed: 1, Failed: 1})
	})
	t.Run("refresh not found", func(t *testing.T) {
		fp := newFakePlex(moviesLib)
		fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
		fp.refreshErrs["101"] = errNotFound
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		p := withProgress(f)

		f.run(t0)

		p.wantStoredCounts(t, f, Counts{Refreshed: 1})
	})
	t.Run("TMDB failure", func(t *testing.T) {
		fp := newFakePlex(showsLib)
		ft := &fakeTMDB{errs: map[string]error{"tvdb 5 true": errors.New("GET /3/find/5: 500 Internal Server Error")}}
		f := newTMDBFixture(t, fp, ft, "tvdb-5")
		p := withProgress(f)

		f.run(t0)

		wantProgress(t, p.itemCalls(), "ItemStarted home 201 Title 201", "ItemDone home 201 refresh")
		p.wantStoredCounts(t, f, Counts{Refreshed: 1, Failed: 1})
	})
	t.Run("match", func(t *testing.T) {
		f, _ := matchFixture(t)
		p := withProgress(f)

		f.run(t0)

		// The refresh that follows the match counts too.
		p.wantStoredCounts(t, f, Counts{Refreshed: 1, Matched: 1})
	})
}

func TestProgressReportsEmptyBatch(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{completeItem("101")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	p := withProgress(f)

	f.run(t0)

	wantProgress(t, p.calls,
		"RunStarted home "+strconv.FormatInt(t0.Unix(), 10),
		"LibraryRead home Movies",
		"Batch home 0 0",
		"RunFinished home finished",
	)
}

func TestProgressReportsGoneAndDryRun(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		fp := newFakePlex(moviesLib)
		fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
		fp.refreshErrs["101"] = errNotFound
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		p := withProgress(f)

		f.run(t0)

		wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 gone")
	})
	t.Run("dry run", func(t *testing.T) {
		fp := newFakePlex(moviesLib)
		fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
		f := newFixture(t, Server{Name: testServer, Plex: fp})
		p := withProgress(f)

		f.run(t0)

		wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 dry-run")
		res, err := p.finished(t)
		if err != nil || res.Status != store.RunFinished || res.Selected != 1 {
			t.Errorf("RunFinished = %+v, %v; want a finished run with 1 selected item", res, err)
		}
	})
	t.Run("dry run gone", func(t *testing.T) {
		fp := newFakePlex(moviesLib)
		fp.pathErrs["101"] = errNotFound
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)
		f.r.opts.DryRun = true
		p := withProgress(f)

		f.run(t0)

		wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 gone")
	})
}

func TestProgressReportsMatchOutcome(t *testing.T) {
	f, _ := matchFixture(t)
	p := withProgress(f)

	f.run(t0)

	wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 match")
}

func TestProgressReportsUnavailableServer(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.librariesErr = errUnavailable
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	p := withProgress(f)

	f.runUnavailable(t0)

	wantProgress(t, p.calls, "RunStarted home "+strconv.FormatInt(t0.Unix(), 10), "RunFinished home failed")
	res, err := p.finished(t)
	if !errors.Is(err, plex.ErrUnavailable) || strings.HasPrefix(err.Error(), "server ") {
		t.Errorf("RunFinished error = %v, want the unavailable error without the server name", err)
	}
	if res.Error != err.Error() {
		t.Errorf("RunFinished result error = %q, want %q", res.Error, err.Error())
	}
}

func TestProgressReportsUnauthorizedStop(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	fp.refreshErrs["101"] = fmt.Errorf("refresh: 401 Unauthorized: %w: %w", plex.ErrUnauthorized, plex.ErrUnavailable)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	p := withProgress(f)

	f.runUnavailable(t0)

	wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 refresh-failed")
	res, err := p.finished(t)
	if !errors.Is(err, plex.ErrUnauthorized) || res.Status != store.RunFailed {
		t.Errorf("RunFinished = %+v, %v; want a failed run with the unauthorized error", res, err)
	}
}

func TestProgressReportsCanceledAfterSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	fp.refreshErrs["101"] = fmt.Errorf("request failed: %w: %w", plex.ErrUnavailable, context.Canceled)
	fp.onCall = func(call string) {
		if call == "Refresh 101" {
			cancel()
		}
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	p := withProgress(f)

	f.now = t0
	if err := f.r.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 refresh-failed")
	if e := f.newestEvent("101"); e.Outcome != "refresh-failed" {
		t.Errorf("newest event outcome = %q, want refresh-failed", e.Outcome)
	}
	res, err := p.finished(t)
	if err != nil || res.Status != store.RunCanceled {
		t.Errorf("RunFinished = %+v, %v; want a canceled run without an error", res, err)
	}
}

func TestProgressSkipsCanceledBeforeSend(t *testing.T) {
	t.Run("sleep", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fp := newFakePlex(moviesLib)
		fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		f.r.sleep = func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		}
		p := withProgress(f)

		f.now = t0
		if err := f.r.Run(ctx); err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}

		wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101", "ItemDone home 101 refresh")
		if res, err := p.finished(t); err != nil || res.Status != store.RunCanceled {
			t.Errorf("RunFinished = %+v, %v; want a canceled run without an error", res, err)
		}
	})
	t.Run("match search", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fp := newFakePlex(moviesLib)
		fp.pathErrs["101"] = fmt.Errorf("request failed: %w: %w", plex.ErrUnavailable, context.Canceled)
		fp.onCall = func(call string) {
			if call == "ItemPaths 101" {
				cancel()
			}
		}
		f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
		f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 1)
		p := withProgress(f)

		f.now = t0
		if err := f.r.Run(ctx); err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}

		wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101")
		if res, err := p.finished(t); err != nil || res.Status != store.RunCanceled {
			t.Errorf("RunFinished = %+v, %v; want a canceled run without an error", res, err)
		}
	})
}

// failingFinish is a store whose run ends fail.
type failingFinish struct{ *store.Store }

func (failingFinish) FinishRun(int64, store.RunStart, store.RunResult) (bool, error) {
	return false, errDisk
}

// A failed FinishRun is the error of Run, but the progress gets the result
// of the run and its error, which is nil for a finished run.
func TestProgressReportsRunErrorWhenFinishFails(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.store = failingFinish{f.store}
	p := withProgress(f)

	f.now = t0
	if err := f.r.Run(context.Background()); !errors.Is(err, errDisk) {
		t.Fatalf("Run = %v, want the store error", err)
	}

	if res, err := p.finished(t); err != nil || res.Status != store.RunFinished {
		t.Errorf("RunFinished = %+v, %v; want a finished run without an error", res, err)
	}
}

// failingRequest is a store whose request records fail.
type failingRequest struct{ *store.Store }

func (failingRequest) RecordRequest(store.Request) error { return errDisk }

func TestProgressSkipsStoreFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refreshErr error
	}{{"accepted", nil}, {"failed", errServer}} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePlex(moviesLib)
			fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
			fp.refreshErrs["101"] = tc.refreshErr
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			f.r.store = failingRequest{f.store}
			p := withProgress(f)

			f.now = t0
			if err := f.r.Run(context.Background()); !errors.Is(err, errDisk) {
				t.Fatalf("Run = %v, want the store error", err)
			}

			wantProgress(t, p.itemCalls(), "ItemStarted home 101 Title 101")
			res, err := p.finished(t)
			if !errors.Is(err, errDisk) || res.Status != store.RunFailed {
				t.Errorf("RunFinished = %+v, %v; want a failed run with the store error", res, err)
			}
		})
	}
}

// Before the pace wait for an item, the run reports that item as the next
// one with the time that the wait ends. Each started item carries its
// library, year, type, thumb, and reasons.
func TestRunReportsNextItemBeforePace(t *testing.T) {
	first, second := incompleteItem("101"), incompleteItem("102")
	first.Thumb = "/library/metadata/101/thumb/170"
	second.AddedAt = addedAt.Add(time.Hour)
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{first, second}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	p := withProgress(f)

	f.run(t0)

	next := "ItemNext home 102 " + t0.Add(refreshDelay).UTC().Format(time.RFC3339)
	if got := p.nextCalls(); !slices.Equal(got, []string{next}) {
		t.Fatalf("next calls = %q, want %q", got, next)
	}
	done, at, started := slices.Index(p.calls, "ItemDone home 101 refresh"), slices.Index(p.calls, next), slices.Index(p.calls, "ItemStarted home 102 Title 102")
	if done < 0 || done >= at || at >= started {
		t.Errorf("calls = %q, want ItemDone 101, then ItemNext 102, then ItemStarted 102", p.calls)
	}
	want := ProgressItem{RatingKey: "101", Title: "Title 101", Type: "movie", Library: "Movies", Thumb: "/library/metadata/101/thumb/170", Year: 2020, Reasons: store.NoSummary}
	if items := p.startedItems(); len(items) != 2 || items[0] != want {
		t.Errorf("started items = %+v, want first %+v", items, want)
	}
}

func TestRunReportsNoIgnoredReasons(t *testing.T) {
	it := incompleteItem("101")
	it.Art = ""
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{it}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.IgnoreReasons = store.NoArt
	p := withProgress(f)

	f.run(t0)

	if items := p.startedItems(); len(items) != 1 || items[0].Reasons != store.NoSummary {
		t.Errorf("started items = %+v, want one with only the no-summary reason", items)
	}
}

func TestRunWithoutDelayReportsNoNextItem(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.RefreshDelay = 0
	p := withProgress(f)

	f.run(t0)

	if got := p.nextCalls(); len(got) != 0 {
		t.Errorf("next calls = %q, want none", got)
	}
}

func TestCanceledRunReportsNoNextItem(t *testing.T) {
	f := newRefreshFixture(t, Server{Name: testServer, Plex: newFakePlex(moviesLib)})
	p := withProgress(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &serverRun{server: Server{Name: testServer}}

	if err := f.r.pace(ctx, run, ProgressItem{RatingKey: "102"}, false); !errors.Is(err, context.Canceled) {
		t.Errorf("pace = %v, want context.Canceled", err)
	}
	if got := p.nextCalls(); len(got) != 0 {
		t.Errorf("next calls = %q, want none", got)
	}
}

func TestDryRunReportsNoNextItem(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	f.r.opts.DryRun = true
	p := withProgress(f)

	f.run(t0)

	if got := p.nextCalls(); len(got) != 0 {
		t.Errorf("next calls = %q, want none", got)
	}
	if items := p.startedItems(); len(items) != 2 {
		t.Errorf("started items = %+v, want 2", items)
	}
}
