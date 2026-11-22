package main

import (
	"context"

	"github.com/l3uddz/autorefresh/internal/refresher"
)

type tmdbHealth struct {
	tmdb refresher.TMDB
	rec  interface{ TMDBChecked(error) }
}

func (h tmdbHealth) Find(ctx context.Context, source, id string, show bool) (string, bool, error) {
	value, found, err := h.tmdb.Find(ctx, source, id, show)
	h.report(ctx, err)
	return value, found, err
}

func (h tmdbHealth) Year(ctx context.Context, id string, show bool) (int, bool, error) {
	year, found, err := h.tmdb.Year(ctx, id, show)
	h.report(ctx, err)
	return year, found, err
}

func (h tmdbHealth) SeasonEpisodes(ctx context.Context, id string, season int) ([]int, error) {
	episodes, err := h.tmdb.SeasonEpisodes(ctx, id, season)
	h.report(ctx, err)
	return episodes, err
}

func (h tmdbHealth) report(ctx context.Context, err error) {
	if ctx.Err() == nil {
		h.rec.TMDBChecked(err)
	}
}
