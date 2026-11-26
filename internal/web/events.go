package web

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// The defaults of the stream timing and concurrency.
const (
	// defaultWriteTimeout bounds each write to an event stream.
	defaultWriteTimeout = 10 * time.Second
	// defaultKeepalive is the interval of the keepalive comments, which keep
	// proxies from closing an idle stream.
	defaultKeepalive = 25 * time.Second
	// defaultMaxSSEStreams caps concurrent /api/events streams.
	defaultMaxSSEStreams = 32
)

// keepaliveComment is the comment that an idle stream sends.
var keepaliveComment = []byte(": keepalive\n\n")

// serveStatus answers GET /api/status with the live status.
func (s *Server) serveStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.redactStatus(s.status.Snapshot()))
}

// serveEvents answers GET /api/events with a stream of server-sent events.
// It sends the status at once and after each change, and a keepalive
// comment when idle. It returns when Stop ends the streams, the request
// ends, or a write fails.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	if code, msg := s.reserveSSE(); code != 0 {
		writeError(w, code, msg)
		return
	}
	defer s.releaseSSE()

	changes, unsubscribe := s.status.subscribe()
	defer unsubscribe()

	initial, err := s.statusEvent()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if s.beforeSSECommit != nil {
		s.beforeSSECommit()
	}
	if !s.commitSSE(w) {
		writeError(w, http.StatusServiceUnavailable, "shutting down")
		return
	}

	rc := http.NewResponseController(w)
	if s.sseCtx.Err() != nil || s.send(rc, w, initial) != nil {
		return
	}
	s.streamEvents(w, r, rc, changes)
}

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, rc *http.ResponseController, changes <-chan struct{}) {
	keepalive := time.NewTicker(s.keepalive)
	defer keepalive.Stop()
	for {
		var err error
		select {
		case <-s.sseCtx.Done():
			return
		case <-r.Context().Done():
			return
		case <-changes:
			if s.sseCtx.Err() != nil {
				return
			}
			err = s.sendStatus(rc, w)
		case <-keepalive.C:
			if s.sseCtx.Err() != nil {
				return
			}
			err = s.send(rc, w, keepaliveComment)
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) stopSSE() {
	s.sseMu.Lock()
	s.sseClosing = true
	s.cancelSSE()
	s.sseMu.Unlock()
}

// reserveSSE checks shutdown and claims a stream slot before subscribing or
// marshaling an initial snapshot.
func (s *Server) reserveSSE() (int, string) {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	if s.sseClosing || s.sseCtx.Err() != nil {
		return http.StatusServiceUnavailable, "shutting down"
	}
	if s.sseStreams >= s.maxSSEStreams {
		return http.StatusServiceUnavailable, "too many event streams"
	}
	s.sseStreams++
	return 0, ""
}

// commitSSE serializes the initial 200 SSE response commitment against
// stopSSE so shutdown cannot race with committing headers.
func (s *Server) commitSSE(w http.ResponseWriter) bool {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	if s.sseClosing || s.sseCtx.Err() != nil {
		return false
	}
	setStreamHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
	return true
}

func (s *Server) releaseSSE() {
	s.sseMu.Lock()
	s.sseStreams--
	s.sseMu.Unlock()
}

// serveEventsHead answers HEAD /api/events with the headers of the stream
// and no body. It does not subscribe, so it returns at once. Without it,
// the GET route would also answer HEAD and keep the stream open.
func serveEventsHead(w http.ResponseWriter, _ *http.Request) {
	setStreamHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
}

// setStreamHeaders sets the headers of an event stream.
func setStreamHeaders(h http.Header) {
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
}

// statusEvent formats the current live status as one SSE status frame.
func (s *Server) statusEvent() ([]byte, error) {
	data, err := json.Marshal(s.redactStatus(s.status.Snapshot()))
	if err != nil {
		return nil, err
	}
	msg := make([]byte, 0, len(data)+len("event: status\ndata: \n\n"))
	msg = append(msg, "event: status\ndata: "...)
	msg = append(msg, data...)
	msg = append(msg, "\n\n"...)
	return msg, nil
}

// sendStatus sends the live status as a status event.
func (s *Server) sendStatus(rc *http.ResponseController, w io.Writer) error {
	msg, err := s.statusEvent()
	if err != nil {
		return err
	}
	return s.send(rc, w, msg)
}

// send writes msg and flushes it within writeTimeout. It clears the
// deadline afterwards, so no deadline is armed while the stream is idle.
func (s *Server) send(rc *http.ResponseController, w io.Writer, msg []byte) error {
	if err := rc.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := rc.Flush(); err != nil {
		return err
	}
	return rc.SetWriteDeadline(time.Time{})
}

// redactStatus removes configured secrets from the copied status view.
func (s *Server) redactStatus(v StatusView) StatusView {
	for _, server := range v.Servers {
		if server.Last != nil {
			server.Last.Error = s.redact(server.Last.Error)
		}
	}
	return v
}
