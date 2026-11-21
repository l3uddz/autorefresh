package refresher

import (
	"context"
	"errors"
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
	run.due, run.remaining = total, total
	for i, it := range due {
		if i > 0 && !r.opts.DryRun {
			if err := r.sleep(ctx, r.opts.RefreshDelay); err != nil {
				return err
			}
		}
		run.remaining-- // the item is reached, also if it stops the server
		if err := r.refreshItem(ctx, run, it); err != nil {
			return err
		}
	}
	return nil
}

// logSummary logs the summary line of the server.
func (r *Refresher) logSummary(run *serverRun) error {
	incomplete, err := r.store.CountIncomplete(run.server.Name, run.sections(), r.opts.IgnoreReasons)
	if err != nil {
		return err
	}
	log.Info().Str("server", run.server.Name).
		Int("full_read_items", run.fullReadItems).Int("incremental_read_items", run.incrementalReadItems).
		Int("incomplete_items", incomplete).Int("due_items", run.due).
		Int("refresh_requests", run.refreshes).Int("match_requests", run.matches).Int("failed_requests", run.failures).
		Int("remaining_items", run.remaining).Int("completed_items", len(run.completed)).
		Int("completed_after_request", run.fixed).
		Dur("duration", r.now().Sub(run.began).Round(time.Millisecond)).Msg("Run summary")
	return nil
}

// refreshItem chooses the action of one due item and sends it, or logs it
// in a dry run.
func (r *Refresher) refreshItem(ctx context.Context, run *serverRun, it store.Item) error {
	lib := run.library(it.Section)
	cand, match, err := r.findMatch(ctx, run, lib, it)
	if err != nil {
		return r.itemFailed(ctx, run, it, "", err)
	}

	action := "refresh"
	if match {
		action = "match"
	}
	if r.opts.DryRun {
		itemEvent(log.Info(), run.server.Name, lib, it).Str("action", action).Msg("Dry run")
		return nil
	}

	// A canceled request still counts as sent and as an attempt.
	if err := ctx.Err(); err != nil {
		return err
	}
	if match {
		run.matches++
		err = run.server.Plex.Match(ctx, it.RatingKey, cand)
	} else {
		run.refreshes++
		err = run.server.Plex.Refresh(ctx, it.RatingKey)
	}
	if err != nil {
		return r.itemFailed(ctx, run, it, action, err)
	}

	if err := r.store.RecordAttempt(run.server.Name, it.RatingKey, r.now(), true); err != nil {
		return err
	}
	itemEvent(log.Info(), run.server.Name, lib, it).Str("action", action).Msg("Sent request")
	r.warnAtLongestWait(run.server.Name, lib, it)
	return nil
}

// itemFailed handles the failure of a due item: a failed refresh or match
// request, with its action, or a failed match search, with no action.
//
//   - A 404 answer removes the row. A dry run keeps it and logs a warning.
//   - Each other failure counts and records an attempt, also when it stops
//     the server. Plex can apply a request before its response fails, and
//     the attempt moves the item to the end of the queue, so it cannot stop
//     the server at the same item in every run. A dry run records nothing.
//   - A match search that the end of the run interrupted changes nothing.
//
// It returns the error of stopError or a store error.
func (r *Refresher) itemFailed(ctx context.Context, run *serverRun, it store.Item, action string, err error) error {
	sent := action != ""
	if errors.Is(err, errCache) {
		return err
	}
	if !sent && ctx.Err() != nil {
		return ctx.Err()
	}

	if ctx.Err() == nil && errors.Is(err, plex.ErrNotFound) {
		if !r.opts.DryRun {
			return r.store.Delete(run.server.Name, it.RatingKey)
		}
	} else {
		run.failures++
		if !r.opts.DryRun {
			if err := r.store.RecordAttempt(run.server.Name, it.RatingKey, r.now(), false); err != nil {
				return err
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}

	ev := itemEvent(log.Warn(), run.server.Name, run.library(it.Section), it).Err(err)
	if sent {
		ev.Str("action", action).Msg("Request failed")
	} else {
		ev.Msg("Match search failed")
	}
	return stopError(ctx, err)
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

// findMatch returns the candidate of a fix match. match is false for a plain
// refresh. Only an unmatched item that was refreshed before gets a fix
// match, and only if the unmatched reason is not ignored. If no search found
// a candidate, a TMDB client gives a second pass with TMDB IDs. err is the
// error of a failed ItemPaths or SearchMatches request, or of the TMDB ID
// cache (errCache).
func (r *Refresher) findMatch(ctx context.Context, run *serverRun, lib plex.Library, it store.Item) (cand plex.Candidate, match bool, err error) {
	if it.Reasons&store.Unmatched == 0 || r.opts.IgnoreReasons&store.Unmatched != 0 || it.Refreshes < 1 {
		return plex.Candidate{}, false, nil
	}
	srv := run.server

	var paths []string
	if it.Type == plex.MovieLibrary {
		paths = run.files[it.RatingKey]
	}
	if len(paths) == 0 {
		if paths, err = srv.Plex.ItemPaths(ctx, it.RatingKey); err != nil {
			return plex.Candidate{}, false, err
		}
	}

	ids := parseIDs(paths)
	var results []searchResult
	for _, id := range ids {
		candidates, err := srv.Plex.SearchMatches(ctx, it.RatingKey, id.title(), lib.Agent, lib.Language)
		if err != nil && !searchFailureIsEmpty(ctx, id, err) {
			return plex.Candidate{}, false, err
		}
		if err != nil {
			itemEvent(log.Debug(), srv.Name, lib, it).Str("id", id.title()).Err(err).Msg("Match search failed")
		}
		itemEvent(log.Debug(), srv.Name, lib, it).Str("id", id.title()).Int("candidates", len(candidates)).
			Msg("Match search result")
		results = append(results, searchResult{id: id, candidates: candidates})
	}

	if !run.tmdbFailed && r.useTMDB(ids, results) {
		if results, err = r.tmdbFallback(ctx, run, lib, it, ids); err != nil {
			return plex.Candidate{}, false, err
		}
	}

	cand, match, conflict := chooseMatch(it.Year, results)
	if conflict != "" {
		itemEvent(log.Warn(), srv.Name, lib, it).Str("conflict", conflict).Msg("Match conflict")
	}
	return cand, match, nil
}

// tmdbFallback returns the results of the second pass with TMDB IDs. If a
// TMDB lookup fails, it logs the failure, turns the second pass off for the
// rest of the run, and returns no result. The first pass found no candidate,
// so the item then gets a plain refresh, as without a TMDB client.
func (r *Refresher) tmdbFallback(ctx context.Context, run *serverRun, lib plex.Library, it store.Item, ids []pathID) ([]searchResult, error) {
	results, err := r.tmdbResults(ctx, run.server, lib, it, ids)
	if !errors.Is(err, errTMDB) || ctx.Err() != nil {
		return results, err
	}
	run.tmdbFailed = true
	run.failures++
	itemEvent(log.Warn(), run.server.Name, lib, it).Err(err).Msg("TMDB lookup failed")
	return nil, nil
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
