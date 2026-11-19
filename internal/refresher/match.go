package refresher

import "github.com/l3uddz/autorefresh/internal/plex"

// searchResult holds the match candidates that one path ID found.
type searchResult struct {
	id         pathID
	candidates []plex.Candidate
}

// chooseMatch applies fix match steps 4 to 7 of the spec. ok is true only
// for a match to apply. conflict is "" or a short reason for the log.
func chooseMatch(itemYear int, results []searchResult) (cand plex.Candidate, ok bool, conflict string) {
	found := false
	for _, r := range results {
		switch {
		case len(r.candidates) == 0:
			continue
		case len(r.candidates) > 1:
			return plex.Candidate{}, false, "several candidates"
		case r.candidates[0].GUID == "":
			return plex.Candidate{}, false, "candidate without guid"
		case found && r.candidates[0].GUID != cand.GUID:
			return plex.Candidate{}, false, "different candidates"
		}
		cand, found = r.candidates[0], true
	}
	if !found {
		return plex.Candidate{}, false, ""
	}
	if itemYear != 0 && cand.Year != 0 && (itemYear-cand.Year > 1 || cand.Year-itemYear > 1) {
		return plex.Candidate{}, false, "year differs"
	}
	return cand, true, ""
}
