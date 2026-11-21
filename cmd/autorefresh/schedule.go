package main

import (
	"context"
	"sync"

	"github.com/robfig/cron/v3"
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

// serve starts one run at once and then a run at each time of sched, one
// run at a time. If a time of sched comes before the first run starts, that
// run is the first run, and the first start is skipped. serve returns after
// ctx ends and the busy run returns.
func serve(ctx context.Context, r *runner, sched cron.Schedule) {
	c := cron.New()
	c.Schedule(sched, cron.FuncJob(func() { r.start(ctx) }))

	var first sync.WaitGroup
	first.Go(func() { r.start(ctx) })
	c.Start()

	<-ctx.Done()
	// Stop waits for a busy scheduled run to return.
	<-c.Stop().Done()
	first.Wait()
}
