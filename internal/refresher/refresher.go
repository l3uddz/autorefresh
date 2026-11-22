// Package refresher runs the check of each Plex server: it reads the
// libraries into the cache, finds the due items, and sends each one a
// refresh or a fix match.
package refresher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// Plex is the part of plex.Client that the refresher uses.
type Plex interface {
	Libraries(ctx context.Context) ([]plex.Library, error)
	List(ctx context.Context, section string, itemType int, filters ...plex.Filter) ([]plex.Item, error)
	ItemPaths(ctx context.Context, ratingKey string) (plex.Paths, error)
	EpisodeNumbers(ctx context.Context, ratingKey string) (map[int][]int, error)
	Refresh(ctx context.Context, ratingKey string) error
	SearchMatches(ctx context.Context, ratingKey, title, agent, language string) ([]plex.Candidate, error)
	Match(ctx context.Context, ratingKey string, cand plex.Candidate) error
}

// Store is the part of store.Store that the refresher uses.
type Store interface {
	Library(server, section string) (store.Library, error)
	ApplyRead(r store.Read) (fixed []store.Item, err error)
	Unseen(server, section string, seen []string) ([]store.Item, error)
	Due(q store.DueQuery) (items []store.Item, total int, err error)
	CountIncomplete(server string, sections []string, ignored store.Reasons) (int, error)
	RecordRequest(r store.Request) error
	RecordGone(server, ratingKey string, at time.Time, source store.Source) error
	RecordMatchDecision(d store.MatchDecision) error
	SaveLibraries(server string, libs []store.LibraryInfo) error
	SetPath(server, ratingKey, path string) error
	TMDBID(server, ratingKey, source, externalID string) (tmdbID string, ok bool, err error)
	SaveTMDBID(server, ratingKey, source, externalID, tmdbID string) error
	PruneServers(servers []string) ([]store.Pruned, error)
	PruneSections(server string, sections []string) ([]store.Pruned, error)
	StartRun(r store.RunStart) (int64, error)
	FinishRun(id int64, start store.RunStart, r store.RunResult) error
	PruneHistory(before time.Time) (events, runs int, err error)
}

// Server is one Plex server to check.
type Server struct {
	Name      string
	Plex      Plex
	Libraries []string // titles; empty means all supported libraries
}

// Schedule is the part of cron.Schedule that the refresher uses.
type Schedule interface{ Next(time.Time) time.Time }

// Options holds the rules of a run.
type Options struct {
	MinimumAge, RefreshInterval, Oversample, RefreshDelay time.Duration
	MaxRefreshInterval                                    time.Duration // below RefreshInterval: no backoff
	CacheRefresh                                          Schedule
	MaxRefreshesPerRun                                    int           // the most due items in each run; 0: no limit
	IgnoreReasons                                         store.Reasons // never refreshed for, and not counted
	DryRun                                                bool
	TMDB                                                  TMDB          // nil: no TMDB fallback in fix match
	HistoryRetention                                      time.Duration // events and runs older than this are removed; 0: no retention
	Progress                                              Progress      // nil: no progress
}

// Refresher checks the libraries of each server.
type Refresher struct {
	servers []Server
	store   Store
	opts    Options
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	warnMu  sync.Mutex      // guards warned, because the servers run in parallel
	warned  map[string]bool // the keys of the warnings that log once per process
}

// New returns a Refresher for the servers.
func New(servers []Server, st Store, opts Options) *Refresher {
	return &Refresher{
		servers: servers,
		store:   st,
		opts:    opts,
		now:     time.Now,
		sleep:   sleep,
		warned:  map[string]bool{},
	}
}

// sleep waits for d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// serverRun is the state of one server in one run.
type serverRun struct {
	server               Server
	began                time.Time
	libraries            []plex.Library      // the selected libraries
	files                map[string][]string // movie files by rating key, from the reads of this run
	fullReadItems        int                 // items that full reads listed
	incrementalReadItems int                 // items that incremental reads listed
	completed            []completedItem     // rows that became complete, logged after the refresh step
	fixed                int                 // completed rows that had a request that Plex accepted
	fixedAfterMatch      int                 // completed rows that had a match request that Plex accepted
	due                  int
	refreshes            int  // refresh requests sent, also failed ones
	matches              int  // match requests sent, also failed ones
	failures             int  // failed requests and match searches, without 404 answers
	selected             int  // due items that this run handles, after the limit
	incomplete           int  // incomplete items that the summary counted; 0 without a summary
	remaining            int  // due items that the limit or a stop kept from this run
	tmdbFailed           bool // a TMDB lookup failed, so the run asks TMDB nothing more
}

// completedItem is a row that a read of the run found complete.
type completedItem struct {
	library string // the library title
	item    store.Item
}

// logCompleted logs the rows that the reads of the run found complete. It
// logs them after the refresh step, so they stay next to the summary.
func logCompleted(run *serverRun) {
	for _, c := range run.completed {
		ev := log.Info().Str("server", run.server.Name).Str("library", c.library).Str("title", c.item.Title).
			Int("refreshes", c.item.Refreshes)
		// Rows from before actions were recorded have none.
		if c.item.LastAction != "" {
			ev.Str("last_action", string(c.item.LastAction))
		}
		if c.item.LastMatch != "" {
			ev.Str("last_match", string(c.item.LastMatch))
		}
		ev.Msg("Item complete")
	}
}

// library returns the selected library of a section.
func (run *serverRun) library(section string) plex.Library {
	for _, lib := range run.libraries {
		if lib.ID == section {
			return lib
		}
	}
	return plex.Library{}
}

// sections returns the section IDs of the selected libraries.
func (run *serverRun) sections() []string {
	sections := make([]string, 0, len(run.libraries))
	for _, lib := range run.libraries {
		sections = append(sections, lib.ID)
	}
	return sections
}

// pruneBeforeRun removes the cache of the servers that are not configured,
// and the events and runs that are older than the history retention. A dry
// run keeps both.
func (r *Refresher) pruneBeforeRun(now time.Time) error {
	if r.opts.DryRun {
		return nil
	}
	names := make([]string, 0, len(r.servers))
	for _, srv := range r.servers {
		names = append(names, srv.Name)
	}
	pruned, err := r.store.PruneServers(names)
	if err != nil {
		return err
	}
	logPruned(pruned, nil)
	if r.opts.HistoryRetention <= 0 {
		return nil
	}
	events, runs, err := r.store.PruneHistory(now.Add(-r.opts.HistoryRetention))
	if err != nil {
		return err
	}
	if events > 0 || runs > 0 {
		log.Debug().Int("events", events).Int("runs", runs).Msg("Pruned history")
	}
	return nil
}

// Run removes the cache of the servers that are not configured and the
// history past its retention, unless it is a dry run. Then it
// checks each server in its own goroutine. It reads the libraries of the
// server, refreshes its due items, and logs its summary. It returns the
// store errors and the errors of the unavailable servers. A store error of
// one server also stops the other servers. An unavailable server stops only
// itself. If ctx ends, Run stops and returns nil.
func (r *Refresher) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := r.pruneBeforeRun(r.now()); err != nil {
		return err
	}

	errs := make([]error, len(r.servers))
	var wg sync.WaitGroup
	for i, srv := range r.servers {
		wg.Go(func() {
			err := r.runServer(ctx, srv)
			errs[i] = err
			if err != nil && !errors.Is(err, plex.ErrUnavailable) {
				cancel()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// runServer checks one server and records the run, unless it is a dry run.
// It returns a store error, or the error of an unavailable server after it
// logged that error.
func (r *Refresher) runServer(ctx context.Context, srv Server) error {
	if ctx.Err() != nil {
		log.Debug().Str("server", srv.Name).Msg("Run canceled")
		return nil
	}
	run := &serverRun{server: srv, began: r.now(), files: map[string][]string{}}
	var id int64
	if !r.opts.DryRun {
		var err error
		id, err = r.store.StartRun(run.start())
		if err != nil {
			return fmt.Errorf("server %s: %w", srv.Name, err)
		}
	}
	r.progress().RunStarted(srv.Name, run.began)
	return r.finishRun(ctx, run, id, r.checkServer(ctx, run))
}

// stopped reports whether the end of ctx stopped a run that ended with err.
func stopped(ctx context.Context, err error) bool {
	ctxErr := ctx.Err()
	return ctxErr != nil && (err == nil || errors.Is(err, ctxErr))
}

// checkServer reads the libraries of the server, refreshes its due items,
// and logs its summary. It returns the error that ended the run.
func (r *Refresher) checkServer(ctx context.Context, run *serverRun) error {
	err := r.readLibraries(ctx, run)
	readsDone := err == nil
	if readsDone {
		err = r.refreshDue(ctx, run)
	}
	// The reads removed these rows from the cache, so each run logs them,
	// also a run without a summary.
	logCompleted(run)
	// A canceled run logs no summary, also when the server finished without
	// an error. The store calls do not see ctx, so they can still succeed.
	if stopped(ctx, err) {
		log.Debug().Str("server", run.server.Name).Msg("Run canceled")
		return err
	}
	unavailable := errors.Is(err, plex.ErrUnavailable)
	if unavailable {
		// The run can take long, so the error shows at once.
		log.Error().Str("server", run.server.Name).Err(err).Msg("Plex server unavailable")
	}
	// A server that the refresh step stopped also gets its summary.
	if err == nil || unavailable && readsDone {
		if summaryErr := r.logSummary(run); summaryErr != nil {
			err = summaryErr
		}
	}
	return err
}

// recordFinish stores res as the end of the run id and returns the error of
// the run. A failed FinishRun becomes that error only if the run has none;
// otherwise it is logged, so it is not lost.
func (r *Refresher) recordFinish(run *serverRun, id int64, res store.RunResult, err error) error {
	finishErr := r.store.FinishRun(id, run.start(), res)
	switch {
	case finishErr == nil:
		return err
	case err == nil:
		return finishErr
	}
	log.Error().Str("server", run.server.Name).Err(finishErr).Msg("Recording run failed")
	return err
}

// start returns how the run started, as StartRun and FinishRun store it.
func (run *serverRun) start() store.RunStart {
	return store.RunStart{
		Server: run.server.Name, Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: run.began,
	}
}

// finishRun records the end of the run id, unless it is a dry run, reports
// it to the progress, and returns the result of runServer. The end of ctx is
// a clean exit, so a canceled run returns only a failed FinishRun. Otherwise
// a failed FinishRun is the error of the run only if the run has no error.
func (r *Refresher) finishRun(ctx context.Context, run *serverRun, id int64, err error) error {
	canceled := stopped(ctx, err)
	if canceled {
		err = nil
	}
	res := run.result(r.now(), canceled, err)
	runErr := err // the progress gets the error of res, also if FinishRun fails
	if !r.opts.DryRun {
		err = r.recordFinish(run, id, res, err)
	}
	r.progress().RunFinished(run.server.Name, res, runErr)
	if err == nil {
		return nil
	}
	return fmt.Errorf("server %s: %w", run.server.Name, err)
}

// result returns the end of the run at finishedAt: canceled, failed with
// err, or else finished.
func (run *serverRun) result(finishedAt time.Time, canceled bool, err error) store.RunResult {
	res := store.RunResult{
		FinishedAt: finishedAt, Status: store.RunFinished,
		ReadItems: run.fullReadItems + run.incrementalReadItems, Refreshed: run.refreshes,
		Matched: run.matches, Failed: run.failures, Completed: len(run.completed), Fixed: run.fixed,
		Incomplete: run.incomplete, Due: run.due, Selected: run.selected,
	}
	switch {
	case canceled:
		res.Status = store.RunCanceled
	case err != nil:
		res.Status, res.Error = store.RunFailed, err.Error()
	}
	return res
}

// warnOnce reports whether the warning with key was not logged yet in this
// process, and marks it as logged.
func (r *Refresher) warnOnce(key string) bool {
	r.warnMu.Lock()
	defer r.warnMu.Unlock()
	if r.warned[key] {
		return false
	}
	r.warned[key] = true
	return true
}
