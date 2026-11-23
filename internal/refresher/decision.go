package refresher

import (
	"context"
	"errors"
	"fmt"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// The sources of a search in a match decision.
const (
	searchPath = "path" // a path ID
	searchTMDB = "tmdb" // the TMDB ID of a path ID, from the TMDB fallback
	searchTie  = "tie"  // the TMDB ID that year confirmation checks
)

// The verdicts of a candidate that are not a conflict.
const (
	verdictPicked      = "picked"
	verdictConfirms    = "confirms"
	verdictDifferent   = "different candidate"
	verdictNotCompared = "not compared"
)

// decisionLog collects one fix match evaluation as it happens.
type decisionLog struct {
	searches []store.MatchSearch
	err      error // a failure that the code handled (a swallowed TMDB or confirmation failure)
}

// add logs the search for id and its candidates, with no verdict.
func (d *decisionLog) add(id pathID, source, fromID string, cands []plex.Candidate, failed bool) {
	m := store.MatchSearch{Query: id.title(), Source: source, FromID: fromID, Failed: failed}
	for _, c := range cands {
		m.Candidates = append(m.Candidates, store.MatchCandidate{
			GUID: c.GUID, Name: c.Name, Year: c.Year, Type: c.Type, Summary: c.Summary, Thumb: c.Thumb, Score: c.Score,
		})
	}
	d.searches = append(d.searches, m)
}

// fail keeps the first handled failure.
func (d *decisionLog) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// judge sets the verdict of each candidate of results, the searches that
// resolveMatch evaluated, and of the tie searches. picked is the candidate
// of a match, and conflict the conflict of no match. A handled failure
// leaves every verdict empty.
func (d *decisionLog) judge(results []searchResult, picked plex.Candidate, conflict string) {
	if d.err != nil {
		return
	}
	// A tie search runs only for a year conflict. chooseMatch then returns
	// no candidate, so the tie search compares with the agreed candidate.
	guid := picked.GUID
	if conflict == conflictYear {
		cand, _, _ := chooseCandidate(results)
		guid = cand.GUID
	}
	for _, res := range results {
		d.judgeSearch(&d.searches[res.pos], guid, conflict)
	}
	for i := range d.searches {
		if d.searches[i].Source == searchTie {
			d.judgeSearch(&d.searches[i], guid, conflict)
		}
	}
}

func (d *decisionLog) judgeSearch(m *store.MatchSearch, guid, conflict string) {
	for i := range m.Candidates {
		m.Candidates[i].Verdict = verdict(*m, m.Candidates[i], guid, conflict)
	}
}

// verdict returns the verdict of the candidate c of the search m, by the
// first rule that applies. guid is the GUID of the evaluated candidate.
func verdict(m store.MatchSearch, c store.MatchCandidate, guid, conflict string) string {
	switch {
	case len(m.Candidates) > 1:
		return conflictSeveral
	case c.GUID == "":
		return conflictNoGUID
	case m.Source == searchTie:
		return sameGUID(c.GUID, guid, verdictConfirms)
	case conflict == "":
		return sameGUID(c.GUID, guid, verdictPicked)
	case conflict == conflictDifferent || conflict == conflictYear:
		return conflict
	default: // another search has several candidates or one without a GUID
		return verdictNotCompared
	}
}

// sameGUID returns same if the GUIDs are equal, else verdictDifferent.
func sameGUID(a, b, same string) string {
	if a == b {
		return same
	}
	return verdictDifferent
}

// recordDecision records the decision of the fix match evaluation ev, which
// ended with err, unless it is a dry run or skipDecision applies. A failed
// write returns an errCache error.
func (r *Refresher) recordDecision(ctx context.Context, run *serverRun, it store.Item, d *decisionLog, ev evaluation, err error) error {
	if r.opts.DryRun || skipDecision(ctx, err) {
		return nil
	}
	dec := d.decision(ev, err)
	dec.Server, dec.RatingKey, dec.At, dec.Source = run.server.Name, it.RatingKey, r.now(), store.SourceAuto
	dec.RunID = run.id
	if err := r.store.RecordMatchDecision(dec); err != nil {
		return fmt.Errorf("%w: %w", errCache, err)
	}
	return nil
}

// skipDecision reports whether a fix match that ended with err records no
// decision: a store error stops the run, the end of the run interrupted
// the evaluation, or Plex says that the item is gone, which a gone event
// records.
func skipDecision(ctx context.Context, err error) bool {
	return errors.Is(err, errCache) || ctx.Err() != nil || errors.Is(err, plex.ErrNotFound)
}

// decision returns the outcome of the evaluation ev, which ended with err,
// with the logged searches.
func (d *decisionLog) decision(ev evaluation, err error) store.MatchDecision {
	dec := store.MatchDecision{Searches: d.searches}
	switch {
	case err != nil:
		dec.Outcome, dec.Error = store.DecisionFailed, err.Error()
	case d.err != nil:
		dec.Outcome, dec.Error = store.DecisionFailed, d.err.Error()
	case ev.noIDs:
		dec.Outcome = store.DecisionNoIDs
	default:
		dec.Outcome, dec.Conflict = store.DecisionNoMatch, ev.conflict
		if ev.fm != nil {
			dec.Outcome = store.DecisionMatched
		}
		dec.PathYear, dec.TMDBYear, dec.EpisodeCount = ev.years.pathYear, ev.years.tmdbYear, ev.years.episodeCount
	}
	return dec
}
