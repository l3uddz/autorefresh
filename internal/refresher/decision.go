package refresher

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	verdictYearAgrees  = "year agrees" // of a year conflict that another search's year caused
)

// decisionLog collects one fix match evaluation as it happens.
type decisionLog struct {
	searches []store.MatchSearch
	evidence store.MatchEvidence
	err      error // a failure that the code handled (a swallowed TMDB or confirmation failure)
}

// skipTMDB keeps the first reason why the evaluation asked TMDB nothing. An
// empty reason changes nothing.
func (d *decisionLog) skipTMDB(reason string) {
	if d.evidence.TMDBSkip == "" {
		d.evidence.TMDBSkip = reason
	}
}

// noTMDBEntry logs that TMDB has no entry for the path ID from.
func (d *decisionLog) noTMDBEntry(from pathID) {
	if !slices.Contains(d.evidence.TMDBMissing, from.title()) {
		d.evidence.TMDBMissing = append(d.evidence.TMDBMissing, from.title())
	}
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
// of a match, conflict the conflict of no match, and itemYear the year of
// the item. A handled failure leaves every verdict empty.
func (d *decisionLog) judge(results []searchResult, picked plex.Candidate, conflict string, itemYear int) {
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
		d.judgeSearch(&d.searches[res.pos], guid, conflict, itemYear)
	}
	for i := range d.searches {
		if d.searches[i].Source == searchTie {
			d.judgeSearch(&d.searches[i], guid, conflict, itemYear)
		}
	}
}

func (d *decisionLog) judgeSearch(m *store.MatchSearch, guid, conflict string, itemYear int) {
	for i := range m.Candidates {
		m.Candidates[i].Verdict = verdict(*m, m.Candidates[i], guid, conflict, itemYear)
	}
}

// verdict returns the verdict of the candidate c of the search m, by the
// first rule that applies. guid is the GUID of the evaluated candidate. Of
// a year conflict, only a candidate whose year differs from itemYear gets
// the conflict: a search can return the same candidate with another year.
func verdict(m store.MatchSearch, c store.MatchCandidate, guid, conflict string, itemYear int) string {
	switch {
	case len(m.Candidates) > 1:
		return conflictSeveral
	case c.GUID == "":
		return conflictNoGUID
	case m.Source == searchTie:
		return sameGUID(c.GUID, guid, verdictConfirms)
	case conflict == "":
		return sameGUID(c.GUID, guid, verdictPicked)
	case conflict == conflictYear && c.Year == 0:
		return verdictNotCompared
	case conflict == conflictYear && !yearDiffers(itemYear, c.Year):
		return verdictYearAgrees
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

// recordDecision records the decision dec of a fix match evaluation, which
// ended with err, unless it is a dry run or skipDecision applies. A failed
// write returns an errCache error.
func (r *Refresher) recordDecision(ctx context.Context, run *serverRun, it store.Item, dec store.MatchDecision, err error) error {
	if r.opts.DryRun || skipDecision(ctx, err) {
		return nil
	}
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
// with the logged searches and evidence. The conflict and the years that
// the evaluation compared stay also when a later step failed.
func (d *decisionLog) decision(ev evaluation, err error) store.MatchDecision {
	dec := store.MatchDecision{Searches: d.searches, MatchEvidence: d.evidence, Conflict: ev.conflict}
	dec.PathYear, dec.TMDBYear, dec.EpisodeCount = ev.years.folderYear, ev.years.tmdbYear, ev.years.episodeCount
	switch {
	case err != nil:
		dec.Outcome, dec.Error = store.DecisionFailed, err.Error()
	case d.err != nil:
		dec.Outcome, dec.Error = store.DecisionFailed, d.err.Error()
	case ev.noIDs:
		dec.Outcome = store.DecisionNoIDs
	default:
		dec.Outcome = store.DecisionNoMatch
		if ev.fm != nil {
			dec.Outcome = store.DecisionMatched
		}
	}
	return dec
}
