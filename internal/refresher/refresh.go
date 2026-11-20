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

// refreshStep is the state of the refresh step of one server.
type refreshStep struct {
	run       *serverRun
	libraries map[string]plex.Library // by section
	sent      int                     // refresh and match requests, or logged items in a dry run
}

// refreshDue sends a refresh or a match request to each due item of the
// server, in Due order. It returns only the error of stopError, a sleep
// error, or a store error.
func (r *Refresher) refreshDue(ctx context.Context, run *serverRun) error {
	s := &refreshStep{run: run, libraries: map[string]plex.Library{}}
	for _, lib := range run.libraries {
		s.libraries[lib.ID] = lib
	}

	due, err := r.store.Due(run.server.Name, run.sections(), r.now(), r.opts.MinimumAge,
		r.opts.RefreshInterval, r.opts.MaxRefreshInterval, r.opts.IgnoreReasons)
	if err != nil {
		return err
	}
	run.due = len(due)
	for i, it := range due {
		if r.opts.MaxRefreshesPerRun > 0 && s.sent >= r.opts.MaxRefreshesPerRun {
			run.remaining = len(due) - i
			break
		}
		if err := r.refreshItem(ctx, s, it); err != nil {
			run.remaining = len(due) - i - 1 // the item of the error was reached
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
func (r *Refresher) refreshItem(ctx context.Context, s *refreshStep, it store.Item) error {
	lib := s.libraries[it.Section]
	cand, match, err := r.findMatch(ctx, s.run.server, lib, it, s.run.files)
	if err != nil {
		return r.matchSearchFailed(ctx, s, lib, it, err)
	}

	action := "refresh"
	if match {
		action = "match"
	}
	if r.opts.DryRun {
		s.sent++
		itemEvent(log.Info(), s.run.server.Name, lib, it).Str("action", action).Msg("Dry run")
		return nil
	}

	if s.sent > 0 {
		if err := r.sleep(ctx, r.opts.RefreshDelay); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.sent++
	client := s.run.server.Plex
	if match {
		s.run.matches++
		err = client.Match(ctx, it.RatingKey, cand)
	} else {
		s.run.refreshes++
		err = client.Refresh(ctx, it.RatingKey)
	}

	name := s.run.server.Name
	if err == nil {
		if err := r.store.RecordAttempt(name, it.RatingKey, r.now(), true); err != nil {
			return err
		}
		itemEvent(log.Info(), name, lib, it).Str("action", action).Msg("Sent request")
		r.warnAtLongestWait(name, lib, it)
		return nil
	}
	if ctx.Err() == nil && errors.Is(err, plex.ErrNotFound) {
		return r.store.Delete(name, it.RatingKey)
	}
	// Plex can apply a request before its response fails, so each failed
	// request counts as an attempt, also when it stops the server.
	if err := r.store.RecordAttempt(name, it.RatingKey, r.now(), false); err != nil {
		return err
	}
	s.run.failures++
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	itemEvent(log.Warn(), name, lib, it).Str("action", action).Err(err).Msg("Request failed")
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
// match. If no search found a candidate, a TMDB client gives a second pass
// with TMDB IDs. err is the error of a failed ItemPaths or SearchMatches
// request, of a TMDB lookup (errTMDB), or of the TMDB ID cache (errCache).
func (r *Refresher) findMatch(ctx context.Context, srv Server, lib plex.Library, it store.Item, files map[string][]string) (cand plex.Candidate, match bool, err error) {
	if it.Reasons&store.Unmatched == 0 || it.Refreshes < 1 {
		return plex.Candidate{}, false, nil
	}

	var paths []string
	if it.Type == "movie" {
		paths = files[it.RatingKey]
	}
	if len(paths) == 0 {
		if err := ctx.Err(); err != nil {
			return plex.Candidate{}, false, err
		}
		if paths, err = srv.Plex.ItemPaths(ctx, it.RatingKey); err != nil {
			return plex.Candidate{}, false, err
		}
	}

	ids := parseIDs(paths)
	var results []searchResult
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return plex.Candidate{}, false, err
		}
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

	if r.useTMDB(ids, results) {
		if results, err = r.tmdbResults(ctx, srv, lib, it, ids); err != nil {
			return plex.Candidate{}, false, err
		}
	}

	cand, match, conflict := chooseMatch(it.Year, results)
	if conflict != "" {
		itemEvent(log.Warn(), srv.Name, lib, it).Str("conflict", conflict).Msg("Match conflict")
	}
	return cand, match, nil
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

// matchSearchFailed handles a failed ItemPaths or SearchMatches request of
// a fix match, or a failed TMDB lookup. The item gets no request in this run.
// It returns the error of stopError or a store error.
func (r *Refresher) matchSearchFailed(ctx context.Context, s *refreshStep, lib plex.Library, it store.Item, err error) error {
	if errors.Is(err, errCache) {
		return err
	}
	name := s.run.server.Name
	if stop := stopError(ctx, err); stop != nil {
		if ctx.Err() != nil {
			return stop
		}
		s.run.failures++ // unavailable: the summary still counts it
		// The attempt moves the item to the end of the queue, so it cannot
		// stop the server at the same item in every run.
		if !r.opts.DryRun {
			if err := r.store.RecordAttempt(name, it.RatingKey, r.now(), false); err != nil {
				return err
			}
		}
		return stop
	}
	notFound := errors.Is(err, plex.ErrNotFound)
	if notFound && !r.opts.DryRun {
		return r.store.Delete(name, it.RatingKey)
	}
	if !notFound {
		s.run.failures++
	}
	msg := "Match search failed"
	if errors.Is(err, errTMDB) {
		msg = "TMDB lookup failed"
	}
	itemEvent(log.Warn(), name, lib, it).Err(err).Msg(msg)
	if r.opts.DryRun {
		return nil
	}
	return r.store.RecordAttempt(name, it.RatingKey, r.now(), false)
}

// itemEvent adds the server, the library, the title, and the reasons of an
// item to ev.
func itemEvent(ev *zerolog.Event, server string, lib plex.Library, it store.Item) *zerolog.Event {
	return ev.Str("server", server).Str("library", lib.Title).Str("title", it.Title).Str("reasons", it.Reasons.String())
}
