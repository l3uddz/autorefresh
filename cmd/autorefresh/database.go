package main

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/store"
)

// optimizeTimeout bounds PRAGMA optimize at a stop, so a lock that another
// process holds cannot hold up the stop.
const optimizeTimeout = 5 * time.Second

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

// finishStore updates the planner statistics, unless dryRun, and closes the
// database. It returns the error of closeStore.
func finishStore(st *store.Store, dryRun bool) error {
	if !dryRun {
		// The run context has ended by now.
		ctx, cancel := context.WithTimeout(context.Background(), optimizeTimeout)
		if err := st.Optimize(ctx); err != nil {
			log.Warn().Err(err).Msg("Updating database statistics failed")
		}
		cancel()
	}
	return closeStore(st)
}
