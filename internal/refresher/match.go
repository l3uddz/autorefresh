package refresher

import "github.com/l3uddz/autorefresh/internal/plex"

const (
	conflictSeveral   = "several candidates"
	conflictNoGUID    = "candidate without guid"
	conflictDifferent = "different candidates"
	conflictYear      = "year differs"
	// conflictUnconfirmed: the TMDB episode numbers did not confirm the
	// candidate of a title search, or the metadata provider has no TMDB ID
	// for it.
	conflictUnconfirmed = "not confirmed"
	// conflictFolderID: the metadata provider gives the candidate of a title
	// search another TMDB ID than the folder names.
	conflictFolderID = "folder id differs"
)

// searchResult holds the match candidates that one path ID found.
type searchResult struct {
	id         pathID
	candidates []plex.Candidate
	pos        int // the index of its search in the decisionLog
}

// chooseCandidate checks that every non-empty result has one candidate with
// a GUID and that all such candidates share the same GUID. It does not check
// the year.
func chooseCandidate(results []searchResult) (cand plex.Candidate, ok bool, conflict string) {
	found := false
	for _, r := range results {
		switch {
		case len(r.candidates) == 0:
			continue
		case len(r.candidates) > 1:
			return plex.Candidate{}, false, conflictSeveral
		case r.candidates[0].GUID == "":
			return plex.Candidate{}, false, conflictNoGUID
		case found && r.candidates[0].GUID != cand.GUID:
			return plex.Candidate{}, false, conflictDifferent
		}
		cand, found = r.candidates[0], true
	}
	return cand, found, ""
}

// chooseMatch checks candidate agreement across all results first, then
// checks the year of every returned candidate against itemYear. ok is true
// only for a match to apply. conflict is "" or a short reason for the log.
func chooseMatch(itemYear int, results []searchResult) (cand plex.Candidate, ok bool, conflict string) {
	cand, found, conflict := chooseCandidate(results)
	if !found || conflict != "" {
		return plex.Candidate{}, false, conflict
	}
	for _, r := range results {
		if len(r.candidates) == 1 && yearDiffers(itemYear, r.candidates[0].Year) {
			return plex.Candidate{}, false, conflictYear
		}
	}
	return cand, true, ""
}

// yearDiffers is true when both years are known and differ by more than 1.
func yearDiffers(itemYear, candYear int) bool {
	if itemYear == 0 || candYear == 0 {
		return false
	}
	diff := itemYear - candYear
	return diff > 1 || diff < -1
}
