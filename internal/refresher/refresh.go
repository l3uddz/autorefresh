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
	now       time.Time
	libraries map[string]plex.Library // by section
	sent      int                     // refresh and match requests, or logged items in a dry run
	due       int
	refreshes int // refresh requests sent, also failed ones
	matches   int // match requests sent, also failed ones
	failures  int // failed requests and match searches, without 404 answers
}

// refreshDue sends a refresh or a match request to each due item of the
// server, in Due order, and then logs the summary of the server. It returns
// only the error of stopError, a sleep error, or a store error.
func (r *Refresher) refreshDue(ctx context.Context, run *serverRun) error {
	s := &refreshStep{run: run, now: r.now(), libraries: map[string]plex.Library{}}
	sections := make([]string, 0, len(run.libraries))
	for _, lib := range run.libraries {
		s.libraries[lib.ID] = lib
		sections = append(sections, lib.ID)
	}

	due, err := r.store.Due(run.server.Name, sections, s.now, r.opts.MinimumAge, r.opts.RefreshInterval)
	if err != nil {
		return err
	}
	s.due = len(due)
	for _, it := range due {
		if r.opts.MaxRefreshesPerRun > 0 && s.sent >= r.opts.MaxRefreshesPerRun {
			break
		}
		if err := r.refreshItem(ctx, s, it); err != nil {
			return err
		}
	}

	incomplete, err := r.store.CountIncomplete(run.server.Name, sections)
	if err != nil {
		return err
	}
	log.Info().Str("server", run.server.Name).Int("items", run.itemsRead).Int("incomplete", incomplete).
		Int("due", s.due).Int("refreshes", s.refreshes).Int("matches", s.matches).Int("failures", s.failures).
		Int("completed", run.completed).Dur("duration", time.Since(run.began)).Msg("Run summary")
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
		s.matches++
		err = client.Match(ctx, it.RatingKey, cand)
	} else {
		s.refreshes++
		err = client.Refresh(ctx, it.RatingKey)
	}

	name := s.run.server.Name
	if err == nil {
		if err := r.store.RecordAttempt(name, it.RatingKey, s.now, true); err != nil {
			return err
		}
		itemEvent(log.Info(), name, lib, it).Str("action", action).Msg("Sent request")
		return nil
	}
	if stop := stopError(ctx, err); stop != nil {
		return stop
	}
	if errors.Is(err, plex.ErrNotFound) {
		return r.store.Delete(name, it.RatingKey)
	}
	if err := r.store.RecordAttempt(name, it.RatingKey, s.now, false); err != nil {
		return err
	}
	s.failures++
	itemEvent(log.Warn(), name, lib, it).Str("action", action).Err(err).Msg("Request failed")
	return nil
}

// findMatch returns the candidate of a fix match. match is false for a plain
// refresh. Only an unmatched item that was refreshed before gets a fix
// match. err is the error of a failed ItemPaths or SearchMatches request.
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

	var results []searchResult
	for _, id := range parseIDs(paths) {
		if err := ctx.Err(); err != nil {
			return plex.Candidate{}, false, err
		}
		candidates, err := srv.Plex.SearchMatches(ctx, it.RatingKey, id.title(), lib.Agent, lib.Language)
		if err != nil {
			return plex.Candidate{}, false, err
		}
		itemEvent(log.Debug(), srv.Name, lib, it).Str("id", id.title()).Int("candidates", len(candidates)).
			Msg("Match search result")
		results = append(results, searchResult{id: id, candidates: candidates})
	}

	cand, match, conflict := chooseMatch(it.Year, results)
	if conflict != "" {
		itemEvent(log.Warn(), srv.Name, lib, it).Str("conflict", conflict).Msg("Match conflict")
	}
	return cand, match, nil
}

// matchSearchFailed handles a failed ItemPaths or SearchMatches request of
// a fix match. The item gets no request in this run. It returns the error of
// stopError or a store error.
func (r *Refresher) matchSearchFailed(ctx context.Context, s *refreshStep, lib plex.Library, it store.Item, err error) error {
	if stop := stopError(ctx, err); stop != nil {
		return stop
	}
	name := s.run.server.Name
	notFound := errors.Is(err, plex.ErrNotFound)
	if notFound && !r.opts.DryRun {
		return r.store.Delete(name, it.RatingKey)
	}
	if !notFound {
		s.failures++
	}
	itemEvent(log.Warn(), name, lib, it).Err(err).Msg("Match search failed")
	if r.opts.DryRun {
		return nil
	}
	return r.store.RecordAttempt(name, it.RatingKey, s.now, false)
}

// itemEvent adds the server, the library, the title, and the reasons of an
// item to ev.
func itemEvent(ev *zerolog.Event, server string, lib plex.Library, it store.Item) *zerolog.Event {
	return ev.Str("server", server).Str("library", lib.Title).Str("title", it.Title).Str("reasons", it.Reasons.String())
}
