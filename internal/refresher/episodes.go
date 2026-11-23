package refresher

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/store"
)

// confirmEpisodeNumbers requires every locally represented regular season to
// match TMDB in full. A season that the local library lacks supplies no
// evidence. It logs a season that differs in mc.d.
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
	if result.diff.Season > 0 {
		mc.d.evidence.EpisodeDiff = result.diff
		itemEvent(log.Debug(), run.server.Name, mc.lib, mc.it).Int("season", result.diff.Season).
			Int("local_episode_count", result.diff.Plex).Int("tmdb_episode_count", result.diff.TMDB).Msg("Episode numbers differ")
	}
	return result.count, nil
}

// episodeComparison is the result of compareEpisodes. count is the number
// of regular episodes when every local season matched. Otherwise diff is the
// first season that differs, and missing and extra are the episode numbers
// of that season that only TMDB, or only Plex, has. Both are zero when Plex
// lists no complete regular episodes.
type episodeComparison struct {
	count          int
	diff           store.EpisodeDiff
	missing, extra []int
}

// compareEpisodes compares the regular episode numbers of the series key in
// Plex with those of the TMDB ID id, season by season, and stops at the
// first season that differs.
func compareEpisodes(ctx context.Context, p Plex, t TMDB, key, id string) (episodeComparison, error) {
	local, err := p.EpisodeNumbers(ctx, key)
	if err != nil {
		return episodeComparison{}, err
	}
	count := 0
	for _, season := range slices.Sorted(maps.Keys(local)) {
		catalog, err := t.SeasonEpisodes(ctx, id, season)
		if err != nil {
			return episodeComparison{}, fmt.Errorf("%w: tmdb-%s: %w", ErrTMDB, id, err)
		}
		numbers := local[season]
		if !sameEpisodeNumbers(numbers, catalog) {
			return episodeComparison{
				diff:    store.EpisodeDiff{Season: season, Plex: len(numbers), TMDB: len(catalog)},
				missing: onlyIn(catalog, numbers), extra: onlyIn(numbers, catalog),
			}, nil
		}
		count += len(catalog)
	}
	return episodeComparison{count: count}, nil
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

func sameEpisodeNumbers(local, catalog []int) bool {
	if len(local) == 0 || len(local) != len(catalog) {
		return false
	}
	remaining := make(map[int]struct{}, len(local))
	for _, n := range local {
		remaining[n] = struct{}{}
	}
	for _, n := range catalog {
		if _, ok := remaining[n]; !ok || n <= 0 {
			return false
		}
		delete(remaining, n)
	}
	return true
}
