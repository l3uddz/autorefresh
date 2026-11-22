package refresher

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/rs/zerolog/log"
)

// confirmEpisodeNumbers requires every locally represented regular season to
// match TMDB in full. A season that the local library lacks supplies no evidence.
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
	if result.mismatch {
		itemEvent(log.Debug(), run.server.Name, mc.lib, mc.it).Int("season", result.season).
			Int("local_episode_count", result.localCount).Int("tmdb_episode_count", result.tmdbCount).Msg("Episode numbers differ")
	}
	return result.count, nil
}

type episodeComparison struct {
	count, season, localCount, tmdbCount int
	mismatch                             bool
}

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
			return episodeComparison{season: season, localCount: len(numbers), tmdbCount: len(catalog), mismatch: true}, nil
		}
		count += len(catalog)
	}
	return episodeComparison{count: count}, nil
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
