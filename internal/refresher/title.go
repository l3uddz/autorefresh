package refresher

import (
	"cmp"
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

// maxTitleYearChecks is the most candidates of a title search whose year
// the title fallback checks with TMDB. Each check asks the Plex metadata
// provider and TMDB.
const maxTitleYearChecks = 3

// titlePick is the outcome of the title fallback: the candidate cand that
// count episodes confirmed, or the conflict. tmdbYear is TMDB's year of a
// candidate whose Plex year did not agree. verdicts holds the verdicts of
// the candidates whose TMDB year the fallback checked, by GUID.
type titlePick struct {
	cand     plex.Candidate
	count    int
	conflict string
	tmdbYear int
	verdicts map[string]string
}

// titleMatch searches Plex for the title and the year of the item and picks
// the one candidate with the same title and year (sameTitle), also with the
// year of the series folder, if it has one. Without one, the year of TMDB
// can stand in for a stale Plex year (pickByTMDBYear). The Plex
// metadata provider gives the TMDB ID of that candidate, which must equal
// every TMDB ID of the folder names (folderTMDBIDs), and the local seasons
// must confirm the candidate against TMDB's episode numbers
// (confirmEpisodeNumbers). err is the error of the title
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
	p, err := r.pickTitle(ctx, run, mc, pathYear, cands)
	ev := evaluation{conflict: p.conflict, years: yearResolution{folderYear: pathYear, tmdbYear: p.tmdbYear, episodeCount: p.count}}
	if err != nil {
		return ev, err
	}
	if p.count == 0 {
		p.cand = plex.Candidate{}
	}
	mc.d.judgeTitle(pos, mc.it, pathYear, p.cand.GUID, p.conflict, p.verdicts)
	if p.count > 0 {
		ev.fm = &fixMatch{
			cand: p.cand, action: store.ActionTitleMatch, episodeCount: p.count, tmdbYear: p.tmdbYear,
			links: []string{pathID{"tmdb", mc.d.evidence.TMDBID}.link(true)},
		}
	}
	return ev, nil
}

// pickTitle picks the candidate of the title search cands and confirms it.
// The one candidate whose title and year agree (sameTitle) goes on to
// confirmTitle. When no candidate's year agrees, pickByTMDBYear checks the
// candidates with the title of the item and another Plex year, unless the
// folder year differs
// from the year of the item, so that no TMDB year can equal both.
func (r *Refresher) pickTitle(ctx context.Context, run *serverRun, mc matchContext, pathYear int, cands []plex.Candidate) (titlePick, error) {
	agreeing := sameTitle(mc.it, pathYear, cands)
	if len(agreeing) == 0 {
		if pathYear != 0 && pathYear != mc.it.Year {
			return titlePick{}, nil
		}
		return r.pickByTMDBYear(ctx, run, mc, pathYear, staleYears(mc.it, pathYear, cands))
	}
	cand, found, conflict := chooseCandidate([]searchResult{{candidates: agreeing}})
	p := titlePick{cand: cand, conflict: conflict}
	if !found || conflict != "" {
		return p, nil
	}
	var err error
	p.count, p.conflict, err = r.confirmTitle(ctx, run, mc, cand)
	return p, err
}

// confirmTitle confirms the candidate of a title search with the TMDB ID
// that the Plex metadata provider gives for it and with the TMDB episode
// numbers. It returns the number of episodes that confirmed the candidate,
// or 0 and the conflict. A failed provider, episode, or TMDB request is a
// handled failure of the decision, with no conflict: the item gets a plain
// refresh, and the run goes on.
func (r *Refresher) confirmTitle(ctx context.Context, run *serverRun, mc matchContext, cand plex.Candidate) (int, string, error) {
	tmdbID, ok, err := r.providerTMDBID(ctx, run, mc, cand)
	switch {
	case err != nil || !ok:
		return 0, "", err
	case tmdbID == "":
		return 0, conflictUnconfirmed, nil
	}
	mc.d.evidence.TMDBID = tmdbID
	return r.confirmTMDBID(ctx, run, mc, tmdbID)
}

// providerTMDBID returns the TMDB ID that the Plex metadata provider gives
// for cand, or "" when it has none. ok is false after a handled failure
// (titleRequestFailed).
func (r *Refresher) providerTMDBID(ctx context.Context, run *serverRun, mc matchContext, cand plex.Candidate) (string, bool, error) {
	series, err := run.server.Plex.ProviderSeries(ctx, cand.GUID)
	switch {
	case ctx.Err() != nil:
		return "", false, ctx.Err()
	case errors.Is(err, plex.ErrNotFound) || errors.Is(err, plex.ErrInvalidGUID):
		return "", true, nil
	case err != nil:
		return "", false, r.titleRequestFailed(ctx, run, mc, err, "Plex metadata provider lookup failed")
	}
	return series.TMDBID, true, nil
}

// confirmTMDBID confirms a title candidate whose TMDB ID tmdbID the Plex
// metadata provider gave: it must equal every TMDB ID of the folder names,
// and the TMDB episode numbers must confirm the local seasons. It returns
// the number of episodes that confirmed the candidate, or 0 and the
// conflict, as confirmTitle does.
func (r *Refresher) confirmTMDBID(ctx context.Context, run *serverRun, mc matchContext, tmdbID string) (int, string, error) {
	folder, ok, err := r.folderTMDBIDs(ctx, run, mc)
	if err != nil || !ok {
		return 0, "", err
	}
	if folder.several || slices.ContainsFunc(folder.ids, func(id string) bool { return id != tmdbID }) {
		return 0, conflictFolderID, nil
	}
	count, err := r.confirmEpisodeNumbers(ctx, run, mc, tmdbID)
	if err != nil {
		return 0, "", r.titleRequestFailed(ctx, run, mc, err, "Match episode read failed")
	}
	if count > 0 || mc.d.err != nil {
		return count, "", nil
	}
	return 0, conflictUnconfirmed, nil
}

// titleYear is what the title fallback learned of the year of a candidate
// whose Plex year did not agree: the TMDB ID that the Plex metadata
// provider gives for it, TMDB's year of that ID (0: none), and whether
// that year agrees with the item.
type titleYear struct {
	cand   plex.Candidate
	tmdbID string
	year   int
	agrees bool
}

// pickByTMDBYear checks the candidates stale (staleYears) when no
// candidate's year agrees. Plex's year of a series can be stale, such as the year of an
// earlier pilot. TMDB's premiere year (of its season 1) of the TMDB ID that the Plex metadata provider gives for a candidate then
// stands in for it, and must equal the year of the item and the folder
// year, if there is one. The one candidate that TMDB's year confirms goes
// on to confirmTMDBID. Several such candidates are a conflict, and so are
// more than maxTitleYearChecks candidates, which it does not check. A
// candidate that nothing confirms gets the year conflict.
func (r *Refresher) pickByTMDBYear(ctx context.Context, run *serverRun, mc matchContext, pathYear int, stale []plex.Candidate) (titlePick, error) {
	if len(stale) == 0 {
		return titlePick{}, nil
	}
	p := titlePick{conflict: conflictYear, verdicts: make(map[string]string, len(stale))}
	if len(stale) > maxTitleYearChecks {
		p.conflict = conflictSeveral
		for _, c := range stale {
			p.verdicts[c.GUID] = conflictSeveral
		}
		return p, nil
	}
	checked, ok, err := r.titleYears(ctx, run, mc, pathYear, stale)
	if err != nil || !ok {
		return titlePick{}, err
	}
	agreed := slices.DeleteFunc(slices.Clone(checked), func(y titleYear) bool { return !y.agrees })
	for _, y := range checked {
		p.verdicts[y.cand.GUID] = conflictYear
	}
	switch {
	case len(agreed) > 1:
		p.conflict = conflictSeveral
		for _, y := range agreed {
			p.verdicts[y.cand.GUID] = conflictSeveral
		}
		return p, nil
	case len(agreed) == 0 && len(checked) == 1:
		// The decision explains the year of the one candidate that it checked.
		mc.d.evidence.TMDBID, p.tmdbYear = checked[0].tmdbID, checked[0].year
		return p, nil
	case len(agreed) == 0:
		return p, nil
	}
	y := agreed[0]
	mc.d.evidence.TMDBID, p.cand, p.tmdbYear = y.tmdbID, y.cand, y.year
	p.count, p.conflict, err = r.confirmTMDBID(ctx, run, mc, y.tmdbID)
	p.verdicts[y.cand.GUID] = cmp.Or(p.conflict, verdictPicked)
	return p, err
}

// titleYears checks the year of each candidate of stale (titleYear). ok is
// false after a handled failure.
func (r *Refresher) titleYears(ctx context.Context, run *serverRun, mc matchContext, pathYear int, stale []plex.Candidate) ([]titleYear, bool, error) {
	checked := make([]titleYear, 0, len(stale))
	for _, c := range stale {
		y, ok, err := r.titleYear(ctx, run, mc, pathYear, c)
		if err != nil || !ok {
			return nil, ok, err
		}
		checked = append(checked, y)
	}
	return checked, true, nil
}

// titleYear asks the Plex metadata provider for the TMDB ID of cand and
// TMDB for the premiere year of that ID: the year of its season 1, which a
// later season's year never stands in for. The year agrees when it equals the year of
// the item and pathYear, if there is one. ok is false after a handled
// failure: of the provider (titleRequestFailed) or of TMDB, which turns
// TMDB off for the run (confirmationLookupFailed).
func (r *Refresher) titleYear(ctx context.Context, run *serverRun, mc matchContext, pathYear int, cand plex.Candidate) (titleYear, bool, error) {
	y := titleYear{cand: cand}
	tmdbID, ok, err := r.providerTMDBID(ctx, run, mc, cand)
	if err != nil || !ok || tmdbID == "" {
		return y, ok, err
	}
	y.tmdbID = tmdbID
	year, found, err := r.opts.TMDB.PremiereYear(ctx, tmdbID)
	if err != nil {
		return y, false, r.confirmationLookupFailed(ctx, run, mc, tmdbID, err)
	}
	if found {
		y.year = year
	}
	y.agrees = found && year == mc.it.Year && (pathYear == 0 || year == pathYear)
	return y, true, nil
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
// and the folder year pathYear (titleAgrees), deduplicating non-empty GUIDs.
func sameTitle(it store.Item, pathYear int, cands []plex.Candidate) []plex.Candidate {
	var out []plex.Candidate
	for _, c := range cands {
		if !titleAgrees(it, pathYear, c.Name, c.Year) {
			continue
		}
		if c.GUID != "" && slices.ContainsFunc(out, func(o plex.Candidate) bool { return o.GUID == c.GUID }) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// titleAgrees reports whether a candidate with the title name and the year
// year has the title of the item (nameAgrees). Its year must be known and
// must not differ from the year of the item, or from the folder year
// pathYear, if there is one.
func titleAgrees(it store.Item, pathYear int, name string, year int) bool {
	if year == 0 || yearDiffers(it.Year, year) || yearDiffers(pathYear, year) {
		return false
	}
	return nameAgrees(it.Title, name)
}

// staleYears returns the candidates with a GUID and the title of the item
// (nameAgrees) whose Plex year is known but does not agree (titleAgrees),
// each GUID once. A candidate without a Plex year is not one: Plex gives no
// year that could be stale.
func staleYears(it store.Item, pathYear int, cands []plex.Candidate) []plex.Candidate {
	var out []plex.Candidate
	for _, c := range cands {
		listed := slices.ContainsFunc(out, func(o plex.Candidate) bool { return o.GUID == c.GUID })
		if !listed && staleYear(it, pathYear, c.GUID, c.Name, c.Year) {
			out = append(out, c)
		}
	}
	return out
}

// staleYear reports whether a candidate with the GUID guid, the title name,
// and the Plex year year is one of staleYears.
func staleYear(it store.Item, pathYear int, guid, name string, year int) bool {
	return guid != "" && year != 0 && nameAgrees(it.Title, name) && !titleAgrees(it, pathYear, name, year)
}

// nameAgrees reports whether name is the title, ignoring case, spaces, and
// punctuation. A title without letters or digits agrees with nothing.
func nameAgrees(title, name string) bool {
	want := foldTitle(title)
	if !strings.ContainsFunc(want, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
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
	if value, ok := mc.d.resolvedTMDB[id.title()]; ok {
		return value, tmdbOne, nil
	}
	value, entries, err := r.findTMDBID(ctx, run.server.Name, mc.it, id, !run.preview)
	if err == nil {
		if entries == tmdbOne {
			mc.d.rememberTMDB(id, value)
		} else {
			mc.d.noTMDBEntry(id, entries)
		}
	}
	return value, entries, err
}
