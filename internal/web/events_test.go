package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// openStream sends GET /api/events to base with a 2 s deadline and returns
// the response and a reader of its body.
func openStream(t *testing.T, base string) (*http.Response, *bufio.Reader) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body)
}

// readEvent reads the lines of one event or comment, up to its blank line.
func readEvent(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var lines []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read event: %v (read %q)", err, lines)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return strings.Join(lines, "\n")
		}
		lines = append(lines, line)
	}
}

// statusData decodes the data line of a status event.
func statusData(t *testing.T, event string) StatusView {
	t.Helper()
	data, ok := strings.CutPrefix(event, "event: status\ndata: ")
	if !ok {
		t.Fatalf("event = %q, want a status event", event)
	}
	var v StatusView
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

func TestEventsStreamSnapshots(t *testing.T) {
	st := NewStatus([]string{"a"}, false)
	ts := httptest.NewServer(New(Options{Status: st}).Handler())
	defer ts.Close()

	resp, br := openStream(t, ts.URL)
	defer func() { _ = resp.Body.Close() }()
	want := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
	}
	for name, value := range want {
		if got := resp.Header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}

	first := readEvent(t, br)
	if !strings.Contains(first, `"run":null`) {
		t.Errorf("first event = %q, want an idle run", first)
	}
	st.RunStarted("a", time.Now())
	if v := statusData(t, readEvent(t, br)); v.Servers[0].Run == nil {
		t.Errorf("event after RunStarted = %+v, want a run", v)
	}
}

func TestEventsHeadDoesNotSubscribe(t *testing.T) {
	st := NewStatus([]string{"a"}, false)
	ts := httptest.NewServer(New(Options{Status: st}).Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HEAD /api/events: %v", err)
	}
	_ = resp.Body.Close()

	st.mu.Lock()
	subscribers := len(st.subs)
	st.mu.Unlock()
	if subscribers != 0 {
		t.Errorf("subscribers after HEAD = %d, want 0", subscribers)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	for name, value := range map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-cache"} {
		if got := resp.Header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestEventsKeepalive(t *testing.T) {
	s := New(Options{})
	s.keepalive = 20 * time.Millisecond
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, br := openStream(t, ts.URL)
	defer func() { _ = resp.Body.Close() }()
	statusData(t, readEvent(t, br))
	if got := readEvent(t, br); got != ": keepalive" {
		t.Errorf("event = %q, want a keepalive", got)
	}
}

func TestEventsKeepaliveAfterLongIdle(t *testing.T) {
	s := New(Options{})
	s.writeTimeout = 20 * time.Millisecond
	s.keepalive = 100 * time.Millisecond
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, br := openStream(t, ts.URL)
	defer func() { _ = resp.Body.Close() }()
	statusData(t, readEvent(t, br))
	if got := readEvent(t, br); got != ": keepalive" {
		t.Errorf("event = %q, want a keepalive", got)
	}
}

func TestStopEndsOpenStreams(t *testing.T) {
	s := New(Options{})
	base := startServer(t, s)
	_, br := openStream(t, base)
	statusData(t, readEvent(t, br))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}
	if line, err := br.ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Errorf("read after Stop = %q, %v, want EOF", line, err)
	}
}

// fakeStream is a ResponseWriter with the methods that
// http.ResponseController uses. It records its calls in order.
type fakeStream struct {
	header   http.Header
	flushErr error
	// stall makes Write block until the given deadline passes, then fail.
	// Without a deadline, Write blocks until release is closed.
	stall   bool
	release chan struct{}
	// cleared receives a signal after each SetWriteDeadline(time.Time{}).
	cleared chan struct{}

	mu       sync.Mutex
	calls    []string
	deadline time.Time
}

func newFakeStream(t *testing.T) *fakeStream {
	f := &fakeStream{
		header:  http.Header{},
		release: make(chan struct{}),
		cleared: make(chan struct{}, 16),
	}
	t.Cleanup(func() { close(f.release) })
	return f
}

func (f *fakeStream) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeStream) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeStream) Header() http.Header { return f.header }

func (f *fakeStream) WriteHeader(int) {}

func (f *fakeStream) Write(p []byte) (int, error) {
	f.record("Write")
	if !f.stall {
		return len(p), nil
	}
	f.mu.Lock()
	deadline := f.deadline
	f.mu.Unlock()
	if deadline.IsZero() {
		<-f.release
	} else {
		time.Sleep(time.Until(deadline))
	}
	return 0, errors.New("i/o timeout")
}

func (f *fakeStream) SetWriteDeadline(d time.Time) error {
	if d.IsZero() {
		f.record("SetWriteDeadline(zero)")
	} else {
		f.record("SetWriteDeadline(set)")
	}
	f.mu.Lock()
	f.deadline = d
	f.mu.Unlock()
	if d.IsZero() {
		f.cleared <- struct{}{}
	}
	return nil
}

func (f *fakeStream) FlushError() error {
	f.record("FlushError")
	return f.flushErr
}

// serveInBackground serves r to the handler of s with w and returns a
// channel that is closed when the handler returns.
func serveInBackground(s *Server, w http.ResponseWriter, r *http.Request) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(w, r)
	}()
	return done
}

// waitReturned fails the test unless done is closed within limit.
func waitReturned(t *testing.T, done <-chan struct{}, limit time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("handler did not return within %v", limit)
	}
}

func TestEventsStalledWriterEnds(t *testing.T) {
	s := New(Options{})
	s.writeTimeout = 50 * time.Millisecond
	f := newFakeStream(t)
	f.stall = true

	done := serveInBackground(s, f, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	waitReturned(t, done, time.Second)

	calls := f.recorded()
	if len(calls) < 2 || calls[0] != "SetWriteDeadline(set)" || calls[1] != "Write" {
		t.Errorf("calls = %q, want a deadline before the write", calls)
	}
}

func TestEventsFlushFailureEnds(t *testing.T) {
	s := New(Options{})
	f := newFakeStream(t)
	f.flushErr = errors.New("connection reset")

	done := serveInBackground(s, f, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	waitReturned(t, done, time.Second)

	calls := strings.Join(f.recorded(), " ")
	if !strings.Contains(calls, "Write") || !strings.Contains(calls, "FlushError") {
		t.Errorf("calls = %q, want a Write and a FlushError", calls)
	}
}

func TestEventsClearsDeadlineAfterFlush(t *testing.T) {
	s := New(Options{})
	f := newFakeStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/events", nil)

	done := serveInBackground(s, f, req)
	select {
	case <-f.cleared:
	case <-time.After(time.Second):
		t.Fatal("the handler did not clear the deadline")
	}
	cancel()
	waitReturned(t, done, time.Second)

	want := []string{"SetWriteDeadline(set)", "Write", "FlushError", "SetWriteDeadline(zero)"}
	if got := f.recorded(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestEventsAfterStopSendNothing(t *testing.T) {
	s := New(Options{})
	s.cancelSSE()
	f := newFakeStream(t)

	done := serveInBackground(s, f, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	waitReturned(t, done, time.Second)

	if calls := f.recorded(); len(calls) != 0 {
		t.Errorf("calls = %q, want none", calls)
	}
	if got := f.header.Get("Content-Type"); got == "text/event-stream" {
		t.Error("the handler started a stream after Stop")
	}
}
