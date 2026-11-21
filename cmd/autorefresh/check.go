package main

import (
	"context"
	"errors"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

// checkTokens checks each Plex server and, if set, TMDB at the same time,
// so a wrong token or URL shows at the start and a slow Plex server does not
// delay the TMDB check. It only logs, because a service can still be starting.
func checkTokens(ctx context.Context, servers []refresher.Server, tmdbClient interface{ Check(context.Context) error }) {
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Go(func() { checkServer(ctx, srv) })
	}
	if tmdbClient != nil {
		wg.Go(func() { checkTMDB(ctx, tmdbClient) })
	}
	wg.Wait()
}

func checkServer(ctx context.Context, srv refresher.Server) {
	libraries, err := srv.Plex.Libraries(ctx)
	switch {
	case ctx.Err() != nil:
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
func checkTMDB(ctx context.Context, client interface{ Check(context.Context) error }) {
	err := client.Check(ctx)
	switch {
	case ctx.Err() != nil:
	case err == nil:
		log.Info().Msg("TMDB accepted the token")
	case errors.Is(err, tmdb.ErrUnauthorized):
		log.Error().Err(err).Msg("TMDB rejected the token")
	default:
		log.Warn().Err(err).Msg("Checking TMDB failed")
	}
}
