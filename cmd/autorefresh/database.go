package main

import (
	"context"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/store"
)

// convertDatabase switches an older database to incremental auto-vacuum
// once. A dry run changes nothing in the file, so it skips the step. A
// failure only logs, and the next start tries again.
func convertDatabase(ctx context.Context, st *store.Store, dryRun bool) {
	if dryRun {
		return
	}
	c, err := st.ConvertAutoVacuum(ctx)
	switch {
	case err != nil:
		log.Warn().Err(err).Msg("Database conversion failed")
	case c.TooLarge:
		log.Info().Int64("bytes", c.Before).Msg("Database too large to convert")
	case c.Converted:
		log.Info().Int64("bytes_before", c.Before).Int64("bytes_after", c.After).Msg("Converted database")
	}
}
