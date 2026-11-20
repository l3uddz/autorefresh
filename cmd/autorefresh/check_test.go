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
	"reflect"
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
	return refresher.Server{Name: name, Plex: plex.New(name, srv.URL, "token", time.Second)}
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
	closed.Plex = plex.New("closed", "http://127.0.0.1:1", "token", time.Second)

	checkServers(context.Background(), []refresher.Server{
		checkTarget(t, "ok", http.StatusOK),
		checkTarget(t, "bad-token", http.StatusUnauthorized),
		checkTarget(t, "down", http.StatusServiceUnavailable),
		closed,
	})

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

	checkServers(ctx, []refresher.Server{checkTarget(t, "ok", http.StatusOK)})

	if got := logEvents(t, buf); len(got) != 0 {
		t.Errorf("logged %v, want nothing", got)
	}
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
			checkTMDB(context.Background(), checkerFunc(func(context.Context) error { return tt.err }))

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

	checkTMDB(ctx, checkerFunc(func(ctx context.Context) error { return ctx.Err() }))

	if buf.Len() != 0 {
		t.Errorf("logged %s, want nothing", buf)
	}
}
