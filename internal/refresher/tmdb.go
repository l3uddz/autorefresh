package refresher

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// TMDB is the part of tmdb.Client that the refresher uses.
type TMDB interface {
	Find(ctx context.Context, source, id string, show bool) (tmdbID string, ok bool, err error)
}

var (
	// errTMDB marks a failed TMDB request.
	errTMDB = errors.New("tmdb lookup")
	// errCache marks a failed read or write of the TMDB ID cache, a store error.
	errCache = errors.New("tmdb cache")
)

// useTMDB reports whether a fix match asks TMDB for the TMDB IDs of the path
// IDs: a TMDB client is set, no search found a candidate, and the paths
// hold no tmdb ID that Plex already searched.
func (r *Refresher) useTMDB(ids []pathID, results []searchResult) bool {
	if r.opts.TMDB == nil {
		return false
	}
	for _, id := range ids {
		if id.Source == "tmdb" {
			return false
		}
	}
	for _, res := range results {
		if len(res.candidates) > 0 {
			return false
		}
	}
	return true
}

// tmdbResults translates the imdb path IDs, and the tvdb path IDs of a
// series, to TMDB IDs and searches Plex once for each different TMDB ID.
func (r *Refresher) tmdbResults(ctx context.Context, srv Server, lib plex.Library, it store.Item, ids []pathID) ([]searchResult, error) {
	var results []searchResult
	searched := map[string]bool{}
	for _, from := range ids {
		if from.Source != "imdb" && (from.Source != "tvdb" || it.Type != "show") {
			continue
		}
		tmdbID, ok, err := r.tmdbID(ctx, srv.Name, it, from)
		if err != nil {
			return nil, err
		}
		if !ok || searched[tmdbID] {
			continue
		}
		searched[tmdbID] = true

		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := pathID{"tmdb", tmdbID}
		candidates, err := srv.Plex.SearchMatches(ctx, it.RatingKey, id.title(), lib.Agent, lib.Language)
		if err != nil {
			return nil, err
		}
		itemEvent(log.Debug(), srv.Name, lib, it).Str("id", id.title()).Str("from", from.title()).
			Int("candidates", len(candidates)).Msg("Match search result")
		results = append(results, searchResult{id: id, candidates: candidates})
	}
	return results, nil
}

// tmdbID returns the TMDB ID of a path ID of the item, from the cache or from
// TMDB. It caches each ID that TMDB found.
func (r *Refresher) tmdbID(ctx context.Context, server string, it store.Item, from pathID) (string, bool, error) {
	tmdbID, ok, err := r.store.TMDBID(server, it.RatingKey, from.Source, from.Value)
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", errCache, err)
	}
	if ok {
		return tmdbID, true, nil
	}

	tmdbID, ok, err = r.opts.TMDB.Find(ctx, from.Source, from.Value, it.Type == "show")
	if err != nil {
		return "", false, fmt.Errorf("%w: %s: %w", errTMDB, from.title(), err)
	}
	if !ok {
		return "", false, nil
	}
	if err := r.store.SaveTMDBID(server, it.RatingKey, from.Source, from.Value, tmdbID); err != nil {
		return "", false, fmt.Errorf("%w: %w", errCache, err)
	}
	return tmdbID, true, nil
}
