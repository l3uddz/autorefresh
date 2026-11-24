package refresher

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

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

	// maxEpisodeGroupName bounds stored episode group names.
	maxEpisodeGroupName = 120
)

// episodeGroupFallbackBudget caps total wall time spent on optional episode
// group lookups without extending the caller's deadline. Tests may shorten it.
var episodeGroupFallbackBudget = 15 * time.Second

// confirmEpisodeNumbers requires every locally represented regular season to
// contain only valid TMDB episode numbers (no extra or duplicate numbers) and
// at least 90% of TMDB's episodes across the local regular seasons, checking
// TMDB's default seasons first and then TVDB-aligned TMDB episode groups. A
// season that the local library lacks supplies no evidence. It logs a
// differing season in mc.d when the comparison does not confirm the candidate,
// and records the confirming episode group name when an alternate group
// confirmed it.
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
		mc.d.evidence.EpisodeGroup = result.group
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
// Absolute, TV) within episodeGroupFallbackBudget and stops at the first group
// that confirms. Isolated group failures keep trying later groups and fall
// back to the default seasons' diff; caller cancellation and service-wide TMDB
// failures (401, 429) stop immediately.
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
	// Pre-check to avoid episode group requests when local numbers are empty,
	// duplicate, or non-positive (no group could ever confirm them).
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
	gctx, cancel := context.WithTimeout(ctx, episodeGroupFallbackBudget)
	defer cancel()

	groups, err := t.EpisodeGroups(gctx, id)
	if err != nil {
		return episodeComparison{}, fallbackGroupError(ctx, gctx, id, "", err)
	}
	for _, g := range selectEpisodeGroups(groups, localTotal) {
		if ctx.Err() != nil {
			return episodeComparison{}, ctx.Err()
		}
		if gctx.Err() != nil {
			return episodeComparison{}, nil
		}
		res, stop, err := tryEpisodeGroup(ctx, gctx, t, id, g, local)
		if err != nil || stop || res.count > 0 {
			return res, err
		}
	}
	return episodeComparison{}, nil
}

func tryEpisodeGroup(ctx, gctx context.Context, t TMDB, id string, g tmdb.EpisodeGroup, local map[int][]int) (episodeComparison, bool, error) {
	seasons, err := t.EpisodeGroupSeasons(gctx, g.ID)
	if err != nil {
		if fatal := fallbackGroupError(ctx, gctx, id, g.ID, err); fatal != nil {
			return episodeComparison{}, true, fatal
		}
		return episodeComparison{}, gctx.Err() != nil, nil
	}
	if len(seasons) == 0 {
		return episodeComparison{}, false, nil
	}
	res, _ := compareSeasons(local, func(season int) ([]int, error) { return seasons[season], nil })
	if res.count > 0 {
		return episodeComparison{count: res.count, group: trimGroupName(g.Name)}, true, nil
	}
	return episodeComparison{}, false, nil
}

// fallbackGroupError separates fatal errors (parent cancellation, 401, 429)
// from isolated optional-lookup failures, which are logged and ignored so
// later groups can run and the default diff stays intact.
func fallbackGroupError(ctx, gctx context.Context, tmdbID, groupID string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, tmdb.ErrUnauthorized) || errors.Is(err, tmdb.ErrRateLimited) {
		return fmt.Errorf("%w: tmdb-%s: %w", ErrTMDB, tmdbID, err)
	}
	if gctx.Err() != nil {
		log.Debug().Str("tmdb_id", tmdbID).Str("group_id", groupID).Msg("Episode group fallback budget exhausted")
		return nil
	}
	log.Warn().Str("tmdb_id", tmdbID).Str("group_id", groupID).Err(err).Msg("TMDB episode group lookup failed")
	return nil
}

// trimGroupName trims whitespace and bounds a TMDB episode group name to
// maxEpisodeGroupName runes before storing or returning it.
func trimGroupName(name string) string {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) > maxEpisodeGroupName {
		return string(runes[:maxEpisodeGroupName])
	}
	return name
}

// selectEpisodeGroups deduplicates TVDB-aligned episode groups, ranks them by
// episode_count plausibility (plausible >= localTotal first, then unknown 0,
// then undersized), selects fairly across the 4 group types so no single type
// consumes the entire maxEpisodeGroups budget, and returns the chosen groups
// ordered by (plausibility tier, type priority).
func selectEpisodeGroups(groups []tmdb.EpisodeGroup, localTotal int) []tmdb.EpisodeGroup {
	var buckets [3][4][]tmdb.EpisodeGroup
	seen := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		prio := episodeGroupPriority(g.Type)
		if prio == 0 || g.ID == "" {
			continue
		}
		if _, dup := seen[g.ID]; dup {
			continue
		}
		seen[g.ID] = struct{}{}
		tier := countTier(g.EpisodeCount, localTotal)
		buckets[tier][prio-1] = append(buckets[tier][prio-1], g)
	}
	selected := pickAcrossBuckets(&buckets, maxEpisodeGroups)
	slices.SortStableFunc(selected, func(a, b tmdb.EpisodeGroup) int {
		if d := countTier(a.EpisodeCount, localTotal) - countTier(b.EpisodeCount, localTotal); d != 0 {
			return d
		}
		return episodeGroupPriority(a.Type) - episodeGroupPriority(b.Type)
	})
	return selected
}

func countTier(episodeCount, localTotal int) int {
	switch {
	case episodeCount >= localTotal:
		return 0
	case episodeCount == 0:
		return 1
	default:
		return 2
	}
}

func pickAcrossBuckets(buckets *[3][4][]tmdb.EpisodeGroup, limit int) []tmdb.EpisodeGroup {
	out := make([]tmdb.EpisodeGroup, 0, limit)
	for tier := range buckets {
		for progress := true; progress && len(out) < limit; {
			progress = false
			for p := range buckets[tier] {
				if len(buckets[tier][p]) == 0 || len(out) >= limit {
					continue
				}
				out = append(out, buckets[tier][p][0])
				buckets[tier][p] = buckets[tier][p][1:]
				progress = true
			}
		}
	}
	return out
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
