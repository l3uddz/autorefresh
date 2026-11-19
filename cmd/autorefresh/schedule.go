package main

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"
)

// runner starts one run at a time.
type runner struct {
	mu  sync.Mutex
	run func(context.Context) error
}

// start runs r.run. If a run is busy, it logs a warning and returns.
func (r *runner) start(ctx context.Context) {
	if !r.mu.TryLock() {
		log.Warn().Msg("Skipped scheduled start, a run is busy")
		return
	}
	// A deferred unlock also runs if r.run panics.
	defer r.mu.Unlock()

	if err := r.run(ctx); err != nil {
		log.Error().Err(err).Msg("Run failed")
	}
}
