package main

import (
	"context"
	"testing"

	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

type healthLookup struct{ refresher.TMDB }

func (healthLookup) Find(_ context.Context, _, id string, _ bool) (string, bool, error) {
	if id == "tt2" {
		return "", false, tmdb.ErrSeveral
	}
	return "", false, tmdb.ErrUnauthorized
}

func (healthLookup) EpisodeGroups(context.Context, string) ([]tmdb.EpisodeGroup, error) {
	return nil, tmdb.ErrRateLimited
}

func (healthLookup) EpisodeGroupSeasons(context.Context, string) (map[int][]int, error) {
	return nil, nil
}

type tmdbHealthProbe struct{ reports []error }

func (p *tmdbHealthProbe) TMDBChecked(err error) { p.reports = append(p.reports, err) }

func TestTMDBUseReportsHealthUnlessCanceled(t *testing.T) {
	probe := &tmdbHealthProbe{}
	wrapped := tmdbHealth{tmdb: healthLookup{}, rec: probe}
	_, _, _ = wrapped.Find(t.Context(), "imdb", "tt123", false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, _ = wrapped.Find(ctx, "imdb", "tt123", false)
	if len(probe.reports) != 1 || probe.reports[0] != tmdb.ErrUnauthorized {
		t.Fatalf("reports = %v", probe.reports)
	}
}

// Several entries are an answer of TMDB, so TMDB is healthy, and the caller
// still gets ErrSeveral.
func TestTMDBSeveralEntriesReportHealthy(t *testing.T) {
	probe := &tmdbHealthProbe{}
	wrapped := tmdbHealth{tmdb: healthLookup{}, rec: probe}
	if _, _, err := wrapped.Find(t.Context(), "imdb", "tt2", true); err != tmdb.ErrSeveral {
		t.Fatalf("err = %v, want ErrSeveral", err)
	}
	if len(probe.reports) != 1 || probe.reports[0] != nil {
		t.Fatalf("reports = %v, want one healthy report", probe.reports)
	}
}

func TestTMDBEpisodeGroupsReportHealthUnlessCanceled(t *testing.T) {
	probe := &tmdbHealthProbe{}
	wrapped := tmdbHealth{tmdb: healthLookup{}, rec: probe}
	_, _ = wrapped.EpisodeGroups(t.Context(), "77")
	_, _ = wrapped.EpisodeGroupSeasons(t.Context(), "5e9077d2e640d600151f32bd")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _ = wrapped.EpisodeGroups(ctx, "77")
	_, _ = wrapped.EpisodeGroupSeasons(ctx, "5e9077d2e640d600151f32bd")
	if len(probe.reports) != 2 || probe.reports[0] != tmdb.ErrRateLimited || probe.reports[1] != nil {
		t.Fatalf("reports = %v, want [ErrRateLimited nil]", probe.reports)
	}
}
