package refresher

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
// records each step as a manual action. A cached item that
// refreshSeeksMatch gets the automatic fix match decision, also before its
// first refresh, and the decision is recorded. With a pick, RefreshItem
// applies the match and follows it up as settleMatch does. Otherwise, also when the decision
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
	refresh, err := r.sendManual(ctx, item.server, item.row, item.cached, nil)
	if err != nil {
		return RefreshResult{}, err
	}
	if !item.cached {
		return RefreshResult{}, nil
	}
	checked, err := r.followUpCheck(ctx, item, nil, followUp{chain: chainOf(refresh)})
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
// refreshSeeksMatch. It returns the fix match of the decision, or nil for a
// plain refresh. A failed search, also one that ran out of manualSearchTimeout,
// gives nil too, so the refresh follows, and a 404 answer of that refresh
// records the item as gone. It returns a store error (ErrStore) or the
// error of ctx.
func (r *Refresher) manualMatch(ctx context.Context, item manualItem) (*fixMatch, error) {
	if !item.cached || !r.refreshSeeksMatch(item.row) {
		return nil, nil
	}
	lib, err := r.itemLibrary(item.server.Name, item.row.Section)
	if err != nil {
		return nil, err
	}
	searchCtx, cancel := context.WithTimeout(ctx, manualSearchTimeout)
	defer cancel()
	run := &serverRun{server: item.server, files: map[string][]string{}}
	paths, state, ok, err := r.preCheck(ctx, searchCtx, run, lib, item)
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
	return r.keepMatch(ctx, item, lib, ev.fm, state), nil
}

// matchState is what the first read of manualMatch found: an unmatched
// item, or the current match of a mismatched movie with the IDs of its
// files, which the search used. The flag and the GUID are separate, so an
// empty GUID never passes as unmatched.
type matchState struct {
	unmatched bool
	guid      string   // the current match of a mismatched movie
	ids       []pathID // the file IDs of a mismatched movie
}

// stateOf returns the match state of the cached row from its live item, or
// the reason why the item no longer needs the fix match.
func stateOf(row store.Item, live plex.Item) (matchState, string) {
	if row.Reasons&store.Unmatched != 0 {
		return matchState{unmatched: true}, matchState{unmatched: true}.lost(live)
	}
	state := matchState{guid: live.GUID, ids: parseIDs(live.Files)}
	if live.GUID == "" {
		return state, "no longer mismatched"
	}
	return state, state.lost(live)
}

// lost returns why the live item no longer needs the fix match of the
// state, or "" when it still does: an unmatched item that Plex matched, or
// a mismatched movie whose match changed, whose IDs no longer disagree with
// its paths, or whose file IDs changed since the search.
func (s matchState) lost(live plex.Item) string {
	if s.unmatched {
		if itemReasons(live)&store.Unmatched == 0 {
			return "no longer unmatched"
		}
		return ""
	}
	if live.GUID != s.guid || CompareMovieIDs(live.GUID, live.Files, live.ExternalIDs).Result != WrongMatchMismatched {
		return "no longer mismatched"
	}
	if !slices.Equal(parseIDs(live.Files), s.ids) {
		return "paths changed"
	}
	return ""
}

// preCheck reads the item and its paths once before autoMatch, and returns
// the paths and the match state. If Plex no longer has the item unmatched,
// or the movie mismatched, it skips autoMatch without recording a
// decision. On 404 it records no decision so the plain refresh records the
// item gone once; other failures record a failed decision.
func (r *Refresher) preCheck(ctx, searchCtx context.Context, run *serverRun, lib plex.Library, item manualItem) (plex.Paths, matchState, bool, error) {
	live, paths, err := item.server.Plex.ItemWithPaths(searchCtx, item.row.RatingKey)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return plex.Paths{}, matchState{}, false, ctxErr
	}
	if err != nil {
		dec := store.MatchDecision{ItemYear: item.row.Year, Outcome: store.DecisionFailed, Error: err.Error()}
		if recErr := r.recordDecision(ctx, run, item.row, dec, err); recErr != nil {
			return plex.Paths{}, matchState{}, false, fmt.Errorf("%w: %w", ErrStore, recErr)
		}
		if !errors.Is(err, plex.ErrNotFound) {
			itemEvent(log.Warn(), item.server.Name, lib, item.row).Err(err).Msg("Match search failed")
		}
		return plex.Paths{}, matchState{}, false, nil
	}
	state, reason := stateOf(item.row, live)
	if reason != "" {
		itemEvent(log.Info(), item.server.Name, lib, item.row).Str("reason", reason).Msg("Skipped fix match")
		return plex.Paths{}, matchState{}, false, nil
	}
	return paths, state, true, nil
}

// keepMatch returns the fix match fm of the item of manualMatch if it still
// applies, or else nil for a plain refresh. A pick that is the current
// match of a mismatched movie never applies. Otherwise it asks Plex,
// because the item lock does not hold off the library reads of a run, and
// a read saves what it found only at its end: the item can have got
// another match during the search, which then stays. If Plex cannot tell,
// it keeps no match either.
func (r *Refresher) keepMatch(ctx context.Context, item manualItem, lib plex.Library, fm *fixMatch, state matchState) *fixMatch {
	reason, err := r.matchLost(ctx, item, fm, state)
	if reason == "" {
		return fm
	}
	ev := itemEvent(log.Info(), item.server.Name, lib, item.row).Str("candidate", fm.cand.Name)
	if err != nil {
		ev = ev.Err(err)
	}
	ev.Str("reason", reason).Msg("Skipped fix match")
	return nil
}

// matchLost returns why the fix match fm of keepMatch no longer applies,
// with the error of a failed read, or "" when it still applies.
func (r *Refresher) matchLost(ctx context.Context, item manualItem, fm *fixMatch, state matchState) (string, error) {
	if !state.unmatched && fm.cand.GUID == state.guid {
		return "already matched", nil
	}
	live, err := item.server.Plex.Item(ctx, item.row.RatingKey)
	if err != nil {
		return "match state unknown", err
	}
	return state.lost(live), nil
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
	match, err := r.sendManual(ctx, item.server, item.row, item.cached, fm)
	if err != nil {
		return false, err
	}
	checked, err = r.settleMatch(ctx, item, nil, match, fm.cand.GUID)
	if err != nil {
		r.logSettleFailure(item, "record", err)
	}
	return checked, nil
}

// matchCheckDelays are the waits before each check after a match. Plex
// fetches the metadata of the new match in the background.
var matchCheckDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// settleMatch runs after Plex accepted the match attempt match of item to
// guid, under its item lock: a manual match when run is nil, or else a
// match of run, which then records the steps as its own. It requests a
// refresh, which a run skips without checks when a hold or another write
// attempt holds the item (store.ErrHeld). When it ends before its checks
// for another reason than a 404, it saves a hold for a cached item. Then it
// checks a cached
// item until the check completes it, Plex no longer has it, a run removed
// its row, the waits run out, or a failure ends it. It returns a store
// error (ErrStore). It logs any other failure, because Plex accepted the
// match and the next run reads the item again. An unavailable server or
// the end of ctx (stopError) ends the follow-up; a run then also gets that
// error, which ends the run of the server as a failed request does.
// checked is the result of followUpCheck.
func (r *Refresher) settleMatch(ctx context.Context, item manualItem, run *serverRun, match store.AttemptResult, guid string) (checked bool, err error) {
	refresh, err := r.followUpRefresh(ctx, item, run, match.ID)
	if err != nil {
		if errors.Is(err, store.ErrHeld) {
			log.Info().Str("server", item.server.Name).Str("title", item.row.Title).Str("reason", "held").
				Msg("Skipped follow-up refresh")
			return false, nil
		}
		gone := errors.Is(err, plex.ErrNotFound)
		if end, stop := r.settleFailed(ctx, item, run, "refresh", err); end || gone {
			if item.cached && !gone {
				stop = errors.Join(stop, r.addHold(ctx, item))
			}
			return false, stop
		}
	}
	if !item.cached {
		return false, nil
	}
	return r.followUpCheck(ctx, item, run, followUp{chain: chainOf(match, refresh), guid: guid})
}

// followUp is what the checks after an accepted request confirm: the write
// attempts of its action, and after a match the GUID of the chosen
// candidate.
type followUp struct {
	chain []int64
	guid  string // "" after a plain refresh
}

// chainOf returns the IDs of the attempts that were saved.
func chainOf(attempts ...store.AttemptResult) []int64 {
	var ids []int64
	for _, a := range attempts {
		if a.ID != 0 {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// checkStep is the result of one check after a request.
type checkStep int

const (
	stepSaved     checkStep = iota // the check saved what Plex shows, and the item stays incomplete
	stepSettled                    // the check completed the item or found it gone
	stepOtherGUID                  // Plex still shows another GUID than the match, so the check saved nothing
	stepYielded                    // the store yielded the save to another write and saved a hold
)

// followUpCheck checks the cached item after its accepted request fu: after
// each wait of matchCheckDelays, until a check completes it, its row leaves
// the cache, a check yields to another write, or a failure or the end of
// ctx ends the follow-up. run is nil for a manual request. checked is true
// when a check saved what it read, or found that a run had completed or
// removed the row meanwhile, and false after a yield. When the checks after
// a match end without the GUID of the match, it saves a hold for the item.
// err is the error of settleFailed or of that hold, or for a run the error
// of ctx.
func (r *Refresher) followUpCheck(ctx context.Context, item manualItem, run *serverRun, fu followUp) (checked bool, err error) {
	confirmed := fu.guid == ""
	for _, d := range matchCheckDelays {
		if err := r.sleep(ctx, d); err != nil {
			return checked, errors.Join(runError(run, err), r.holdUnconfirmed(ctx, item, confirmed))
		}
		step, err := r.checkHeld(ctx, item, run, fu)
		if errors.Is(err, ErrNotCached) {
			// A run read the item meanwhile and completed or removed it.
			return true, nil
		}
		if err != nil {
			if end, stop := r.settleFailed(ctx, item, run, "check", err); end {
				return checked, errors.Join(stop, r.holdUnconfirmed(ctx, item, confirmed))
			}
			continue
		}
		switch step {
		case stepYielded:
			return false, nil
		case stepOtherGUID:
			continue
		}
		confirmed, checked = true, true
		if step == stepSettled {
			return true, nil
		}
	}
	return checked, r.holdUnconfirmed(ctx, item, confirmed)
}

// runError returns err for a run, and nil for a manual action (run nil).
func runError(run *serverRun, err error) error {
	if run == nil {
		return nil
	}
	return err
}

// holdUnconfirmed saves a hold for the item unless a check confirmed the
// GUID of its match, as addHold does.
func (r *Refresher) holdUnconfirmed(ctx context.Context, item manualItem, confirmed bool) error {
	if confirmed {
		return nil
	}
	return r.addHold(ctx, item)
}

// addHold saves a hold for the cached item, so the next read of its
// library is full. It uses its own short context, so it also runs after
// the end of ctx. It returns a store error (ErrStore), or only logs it once
// ctx ended, so a stopped run stays stopped.
func (r *Refresher) addHold(ctx context.Context, item manualItem) error {
	hctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	err := r.store.AddHold(hctx, item.server.Name, item.row.RatingKey)
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		log.Warn().Str("server", item.server.Name).Str("title", item.row.Title).Err(err).Msg("Saving hold failed")
		return nil
	default:
		return fmt.Errorf("%w: %w", ErrStore, err)
	}
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

// followUpRefresh sends and records the refresh after the accepted match
// attempt match: a manual request when run is nil, or else a request of
// run. It returns the attempt of the refresh also with an error answer.
func (r *Refresher) followUpRefresh(ctx context.Context, item manualItem, run *serverRun, match int64) (store.AttemptResult, error) {
	if run == nil {
		return r.sendManual(ctx, item.server, item.row, item.cached, nil)
	}
	return r.runRefresh(ctx, run, item.row, match)
}

func (r *Refresher) logSettleFailure(item manualItem, step string, err error) {
	log.Warn().Str("server", item.server.Name).Str("title", item.row.Title).Str("step", step).Err(err).
		Msg("Following up a request failed")
}

// sendManual sends the fix match fm of the item as a manual request, or a
// refresh if fm is nil, and records the result. It returns the attempt of
// the request with its error, or an error that wraps ErrNotSent when it
// sent nothing.
func (r *Refresher) sendManual(ctx context.Context, srv Server, it store.Item, cached bool, fm *fixMatch) (store.AttemptResult, error) {
	action, send := store.ActionRefresh, refreshSend(srv, it.RatingKey)
	if fm != nil {
		action, send = fm.action, matchSend(srv, it.RatingKey, fm)
	}
	req := store.Request{Server: srv.Name, RatingKey: it.RatingKey, At: r.now(), Source: store.SourceManual,
		Action: action, Match: target(fm)}
	res, err := r.sendWrite(ctx, srv, store.AttemptSpec{RatingKey: it.RatingKey, Action: action}, send)
	if errors.Is(err, ErrNotSent) {
		return res, err
	}
	req.Accepted, req.Attempt = err == nil, res
	if err != nil {
		req.Error = err.Error()
	}
	if recordErr := r.store.RecordManual(store.ManualRecord{Request: req, Snapshot: it, Cached: cached, Gone: errors.Is(err, plex.ErrNotFound)}); recordErr != nil {
		return res, fmt.Errorf("%w: %w", ErrStore, recordErr)
	}
	itemEvent(log.Info(), srv.Name, r.logLibrary(it), it).Str("action", string(action)).Bool("accepted", req.Accepted).Msg("Sent manual request")
	return res, err
}

func (r *Refresher) logLibrary(row store.Item) plex.Library {
	info, _, _ := r.store.LibraryInfo(row.Server, row.Section)
	return plex.Library{Title: info.Title}
}

// checkHeld reads the current metadata of the cached item, whose item lock
// the caller holds, and applies it to the row, unless the store yields the
// save to another write (stepYielded): for a manual action when run is
// nil, or else for run, which links the events and counts a completion.
// After a match, it saves nothing while Plex shows another GUID than fu.guid
// (stepOtherGUID). A row that a run completed or removed meanwhile gives
// ErrNotCached before any Plex request.
func (r *Refresher) checkHeld(ctx context.Context, item manualItem, run *serverRun, fu followUp) (checkStep, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	server, key := item.server.Name, item.row.RatingKey
	prev, cached, _, err := r.store.KnownItem(server, key)
	if err != nil {
		return 0, manualStoreError(err)
	} else if !cached {
		return 0, ErrNotCached
	}
	obs, err := r.store.Observe(server, fu.chain, r.now())
	if err != nil {
		return 0, manualStoreError(err)
	}
	live, err := item.server.Plex.Item(ctx, key)
	if errors.Is(err, plex.ErrNotFound) {
		return r.checkSaved(item, true, r.recordCheckGone(item, run, obs))
	}
	if err != nil {
		return 0, err
	}
	if fu.guid != "" && live.GUID != fu.guid {
		itemEvent(log.Debug(), server, r.logLibrary(prev), prev).Str("guid", live.GUID).Msg("Match not shown yet")
		return stepOtherGUID, nil
	}
	live.RatingKey = key
	row := storeItem(server, plex.Library{ID: prev.Section, Type: prev.Type}, live, itemReasons(live))
	flagWrongMatch(&row, prev.Type, live)
	fixed, err := r.store.ApplyCheck(server, row, r.now(), runIDOf(run), obs)
	step, err := r.checkSaved(item, fixed, err)
	if err != nil || step == stepYielded {
		return step, err
	}
	lib := r.logLibrary(row)
	if fixed && run != nil {
		run.complete(lib.Title, prev)
	}
	// The saved reasons include a mismatched value that the check kept.
	saved := row
	saved.Reasons |= prev.Reasons & row.Keep
	itemEvent(log.Info(), server, lib, saved).Bool("completed", fixed).Msg("Checked item")
	return step, nil
}

// checkSaved returns the step of a check save with the error err: settled
// is true when the save completed the item or removed it as gone.
func (r *Refresher) checkSaved(item manualItem, settled bool, err error) (checkStep, error) {
	switch {
	case errors.Is(err, store.ErrCheckYielded):
		log.Info().Str("server", item.server.Name).Str("title", item.row.Title).Msg("Check yielded to another write")
		return stepYielded, nil
	case err != nil:
		return 0, manualStoreError(err)
	case settled:
		return stepSettled, nil
	default:
		return stepSaved, nil
	}
}

// runIDOf returns the ID of run, or 0 for a manual action (run nil).
func runIDOf(run *serverRun) int64 {
	if run == nil {
		return 0
	}
	return run.id
}

// recordCheckGone records that a check of item with the observation obs
// found it gone from Plex: as a manual action when run is nil, or else as
// an event of run.
func (r *Refresher) recordCheckGone(item manualItem, run *serverRun, obs store.Observation) error {
	source := store.SourceManual
	if run != nil {
		source = store.SourceAuto
	}
	return r.store.ApplyCheckGone(item.server.Name, item.row.RatingKey, r.now(), source, runIDOf(run), obs)
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
