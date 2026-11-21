package refresher

import (
	"context"
	"maps"
	"slices"

	"github.com/rs/zerolog/log"
)

// confirmEpisodeNumbers requires every locally represented regular season to
// match TMDB in full. A season that the local library lacks supplies no evidence.
func (r *Refresher) confirmEpisodeNumbers(ctx context.Context, run *serverRun, mc matchContext, tmdbID string) (int, error) {
	local, err := run.server.Plex.EpisodeNumbers(ctx, mc.it.RatingKey)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, season := range slices.Sorted(maps.Keys(local)) {
		catalog, err := r.opts.TMDB.SeasonEpisodes(ctx, tmdbID, season)
		if err != nil {
			return 0, r.confirmationLookupFailed(ctx, run, mc, tmdbID, err)
		}
		numbers := local[season]
		if !sameEpisodeNumbers(numbers, catalog) {
			itemEvent(log.Debug(), run.server.Name, mc.lib, mc.it).
				Int("season", season).Int("local_episode_count", len(numbers)).
				Int("tmdb_episode_count", len(catalog)).Msg("Episode numbers differ")
			return 0, nil
		}
		count += len(catalog)
	}
	return count, nil
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
