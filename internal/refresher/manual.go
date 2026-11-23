package refresher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

var (
	ErrItemBusy      = errors.New("item busy")
	ErrUnknownItem   = errors.New("unknown item")
	ErrNotCached     = errors.New("item not cached")
	ErrUnknownServer = errors.New("unknown server")
	ErrClosed        = errors.New("shutting down")
	ErrStore         = errors.New("store")
)

func (r *Refresher) enter() bool {
	r.gateMu.Lock()
	defer r.gateMu.Unlock()
	if r.closed {
		return false
	}
	r.actions.Add(1)
	return true
}

// Close prevents new manual actions and waits for admitted actions to finish.
func (r *Refresher) Close() {
	r.gateMu.Lock()
	r.closed = true
	r.gateMu.Unlock()
	r.actions.Wait()
}

func (r *Refresher) serverNamed(name string) (Server, error) {
	for _, srv := range r.servers {
		if srv.Name == name {
			return srv, nil
		}
	}
	return Server{}, ErrUnknownServer
}

type manualItem struct {
	server Server
	row    store.Item
	cached bool
}

func (r *Refresher) admitItem(server, key string, needsCache bool) (manualItem, func(), error) {
	if !r.enter() {
		return manualItem{}, nil, ErrClosed
	}
	srv, err := r.serverNamed(server)
	if err != nil {
		r.actions.Done()
		return manualItem{}, nil, err
	}
	unlock, ok := r.locks.tryLock(server, key)
	if !ok {
		r.actions.Done()
		return manualItem{}, nil, ErrItemBusy
	}
	done := func() {
		unlock()
		r.actions.Done()
	}
	row, cached, found, err := r.store.KnownItem(server, key)
	switch {
	case err != nil:
		err = fmt.Errorf("%w: %w", ErrStore, err)
	case !found:
		err = ErrUnknownItem
	case needsCache && !cached:
		err = ErrNotCached
	}
	if err != nil {
		done()
		return manualItem{}, nil, err
	}
	return manualItem{server: srv, row: row, cached: cached}, done, nil
}

// RefreshItem requests a refresh and records its result.
func (r *Refresher) RefreshItem(ctx context.Context, server, ratingKey string) error {
	return r.manualRequest(ctx, server, ratingKey, store.ActionRefresh, nil)
}

// ApplyMatch requests a selected Plex match and records its result.
func (r *Refresher) ApplyMatch(ctx context.Context, server, ratingKey string, cand plex.Candidate) error {
	return r.manualRequest(ctx, server, ratingKey, store.ActionMatch, &cand)
}

func (r *Refresher) manualRequest(ctx context.Context, server, key string, action store.Action, cand *plex.Candidate) error {
	item, done, err := r.admitItem(server, key, false)
	if err != nil {
		return err
	}
	defer done()
	_, err = r.sendManual(ctx, item.server, item.row, item.cached, action, cand)
	if err == nil && action == store.ActionMatch {
		if settleErr := r.settleMatch(ctx, item, nil); settleErr != nil {
			r.logSettleFailure(item, "record", settleErr)
		}
	}
	return err
}

// matchCheckDelays are the waits before each check after a match. Plex
// fetches the metadata of the new match in the background.
var matchCheckDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// settleMatch runs after Plex accepted a match of item, under its item
// lock: a manual match when run is nil, or else a match of run, which then
// records the steps as its own. It requests a refresh, then checks a cached
// item until the check completes it, Plex no longer has it, a run removed
// its row, the waits run out, or a failure ends it. It returns a store
// error (ErrStore). It logs any other failure, because Plex accepted the
// match and the next run reads the item again. An unavailable server or
// the end of ctx (stopError) ends the follow-up; a run then also gets that
// error, which ends the run of the server as a failed request does.
func (r *Refresher) settleMatch(ctx context.Context, item manualItem, run *serverRun) error {
	if err := r.followUpRefresh(ctx, item, run); err != nil {
		if end, stop := r.settleFailed(ctx, item, run, "refresh", err); end || errors.Is(err, plex.ErrNotFound) {
			return stop
		}
	}
	if !item.cached {
		return nil
	}
	return r.checkMatched(ctx, item, run)
}

// checkMatched checks the cached item of settleMatch after each wait of
// matchCheckDelays, until a check completes it, its row leaves the cache, or
// a failure or the end of ctx ends the follow-up. It returns the error of
// settleFailed, or for a run the error of ctx.
func (r *Refresher) checkMatched(ctx context.Context, item manualItem, run *serverRun) error {
	for _, d := range matchCheckDelays {
		if err := r.sleep(ctx, d); err != nil {
			if run == nil {
				return nil
			}
			return err
		}
		settled, err := r.checkHeld(ctx, item, run)
		if errors.Is(err, ErrNotCached) {
			// A run read the item meanwhile and completed or removed it.
			return nil
		}
		if err != nil {
			if end, stop := r.settleFailed(ctx, item, run, "check", err); end {
				return stop
			}
		}
		if settled {
			return nil
		}
	}
	return nil
}

// settleFailed handles the failed step of settleMatch and reports whether
// the follow-up ends, with the error that settleMatch then returns: a store
// error, or the error of stopError for a run. It logs any other failure.
func (r *Refresher) settleFailed(ctx context.Context, item manualItem, run *serverRun, step string, err error) (bool, error) {
	if errors.Is(err, ErrStore) {
		return true, err
	}
	r.logSettleFailure(item, step, err)
	stop := stopError(ctx, err)
	if stop == nil || run == nil {
		return stop != nil, nil
	}
	return true, stop
}

// followUpRefresh sends and records the refresh after an accepted match:
// a manual request when run is nil, or else a request of run.
func (r *Refresher) followUpRefresh(ctx context.Context, item manualItem, run *serverRun) error {
	if run == nil {
		_, err := r.sendManual(ctx, item.server, item.row, item.cached, store.ActionRefresh, nil)
		return err
	}
	return r.runRefresh(ctx, run, item.row)
}

func (r *Refresher) logSettleFailure(item manualItem, step string, err error) {
	log.Warn().Str("server", item.server.Name).Str("title", item.row.Title).Str("step", step).Err(err).
		Msg("Following up a match failed")
}

func (r *Refresher) sendManual(ctx context.Context, srv Server, it store.Item, cached bool, action store.Action, cand *plex.Candidate) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	req := store.Request{Server: srv.Name, RatingKey: it.RatingKey, At: r.now(), Source: store.SourceManual, Action: action}
	var err error
	if cand == nil {
		err = srv.Plex.Refresh(ctx, it.RatingKey)
	} else {
		req.Match = &store.MatchTarget{GUID: cand.GUID, Title: cand.Name, Year: cand.Year}
		err = srv.Plex.Match(ctx, it.RatingKey, *cand)
	}
	req.Accepted = err == nil
	if err != nil {
		req.Error = err.Error()
	}
	if recordErr := r.store.RecordManual(store.ManualRecord{Request: req, Snapshot: it, Cached: cached, Gone: errors.Is(err, plex.ErrNotFound)}); recordErr != nil {
		return true, fmt.Errorf("%w: %w", ErrStore, recordErr)
	}
	itemEvent(log.Info(), srv.Name, r.logLibrary(it), it).Str("action", string(action)).Bool("accepted", req.Accepted).Msg("Sent manual request")
	return true, err
}

func (r *Refresher) logLibrary(row store.Item) plex.Library {
	info, _, _ := r.store.LibraryInfo(row.Server, row.Section)
	return plex.Library{Title: info.Title}
}

// CheckItem reads current metadata and updates a cached item without an attempt.
func (r *Refresher) CheckItem(ctx context.Context, server, key string) error {
	item, done, err := r.admitItem(server, key, true)
	if err != nil {
		return err
	}
	defer done()
	_, err = r.checkHeld(ctx, item, nil)
	return err
}

// checkHeld reads the current metadata of the cached item, whose item lock
// the caller holds, and applies it to the row: for a manual action when
// run is nil, or else for run, which links the events and counts a
// completion. settled is true when the check completed the item or found
// it gone. A row that a run completed or removed meanwhile gives
// ErrNotCached before any Plex request.
func (r *Refresher) checkHeld(ctx context.Context, item manualItem, run *serverRun) (settled bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	server, key := item.server.Name, item.row.RatingKey
	prev, cached, _, err := r.store.KnownItem(server, key)
	if err != nil {
		return false, manualStoreError(err)
	} else if !cached {
		return false, ErrNotCached
	}
	live, err := item.server.Plex.Item(ctx, key)
	if errors.Is(err, plex.ErrNotFound) {
		return true, manualStoreError(r.recordCheckGone(item, run))
	}
	if err != nil {
		return false, err
	}
	live.RatingKey = key
	row := storeItem(server, plex.Library{ID: item.row.Section, Type: item.row.Type}, live, itemReasons(live))
	var runID int64
	if run != nil {
		runID = run.id
	}
	fixed, err := r.store.ApplyCheck(server, row, r.now(), runID)
	if err != nil {
		return false, manualStoreError(err)
	}
	lib := r.logLibrary(row)
	if fixed && run != nil {
		run.complete(lib.Title, prev)
	}
	itemEvent(log.Info(), server, lib, row).Bool("completed", fixed).Msg("Checked item")
	return fixed, nil
}

// recordCheckGone records that a check of item found it gone from Plex: as
// a manual action when run is nil, or else as an event of run.
func (r *Refresher) recordCheckGone(item manualItem, run *serverRun) error {
	server, key := item.server.Name, item.row.RatingKey
	if run != nil {
		return r.store.RecordGone(server, key, r.now(), store.SourceAuto, run.id)
	}
	req := store.Request{Server: server, RatingKey: key, At: r.now(), Source: store.SourceManual}
	return r.store.RecordManual(store.ManualRecord{Request: req, Snapshot: item.row, Cached: true, Gone: true})
}

// SetStopped stops or resumes automatic work for a cached item.
func (r *Refresher) SetStopped(ctx context.Context, server, key string, stop bool) (bool, error) {
	item, done, err := r.admitItem(server, key, true)
	if err != nil {
		return false, err
	}
	defer done()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	changed, err := r.store.SetStopped(ctx, server, key, r.now(), stop)
	if err != nil {
		return false, manualStoreError(err)
	}
	if changed {
		log.Info().Str("server", server).Str("title", item.row.Title).Bool("stopped", stop).Msg("Changed stopped state")
	}
	return changed, nil
}

func manualStoreError(err error) error {
	switch {
	case err == nil, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, store.ErrNoItem):
		return ErrNotCached
	default:
		return fmt.Errorf("%w: %w", ErrStore, err)
	}
}
