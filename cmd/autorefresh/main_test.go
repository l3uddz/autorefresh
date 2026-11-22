package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/store"
)

// runTarget starts a Plex server that answers each request with status and,
// for 200, with no library. onRequest gets the number of each request,
// from 1.
func runTarget(t *testing.T, status int, onRequest func(n int32)) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if onRequest != nil {
			onRequest(n.Add(1))
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			w.Write([]byte(`{"MediaContainer":{"Directory":[]}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runOptions writes a config with one Plex server at plexURL and returns
// options with that config and a database in a temporary directory.
func runOptions(t *testing.T, plexURL string) options {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yml")
	body := "schedule: \"0 0 1 1 *\"\nplex:\n  - name: test\n    url: " + plexURL + "\n    token: token\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return options{Config: cfg, Database: filepath.Join(dir, "autorefresh.db")}
}

// wantDatabase checks that the run left a database that another process
// can open.
func wantDatabase(t *testing.T, file string) {
	t.Helper()
	st, err := store.Open(file)
	if err != nil {
		t.Fatalf("open database after run: %v", err)
	}
	st.Close()
}

func TestRunOnceSucceeds(t *testing.T) {
	buf := captureLog(t)
	opts := runOptions(t, runTarget(t, http.StatusOK, nil).URL)
	opts.Once = true

	if err := run(context.Background(), func() {}, opts, "test"); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), `"message":"Run summary"`) {
		t.Errorf("no run summary in the log:\n%s", buf)
	}
	wantDatabase(t, opts.Database)
}

// With --once, a run that logs an error gives exit code 1, so run returns
// the error.
func TestRunOnceFailsWhenServerUnavailable(t *testing.T) {
	buf := captureLog(t)
	opts := runOptions(t, runTarget(t, http.StatusServiceUnavailable, nil).URL)
	opts.Once = true

	if err := run(context.Background(), func() {}, opts, "test"); err == nil {
		t.Fatal("run = nil, want the error of the unavailable server")
	}
	if !strings.Contains(buf.String(), `"message":"Run failed"`) {
		t.Errorf("no Run failed line in the log:\n%s", buf)
	}
	wantDatabase(t, opts.Database)
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	buf := captureLog(t)
	opts := runOptions(t, "http://127.0.0.1:1")
	if err := os.WriteFile(opts.Config, []byte("schedule: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run(context.Background(), func() {}, opts, "test"); err == nil {
		t.Fatal("run = nil, want a config error")
	}
	if !strings.Contains(buf.String(), `"message":"Failed decoding config"`) {
		t.Errorf("no config error in the log:\n%s", buf)
	}
	if _, err := os.Stat(opts.Database); err == nil {
		t.Error("run created the database despite the config error")
	}
}

// Without --once, run checks the token, starts the first run at once, and
// returns after a signal ends ctx. It then closes the database.
func TestRunStopsAfterSignal(t *testing.T) {
	buf := captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Request 1 is the token check, request 2 the first run.
	opts := runOptions(t, runTarget(t, http.StatusOK, func(n int32) {
		if n == 2 {
			cancel()
		}
	}).URL)

	done := make(chan error, 1)
	go func() { done <- run(ctx, cancel, opts, "test") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the signal")
	}

	logs := buf.String()
	for _, msg := range []string{"Plex server accepted the token", "Stopped"} {
		if !strings.Contains(logs, `"message":"`+msg+`"`) {
			t.Errorf("no %q line in the log:\n%s", msg, logs)
		}
	}
	wantDatabase(t, opts.Database)
}

// A signal while run waits for a missing config stops the process without
// an error, like a signal after the start.
func TestRunStopsDuringConfigWait(t *testing.T) {
	buf := captureLog(t)
	opts := runOptions(t, "http://127.0.0.1:1")
	if err := os.Remove(opts.Config); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() { done <- run(ctx, cancel, opts, "test") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the signal")
	}
	if logs := buf.String(); !strings.Contains(logs, `"message":"Stopped"`) || strings.Contains(logs, `"level":"fatal"`) {
		t.Errorf("want a Stopped line and no fatal line:\n%s", logs)
	}
}

// A signal during startup token checks stops before starting the schedule or
// first run.
func TestRunStopsDuringTokenCheck(t *testing.T) {
	buf := captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	opts := runOptions(t, runTarget(t, http.StatusOK, func(n int32) {
		calls.Store(n)
		cancel()
	}).URL)

	if err := run(ctx, cancel, opts, "test"); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want only the 1 token check", got)
	}
	if logs := buf.String(); !strings.Contains(logs, `"message":"Stopped"`) || strings.Contains(logs, `"message":"Run summary"`) {
		t.Errorf("want Stopped and no Run summary:\n%s", logs)
	}
}

// startRunning stores a run of server "test" that is still running.
func startRunning(t *testing.T, st *store.Store) {
	t.Helper()
	_, err := st.StartRun(store.RunStart{Server: "test", Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
}

// onlyRunStatus returns the status of the only stored run.
func onlyRunStatus(t *testing.T, st *store.Store) store.RunStatus {
	t.Helper()
	runs, err := st.Runs(context.Background(), store.RunQuery{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v, want 1", runs, err)
	}
	return runs[0].Status
}

func TestScheduledStartMarksInterruptedRuns(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts options
		want store.RunStatus
	}{
		{"scheduled", options{}, store.RunInterrupted},
		{"once", options{Once: true}, store.RunRunning},
		{"dry run", options{DryRun: true}, store.RunRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			st, err := store.Open(filepath.Join(t.TempDir(), "autorefresh.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			startRunning(t, st)

			if err := interruptRuns(st, tc.opts, time.Now()); err != nil {
				t.Fatal(err)
			}

			if got := onlyRunStatus(t, st); got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
			if logged := strings.Contains(buf.String(), `"message":"Marked interrupted runs"`); logged != (tc.want == store.RunInterrupted) {
				t.Errorf("Marked interrupted runs logged = %v:\n%s", logged, buf)
			}
		})
	}
}

// With --once, run leaves the running rows of another process alone.
func TestRunOnceKeepsRunningRows(t *testing.T) {
	captureLog(t)
	opts := runOptions(t, runTarget(t, http.StatusOK, nil).URL)
	opts.Once = true
	st, err := store.Open(opts.Database)
	if err != nil {
		t.Fatal(err)
	}
	startRunning(t, st)
	st.Close()

	if err := run(context.Background(), func() {}, opts, "test"); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}

	st, err = store.Open(opts.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runs, err := st.Runs(context.Background(), store.RunQuery{})
	if err != nil {
		t.Fatal(err)
	}
	running := 0
	for _, r := range runs {
		if r.Status == store.RunRunning {
			running++
		}
	}
	if running != 1 {
		t.Errorf("running rows = %d, want 1 (runs %+v)", running, runs)
	}
}
