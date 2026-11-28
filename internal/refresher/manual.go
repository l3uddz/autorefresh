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

// RefreshResult tells what RefreshItem sent.
type RefreshResult struct {
	// Match is the fix match that the automatic decision applied; nil: a
	// plain refresh.
	Match *store.MatchTarget
	// Checked is true when a check after the request read the item from
	// Plex and saved it, or found that a run had completed or removed it,
	// so the cached reasons are current.
	Checked bool
}

// RefreshItem handles the item now as a run handles a due item, and
// records each step as a manual action. A cached item that seeksMatch gets
// the automatic fix match decision, also before its first refresh, and the
// decision is recorded. With a pick, RefreshItem applies the match and
// follows it up as settleMatch does. Otherwise, also when the decision
// failed, it requests a refresh and then checks a cached item as
// followUpCheck does. It returns the error of the Plex request, a store
// error (ErrStore), or the error of ctx. It logs a failed follow-up,
// because Plex accepted the request.
func (r *Refresher) RefreshItem(ctx context.Context, server, key string) (RefreshResult, error) {
	item, done, err := r.admitItem(server, key, false)
	if err != nil {
		return RefreshResult{}, err
	}
	defer done()
	fm, err := r.manualMatch(ctx, item)
	if err != nil {
		return RefreshResult{}, err
	}
	if fm != nil {
		checked, err := r.applyMatch(ctx, item, fm)
		return RefreshResult{Match: target(fm), Checked: checked}, err
	}
	if _, err := r.sendManual(ctx, item.server, item.row, item.cached, nil); err != nil {
		return RefreshResult{}, err
	}
	if !item.cached {
		return RefreshResult{}, nil
	}
	checked, err := r.followUpCheck(ctx, item, nil)
	if err != nil {
		r.logSettleFailure(item, "record", err)
	}
	return RefreshResult{Checked: checked}, nil
}

// manualSearchTimeout bounds the fix match search of RefreshItem, so the
// plain refresh after a slow search still gets time.
var manualSearchTimeout = 45 * time.Second

// manualMatch runs the automatic fix match of the item of RefreshItem and
// records its decision, as a run does, if the item is cached and
// seeksMatch. It returns the fix match of the decision, or nil for a plain
// refresh. A failed search, also one that ran out of manualSearchTimeout,
// gives nil too, so the refresh follows, and a 404 answer of that refresh
// records the item as gone. It returns a store error (ErrStore) or the
// error of ctx.
func (r *Refresher) manualMatch(ctx context.Context, item manualItem) (*fixMatch, error) {
	if !item.cached || !r.seeksMatch(item.row) {
		return nil, nil
	}
	lib, err := r.itemLibrary(item.server.Name, item.row.Section)
	if err != nil {
		return nil, err
	}
	searchCtx, cancel := context.WithTimeout(ctx, manualSearchTimeout)
	defer cancel()
	run := &serverRun{server: item.server, files: map[string][]string{}}
	paths, ok, err := r.preCheckUnmatched(ctx, searchCtx, run, lib, item)
	if err != nil || !ok {
		return nil, err
	}
	run.prePaths = map[string]plex.Paths{item.row.RatingKey: paths}
	ev, dec, err := r.autoMatch(searchCtx, run, lib, item.row)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if recErr := r.recordDecision(ctx, run, item.row, dec, err); recErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrStore, recErr)
	}
	if errors.Is(err, errCache) {
		return nil, fmt.Errorf("%w: %w", ErrStore, err)
	}
	if err != nil {
		itemEvent(log.Warn(), item.server.Name, lib, item.row).Err(err).Msg("Match search failed")
		return nil, nil
	}
	if ev.fm == nil {
		return nil, nil
	}
	return r.keepMatch(ctx, item, lib, ev.fm), nil
}

// preCheckUnmatched reads the item and its paths once before autoMatch. If
// Plex already matched the item, it skips autoMatch without recording a
// decision. On 404 it records no decision so the plain refresh records the
// item gone once; other failures record a failed decision.
func (r *Refresher) preCheckUnmatched(ctx, searchCtx context.Context, run *serverRun, lib plex.Library, item manualItem) (plex.Paths, bool, error) {
	live, paths, err := item.server.Plex.ItemWithPaths(searchCtx, item.row.RatingKey)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return plex.Paths{}, false, ctxErr
	}
	if err != nil {
		dec := store.MatchDecision{ItemYear: item.row.Year, Outcome: store.DecisionFailed, Error: err.Error()}
		if recErr := r.recordDecision(ctx, run, item.row, dec, err); recErr != nil {
			return plex.Paths{}, false, fmt.Errorf("%w: %w", ErrStore, recErr)
		}
		if !errors.Is(err, plex.ErrNotFound) {
			itemEvent(log.Warn(), item.server.Name, lib, item.row).Err(err).Msg("Match search failed")
		}
		return plex.Paths{}, false, nil
	}
	if itemReasons(live)&store.Unmatched == 0 {
		itemEvent(log.Info(), item.server.Name, lib, item.row).Str("reason", "no longer unmatched").Msg("Skipped fix match")
		return plex.Paths{}, false, nil
	}
	return paths, true, nil
}

// keepMatch returns the fix match fm of the item of manualMatch if Plex
// still has the item unmatched, or else nil for a plain refresh. The item
// lock does not hold off the library reads of a run, and a read saves what
// it found only at its end, so it asks Plex: the item can have got a match
// during the search, which then stays. If Plex cannot tell, it keeps no
// match either.
func (r *Refresher) keepMatch(ctx context.Context, item manualItem, lib plex.Library, fm *fixMatch) *fixMatch {
	live, err := item.server.Plex.Item(ctx, item.row.RatingKey)
	if err == nil && itemReasons(live)&store.Unmatched != 0 {
		return fm
	}
	ev := itemEvent(log.Info(), item.server.Name, lib, item.row).Str("candidate", fm.cand.Name)
	if err != nil {
		ev = ev.Err(err).Str("reason", "match state unknown")
	} else {
		ev = ev.Str("reason", "no longer unmatched")
	}
	ev.Msg("Skipped fix match")
	return nil
}

// ApplyMatch requests a selected Plex match and records its result.
func (r *Refresher) ApplyMatch(ctx context.Context, server, ratingKey string, cand plex.Candidate) error {
	item, done, err := r.admitItem(server, ratingKey, false)
	if err != nil {
		return err
	}
	defer done()
	_, err = r.applyMatch(ctx, item, &fixMatch{cand: cand, action: store.ActionMatch})
	return err
}

// applyMatch sends the match fm of the admitted item as a manual request
// and follows up a match that Plex accepted with settleMatch, whose checked
// it returns. It returns the error of the request. It logs a failed
// follow-up, because Plex accepted the match.
func (r *Refresher) applyMatch(ctx context.Context, item manualItem, fm *fixMatch) (checked bool, err error) {
	if _, err := r.sendManual(ctx, item.server, item.row, item.cached, fm); err != nil {
		return false, err
	}
	checked, err = r.settleMatch(ctx, item, nil)
	if err != nil {
		r.logSettleFailure(item, "record", err)
	}
	return checked, nil
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
// checked is the result of followUpCheck.
func (r *Refresher) settleMatch(ctx context.Context, item manualItem, run *serverRun) (checked bool, err error) {
	if err := r.followUpRefresh(ctx, item, run); err != nil {
		if end, stop := r.settleFailed(ctx, item, run, "refresh", err); end || errors.Is(err, plex.ErrNotFound) {
			return false, stop
		}
	}
	if !item.cached {
		return false, nil
	}
	return r.followUpCheck(ctx, item, run)
}

// followUpCheck checks the cached item after its accepted request: after
// each wait of matchCheckDelays, until a check completes it, its row leaves
// the cache, or a failure or the end of ctx ends the follow-up. run is nil
// for a manual request. checked is true when a check saved what it read, or
// found that a run had completed or removed the row meanwhile. err is the
// error of settleFailed, or for a run the error of ctx.
func (r *Refresher) followUpCheck(ctx context.Context, item manualItem, run *serverRun) (checked bool, err error) {
	for _, d := range matchCheckDelays {
		if err := r.sleep(ctx, d); err != nil {
			if run == nil {
				return checked, nil
			}
			return checked, err
		}
		settled, err := r.checkHeld(ctx, item, run)
		if errors.Is(err, ErrNotCached) {
			// A run read the item meanwhile and completed or removed it.
			return true, nil
		}
		if err != nil {
			if end, stop := r.settleFailed(ctx, item, run, "check", err); end {
				return checked, stop
			}
			continue
		}
		checked = true
		if settled {
			return true, nil
		}
	}
	return checked, nil
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
		_, err := r.sendManual(ctx, item.server, item.row, item.cached, nil)
		return err
	}
	return r.runRefresh(ctx, run, item.row)
}

func (r *Refresher) logSettleFailure(item manualItem, step string, err error) {
	log.Warn().Str("server", item.server.Name).Str("title", item.row.Title).Str("step", step).Err(err).
		Msg("Following up a request failed")
}

// sendManual sends the fix match fm of the item as a manual request, or a
// refresh if fm is nil, and records the result. sent is false if ctx ended
// before the request.
func (r *Refresher) sendManual(ctx context.Context, srv Server, it store.Item, cached bool, fm *fixMatch) (sent bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	action := store.ActionRefresh
	if fm != nil {
		action = fm.action
	}
	req := store.Request{Server: srv.Name, RatingKey: it.RatingKey, At: r.now(), Source: store.SourceManual,
		Action: action, Match: target(fm)}
	if fm == nil {
		_, err = srv.Plex.Refresh(ctx, it.RatingKey)
	} else {
		_, err = srv.Plex.Match(ctx, it.RatingKey, fm.cand)
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
	row := storeItem(server, plex.Library{ID: prev.Section, Type: prev.Type}, live, itemReasons(live))
	flagWrongMatch(&row, prev.Type, live)
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
	// The saved reasons include a mismatched value that the check kept.
	saved := row
	saved.Reasons |= prev.Reasons & row.Keep
	itemEvent(log.Info(), server, lib, saved).Bool("completed", fixed).Msg("Checked item")
	return fixed, nil
}

// recordCheckGone records that a check of item found it gone from Plex: as
// a manual action when run is nil, or else as an event of run.
func (r *Refresher) recordCheckGone(item manualItem, run *serverRun) error {
	server, key := item.server.Name, item.row.RatingKey
	if run != nil {
		return r.store.RecordGone(server, key, r.now(), store.SourceAuto, "", run.id, store.AttemptResult{})
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
