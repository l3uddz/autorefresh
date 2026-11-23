package refresher

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// titleFallback reports whether the fix match of the item searches Plex by
// its title: a series with a year, whose searches found no candidate, with
// a TMDB client that no failure of the run turned off. The title search
// alone proves nothing, so only TMDB episode numbers confirm its candidate.
func (r *Refresher) titleFallback(run *serverRun, it store.Item, results []searchResult) bool {
	if it.Type != plex.ShowLibrary || it.Year == 0 || r.opts.TMDB == nil || run.tmdbFailed {
		return false
	}
	return !slices.ContainsFunc(results, func(res searchResult) bool { return len(res.candidates) > 0 })
}

// titleMatch searches Plex for the title and the year of the item and picks
// the one candidate with the same title and year (sameTitle). The Plex
// metadata provider gives the TMDB ID of that candidate, which must agree
// with each TMDB ID of the folder names (folderTMDB), and every local season
// must have the episode numbers of TMDB. err is the error of the title
// search or of the episode read, or a store error.
func (r *Refresher) titleMatch(ctx context.Context, run *serverRun, mc matchContext, folderTMDB []string) (evaluation, error) {
	srv := run.server
	cands, err := srv.Plex.SearchMatches(ctx, mc.it.RatingKey, mc.it.Title, mc.lib.Agent, mc.lib.Language, mc.it.Year)
	mc.d.addSearch(mc.it.Title, searchTitle, "", cands, err != nil)
	if err != nil {
		return evaluation{}, err
	}
	itemEvent(log.Debug(), srv.Name, mc.lib, mc.it).Int("candidates", len(cands)).Msg("Match title search result")
	pos := len(mc.d.searches) - 1
	cand, found, conflict := chooseCandidate([]searchResult{{candidates: sameTitle(mc.it, cands)}})
	count := 0
	if found && conflict == "" {
		count, conflict, err = r.confirmTitle(ctx, run, mc, cand, folderTMDB)
	}
	ev := evaluation{conflict: conflict, years: yearResolution{episodeCount: count}}
	if err != nil {
		return ev, err
	}
	if count == 0 {
		cand = plex.Candidate{}
	}
	mc.d.judgeTitle(pos, mc.it, cand.GUID, conflict)
	if count > 0 {
		ev.fm = &fixMatch{
			cand: cand, action: store.ActionTitleMatch, episodeCount: count,
			links: []string{pathID{"tmdb", mc.d.evidence.TMDBID}.link(true)},
		}
	}
	return ev, nil
}

// confirmTitle confirms the candidate of a title search with the TMDB ID
// that the Plex metadata provider gives for it and with the TMDB episode
// numbers. It returns the number of episodes that confirmed the candidate,
// or 0 and the conflict. A failed provider or TMDB request is a handled
// failure of the decision, with no conflict: the item gets a plain refresh,
// and the run goes on.
func (r *Refresher) confirmTitle(ctx context.Context, run *serverRun, mc matchContext, cand plex.Candidate, folderTMDB []string) (int, string, error) {
	series, err := run.server.Plex.ProviderSeries(ctx, cand.GUID)
	switch {
	case ctx.Err() != nil:
		return 0, "", ctx.Err()
	case errors.Is(err, plex.ErrNotFound) || errors.Is(err, plex.ErrInvalidGUID):
		return 0, conflictUnconfirmed, nil
	case err != nil:
		mc.d.fail(err)
		run.failures++
		itemEvent(log.Warn(), run.server.Name, mc.lib, mc.it).Err(err).Msg("Plex metadata provider lookup failed")
		return 0, "", nil
	case series.TMDBID == "":
		return 0, conflictUnconfirmed, nil
	}
	mc.d.evidence.TMDBID = series.TMDBID
	if len(folderTMDB) > 0 && !slices.Contains(folderTMDB, series.TMDBID) {
		return 0, conflictFolderID, nil
	}
	count, err := r.confirmEpisodeNumbers(ctx, run, mc, series.TMDBID)
	if err != nil || count > 0 || mc.d.err != nil {
		return count, "", err
	}
	return 0, conflictUnconfirmed, nil
}

// sameTitle returns the candidates whose title equals the title of the item,
// ignoring case, spaces, and punctuation, and whose year is known and does
// not differ from the year of the item.
func sameTitle(it store.Item, cands []plex.Candidate) []plex.Candidate {
	var out []plex.Candidate
	for _, c := range cands {
		if titleAgrees(it, c.Name, c.Year) {
			out = append(out, c)
		}
	}
	return out
}

// titleAgrees reports whether a candidate with the title name and the year
// year has the title and the year of the item.
func titleAgrees(it store.Item, name string, year int) bool {
	return year != 0 && !yearDiffers(it.Year, year) && foldTitle(name) == foldTitle(it.Title)
}

// foldTitle returns the letters and digits of title in lower case.
func foldTitle(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// folderTMDBIDs returns the TMDB IDs that the folder names give: each tmdb
// path ID, and each TMDB ID that the TMDB fallback searched.
func folderTMDBIDs(ids []pathID, results []searchResult) []string {
	var out []string
	for _, id := range ids {
		if id.Source == "tmdb" && !slices.Contains(out, id.Value) {
			out = append(out, id.Value)
		}
	}
	for _, res := range results {
		if res.id.Source == "tmdb" && !slices.Contains(out, res.id.Value) {
			out = append(out, res.id.Value)
		}
	}
	return out
}
