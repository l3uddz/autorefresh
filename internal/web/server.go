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
)

// defaultForceWait is how long Stop waits for the active handlers after a
// failed Shutdown.
const defaultForceWait = 5 * time.Second

// Options configures a Server.
type Options struct {
	Username, Password string // both empty: no auth
}

// Server is the HTTP server of the dashboard.
type Server struct {
	mux     *http.ServeMux
	handler http.Handler
	srv     *http.Server

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
	baseCtx, cancelBase := context.WithCancel(context.Background())
	s := &Server{
		mux:        http.NewServeMux(),
		cancelBase: cancelBase,
		forceWait:  defaultForceWait,
	}
	s.mux.HandleFunc("GET /{$}", servePlaceholder)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})

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

// Stop calls Shutdown(ctx). If Shutdown fails, it closes handler admission,
// cancels the base context of every request, calls Close, and waits for the
// active handlers for at most forceWait. It always waits for Serve to return.
// It returns the Shutdown error. The budget is the deadline of ctx plus
// forceWait.
func (s *Server) Stop(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if err != nil {
		s.force()
	}
	if s.served != nil {
		<-s.served
	}
	return err
}

// force ends the requests that Shutdown did not end in time.
func (s *Server) force() {
	s.closeAdmission()
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
