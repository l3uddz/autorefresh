package main

import (
	"context"
	"testing"

	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

type healthLookup struct{ refresher.TMDB }

func (healthLookup) Find(context.Context, string, string, bool) (string, bool, error) {
	return "", false, tmdb.ErrUnauthorized
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
