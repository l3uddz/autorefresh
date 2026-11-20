package main

import (
	"context"
	"errors"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
)

// checkServers lists the libraries of each server once, at the same time,
// so a wrong token or URL shows at the start and not at the first scheduled
// run. It only logs, because Plex can still be starting.
func checkServers(ctx context.Context, servers []refresher.Server) {
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Go(func() { checkServer(ctx, srv) })
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
