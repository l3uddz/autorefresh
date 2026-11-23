package refresher

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

const (
	// minCoverageNum / minCoverageDen is the minimum share of TMDB's regular
	// episodes across the local regular seasons that must be present locally
	// when some episodes are missing (90%). With at least one missing episode,
	// this requires at least 9 local episodes per missing episode (for example
	// 9 of 10 or 18 of 20). Exact matches always satisfy the ratio.
	minCoverageNum = 9
	minCoverageDen = 10

	// maxEpisodeGroups bounds the number of TMDB episode group detail requests
	// made when default seasons do not confirm a series.
	maxEpisodeGroups = 10
)

// confirmEpisodeNumbers requires every locally represented regular season to
// contain only valid TMDB episode numbers (no extra or duplicate numbers) and
// at least 90% of TMDB's episodes across the local regular seasons, checking
// TMDB's default seasons first and then TVDB-aligned TMDB episode groups. A
// season that the local library lacks supplies no evidence. It logs a
// differing season in mc.d when the comparison does not confirm the candidate.
func (r *Refresher) confirmEpisodeNumbers(ctx context.Context, run *serverRun, mc matchContext, tmdbID string) (int, error) {
	result, err := compareEpisodes(ctx, run.server.Plex, r.opts.TMDB, mc.it.RatingKey, tmdbID)
	if errors.Is(err, ErrTMDB) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		r.recordTMDBFailure(run, mc.lib, mc.it, mc.d, err)
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if result.group != "" {
		itemEvent(log.Debug(), run.server.Name, mc.lib, mc.it).Str("episode_group", result.group).
			Int("episode_count", result.count).Msg("Episode numbers confirmed via episode group")
	}
	if result.diff.Season > 0 {
		mc.d.evidence.EpisodeDiff = result.diff
		itemEvent(log.Debug(), run.server.Name, mc.lib, mc.it).Int("season", result.diff.Season).
			Int("local_episode_count", result.diff.Plex).Int("tmdb_episode_count", result.diff.TMDB).Msg("Episode numbers differ")
	}
	return result.count, nil
}

// episodeComparison is the result of compareEpisodes. count is the number
// of matched regular episodes when the local seasons confirm the series.
// group is non-empty when an alternate TMDB episode group confirmed it.
// Otherwise diff is the first default season with extra or invalid episode
// numbers, or the first default season missing episodes when overall coverage
// is too low. Both are zero when Plex lists no complete regular episodes.
type episodeComparison struct {
	count int
	group string
	diff  store.EpisodeDiff
}

// compareEpisodes compares the regular episode numbers of the series key in
// Plex with those of the TMDB ID id. It checks TMDB's default seasons first.
// When default seasons do not confirm valid local episodes, it checks
// TVDB-aligned TMDB episode groups in priority order (Original air date, DVD,
// Absolute, TV) and stops at the first group that confirms. If none confirm,
// it keeps the default seasons' diff.
func compareEpisodes(ctx context.Context, p Plex, t TMDB, key, id string) (episodeComparison, error) {
	local, err := p.EpisodeNumbers(ctx, key)
	if err != nil {
		return episodeComparison{}, err
	}
	def, err := compareSeasons(local, func(season int) ([]int, error) {
		catalog, err := t.SeasonEpisodes(ctx, id, season)
		if err != nil {
			return nil, fmt.Errorf("%w: tmdb-%s: %w", ErrTMDB, id, err)
		}
		return catalog, nil
	})
	if err != nil || def.count > 0 {
		return def, err
	}
	localTotal, ok := validLocalTotal(local)
	if !ok {
		return def, nil
	}
	grp, err := compareEpisodeGroups(ctx, t, id, local, localTotal)
	if err != nil {
		return episodeComparison{}, err
	}
	if grp.count > 0 {
		return grp, nil
	}
	return def, nil
}

func compareSeasons(local map[int][]int, catalogOf func(season int) ([]int, error)) (episodeComparison, error) {
	var localTotal, catalogTotal int
	var firstMissing store.EpisodeDiff
	for _, season := range slices.Sorted(maps.Keys(local)) {
		catalog, err := catalogOf(season)
		if err != nil {
			return episodeComparison{}, err
		}
		numbers := local[season]
		if !episodeNumbersSubset(numbers, catalog) {
			return episodeComparison{diff: episodeDiff(season, numbers, catalog)}, nil
		}
		localTotal += len(numbers)
		catalogTotal += len(catalog)
		if len(numbers) < len(catalog) && firstMissing.Season == 0 {
			firstMissing = episodeDiff(season, numbers, catalog)
		}
	}
	if localTotal == 0 {
		return episodeComparison{}, nil
	}
	if localTotal*minCoverageDen < catalogTotal*minCoverageNum {
		return episodeComparison{diff: firstMissing}, nil
	}
	return episodeComparison{count: localTotal}, nil
}

func compareEpisodeGroups(ctx context.Context, t TMDB, id string, local map[int][]int, localTotal int) (episodeComparison, error) {
	groups, err := t.EpisodeGroups(ctx, id)
	if err != nil {
		return episodeComparison{}, fmt.Errorf("%w: tmdb-%s: %w", ErrTMDB, id, err)
	}
	for _, g := range selectEpisodeGroups(groups, localTotal) {
		seasons, err := t.EpisodeGroupSeasons(ctx, g.ID)
		if err != nil {
			return episodeComparison{}, fmt.Errorf("%w: tmdb-%s: %w", ErrTMDB, id, err)
		}
		if len(seasons) == 0 {
			continue
		}
		res, _ := compareSeasons(local, func(season int) ([]int, error) { return seasons[season], nil })
		if res.count > 0 {
			return episodeComparison{count: res.count, group: g.Name}, nil
		}
	}
	return episodeComparison{}, nil
}

// selectEpisodeGroups returns the TVDB-aligned episode groups that contain at
// least localTotal episodes, sorted by episodeGroupPriority and capped at
// maxEpisodeGroups.
func selectEpisodeGroups(groups []tmdb.EpisodeGroup, localTotal int) []tmdb.EpisodeGroup {
	eligible := make([]tmdb.EpisodeGroup, 0, len(groups))
	for _, g := range groups {
		if episodeGroupPriority(g.Type) > 0 && g.EpisodeCount >= localTotal {
			eligible = append(eligible, g)
		}
	}
	slices.SortStableFunc(eligible, func(a, b tmdb.EpisodeGroup) int {
		return episodeGroupPriority(a.Type) - episodeGroupPriority(b.Type)
	})
	if len(eligible) > maxEpisodeGroups {
		return eligible[:maxEpisodeGroups]
	}
	return eligible
}

// episodeGroupPriority returns the priority rank (1..4) of TMDB episode group
// types that align with TheTVDB's season/episode formats, or 0 for other types
// (Digital, Story arc, Production).
func episodeGroupPriority(groupType int) int {
	switch groupType {
	case tmdb.EpisodeGroupOriginalAirDate:
		return 1
	case tmdb.EpisodeGroupDVD:
		return 2
	case tmdb.EpisodeGroupAbsolute:
		return 3
	case tmdb.EpisodeGroupTV:
		return 4
	default:
		return 0
	}
}

// validLocalTotal returns the total number of local regular episodes when
// every local season has at least one valid, distinct positive episode number.
func validLocalTotal(local map[int][]int) (int, bool) {
	if len(local) == 0 {
		return 0, false
	}
	total := 0
	for _, numbers := range local {
		if len(numbers) == 0 {
			return 0, false
		}
		if _, ok := positiveSet(numbers); !ok {
			return 0, false
		}
		total += len(numbers)
	}
	return total, true
}

func episodeDiff(season int, local, catalog []int) store.EpisodeDiff {
	return store.EpisodeDiff{
		Season:  season,
		Plex:    len(local),
		TMDB:    len(catalog),
		Missing: onlyIn(catalog, local),
		Extra:   onlyIn(local, catalog),
	}
}

// onlyIn returns the sorted episode numbers of a that b lacks, each once.
func onlyIn(a, b []int) []int {
	var out []int
	for _, n := range a {
		if !slices.Contains(b, n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// episodeNumbersSubset reports whether local and catalog both hold distinct
// positive episode numbers and every number in local appears in catalog.
func episodeNumbersSubset(local, catalog []int) bool {
	if len(local) == 0 || len(local) > len(catalog) {
		return false
	}
	allowed, ok := positiveSet(catalog)
	if !ok {
		return false
	}
	seen, ok := positiveSet(local)
	if !ok {
		return false
	}
	for n := range seen {
		if _, ok := allowed[n]; !ok {
			return false
		}
	}
	return true
}

// positiveSet returns the elements of nums as a set, or false if any number
// is not positive or appears more than once.
func positiveSet(nums []int) (map[int]struct{}, bool) {
	set := make(map[int]struct{}, len(nums))
	for _, n := range nums {
		if n <= 0 {
			return nil, false
		}
		if _, dup := set[n]; dup {
			return nil, false
		}
		set[n] = struct{}{}
	}
	return set, true
}
