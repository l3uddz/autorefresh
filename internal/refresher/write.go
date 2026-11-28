package refresher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/config"
	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// ErrNotSent is the error of a write that sendWrite did not send: the save
// of its attempt failed, the admission held it (store.ErrHeld), or its
// deadline passed before the send. No request is recorded or counted for it.
var ErrNotSent = errors.New("write not sent")

// cleanupTimeout bounds each store write that runs without the context of
// its write: the delete of the attempt of a write that was not sent, and a
// hold after a match that the checks did not confirm.
var cleanupTimeout = 5 * time.Second

// timeout returns the request timeout of the server.
func (s Server) timeout() time.Duration {
	if s.Timeout <= 0 {
		return config.DefaultTimeout
	}
	return s.Timeout
}

// sendWrite saves the write attempt of spec and then sends the write with
// send, both within one deadline: the earlier of the end of ctx and the
// server timeout. It returns the attempt and the outcome of the send with
// its error, or an error that wraps ErrNotSent when it sent nothing. Such an
// error also wraps ErrStore unless it is store.ErrHeld or the end of ctx.
func (r *Refresher) sendWrite(ctx context.Context, srv Server, spec store.AttemptSpec,
	send func(context.Context) (plex.Outcome, error)) (store.AttemptResult, error) {
	spec.Server, spec.Timeout = srv.Name, srv.timeout()
	wctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	id, err := r.store.BeginAttempt(wctx, spec)
	if err != nil {
		return store.AttemptResult{}, notSent(ctx, wctx, err)
	}
	if err := wctx.Err(); err != nil {
		r.dropAttempt(srv.Name, spec.RatingKey, id)
		return store.AttemptResult{}, notSent(ctx, wctx, err)
	}
	outcome, err := send(wctx)
	return store.AttemptResult{ID: id, Outcome: store.AttemptOutcome(outcome)}, err
}

// notSent returns the ErrNotSent error of a write whose save failed with
// err, or whose deadline passed.
func notSent(ctx, wctx context.Context, err error) error {
	switch {
	case errors.Is(err, store.ErrHeld):
		return fmt.Errorf("%w: %w", ErrNotSent, err)
	case ctx.Err() != nil:
		return fmt.Errorf("%w: %w", ErrNotSent, ctx.Err())
	case wctx.Err() != nil:
		return fmt.Errorf("%w: %w: %w", ErrNotSent, ErrStore, wctx.Err())
	default:
		return fmt.Errorf("%w: %w: %w", ErrNotSent, ErrStore, err)
	}
}

// dropAttempt deletes the attempt id of a write that was not sent. When the
// delete fails, the attempt protects the item until its end.
func (r *Refresher) dropAttempt(server, key string, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := r.store.DropAttempt(ctx, id); err != nil {
		log.Warn().Str("server", server).Str("rating_key", key).Err(err).Msg("Dropping write attempt failed")
	}
}

// refreshSend returns the send of a refresh of key on srv.
func refreshSend(srv Server, key string) func(context.Context) (plex.Outcome, error) {
	return func(ctx context.Context) (plex.Outcome, error) { return srv.Plex.Refresh(ctx, key) }
}

// matchSend returns the send of the match fm of key on srv.
func matchSend(srv Server, key string, fm *fixMatch) func(context.Context) (plex.Outcome, error) {
	return func(ctx context.Context) (plex.Outcome, error) { return srv.Plex.Match(ctx, key, fm.cand) }
}
