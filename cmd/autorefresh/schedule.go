package main

import (
	"context"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

type scheduledStarter interface{ Scheduled(context.Context) }

// serve starts one run at once and then a run at each time of sched, one
// run at a time. If a time of sched comes before the first run starts, that
// run is the first run, and the first start is skipped. serve returns after
// ctx ends and the busy run returns. onNext, if not nil, gets the next time
// of the scheduler when it starts and at each of its times, also when the
// run is busy and skips that time.
func serve(ctx context.Context, r scheduledStarter, sched cron.Schedule, onNext func(time.Time)) {
	c := cron.New()
	var id cron.EntryID
	publish := func() {
		if onNext != nil {
			onNext(c.Entry(id).Next)
		}
	}
	id = c.Schedule(sched, cron.FuncJob(func() {
		publish()
		r.Scheduled(ctx)
	}))

	var first sync.WaitGroup
	first.Go(func() { r.Scheduled(ctx) })
	c.Start()
	publish()

	<-ctx.Done()
	// Stop waits for a busy scheduled run to return.
	<-c.Stop().Done()
	first.Wait()
}
