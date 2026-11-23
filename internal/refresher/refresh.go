package refresher

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// refreshDue handles the first MaxRefreshesPerRun due items of the server,
// in Due order, and waits RefreshDelay between two items. Each item gets at
// most one refresh or match request. It returns only the error of
// stopError, a sleep error, or a store error.
func (r *Refresher) refreshDue(ctx context.Context, run *serverRun) error {
	due, total, err := r.store.Due(store.DueQuery{
		Server:             run.server.Name,
		Sections:           run.sections(),
		Now:                r.now(),
		MinimumAge:         r.opts.MinimumAge,
		RefreshInterval:    r.opts.RefreshInterval,
		MaxRefreshInterval: r.opts.MaxRefreshInterval,
		Ignored:            r.opts.IgnoreReasons,
		Limit:              r.opts.MaxRefreshesPerRun,
	})
	if err != nil {
		return err
	}
	run.due, run.remaining, run.selected = total, total, len(due)
	r.progress().Batch(run.server.Name, len(due), total)
	return r.refreshBatch(ctx, run, due)
}

func (r *Refresher) refreshBatch(ctx context.Context, run *serverRun, items []store.Item) error {
	for i, it := range items {
		if err := r.pace(ctx, run, r.shownItem(run, it), i == 0); err != nil {
			return err
		}
		if err := r.runItem(ctx, run, it); err != nil {
			return err
		}
	}
	return nil
}

// pace waits RefreshDelay before the item next, unless it is the first item
// or the run is a dry run. It reports next as the next item before a wait
// that is longer than zero.
func (r *Refresher) pace(ctx context.Context, run *serverRun, next ProgressItem, first bool) error {
	if first || r.opts.DryRun {
		return nil
	}
	if r.opts.RefreshDelay > 0 {
		r.progress().ItemNext(run.server.Name, next, r.now().Add(r.opts.RefreshDelay))
	}
	return r.sleep(ctx, r.opts.RefreshDelay)
}

// shownItem returns the ProgressItem of the due item it of run, without the
// reasons that the run ignores.
func (r *Refresher) shownItem(run *serverRun, it store.Item) ProgressItem {
	shown := progressItem(it, run.library(it.Section).Title)
	shown.Reasons &^= r.opts.IgnoreReasons
	return shown
}

func (r *Refresher) runItem(ctx context.Context, run *serverRun, batch store.Item) error {
	run.remaining--
	server := run.server.Name
	r.progress().ItemStarted(server, r.shownItem(run, batch))
	unlock, err := r.locks.lock(ctx, server, batch.RatingKey)
	if err != nil {
		return err
	}
	defer unlock()
	row, skip, err := r.recheck(run, batch)
	if err != nil {
		return err
	}
	if skip != "" {
		run.skipped++
		log.Debug().Str("server", server).Str("title", batch.Title).Str("reason", skip).Msg("Skipped item")
		r.itemDone(run, batch.RatingKey, OutcomeSkipped)
		return nil
	}
	return r.refreshItem(ctx, run, row)
}

func (r *Refresher) recheck(run *serverRun, batch store.Item) (store.Item, string, error) {
	row, cached, _, err := r.store.KnownItem(run.server.Name, batch.RatingKey)
	switch {
	case err != nil:
		return store.Item{}, "", err
	case !cached:
		return store.Item{}, "gone", nil
	case !row.StoppedAt.IsZero():
		return row, "stopped", nil
	case row.Attempts != batch.Attempts || row.Reasons&^r.opts.IgnoreReasons == 0:
		return row, "changed", nil
	default:
		return row, "", nil
	}
}

// logSummary logs the summary line of the server.
func (r *Refresher) logSummary(run *serverRun) error {
	incomplete, err := r.store.CountIncomplete(run.server.Name, run.sections(), r.opts.IgnoreReasons)
	if err != nil {
		return err
	}
	run.incomplete = incomplete
	log.Info().Str("server", run.server.Name).
		Int("full_read_items", run.fullReadItems).Int("incremental_read_items", run.incrementalReadItems).
		Int("incomplete_items", incomplete).Int("due_items", run.due).
		Int("refresh_requests", run.refreshes).Int("match_requests", run.matches).Int("failed_requests", run.failures).
		Int("remaining_items", run.remaining).Int("completed_items", len(run.completed)).
		Int("completed_after_request", run.fixed).Int("completed_after_match", run.fixedAfterMatch).
		Dur("duration", r.now().Sub(run.began).Round(time.Millisecond)).Msg("Run summary")
	return nil
}

// refreshItem chooses the action of one due item and sends it, or logs it
// in a dry run.
func (r *Refresher) refreshItem(ctx context.Context, run *serverRun, it store.Item) error {
	lib := run.library(it.Section)
	fm, err := r.findMatch(ctx, run, lib, it)
	if err != nil {
		return r.itemFailed(ctx, run, it, "", nil, err)
	}

	action := store.ActionRefresh
	if fm != nil {
		action = fm.action
	}
	if r.opts.DryRun {
		requestEvent(log.Info(), run.server.Name, lib, it, action, fm).Msg("Dry run")
		r.itemDone(run, it.RatingKey, OutcomeDryRun)
		return nil
	}

	// A canceled request still counts as sent and as an attempt.
	if err := ctx.Err(); err != nil {
		return err
	}
	if fm != nil {
		run.matches++
		err = run.server.Plex.Match(ctx, it.RatingKey, fm.cand)
	} else {
		run.refreshes++
		err = run.server.Plex.Refresh(ctx, it.RatingKey)
	}
	if err != nil {
		return r.itemFailed(ctx, run, it, action, fm, err)
	}

	req := r.request(run, it, action, fm)
	req.Accepted = true
	if err := r.store.RecordRequest(req); err != nil {
		return err
	}
	requestEvent(log.Info(), run.server.Name, lib, it, action, fm).Msg("Sent request")
	r.warnAtLongestWait(run.server.Name, lib, it)
	// The run follows up its match as a manual match is followed up, so a
	// match that fixed the item moves it to Fixed now.
	var stop error
	if fm != nil {
		stop = r.settleMatch(ctx, manualItem{server: run.server, row: it, cached: true}, run)
		if errors.Is(stop, ErrStore) {
			return stop
		}
	}
	// Plex accepted the request, so the item is done also when the follow-up
	// of its match ends the run of the server.
	r.itemDone(run, it.RatingKey, req.Outcome())
	return stop
}

// runRefresh sends the refresh after a match that Plex accepted in the run
// and records it as a request of the run. A 404 answer records the item as
// gone. It returns the error of the request, or a store error (ErrStore).
func (r *Refresher) runRefresh(ctx context.Context, run *serverRun, it store.Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	run.refreshes++
	err := run.server.Plex.Refresh(ctx, it.RatingKey)
	if errors.Is(err, plex.ErrNotFound) {
		if recErr := r.store.RecordGone(run.server.Name, it.RatingKey, r.now(), store.SourceAuto, run.id); recErr != nil {
			return fmt.Errorf("%w: %w", ErrStore, recErr)
		}
		return err
	}
	req := r.request(run, it, store.ActionRefresh, nil)
	req.Accepted = err == nil
	if err != nil {
		run.failures++
		req.Error = err.Error()
	}
	if recErr := r.store.RecordRequest(req); recErr != nil {
		return fmt.Errorf("%w: %w", ErrStore, recErr)
	}
	lib := run.library(it.Section)
	if err != nil {
		itemEvent(log.Warn(), run.server.Name, lib, it).Err(err).Str("action", string(store.ActionRefresh)).Msg("Request failed")
		return err
	}
	requestEvent(log.Info(), run.server.Name, lib, it, store.ActionRefresh, nil).Msg("Sent request")
	// The match counted one refresh already.
	matched := it
	matched.Refreshes++
	r.warnAtLongestWait(run.server.Name, lib, matched)
	return nil
}

// itemFailed handles the failure of a due item: a failed refresh or match
// request, with its action and its fix match fm, or a failed match search,
// with no action and no fm.
//
//   - A 404 answer removes the row and records a gone event. A dry run keeps
//     the row and logs a warning.
//   - Each other failure counts and records an attempt, also when it stops
//     the server. Plex can apply a request before its response fails, and
//     the attempt moves the item to the end of the queue, so it cannot stop
//     the server at the same item in every run. A dry run records nothing.
//   - A match search that the end of the run interrupted changes nothing.
//
// It returns the error of stopError or a store error.
func (r *Refresher) itemFailed(ctx context.Context, run *serverRun, it store.Item, action store.Action, fm *fixMatch, err error) error {
	sent := action != ""
	if errors.Is(err, errCache) {
		return err
	}
	if !sent && ctx.Err() != nil {
		return ctx.Err()
	}

	notFound := ctx.Err() == nil && errors.Is(err, plex.ErrNotFound)
	switch {
	case notFound && !r.opts.DryRun:
		if err := r.store.RecordGone(run.server.Name, it.RatingKey, r.now(), store.SourceAuto, run.id); err != nil {
			return err
		}
		r.itemDone(run, it.RatingKey, OutcomeGone)
		return nil
	case notFound:
		r.itemDone(run, it.RatingKey, OutcomeGone)
	default:
		if err := r.countFailure(ctx, run, it, action, fm, err); err != nil {
			return err
		}
	}

	ev := itemEvent(log.Warn(), run.server.Name, run.library(it.Section), it).Err(err)
	if sent {
		ev.Str("action", string(action)).Msg("Request failed")
	} else {
		ev.Msg("Match search failed")
	}
	return stopError(ctx, err)
}

// countFailure counts a failure of the item and records its attempt with
// the error failed and, if a request was sent, its action and fix match. A
// dry run records nothing. It returns a store error, or the error of ctx if
// the run ended.
func (r *Refresher) countFailure(ctx context.Context, run *serverRun, it store.Item, action store.Action, fm *fixMatch, failed error) error {
	run.failures++
	if !r.opts.DryRun {
		req := r.request(run, it, action, fm)
		req.Error = failed.Error()
		if err := r.store.RecordRequest(req); err != nil {
			return err
		}
	}
	r.itemDone(run, it.RatingKey, store.Request{Action: action}.Outcome())
	return ctx.Err()
}

// request returns the request of the item that the schedule sends now. It
// is not accepted and has no error.
func (r *Refresher) request(run *serverRun, it store.Item, action store.Action, fm *fixMatch) store.Request {
	return store.Request{
		Server: run.server.Name, RatingKey: it.RatingKey, At: r.now(), Source: run.source,
		Action: action, Match: target(fm), RunID: run.id,
	}
}

// target returns the match target of fm, or nil for a plain refresh.
func target(fm *fixMatch) *store.MatchTarget {
	if fm == nil {
		return nil
	}
	return &store.MatchTarget{
		GUID: fm.cand.GUID, Title: fm.cand.Name, Year: fm.cand.Year, Links: fm.links,
		PathYear: fm.pathYear, TMDBYear: fm.tmdbYear, EpisodeCount: fm.episodeCount,
	}
}

// warnAtLongestWait logs a warning if the refresh that Plex just accepted
// moves the item to the longest wait. Each item reaches it once, so it gets
// one warning.
func (r *Refresher) warnAtLongestWait(server string, lib plex.Library, it store.Item) {
	n := store.LongestWaitRefreshes(r.opts.RefreshInterval, r.opts.MaxRefreshInterval)
	if n == 0 || it.Refreshes+1 != n {
		return
	}
	itemEvent(log.Warn(), server, lib, it).Int("refreshes", n).Dur("wait", r.opts.MaxRefreshInterval).
		Msg("Item reached the longest wait")
}

// fixMatch is the candidate that a fix match applies.
type fixMatch struct {
	cand         plex.Candidate
	links        []string     // the web pages of the IDs that found or confirmed cand
	action       store.Action // store.ActionMatch, ActionTMDBMatch if TMDB gave the IDs, or ActionTitleMatch
	pathYear     int          // >0 when the path (YYYY) confirmed a year gap
	tmdbYear     int          // >0 when TMDB supplied a year for confirmation
	episodeCount int          // >0 when regular episodes confirmed a series
}

// evaluation is the result of the fix match search of an item.
type evaluation struct {
	fm       *fixMatch      // nil: no match
	noIDs    bool           // the paths hold no ID, and no title search ran, so nothing was searched
	conflict string         // the conflict that kept the searches from a match
	years    yearResolution // the confirmation of a year conflict
}

// findMatch returns the fix match of the item, or nil for a plain refresh.
// Only an unmatched item that was refreshed before gets a fix match, and
// only if the unmatched reason is not ignored. It records the decision of
// the search. err is the error of a failed ItemPaths, SearchMatches, or
// EpisodeNumbers request, or a store error of the TMDB ID cache, the series
// path, or the decision (errCache).
func (r *Refresher) findMatch(ctx context.Context, run *serverRun, lib plex.Library, it store.Item) (*fixMatch, error) {
	if it.Reasons&store.Unmatched == 0 || r.opts.IgnoreReasons&store.Unmatched != 0 || it.Refreshes < 1 {
		return nil, nil
	}
	ev, dec, err := r.autoMatch(ctx, run, lib, it)
	if recErr := r.recordDecision(ctx, run, it, dec, err); recErr != nil {
		return nil, recErr
	}
	return ev.fm, err
}

// autoMatch runs the automatic fix match of the item and returns its
// evaluation and its decision. A run and a preview both decide through it,
// so they always choose the same way. err is the error of evaluateMatch,
// which the decision also holds.
func (r *Refresher) autoMatch(ctx context.Context, run *serverRun, lib plex.Library, it store.Item) (evaluation, store.MatchDecision, error) {
	d := &decisionLog{}
	ev, err := r.evaluateMatch(ctx, run, lib, it, d)
	dec := d.decision(ev, err)
	dec.ItemYear = it.Year
	return ev, dec, err
}

// evaluateMatch searches the fix match of the item and logs each search in
// d. If no search found a candidate, a TMDB client gives a second pass with
// TMDB IDs, and then a series gets a title search (titleFallback).
func (r *Refresher) evaluateMatch(ctx context.Context, run *serverRun, lib plex.Library, it store.Item, d *decisionLog) (evaluation, error) {
	paths, err := r.itemPaths(ctx, run, it)
	if err != nil {
		return evaluation{}, err
	}
	ids := parseIDs(paths)
	if len(ids) == 0 {
		if r.titleFallback(run, it, paths, nil) {
			return r.titleMatch(ctx, run, matchContext{lib: lib, it: it, paths: paths, d: d})
		}
		return evaluation{noIDs: true}, nil
	}
	results, err := searchIDs(ctx, run.server, lib, it, ids, d)
	if err != nil {
		return evaluation{}, err
	}

	action := store.ActionMatch
	skip, reason := r.skipFallback(run, it, ids, results)
	d.skipTMDB(reason)
	if !skip {
		if results, err = r.tmdbFallback(ctx, run, lib, it, ids, d); err != nil {
			return evaluation{}, err
		}
		action = store.ActionTMDBMatch
	}
	mc := matchContext{lib: lib, it: it, paths: paths, ids: ids, results: results, d: d}
	if r.titleFallback(run, it, paths, results) {
		return r.titleMatch(ctx, run, mc)
	}
	return r.resolveMatch(ctx, run, mc, action)
}

// resolveMatch evaluates search results, confirms pure year conflicts via
// the path year or TMDB when possible, logs unresolved conflicts, and sets
// the verdicts of the logged candidates.
func (r *Refresher) resolveMatch(ctx context.Context, run *serverRun, mc matchContext, action store.Action) (evaluation, error) {
	show := mc.it.Type == plex.ShowLibrary
	cand, match, conflict := chooseMatch(mc.it.Year, mc.results)
	var yr yearResolution
	if conflict == conflictYear {
		var err error
		if yr, err = r.confirmYear(ctx, run, mc); err != nil {
			// The decision keeps the conflict and the years compared before
			// the failure.
			return evaluation{conflict: conflict, years: yr}, err
		}
		if yr.ok {
			cand, match, conflict = yr.cand, true, ""
		}
	}
	if conflict != "" {
		ev := itemEvent(log.Warn(), run.server.Name, mc.lib, mc.it).Str("conflict", conflict).Int("item_year", mc.it.Year)
		if yr.tmdbYear != 0 {
			ev.Int("tmdb_year", yr.tmdbYear)
		}
		ev.Strs("candidates", describeCandidates(mc.results, show)).Msg("Match conflict")
	}
	mc.d.judge(mc.results, cand, conflict, mc.it.Year)
	result := evaluation{conflict: conflict, years: yr}
	if !match {
		return result, nil
	}
	links := foundLinks(mc.results, show)
	if yr.extraLink != "" && !slices.Contains(links, yr.extraLink) {
		links = append(links, yr.extraLink)
	}
	result.fm = &fixMatch{
		cand: cand, links: links, action: action,
		pathYear: yr.pathYear, tmdbYear: yr.tmdbYear, episodeCount: yr.episodeCount,
	}
	return result, nil
}

// itemPaths returns the paths of the item: the movie files from the reads
// of the run, or else the paths that Plex gives for the item. It saves the
// folder of a series as the path of its row, also in a dry run, because the
// path is cache metadata, but not in a preview. A failed save returns an
// errCache error.
func (r *Refresher) itemPaths(ctx context.Context, run *serverRun, it store.Item) ([]string, error) {
	if it.Type == plex.MovieLibrary {
		if paths := run.files[it.RatingKey]; len(paths) > 0 {
			return paths, nil
		}
	}
	paths, err := run.server.Plex.ItemPaths(ctx, it.RatingKey)
	if err != nil {
		return nil, err
	}
	if folder := seriesFolder(it.Type, paths); folder != "" && !run.preview {
		if err := r.store.SetPath(run.server.Name, it.RatingKey, folder); err != nil {
			return nil, fmt.Errorf("%w: %w", errCache, err)
		}
	}
	return paths.All(), nil
}

// seriesFolder returns the first location of a series, or "" for another
// item type or a series without a location. A file is never a folder.
func seriesFolder(itemType string, paths plex.Paths) string {
	if itemType != plex.ShowLibrary || len(paths.Locations) == 0 {
		return ""
	}
	return paths.Locations[0]
}

// searchIDs searches Plex once for each path ID of the item and logs each
// search in d. A failed search that searchFailureIsEmpty accepts counts as
// a search without a candidate. Any other failure returns its error.
func searchIDs(ctx context.Context, srv Server, lib plex.Library, it store.Item, ids []pathID, d *decisionLog) ([]searchResult, error) {
	var results []searchResult
	for _, id := range ids {
		candidates, err := srv.Plex.SearchMatches(ctx, it.RatingKey, id.title(), lib.Agent, lib.Language, 0)
		d.add(id, searchPath, "", candidates, err != nil)
		if err != nil {
			if !searchFailureIsEmpty(ctx, id, err) {
				return nil, err
			}
			itemEvent(log.Debug(), srv.Name, lib, it).Str("id", id.title()).Err(err).Msg("Match search failed")
		}
		itemEvent(log.Debug(), srv.Name, lib, it).Str("id", id.title()).Int("candidates", len(candidates)).
			Msg("Match search result")
		results = append(results, searchResult{id: id, candidates: candidates, pos: len(d.searches) - 1})
	}
	return results, nil
}

// foundLinks returns the web page of each search ID that found a candidate.
func foundLinks(results []searchResult, show bool) []string {
	var links []string
	for _, res := range results {
		if len(res.candidates) > 0 {
			links = append(links, res.id.link(show))
		}
	}
	return links
}

// tmdbFallback returns the results of the second pass with TMDB IDs and logs
// each search in d. If a TMDB lookup fails, it logs the failure, turns the
// second pass off for the rest of the run, and returns no result. The first
// pass found no candidate, so the item then gets a plain refresh, as
// without a TMDB client.
func (r *Refresher) tmdbFallback(ctx context.Context, run *serverRun, lib plex.Library, it store.Item, ids []pathID, d *decisionLog) ([]searchResult, error) {
	results, err := r.tmdbResults(ctx, run, lib, it, ids, d)
	if !errors.Is(err, ErrTMDB) || ctx.Err() != nil {
		return results, err
	}
	r.recordTMDBFailure(run, lib, it, d, err)
	return nil, nil
}

// recordTMDBFailure counts and logs a failed TMDB lookup that the fix match
// handles, turns TMDB off for the rest of the run, and keeps the failure in
// the decision log d.
func (r *Refresher) recordTMDBFailure(run *serverRun, lib plex.Library, it store.Item, d *decisionLog, err error) {
	d.fail(err)
	run.tmdbFailed = true
	run.failures++
	itemEvent(log.Warn(), run.server.Name, lib, it).Err(err).Msg("TMDB lookup failed")
}

// searchFailureIsEmpty reports whether a failed search for id counts as a
// search with no candidate. Plex can answer a tvdb or imdb search with an
// error once it stops resolving such IDs. An error that stops the server or
// says that the item is gone keeps its meaning.
func searchFailureIsEmpty(ctx context.Context, id pathID, err error) bool {
	if id.Source != "tvdb" && id.Source != "imdb" {
		return false
	}
	return stopError(ctx, err) == nil && !errors.Is(err, plex.ErrNotFound)
}

// itemEvent adds the server, the library, the title, and the reasons of an
// item to ev.
func itemEvent(ev *zerolog.Event, server string, lib plex.Library, it store.Item) *zerolog.Event {
	return ev.Str("server", server).Str("library", lib.Title).Str("title", it.Title).Str("reasons", it.Reasons.String())
}

// describeCandidates describes each candidate of the searches, with the web
// page of the ID that found it, for example "Film (2020) via
// https://www.themoviedb.org/movie/1". A candidate without a year has none.
func describeCandidates(results []searchResult, show bool) []string {
	var out []string
	for _, res := range results {
		for _, c := range res.candidates {
			name := c.Name
			if c.Year != 0 {
				name += " (" + strconv.Itoa(c.Year) + ")"
			}
			out = append(out, name+" via "+res.id.link(show))
		}
	}
	return out
}

// requestEvent adds the item and the action of a request. For a fix match,
// it also adds the year of the item, the candidate, and the web pages of the
// IDs that found it, so a wrong match can be found in the log.
func requestEvent(ev *zerolog.Event, server string, lib plex.Library, it store.Item, action store.Action, fm *fixMatch) *zerolog.Event {
	ev = itemEvent(ev, server, lib, it).Str("action", string(action))
	if fm == nil {
		return ev
	}
	ev = ev.Int("item_year", it.Year).Str("candidate", fm.cand.Name).Int("candidate_year", fm.cand.Year)
	if fm.pathYear != 0 {
		ev = ev.Int("path_year", fm.pathYear)
	}
	if fm.tmdbYear != 0 {
		ev = ev.Int("tmdb_year", fm.tmdbYear)
	}
	if fm.episodeCount > 0 {
		ev = ev.Int("episode_count", fm.episodeCount)
	}
	return ev.Strs("links", fm.links)
}
