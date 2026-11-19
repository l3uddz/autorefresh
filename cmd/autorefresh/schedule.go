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
	err := r.run(ctx)
	r.mu.Unlock()

	if err != nil {
		log.Error().Err(err).Msg("Run failed")
	}
}
