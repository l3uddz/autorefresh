package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

// checkTimeout bounds startup token checks so one slow server or retrying TMDB
// request cannot delay the first scheduled run indefinitely. Tests may lower it.
var checkTimeout = 15 * time.Second

// healthRecorder receives the result of each start check.
type healthRecorder interface {
	ServerChecked(server string, err error)
	TMDBChecked(err error)
}

// checkTokens checks each Plex server and, if set, TMDB at the same time,
// so a wrong token or URL shows at the start and a slow Plex server does not
// delay the TMDB check. It only logs, because a service can still be starting.
// If rec is not nil, it also gets each result. A check that ends because ctx
// ended shows nothing, so it is neither logged nor recorded.
func checkTokens(ctx context.Context, servers []refresher.Server, tmdbClient interface{ Check(context.Context) error }, rec healthRecorder) {
	if ctx.Err() != nil {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Go(func() { checkServer(ctx, checkCtx, srv, rec) })
	}
	if tmdbClient != nil {
		wg.Go(func() { checkTMDB(ctx, checkCtx, tmdbClient, rec) })
	}
	wg.Wait()
}

func checkServer(parent, ctx context.Context, srv refresher.Server, rec healthRecorder) {
	if parent.Err() != nil {
		return
	}
	libraries, err := srv.Plex.Libraries(ctx)
	if parent.Err() != nil {
		return
	}
	if rec != nil {
		rec.ServerChecked(srv.Name, err)
	}
	switch {
	case err == nil:
		log.Info().Str("server", srv.Name).Int("libraries", len(libraries)).Msg("Plex server accepted the token")
	case errors.Is(err, plex.ErrUnauthorized):
		log.Error().Str("server", srv.Name).Err(err).Msg("Plex server rejected the token")
	default:
		log.Warn().Str("server", srv.Name).Err(err).Msg("Checking Plex server failed")
	}
}

// checkTMDB checks the TMDB token once, so a wrong token shows at the start.
// It only logs, because TMDB can be down for a short time.
func checkTMDB(parent, ctx context.Context, client interface{ Check(context.Context) error }, rec healthRecorder) {
	if parent.Err() != nil {
		return
	}
	err := client.Check(ctx)
	if parent.Err() != nil {
		return
	}
	if rec != nil {
		rec.TMDBChecked(err)
	}
	switch {
	case err == nil:
		log.Info().Msg("TMDB accepted the token")
	case errors.Is(err, tmdb.ErrUnauthorized):
		log.Error().Err(err).Msg("TMDB rejected the token")
	default:
		log.Warn().Err(err).Msg("Checking TMDB failed")
	}
}
