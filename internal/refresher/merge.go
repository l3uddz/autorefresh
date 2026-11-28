package refresher

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// mergedInto returns the rating key of the item that Plex merged the cached
// row into after its match to guid: an item of the same library whose GUID
// is guid and whose valid rating key is not the key of the row. It returns
// "" when Plex lists no such item, and the error of a failed request.
func mergedInto(ctx context.Context, srv Server, row store.Item, guid string) (string, error) {
	items, err := srv.Plex.FindByGUID(ctx, row.Section, itemType(row.Type), guid)
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if it.GUID == guid && it.RatingKey != row.RatingKey && plex.ValidRatingKey(it.RatingKey) {
			return it.RatingKey, nil
		}
	}
	return "", nil
}

// itemType returns the Plex item type of the items of a library of the
// type libType, or 0 for an unknown type, which FindByGUID rejects.
func itemType(libType string) int {
	switch libType {
	case plex.MovieLibrary:
		return plex.TypeMovie
	case plex.ShowLibrary:
		return plex.TypeShow
	default:
		return 0
	}
}

// recordFollowUpGone records the 404 sendErr of a request in the follow-up
// of the match of the cached row to guid, through record: with the rating
// key of the item that Plex merged the row into, or "" for a plain removal.
// With guid "", it looks up nothing. A failed lookup records the item as
// gone. It reports whether record wrote the merged completion, logs it and,
// for a run, counts it as a completion. It returns sendErr, joined with the
// error of a failed lookup that stops the server (stopError), or a store
// error (ErrStore).
func (r *Refresher) recordFollowUpGone(ctx context.Context, srv Server, run *serverRun, row store.Item, guid string,
	sendErr error, record func(merged string) (store.Item, bool, error)) (bool, error) {
	merged, lookupErr := r.lookupMerge(ctx, srv, row, guid)
	removed, wroteMerge, err := record(merged)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrStore, err)
	}
	if wroteMerge {
		r.logMerge(run, srv, removed, guid, merged)
	}
	if stop := stopError(ctx, lookupErr); lookupErr != nil && stop != nil {
		return wroteMerge, errors.Join(sendErr, stop)
	}
	return wroteMerge, sendErr
}

// lookupMerge returns the rating key of mergedInto for the row after its
// match to guid, or "" for guid "". It logs a failed lookup.
func (r *Refresher) lookupMerge(ctx context.Context, srv Server, row store.Item, guid string) (string, error) {
	if guid == "" {
		return "", nil
	}
	merged, err := mergedInto(ctx, srv, row, guid)
	if err != nil {
		itemEvent(log.Warn(), srv.Name, r.logLibrary(row), row).Str("guid", guid).Err(err).Msg("Merge lookup failed")
	}
	return merged, err
}

// logMerge logs that Plex merged the removed row into the item merged after
// its match to guid, and counts it as a completion of run, if any.
func (r *Refresher) logMerge(run *serverRun, srv Server, removed store.Item, guid, merged string) {
	lib := r.logLibrary(removed)
	if run != nil {
		run.complete(lib.Title, removed)
	}
	itemEvent(log.Info(), srv.Name, lib, removed).Str("guid", guid).Str("merged_key", merged).Msg("Plex merged the item")
}
