// Package web serves the maintenance dashboard and its HTTP API.
package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// defaultForceWait is how long Stop waits for the active handlers after a
// failed Shutdown.
const defaultForceWait = 5 * time.Second

// Plex is the part of plex.Client that the web server uses.
type Plex interface {
	Identity(ctx context.Context) (string, error)
	ItemDetails(ctx context.Context, ratingKey string) (plex.Details, error)
	Photo(ctx context.Context, ratingKey, imagePath string, width, height int) ([]byte, error)
}

// Store is the part of store.Store that the web server uses.
type Store interface {
	RunItems(context.Context, int64) ([]store.RunItem, error)
	ListItems(ctx context.Context, q store.ListQuery) ([]store.ListItem, *store.Cursor, error)
	CountItems(ctx context.Context, q store.ListQuery) (store.ListCounts, error)
	Libraries(ctx context.Context, server string) ([]store.LibraryEntry, error)
	Runs(ctx context.Context, q store.RunQuery) ([]store.Run, error)
	ItemDetail(ctx context.Context, server, ratingKey string, rules store.DueRules) (store.ListItem, bool, error)
	TimelineContext(ctx context.Context, server, ratingKey string) ([]store.Event, error)
	FillPath(ctx context.Context, server, ratingKey, path string) (string, error)
}

// PlexServer is one configured Plex server.
type PlexServer struct {
	Name string
	Plex Plex
}

// Options configures a Server.
type Options struct {
	Jobs               Jobs
	Actions            Actions
	Username, Password string // both empty: no auth
	// Status is the live status that /api/status and /api/events show.
	// Without one, the server shows an empty status.
	Status *Status
	// Servers are the configured Plex servers. The API names each by its
	// index, srv.
	Servers []PlexServer
	Store   Store
	// Rules decide the queue state of the listed items. The server sets
	// Now for each request.
	Rules store.DueRules
	// Secrets are the values that the API replaces with "<redacted>" in
	// the stored error texts: Plex tokens, hosts, and path prefixes, the
	// TMDB token, and the UI password. Empty values are skipped.
	Secrets []string
	// Credentials are the Plex tokens, the TMDB token, and the UI
	// password. The API drops a candidate poster whose URL holds one.
	// Empty values are skipped.
	Credentials []string
}

// Server is the HTTP server of the dashboard.
type Server struct {
	jobs    Jobs
	actions Actions
	mux     *http.ServeMux
	handler http.Handler
	srv     *http.Server
	status  *Status

	servers  []PlexServer
	srvIndex map[string]int // the srv of each server name
	store    Store
	rules    store.DueRules
	now      func() time.Time
	secrets  []string
	// credentials are the values that no candidate poster URL may hold.
	credentials []string
	// liveTimeout bounds the Plex request of GET /api/item/live.
	liveTimeout time.Duration

	// idMu guards identities, the cached machine identifier of each server
	// ("": not read yet).
	idMu       sync.Mutex
	identities []string

	// sseCtx ends the event streams. Stop cancels it first, so that
	// Shutdown does not wait for open streams.
	sseCtx    context.Context
	cancelSSE context.CancelFunc
	// writeTimeout bounds each write to an event stream.
	writeTimeout time.Duration
	// keepalive is the interval of the keepalive comments of an idle
	// event stream.
	keepalive time.Duration

	// cancelBase cancels the base context of every request.
	cancelBase context.CancelFunc
	// forceWait bounds the wait for the active handlers after a failed
	// Shutdown.
	forceWait time.Duration
	// served is closed when Serve returns. It is nil before Start.
	served chan struct{}

	// mu guards closed and the calls of active.Add, so that no Add races
	// the Wait in Stop.
	mu     sync.Mutex
	closed bool
	active sync.WaitGroup
}

// New returns a Server with the routes of the dashboard.
func New(opts Options) *Server {
	status := opts.Status
	if status == nil {
		status = NewStatus(nil, false)
	}
	baseCtx, cancelBase := context.WithCancel(context.Background())
	sseCtx, cancelSSE := context.WithCancel(context.Background())
	srvIndex := make(map[string]int, len(opts.Servers))
	for i, ps := range opts.Servers {
		srvIndex[ps.Name] = i
	}
	s := &Server{
		mux:          http.NewServeMux(),
		jobs:         opts.Jobs,
		actions:      opts.Actions,
		status:       status,
		servers:      opts.Servers,
		srvIndex:     srvIndex,
		store:        opts.Store,
		rules:        opts.Rules,
		now:          time.Now,
		secrets:      opts.Secrets,
		credentials:  opts.Credentials,
		liveTimeout:  defaultLiveTimeout,
		identities:   make([]string, len(opts.Servers)),
		sseCtx:       sseCtx,
		cancelSSE:    cancelSSE,
		writeTimeout: defaultWriteTimeout,
		keepalive:    defaultKeepalive,
		cancelBase:   cancelBase,
		forceWait:    defaultForceWait,
	}
	s.mux.HandleFunc("GET /{$}", servePlaceholder)
	s.api("GET /api/status", s.serveStatus)
	s.api("GET /api/events", s.serveEvents)
	s.api("HEAD /api/events", serveEventsHead)
	s.api("GET /api/servers", s.serveServers)
	s.api("GET /api/items", s.serveItems)
	s.api("GET /api/item", s.serveItem)
	s.api("GET /api/item/live", s.serveItemLive)
	s.api("GET /api/items/thumb", s.serveThumb)
	s.api("GET /api/items/art", s.serveArt)
	s.api("GET /api/runs", s.serveRuns)
	s.api("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})

	s.api("POST /api/item/refresh", s.serveRefresh)
	s.api("POST /api/item/match", s.serveMatch)
	s.api("POST /api/item/check", s.serveCheck)
	s.api("POST /api/item/stop", s.serveStop)
	s.api("POST /api/item/resume", s.serveResume)
	s.api("GET /api/item/matches", s.serveMatches)
	s.api("POST /api/item/episode-check", s.serveEpisodeCheck)

	s.api("POST /api/runs", s.serveRunNow)
	s.api("POST /api/runs/cancel", s.serveCancelRun)
	s.api("GET /api/runs/items", s.serveRunItems)
	s.api("POST /api/items/refresh", s.serveBulkRefresh)
	s.api("POST /api/items/stop", s.serveBulkStop)
	s.api("POST /api/items/resume", s.serveBulkResume)

	s.handler = securityHeaders(s.admit(crossOriginProtection(basicAuth(opts.Username, opts.Password, s.mux))))
	s.srv = &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	return s
}

// Handler returns the handler of every request, with all middleware.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Start serves ln in a goroutine and logs a Serve error other than
// http.ErrServerClosed.
func (s *Server) Start(ln net.Listener) {
	s.served = make(chan struct{})
	go func() {
		defer close(s.served)
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("UI server failed")
		}
	}()
}

// Stop closes handler admission, so that every later request gets a 503,
// ends the event streams, and calls Shutdown(ctx). If Shutdown fails, it
// cancels the base context of every request, calls Close, and waits for the
// active handlers for at most forceWait. It always waits for Serve to return.
// It returns the Shutdown error. The budget is the deadline of ctx plus
// forceWait.
func (s *Server) Stop(ctx context.Context) error {
	s.closeAdmission()
	s.cancelSSE()
	err := s.srv.Shutdown(ctx)
	if err != nil {
		s.force()
	}
	if s.served != nil {
		<-s.served
	}
	return err
}

// force ends the requests that Shutdown did not end in time. Stop closed
// admission before, so no new handler starts.
func (s *Server) force() {
	s.cancelBase()
	// Close only reports the error of closing the listeners, which Shutdown
	// already closed.
	_ = s.srv.Close()
	s.waitActive(s.forceWait)
}

// waitActive waits until every admitted handler returned, or limit passed.
func (s *Server) waitActive(limit time.Duration) {
	done := make(chan struct{})
	go func() {
		s.active.Wait()
		close(done)
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

// admit counts each request as an active handler, and answers 503 once
// admission is closed.
func (s *Server) admit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.enter() {
			writeError(w, http.StatusServiceUnavailable, "shutting down")
			return
		}
		defer s.active.Done()
		next.ServeHTTP(w, r)
	})
}

// enter adds an active handler unless admission is closed.
func (s *Server) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.active.Add(1)
	return true
}

// closeAdmission makes every later request answer 503.
func (s *Server) closeAdmission() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// securityHeaders sets the security headers on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// crossOriginProtection rejects non-safe cross-origin browser requests with
// a JSON 403.
func crossOriginProtection(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
	}))
	return cop.Handler(next)
}
