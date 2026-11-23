package refresher

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

// TMDB is the part of tmdb.Client that the refresher uses. Find returns
// tmdb.ErrSeveral when TMDB has several entries for the ID.
type TMDB interface {
	Find(ctx context.Context, source, id string, show bool) (tmdbID string, ok bool, err error)
	Year(ctx context.Context, id string, show bool) (year int, ok bool, err error)
	SeasonEpisodes(ctx context.Context, id string, season int) ([]int, error)
}

var (
	// ErrTMDB marks a failed TMDB request.
	ErrTMDB = errors.New("tmdb lookup")
	// errCache marks a store error of a fix match: a failed read or write of
	// the TMDB ID cache, of the series path, or of the match decision.
	errCache = errors.New("cache")
)

// skipFallback reports whether a fix match skips the TMDB fallback, which
// asks TMDB for the TMDB IDs of the path IDs. reason says why TMDB could
// have helped but is not asked: the paths hold a tmdb ID that Plex already
// searched, they hold no ID that TMDB translates, TMDB is off, or an earlier
// TMDB failure of the run turned it off. The reasons of the paths come
// first, because a token or a later run does not change them. A search that
// found a candidate needs no fallback, so it skips without a reason.
func (r *Refresher) skipFallback(run *serverRun, it store.Item, ids []pathID, results []searchResult) (skip bool, reason string) {
	for _, res := range results {
		if len(res.candidates) > 0 {
			return true, ""
		}
	}
	switch {
	case slices.ContainsFunc(ids, func(id pathID) bool { return id.Source == "tmdb" }):
		return true, store.TMDBHasID
	case !slices.ContainsFunc(ids, func(id pathID) bool { return tmdbSource(id, it.Type) }):
		return true, store.TMDBNoSource
	case r.opts.TMDB == nil:
		return true, store.TMDBOff
	case run.tmdbFailed:
		return true, store.TMDBFailed
	}
	return false, ""
}

// tmdbSource reports whether TMDB translates the path ID of an item of the
// type itemType: an imdb ID, or the tvdb ID of a series.
func tmdbSource(id pathID, itemType string) bool {
	return id.Source == "imdb" || (id.Source == "tvdb" && itemType == plex.ShowLibrary)
}

// tmdbResults translates the imdb path IDs, and the tvdb path IDs of a
// series, to TMDB IDs and searches Plex once for each different TMDB ID. It
// logs each search, and each ID that TMDB has no single entry for, in d.
func (r *Refresher) tmdbResults(ctx context.Context, run *serverRun, lib plex.Library, it store.Item, ids []pathID, d *decisionLog) ([]searchResult, error) {
	srv := run.server
	var results []searchResult
	searched := map[string]bool{}
	for _, from := range ids {
		if !tmdbSource(from, it.Type) {
			continue
		}
		tmdbID, entries, err := r.findTMDBID(ctx, srv.Name, it, from, !run.preview)
		if err != nil {
			return nil, err
		}
		if entries != tmdbOne {
			d.noTMDBEntry(from, entries)
			continue
		}
		if searched[tmdbID] {
			continue
		}
		searched[tmdbID] = true

		id := pathID{"tmdb", tmdbID}
		candidates, err := srv.Plex.SearchMatches(ctx, it.RatingKey, id.title(), lib.Agent, lib.Language, 0)
		d.add(id, searchTMDB, from.title(), candidates, err != nil)
		if err != nil {
			return nil, err
		}
		itemEvent(log.Debug(), srv.Name, lib, it).Str("id", id.title()).Str("from", from.title()).
			Int("candidates", len(candidates)).Msg("Match search result")
		results = append(results, searchResult{id: id, candidates: candidates, pos: len(d.searches) - 1})
	}
	return results, nil
}

// tmdbID returns the TMDB ID of a path ID of the item, from the cache or from
// TMDB. ok is false when TMDB has no single entry for it. It caches each ID
// that TMDB found.
func (r *Refresher) tmdbID(ctx context.Context, server string, it store.Item, from pathID, save bool) (string, bool, error) {
	tmdbID, entries, err := r.findTMDBID(ctx, server, it, from, save)
	return tmdbID, entries == tmdbOne, err
}

// The number of TMDB entries of a path ID that findTMDBID tells apart.
type tmdbEntries int

const (
	tmdbNone tmdbEntries = iota
	tmdbOne
	tmdbSeveral
)

// findTMDBID is tmdbID that also tells no entry from several.
func (r *Refresher) findTMDBID(ctx context.Context, server string, it store.Item, from pathID, save bool) (string, tmdbEntries, error) {
	tmdbID, ok, err := r.store.TMDBID(server, it.RatingKey, from.Source, from.Value)
	if err != nil {
		return "", tmdbNone, fmt.Errorf("%w: %w", errCache, err)
	}
	if ok {
		return tmdbID, tmdbOne, nil
	}

	tmdbID, ok, err = r.opts.TMDB.Find(ctx, from.Source, from.Value, it.Type == plex.ShowLibrary)
	switch {
	case errors.Is(err, tmdb.ErrSeveral):
		return "", tmdbSeveral, nil
	case err != nil:
		return "", tmdbNone, fmt.Errorf("%w: %s: %w", ErrTMDB, from.title(), err)
	case !ok:
		return "", tmdbNone, nil
	case save:
		if err := r.store.SaveTMDBID(server, it.RatingKey, from.Source, from.Value, tmdbID); err != nil {
			return "", tmdbNone, fmt.Errorf("%w: %w", errCache, err)
		}
	}
	return tmdbID, tmdbOne, nil
}

// matchContext bundles the state of an item's fix match search for year
// confirmation and match resolution.
type matchContext struct {
	lib     plex.Library
	it      store.Item
	paths   []string
	ids     []pathID
	results []searchResult
	d       *decisionLog // the log of the searches and of a handled failure
}

// yearResolution is the outcome of confirming a pure year conflict.
type yearResolution struct {
	cand         plex.Candidate
	ok           bool
	folderYear   int    // the (YYYY) release year of the paths, if they have one
	pathYear     int    // >0 when the path's (YYYY) confirmed the candidate
	tmdbYear     int    // >0 when TMDB returned a canonical year
	episodeCount int    // number of regular episodes that confirmed a series
	extraLink    string // TMDB web page added when TMDB confirmed the match
}

// confirmYear tries to confirm a pure year conflict using, in order:
//  1. an unambiguous (YYYY) release year from the item's paths (e.g. when
//     Plex misparsed a bare year in the title), and
//  2. TMDB's canonical year or regular episode numbers, after confirming
//     Plex resolves the TMDB ID to the same GUID.
//
// The resolution keeps the year of the paths, also when it confirmed
// nothing. mc.d logs why TMDB was not asked.
func (r *Refresher) confirmYear(ctx context.Context, run *serverRun, mc matchContext) (yearResolution, error) {
	pathYear := parsePathYear(mc.paths, mc.it.Type == plex.ShowLibrary)
	if pathYear != 0 && pathYear != mc.it.Year {
		if cand, ok, _ := chooseMatch(pathYear, mc.results); ok {
			return yearResolution{cand: cand, ok: true, folderYear: pathYear, pathYear: pathYear}, nil
		}
	}
	var res yearResolution
	var err error
	switch {
	case r.opts.TMDB == nil:
		mc.d.skipTMDB(store.TMDBOff)
	case run.tmdbFailed:
		mc.d.skipTMDB(store.TMDBFailed)
	default:
		res, err = r.confirmWithTMDB(ctx, run, mc, pathYear)
	}
	res.folderYear = pathYear
	return res, err
}

// confirmWithTMDB resolves the item to a single TMDB ID, verifies Plex
// resolves tmdb-<id> to the same candidate GUID if not already searched, and
// compares the local years or regular episode numbers with TMDB's.
func (r *Refresher) confirmWithTMDB(ctx context.Context, run *serverRun, mc matchContext, pathYear int) (yearResolution, error) {
	tmdbID, needSearch, err := r.resolveOneTMDBID(ctx, run, mc)
	if err != nil {
		if errors.Is(err, ErrTMDB) && ctx.Err() == nil {
			r.recordTMDBFailure(run, mc.lib, mc.it, mc.d, err)
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

	resolution, err := r.compareTMDBEvidence(ctx, run, mc, tmdbID, pathYear)
	if err != nil || !resolution.ok {
		return resolution, err
	}
	resolution.cand, _, _ = chooseCandidate(mc.results)
	resolution.extraLink = pathID{"tmdb", tmdbID}.link(mc.it.Type == plex.ShowLibrary)
	return resolution, nil
}

// compareTMDBEvidence compares the years, and for a series then the
// regular episode numbers, with those of the TMDB ID tmdbID. It logs the ID
// in mc.d once TMDB answered the year request, so a failed request records
// no TMDB answer.
func (r *Refresher) compareTMDBEvidence(ctx context.Context, run *serverRun, mc matchContext, tmdbID string, pathYear int) (yearResolution, error) {
	show := mc.it.Type == plex.ShowLibrary
	year, ok, err := r.opts.TMDB.Year(ctx, tmdbID, show)
	if err != nil {
		return yearResolution{}, r.confirmationLookupFailed(ctx, run, mc, tmdbID, err)
	}
	mc.d.evidence.TMDBID = tmdbID
	result := yearResolution{tmdbYear: year, ok: ok && yearConfirms(mc.it.Year, pathYear, year)}
	if result.ok || !show {
		return result, nil
	}
	result.episodeCount, err = r.confirmEpisodeNumbers(ctx, run, mc, tmdbID)
	result.ok = result.episodeCount > 0
	return result, err
}

func (r *Refresher) confirmationLookupFailed(ctx context.Context, run *serverRun, mc matchContext, id string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.recordTMDBFailure(run, mc.lib, mc.it, mc.d, fmt.Errorf("%w: tmdb-%s: %w", ErrTMDB, id, err))
	return nil
}

func yearConfirms(itemYear, pathYear, tmdbYear int) bool {
	if !yearDiffers(itemYear, tmdbYear) {
		return true
	}
	return pathYear != 0 && !yearDiffers(pathYear, tmdbYear)
}

// resolveOneTMDBID returns the single TMDB ID associated with the item's
// match, and whether Plex still needs a tmdb-<id> search to verify the GUID.
func (r *Refresher) resolveOneTMDBID(ctx context.Context, run *serverRun, mc matchContext) (string, bool, error) {
	id, found := singleTMDBResult(mc.results)
	// A fallback search already considered the external IDs. A search of a
	// direct path ID can still disagree with a translated IMDb or TVDB ID.
	if found && (id == "" || !slices.Contains(mc.ids, pathID{"tmdb", id})) {
		return id, false, nil
	}
	only, err := r.singlePathTMDBID(ctx, run, mc, id)
	if err != nil || only == "" {
		return "", false, err
	}
	return only, !found, nil
}

// singlePathTMDBID checks that the item's translated path IDs agree with
// each other and with only, the TMDB ID already found, if any. It logs each
// ID that TMDB has no single entry for in mc.d.
func (r *Refresher) singlePathTMDBID(ctx context.Context, run *serverRun, mc matchContext, only string) (string, error) {
	for _, id := range mc.ids {
		if !tmdbSource(id, mc.it.Type) {
			continue
		}
		tmdbID, entries, err := r.findTMDBID(ctx, run.server.Name, mc.it, id, !run.preview)
		if err != nil {
			return "", err
		}
		if entries != tmdbOne {
			mc.d.noTMDBEntry(id, entries)
			continue
		}
		if only != "" && only != tmdbID {
			return "", nil
		}
		only = tmdbID
	}
	return only, nil
}

// singleTMDBResult inspects all tmdb search results in results. found is true
// if any tmdb search was run. id is non-empty only if every tmdb search used
// the same TMDB ID and found a candidate.
func singleTMDBResult(results []searchResult) (id string, found bool) {
	for _, res := range results {
		if res.id.Source != "tmdb" {
			continue
		}
		if len(res.candidates) == 0 || (found && res.id.Value != id) {
			return "", true
		}
		id, found = res.id.Value, true
	}
	return id, found
}

// tieTMDBCandidate checks that Plex resolves tmdb-<tmdbID> to the exact same
// candidate GUID as mc.results, and logs the search in mc.d. A failed Plex
// search propagates its error, matching the tmdb search handling in
// tmdbResults.
func tieTMDBCandidate(ctx context.Context, srv Server, mc matchContext, tmdbID string) (bool, error) {
	id := pathID{"tmdb", tmdbID}
	candidates, err := srv.Plex.SearchMatches(ctx, mc.it.RatingKey, id.title(), mc.lib.Agent, mc.lib.Language, 0)
	mc.d.add(id, searchTie, "", candidates, err != nil)
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
