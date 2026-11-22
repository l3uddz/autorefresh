package web

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// The defaults of the stream timing.
const (
	// defaultWriteTimeout bounds each write to an event stream.
	defaultWriteTimeout = 10 * time.Second
	// defaultKeepalive is the interval of the keepalive comments, which keep
	// proxies from closing an idle stream.
	defaultKeepalive = 25 * time.Second
)

// keepaliveComment is the comment that an idle stream sends.
var keepaliveComment = []byte(": keepalive\n\n")

// serveStatus answers GET /api/status with the live status.
func (s *Server) serveStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.status.Snapshot())
}

// serveEvents answers GET /api/events with a stream of server-sent events.
// It sends the status at once and after each change, and a keepalive
// comment when idle. It returns when Stop ends the streams, the request
// ends, or a write fails.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	if s.sseCtx.Err() != nil {
		return
	}
	changes, unsubscribe := s.status.subscribe()
	defer unsubscribe()

	setStreamHeaders(w.Header())
	rc := http.NewResponseController(w)
	if s.sendStatus(rc, w) != nil {
		return
	}

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
			err = s.sendStatus(rc, w)
		case <-keepalive.C:
			err = s.send(rc, w, keepaliveComment)
		}
		if err != nil {
			return
		}
	}
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

// sendStatus sends the live status as a status event.
func (s *Server) sendStatus(rc *http.ResponseController, w io.Writer) error {
	data, err := json.Marshal(s.status.Snapshot())
	if err != nil {
		return err
	}
	msg := make([]byte, 0, len(data)+len("event: status\ndata: \n\n"))
	msg = append(msg, "event: status\ndata: "...)
	msg = append(msg, data...)
	msg = append(msg, "\n\n"...)
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
