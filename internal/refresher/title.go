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
// its title: a series with a year, whose folders do not hold different
// years, whose searches found no candidate, with a TMDB client that no
// failure of the run turned off. The title search alone proves nothing, so
// only TMDB episode numbers confirm its candidate.
func (r *Refresher) titleFallback(run *serverRun, it store.Item, paths []string, results []searchResult) bool {
	if it.Type != plex.ShowLibrary || it.Year == 0 || r.opts.TMDB == nil || run.tmdbFailed {
		return false
	}
	if _, ambiguous := pathYears(paths, true); ambiguous {
		return false
	}
	return !slices.ContainsFunc(results, func(res searchResult) bool { return len(res.candidates) > 0 })
}

// titleMatch searches Plex for the title and the year of the item and picks
// the one candidate with the same title and year (sameTitle), also with the
// year of the series folder, if it has one. The Plex
// metadata provider gives the TMDB ID of that candidate, which must equal
// every TMDB ID of the folder names (folderTMDBIDs), and every local season
// must have the episode numbers of TMDB. err is the error of the title
// search or the episode read that says that the item is gone, or a store
// error. Other failed requests are handled failures (titleRequestFailed).
func (r *Refresher) titleMatch(ctx context.Context, run *serverRun, mc matchContext) (evaluation, error) {
	srv := run.server
	cands, err := srv.Plex.SearchMatches(ctx, mc.it.RatingKey, mc.it.Title, mc.lib.Agent, mc.lib.Language, mc.it.Year)
	mc.d.addSearch(mc.it.Title, searchTitle, "", cands, err != nil)
	if err != nil {
		return evaluation{}, r.titleRequestFailed(ctx, run, mc, err, "Match title search failed")
	}
	itemEvent(log.Debug(), srv.Name, mc.lib, mc.it).Int("candidates", len(cands)).Msg("Match title search result")
	pos := len(mc.d.searches) - 1
	pathYear := parsePathYear(mc.paths, true)
	cand, found, conflict := chooseCandidate([]searchResult{{candidates: sameTitle(mc.it, pathYear, cands)}})
	count := 0
	if found && conflict == "" {
		count, conflict, err = r.confirmTitle(ctx, run, mc, cand)
	}
	ev := evaluation{conflict: conflict, years: yearResolution{folderYear: pathYear, episodeCount: count}}
	if err != nil {
		return ev, err
	}
	if count == 0 {
		cand = plex.Candidate{}
	}
	mc.d.judgeTitle(pos, mc.it, pathYear, cand.GUID, conflict)
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
// or 0 and the conflict. A failed provider, episode, or TMDB request is a
// handled failure of the decision, with no conflict: the item gets a plain
// refresh, and the run goes on.
func (r *Refresher) confirmTitle(ctx context.Context, run *serverRun, mc matchContext, cand plex.Candidate) (int, string, error) {
	series, err := run.server.Plex.ProviderSeries(ctx, cand.GUID)
	switch {
	case ctx.Err() != nil:
		return 0, "", ctx.Err()
	case errors.Is(err, plex.ErrNotFound) || errors.Is(err, plex.ErrInvalidGUID):
		return 0, conflictUnconfirmed, nil
	case err != nil:
		return 0, "", r.titleRequestFailed(ctx, run, mc, err, "Plex metadata provider lookup failed")
	case series.TMDBID == "":
		return 0, conflictUnconfirmed, nil
	}
	mc.d.evidence.TMDBID = series.TMDBID
	folder, ok, err := r.folderTMDBIDs(ctx, run, mc)
	if err != nil || !ok {
		return 0, "", err
	}
	if folder.several || slices.ContainsFunc(folder.ids, func(id string) bool { return id != series.TMDBID }) {
		return 0, conflictFolderID, nil
	}
	count, err := r.confirmEpisodeNumbers(ctx, run, mc, series.TMDBID)
	if err != nil {
		return 0, "", r.titleRequestFailed(ctx, run, mc, err, "Match episode read failed")
	}
	if count > 0 || mc.d.err != nil {
		return count, "", nil
	}
	return 0, conflictUnconfirmed, nil
}

// titleRequestFailed handles a failed Plex request of the title fallback
// and returns the error that ends the fix match: the end of the run, or a
// 404 answer, which says that the item is gone. Any other failure is a
// failed decision, and the item gets the plain refresh that it got before
// the title fallback, so the run goes on.
func (r *Refresher) titleRequestFailed(ctx context.Context, run *serverRun, mc matchContext, err error, msg string) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, plex.ErrNotFound):
		return err
	}
	mc.d.fail(err)
	run.failures++
	itemEvent(log.Warn(), run.server.Name, mc.lib, mc.it).Err(err).Msg(msg)
	return nil
}

// sameTitle returns the candidates whose title and year agree with the item
// and the folder year pathYear (titleAgrees).
func sameTitle(it store.Item, pathYear int, cands []plex.Candidate) []plex.Candidate {
	var out []plex.Candidate
	for _, c := range cands {
		if titleAgrees(it, pathYear, c.Name, c.Year) {
			out = append(out, c)
		}
	}
	return out
}

// titleAgrees reports whether a candidate with the title name and the year
// year has the title of the item, ignoring case, spaces, and punctuation.
// Its year must be known and must not differ from the year of the item, or
// from the folder year pathYear, if there is one. A title without letters
// or digits agrees with nothing.
func titleAgrees(it store.Item, pathYear int, name string, year int) bool {
	want := foldTitle(it.Title)
	switch {
	case !strings.ContainsFunc(want, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }):
		return false
	case year == 0 || yearDiffers(it.Year, year) || yearDiffers(pathYear, year):
		return false
	}
	return foldTitle(name) == want
}

// foldTitle returns title in lower case without spaces and punctuation. It
// keeps letters, digits, symbols such as "+", and marks such as the vowel
// signs of many scripts, which tell titles apart.
func foldTitle(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSymbol(r) || unicode.IsMark(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// folderIDs are the TMDB IDs that the folder names give. several is true
// when TMDB has several entries for a path ID, so the folder names no
// single series.
type folderIDs struct {
	ids     []string
	several bool
}

// folderTMDBIDs returns the TMDB IDs of the folder names: each tmdb path ID,
// and the TMDB ID of each path ID that TMDB translates. ok is false after a
// handled TMDB failure.
func (r *Refresher) folderTMDBIDs(ctx context.Context, run *serverRun, mc matchContext) (folder folderIDs, ok bool, err error) {
	for _, id := range mc.ids {
		value, entries, err := r.folderTMDBID(ctx, run, mc, id)
		if errors.Is(err, ErrTMDB) && ctx.Err() == nil {
			r.recordTMDBFailure(run, mc.lib, mc.it, mc.d, err)
			return folderIDs{}, false, nil
		}
		if err != nil {
			return folderIDs{}, false, err
		}
		folder.several = folder.several || entries == tmdbSeveral
		if entries == tmdbOne && !slices.Contains(folder.ids, value) {
			folder.ids = append(folder.ids, value)
		}
	}
	return folder, true, nil
}

// folderTMDBID returns the TMDB ID of the path ID id and how many entries
// TMDB has for it. A path ID that TMDB does not translate has none. An ID
// that the TMDB fallback of this evaluation already asked about is not
// asked again.
func (r *Refresher) folderTMDBID(ctx context.Context, run *serverRun, mc matchContext, id pathID) (string, tmdbEntries, error) {
	switch {
	case id.Source == "tmdb":
		return id.Value, tmdbOne, nil
	case !tmdbSource(id, mc.it.Type):
		return "", tmdbNone, nil
	case slices.Contains(mc.d.severalTMDB, id.title()):
		return "", tmdbSeveral, nil
	case slices.Contains(mc.d.evidence.TMDBMissing, id.title()):
		return "", tmdbNone, nil
	}
	value, entries, err := r.findTMDBID(ctx, run.server.Name, mc.it, id, !run.preview)
	if err == nil && entries != tmdbOne {
		mc.d.noTMDBEntry(id, entries)
	}
	return value, entries, err
}
