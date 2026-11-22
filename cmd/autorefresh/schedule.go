package main

import (
	"context"
	"sync"

	"github.com/robfig/cron/v3"
)

type scheduledStarter interface{ Scheduled(context.Context) }

// serve starts one run at once and then a run at each time of sched, one
// run at a time. If a time of sched comes before the first run starts, that
// run is the first run, and the first start is skipped. serve returns after
// ctx ends and the busy run returns.
func serve(ctx context.Context, r scheduledStarter, sched cron.Schedule) {
	c := cron.New()
	c.Schedule(sched, cron.FuncJob(func() { r.Scheduled(ctx) }))

	var first sync.WaitGroup
	first.Go(func() { r.Scheduled(ctx) })
	c.Start()

	<-ctx.Done()
	// Stop waits for a busy scheduled run to return.
	<-c.Stop().Done()
	first.Wait()
}
