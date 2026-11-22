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
		r.settleMatch(ctx, item)
	}
	return err
}

// matchCheckDelays are the waits before each check after a manual match.
// Plex fetches the metadata of the new match in the background.
var matchCheckDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// settleMatch runs after Plex accepted a manual match of item, under its
// item lock. It requests a refresh, then checks a cached item until the
// check completes it, or Plex no longer has it, or the waits run out. A
// failure is logged, because Plex accepted the match and the next run reads
// the item again.
func (r *Refresher) settleMatch(ctx context.Context, item manualItem) {
	if _, err := r.sendManual(ctx, item.server, item.row, item.cached, store.ActionRefresh, nil); err != nil {
		r.logSettleFailure(item, "refresh", err)
		if errors.Is(err, plex.ErrNotFound) || ctx.Err() != nil {
			return
		}
	}
	if !item.cached {
		return
	}
	for _, d := range matchCheckDelays {
		if r.sleep(ctx, d) != nil {
			return
		}
		settled, err := r.checkHeld(ctx, item)
		if err != nil {
			r.logSettleFailure(item, "check", err)
		}
		if settled {
			return
		}
	}
}

func (r *Refresher) logSettleFailure(item manualItem, step string, err error) {
	log.Warn().Str("server", item.server.Name).Str("title", item.row.Title).Str("step", step).Err(err).
		Msg("Following up a manual match failed")
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
	_, err = r.checkHeld(ctx, item)
	return err
}

// checkHeld reads the current metadata of the cached item, whose item lock
// the caller holds, and applies it to the row. settled is true when the
// check completed the item or found it gone.
func (r *Refresher) checkHeld(ctx context.Context, item manualItem) (settled bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	server, key := item.server.Name, item.row.RatingKey
	live, err := item.server.Plex.Item(ctx, key)
	if errors.Is(err, plex.ErrNotFound) {
		req := store.Request{Server: server, RatingKey: key, At: r.now(), Source: store.SourceManual}
		return true, manualStoreError(r.store.RecordManual(store.ManualRecord{Request: req, Snapshot: item.row, Cached: true, Gone: true}))
	}
	if err != nil {
		return false, err
	}
	live.RatingKey = key
	row := storeItem(server, plex.Library{ID: item.row.Section, Type: item.row.Type}, live, itemReasons(live))
	fixed, err := r.store.ApplyCheck(server, row, r.now())
	if err != nil {
		return false, manualStoreError(err)
	}
	itemEvent(log.Info(), server, r.logLibrary(row), row).Bool("completed", fixed).Msg("Checked item")
	return fixed, nil
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
