package refresher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// Plex is the part of plex.Client that the refresher uses.
type Plex interface {
	Libraries(ctx context.Context) ([]plex.Library, error)
	List(ctx context.Context, section string, itemType int, filters ...plex.Filter) ([]plex.Item, error)
	ItemPaths(ctx context.Context, ratingKey string) ([]string, error)
	Refresh(ctx context.Context, ratingKey string) error
	SearchMatches(ctx context.Context, ratingKey, title, agent, language string) ([]plex.Candidate, error)
	Match(ctx context.Context, ratingKey string, cand plex.Candidate) error
}

// Store is the part of store.Store that the refresher uses.
type Store interface {
	Library(server, section string) (store.Library, error)
	ApplyRead(r store.Read) (fixed []store.Item, err error)
	Unseen(server, section string, seen []string) ([]store.Item, error)
	Due(server string, sections []string, now time.Time, minimumAge, refreshInterval time.Duration) ([]store.Item, error)
	CountIncomplete(server string, sections []string) (int, error)
	RecordAttempt(server, ratingKey string, at time.Time, refreshed bool) error
	Delete(server, ratingKey string) error
}

// Server is one Plex server to check.
type Server struct {
	Name      string
	Plex      Plex
	Libraries []string // titles; empty means all supported libraries
}

// Schedule is the part of cron.Schedule that the refresher uses.
type Schedule interface{ Next(time.Time) time.Time }

// Options holds the rules of a run.
type Options struct {
	MinimumAge, RefreshInterval, Oversample, RefreshDelay time.Duration
	CacheRefresh                                          Schedule
	MaxRefreshesPerRun                                    int // 0: no limit
	DryRun                                                bool
}

// Refresher checks the libraries of each server.
type Refresher struct {
	servers []Server
	store   Store
	opts    Options
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	warned  map[string]bool // the keys of the warnings that log once per process
}

// New returns a Refresher for the servers.
func New(servers []Server, st Store, opts Options) *Refresher {
	return &Refresher{
		servers: servers,
		store:   st,
		opts:    opts,
		now:     time.Now,
		sleep:   sleep,
		warned:  map[string]bool{},
	}
}

// sleep waits for d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// serverRun is the state of one server in one run.
type serverRun struct {
	server    Server
	libraries []plex.Library      // the selected libraries
	files     map[string][]string // movie files by rating key, from the reads of this run
	itemsRead int
	completed int // rows that became complete
}

// Run checks each server in order. It returns an error only for a store error.
func (r *Refresher) Run(ctx context.Context) error {
	for _, srv := range r.servers {
		run := &serverRun{server: srv, files: map[string][]string{}}
		err := r.readLibraries(ctx, run)
		if errors.Is(err, plex.ErrUnavailable) {
			log.Error().Str("server", srv.Name).Err(err).Msg("Plex server unavailable")
			continue
		}
		if err != nil {
			return fmt.Errorf("server %s: %w", srv.Name, err)
		}
	}
	return nil
}

// warnOnce reports whether the warning with key was not logged yet in this
// process, and marks it as logged.
func (r *Refresher) warnOnce(key string) bool {
	if r.warned[key] {
		return false
	}
	r.warned[key] = true
	return true
}
