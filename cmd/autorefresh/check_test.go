package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

const sectionsBody = `{"MediaContainer":{"Directory":[
	{"key":"1","title":"Movies","type":"movie","agent":"tv.plex.agents.movie","language":"en-US"},
	{"key":"2","title":"TV","type":"show","agent":"tv.plex.agents.series","language":"en-US"}
]}}`

// checkTarget returns a server that answers each request with status and,
// for 200, with two libraries.
func checkTarget(t *testing.T, name string, status int) refresher.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if status == http.StatusOK {
			w.Write([]byte(sectionsBody))
		}
	}))
	t.Cleanup(srv.Close)
	return refresher.Server{Name: name, Plex: plex.New(name, mustURL(t, srv.URL), "token", time.Second)}
}

// mustURL parses a test server URL.
func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// logEvents returns "<level> <message> <server>" for each logged line above
// trace level.
func logEvents(t *testing.T, buf *bytes.Buffer) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("decode log line %q: %v", sc.Text(), err)
		}
		if ev["level"] == "trace" {
			continue
		}
		got[ev["level"].(string)+" "+ev["message"].(string)+" "+ev["server"].(string)] = true
	}
	return got
}

func TestCheckServersLogsEachResult(t *testing.T) {
	buf := captureLog(t)
	closed := checkTarget(t, "closed", http.StatusOK)
	closed.Plex = plex.New("closed", mustURL(t, "http://127.0.0.1:1"), "token", time.Second)

	checkTokens(context.Background(), []refresher.Server{
		checkTarget(t, "ok", http.StatusOK),
		checkTarget(t, "bad-token", http.StatusUnauthorized),
		checkTarget(t, "down", http.StatusServiceUnavailable),
		closed,
	}, nil, nil)

	want := map[string]bool{
		"info Plex server accepted the token ok":         true,
		"error Plex server rejected the token bad-token": true,
		"warn Checking Plex server failed down":          true,
		"warn Checking Plex server failed closed":        true,
	}
	if got := logEvents(t, buf); !reflect.DeepEqual(got, want) {
		t.Errorf("log events:\n got %v\nwant %v", got, want)
	}
}

func TestCheckServersLogsNothingWhenCanceled(t *testing.T) {
	buf := captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	checkTokens(ctx, []refresher.Server{checkTarget(t, "ok", http.StatusOK)}, nil, nil)

	if got := logEvents(t, buf); len(got) != 0 {
		t.Errorf("logged %v, want nothing", got)
	}
}

// checkTokens runs the TMDB check at the same time as the Plex checks, so a
// slow Plex server does not block TMDB token verification.
func TestCheckTokensRunsTMDBInParallel(t *testing.T) {
	captureLog(t)
	tmdbDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-tmdbDone:
		case <-time.After(5 * time.Second):
			t.Error("TMDB check did not run while Plex check was in flight")
		}
		w.Write([]byte(sectionsBody))
	}))
	t.Cleanup(srv.Close)

	// Client timeout exceeds the handler's 5s wait so a sequential run hits
	// the handler timeout and fails instead of timing out the Plex call first.
	target := refresher.Server{Name: "slow", Plex: plex.New("slow", mustURL(t, srv.URL), "token", 10*time.Second)}
	checkTokens(context.Background(), []refresher.Server{target}, checkerFunc(func(context.Context) error {
		close(tmdbDone)
		return nil
	}), nil)
}

// checkerFunc is a TMDB token check.
type checkerFunc func(context.Context) error

func (f checkerFunc) Check(ctx context.Context) error { return f(ctx) }

func TestCheckTMDBLogsResult(t *testing.T) {
	tests := []struct {
		err   error
		level string
		msg   string
	}{
		{nil, "info", "TMDB accepted the token"},
		{fmt.Errorf("GET /3/authentication: %w", tmdb.ErrUnauthorized), "error", "TMDB rejected the token"},
		{errors.New("GET /3/authentication: 503 Service Unavailable"), "warn", "Checking TMDB failed"},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			buf := captureLog(t)
			checkTMDB(context.Background(), context.Background(), checkerFunc(func(context.Context) error { return tt.err }), nil)

			var ev map[string]any
			if err := json.Unmarshal(buf.Bytes(), &ev); err != nil {
				t.Fatalf("decode log %q: %v", buf, err)
			}
			if ev["level"] != tt.level || ev["message"] != tt.msg {
				t.Errorf("logged %v, want %s %q", ev, tt.level, tt.msg)
			}
		})
	}
}

func TestCheckTMDBLogsNothingWhenCanceled(t *testing.T) {
	buf := captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	checkTokens(ctx, nil, checkerFunc(func(ctx context.Context) error {
		called = true
		return ctx.Err()
	}), nil)

	if called {
		t.Error("TMDB checker was invoked after context cancellation")
	}
	if buf.Len() != 0 {
		t.Errorf("logged %s, want nothing", buf)
	}
}

// healthLog records the health checks that checkTokens reports.
type healthLog struct {
	mu      sync.Mutex
	servers map[string]error
	tmdb    []error
}

func newHealthLog() *healthLog { return &healthLog{servers: map[string]error{}} }

func (h *healthLog) ServerChecked(server string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.servers[server] = err
}

func (h *healthLog) TMDBChecked(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tmdb = append(h.tmdb, err)
}

func TestCheckTokensRecordsHealth(t *testing.T) {
	captureLog(t)
	rec := newHealthLog()
	tmdbErr := errors.New("GET /3/authentication: 503 Service Unavailable")

	checkTokens(context.Background(), []refresher.Server{
		checkTarget(t, "ok", http.StatusOK),
		checkTarget(t, "bad-token", http.StatusUnauthorized),
		checkTarget(t, "down", http.StatusServiceUnavailable),
	}, checkerFunc(func(context.Context) error { return tmdbErr }), rec)

	if len(rec.servers) != 3 {
		t.Fatalf("recorded servers %v, want 3", rec.servers)
	}
	if err := rec.servers["ok"]; err != nil {
		t.Errorf("ok = %v, want nil", err)
	}
	if err := rec.servers["bad-token"]; !errors.Is(err, plex.ErrUnauthorized) {
		t.Errorf("bad-token = %v, want ErrUnauthorized", err)
	}
	if err := rec.servers["down"]; err == nil || errors.Is(err, plex.ErrUnauthorized) {
		t.Errorf("down = %v, want an unavailable error", err)
	}
	if len(rec.tmdb) != 1 || rec.tmdb[0] != tmdbErr {
		t.Errorf("TMDB = %v, want [%v]", rec.tmdb, tmdbErr)
	}
}

// A check that ends because ctx ended shows nothing about the health.
func TestCheckTokensRecordsNothingWhenCanceled(t *testing.T) {
	captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.Write([]byte(sectionsBody))
	}))
	t.Cleanup(srv.Close)
	rec := newHealthLog()

	checkTokens(ctx, []refresher.Server{
		{Name: "canceled", Plex: plex.New("canceled", mustURL(t, srv.URL), "token", time.Second)},
	}, checkerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}), rec)

	if len(rec.servers) != 0 || len(rec.tmdb) != 0 {
		t.Errorf("recorded servers %v and TMDB %v, want nothing", rec.servers, rec.tmdb)
	}
}

func TestCheckTokensTimesOutSlowServer(t *testing.T) {
	buf := captureLog(t)
	old := checkTimeout
	checkTimeout = 50 * time.Millisecond
	t.Cleanup(func() { checkTimeout = old })

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	rec := newHealthLog()

	start := time.Now()
	checkTokens(context.Background(), []refresher.Server{
		{Name: "slow", Plex: plex.New("slow", mustURL(t, srv.URL), "token", 5*time.Minute)},
	}, nil, rec)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("checkTokens took %v, want about 50ms", took)
	}
	if err := rec.servers["slow"]; err == nil {
		t.Fatal("slow server recorded nil error, want timeout")
	}
	if got := logEvents(t, buf); !got["warn Checking Plex server failed slow"] {
		t.Errorf("log events = %v, want warn Checking Plex server failed slow", got)
	}
}
