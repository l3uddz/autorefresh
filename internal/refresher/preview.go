package refresher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// PreviewMatch runs the automatic fix match of a known item through
// autoMatch, as a run does, and returns its decision. Unlike a run, it
// takes any item, records no decision, sends no match, and writes nothing
// to the store, also no cache metadata. A failure that a run keeps in its
// decision stays in the decision. The end of ctx, a store error (ErrStore),
// and a 404 answer of Plex (plex.ErrNotFound) return an error instead.
func (r *Refresher) PreviewMatch(ctx context.Context, server, key string) (store.MatchDecision, error) {
	if !r.enter() {
		return store.MatchDecision{}, ErrClosed
	}
	defer r.actions.Done()
	it, err := r.lookupItem(server, key)
	if err != nil {
		return store.MatchDecision{}, err
	}
	info, _, err := r.store.LibraryInfo(server, it.row.Section)
	if err != nil {
		return store.MatchDecision{}, fmt.Errorf("%w: %w", ErrStore, err)
	}
	lib := plex.Library{ID: info.Section, Title: info.Title, Type: info.Type, Agent: info.Agent, Language: info.Language}
	run := &serverRun{server: it.server, files: map[string][]string{}, preview: true}
	_, dec, err := r.autoMatch(ctx, run, lib, it.row)
	if skipDecision(ctx, err) {
		switch {
		case ctx.Err() != nil:
			err = ctx.Err()
		case errors.Is(err, errCache):
			err = fmt.Errorf("%w: %w", ErrStore, err)
		}
		return store.MatchDecision{}, err
	}
	// The store keeps whole seconds, so the preview does too: the dashboard
	// compares its time with the time of a recorded decision.
	dec.Server, dec.RatingKey, dec.At, dec.Source = server, key, r.now().Truncate(time.Second), store.SourceAuto
	return dec, nil
}
