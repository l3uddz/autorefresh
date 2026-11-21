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
	Year(ctx context.Context, id string, show bool) (year int, ok bool, err error)
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
		if from.Source != "imdb" && (from.Source != "tvdb" || it.Type != plex.ShowLibrary) {
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

	tmdbID, ok, err = r.opts.TMDB.Find(ctx, from.Source, from.Value, it.Type == plex.ShowLibrary)
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

// matchContext bundles the state of an item's fix match search for year
// confirmation and match resolution.
type matchContext struct {
	lib     plex.Library
	it      store.Item
	paths   []string
	ids     []pathID
	results []searchResult
}

// yearResolution is the outcome of confirming a pure year conflict.
type yearResolution struct {
	cand      plex.Candidate
	ok        bool
	pathYear  int    // >0 when the path's (YYYY) confirmed the candidate
	tmdbYear  int    // >0 when TMDB returned a canonical year
	extraLink string // TMDB web page added when TMDB confirmed the match
}

// confirmYear tries to confirm a pure year conflict using, in order:
//  1. an unambiguous (YYYY) release year from the item's paths (e.g. when
//     Plex misparsed a bare year in the title), and
//  2. TMDB's canonical year (earliest regular season for a series, earliest
//     release date for a movie) after confirming Plex resolves the TMDB ID
//     to the same GUID.
func (r *Refresher) confirmYear(ctx context.Context, run *serverRun, mc matchContext) (yearResolution, error) {
	pathYear := parsePathYear(mc.paths, mc.it.Type == plex.ShowLibrary)
	if pathYear != 0 && pathYear != mc.it.Year {
		if cand, ok, _ := chooseMatch(pathYear, mc.results); ok {
			return yearResolution{cand: cand, ok: true, pathYear: pathYear}, nil
		}
	}
	if r.opts.TMDB == nil || run.tmdbFailed {
		return yearResolution{}, nil
	}
	return r.confirmTMDBYear(ctx, run, mc, pathYear)
}

// confirmTMDBYear resolves the item to a single TMDB ID, verifies Plex
// resolves tmdb-<id> to the same candidate GUID if not already searched, and
// checks TMDB's canonical release year against the item year or pathYear.
func (r *Refresher) confirmTMDBYear(ctx context.Context, run *serverRun, mc matchContext, pathYear int) (yearResolution, error) {
	tmdbID, needSearch, err := r.resolveOneTMDBID(ctx, run.server.Name, mc)
	if err != nil {
		if errors.Is(err, errTMDB) && ctx.Err() == nil {
			r.recordTMDBFailure(run, mc.lib, mc.it, err)
			return yearResolution{}, nil
		}
		return yearResolution{}, err
	}
	if tmdbID == "" {
		return yearResolution{}, nil
	}
	if needSearch {
		matched, err := tieTMDBCandidate(ctx, run.server, mc, tmdbID)
		if err != nil || !matched {
			return yearResolution{}, err
		}
	}

	show := mc.it.Type == plex.ShowLibrary
	tmdbYear, ok, err := r.opts.TMDB.Year(ctx, tmdbID, show)
	if err != nil {
		if ctx.Err() != nil {
			return yearResolution{}, ctx.Err()
		}
		r.recordTMDBFailure(run, mc.lib, mc.it, fmt.Errorf("%w: tmdb-%s: %w", errTMDB, tmdbID, err))
		return yearResolution{}, nil
	}
	if !ok || !yearConfirms(mc.it.Year, pathYear, tmdbYear) {
		return yearResolution{tmdbYear: tmdbYear}, nil
	}
	cand, _, _ := chooseCandidate(mc.results)
	return yearResolution{
		cand:      cand,
		ok:        true,
		tmdbYear:  tmdbYear,
		extraLink: pathID{"tmdb", tmdbID}.link(show),
	}, nil
}

func yearConfirms(itemYear, pathYear, tmdbYear int) bool {
	if !yearDiffers(itemYear, tmdbYear) {
		return true
	}
	return pathYear != 0 && !yearDiffers(pathYear, tmdbYear)
}

// resolveOneTMDBID returns the single TMDB ID associated with the item's
// match, and whether Plex still needs a tmdb-<id> search to verify the GUID.
// If the single ID was already searched in mc.results and found nothing, it
// returns "" to avoid repeating a search that cannot match.
func (r *Refresher) resolveOneTMDBID(ctx context.Context, server string, mc matchContext) (string, bool, error) {
	if id, found := singleMatchedTMDBID(mc.results); found {
		return id, false, nil
	}
	only, err := r.singlePathTMDBID(ctx, server, mc)
	if err != nil || only == "" || searchedTMDBEmpty(mc.results, only) {
		return "", false, err
	}
	return only, true, nil
}

// singlePathTMDBID resolves all path IDs to TMDB IDs and returns the unique
// TMDB ID if exactly one distinct ID is found.
func (r *Refresher) singlePathTMDBID(ctx context.Context, server string, mc matchContext) (string, error) {
	var only string
	for _, id := range mc.ids {
		var tmdbID string
		switch {
		case id.Source == "tmdb":
			tmdbID = id.Value
		case id.Source == "imdb" || (id.Source == "tvdb" && mc.it.Type == plex.ShowLibrary):
			var ok bool
			var err error
			if tmdbID, ok, err = r.tmdbID(ctx, server, mc.it, id); err != nil {
				return "", err
			}
			if !ok {
				continue
			}
		default:
			continue
		}
		if only != "" && only != tmdbID {
			return "", nil
		}
		only = tmdbID
	}
	return only, nil
}

// searchedTMDBEmpty reports whether results already searched tmdb-<id> and
// returned no candidates.
func searchedTMDBEmpty(results []searchResult, id string) bool {
	for _, res := range results {
		if res.id.Source == "tmdb" && res.id.Value == id && len(res.candidates) == 0 {
			return true
		}
	}
	return false
}

// singleMatchedTMDBID returns the unique TMDB ID if one or more tmdb search
// results already found a candidate. found is true whenever any tmdb result
// found a candidate; id is "" if multiple distinct TMDB IDs matched.
func singleMatchedTMDBID(results []searchResult) (id string, found bool) {
	for _, res := range results {
		if res.id.Source != "tmdb" || len(res.candidates) == 0 {
			continue
		}
		if found && res.id.Value != id {
			return "", true
		}
		id, found = res.id.Value, true
	}
	return id, found
}

// tieTMDBCandidate checks that Plex resolves tmdb-<tmdbID> to the exact same
// candidate GUID as mc.results. A failed Plex search propagates its error,
// matching the tmdb search handling in tmdbResults.
func tieTMDBCandidate(ctx context.Context, srv Server, mc matchContext, tmdbID string) (bool, error) {
	id := pathID{"tmdb", tmdbID}
	candidates, err := srv.Plex.SearchMatches(ctx, mc.it.RatingKey, id.title(), mc.lib.Agent, mc.lib.Language)
	if err != nil {
		return false, err
	}
	itemEvent(log.Debug(), srv.Name, mc.lib, mc.it).Str("id", id.title()).Int("candidates", len(candidates)).
		Msg("Match search result")
	if len(candidates) != 1 {
		return false, nil
	}
	all := make([]searchResult, len(mc.results)+1)
	copy(all, mc.results)
	all[len(mc.results)] = searchResult{id: id, candidates: candidates}
	_, ok, conflict := chooseCandidate(all)
	return ok && conflict == "", nil
}
