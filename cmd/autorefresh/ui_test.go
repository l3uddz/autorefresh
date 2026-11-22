package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/config"
	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

const noAuthWarning = "UI has no authentication, so allow access only through an authenticating reverse proxy or a trusted network"

// uiConfig returns a ui section that listens on listen.
func uiConfig(listen string) string {
	return "ui:\n  listen: \"" + listen + "\"\n"
}

// occupiedAddr returns a local address that a listener holds until the
// test ends.
func occupiedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

// recordedListener is a listener that records its Close.
type recordedListener struct {
	net.Listener
	closed atomic.Bool
}

func (l *recordedListener) Close() error {
	l.closed.Store(true)
	return l.Listener.Close()
}

// recordListener makes listenFunc listen on a free local port, whatever
// address the config names. The channel receives each listener.
func recordListener(t *testing.T) <-chan *recordedListener {
	t.Helper()
	listeners := make(chan *recordedListener, 1)
	original := listenFunc
	listenFunc = func(network, _ string) (net.Listener, error) {
		ln, err := net.Listen(network, "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		rl := &recordedListener{Listener: ln}
		listeners <- rl
		return rl, nil
	}
	t.Cleanup(func() { listenFunc = original })
	return listeners
}

// goRun calls run in a goroutine and returns the channel of its result.
// When the test ends, it cancels ctx and waits for run, so run never
// outlives the log capture and the listenFunc of the test.
func goRun(t *testing.T, ctx context.Context, cancel context.CancelFunc, opts options) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- run(ctx, cancel, opts, "test")
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	return done
}

// waitRun returns the result of run, or fails the test if run does not
// return within 10 seconds.
func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return")
		return nil
	}
}

// waitHealthy polls the status API at addr until it shows the server test
// with the health ok.
func waitHealthy(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	u := "http://" + addr + "/api/status"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if statusHealthy(client, u) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the status API did not show server test with health ok")
}

// statusHealthy reports whether the status API at u answers 200 with the
// server test in the health ok.
func statusHealthy(client *http.Client, u string) bool {
	resp, err := client.Get(u)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		Servers []struct {
			Name   string `json:"name"`
			Health string `json:"health"`
		} `json:"servers"`
	}
	err = json.NewDecoder(resp.Body).Decode(&body)
	io.Copy(io.Discard, resp.Body)
	return err == nil && resp.StatusCode == http.StatusOK && len(body.Servers) == 1 &&
		body.Servers[0].Name == "test" && body.Servers[0].Health == "ok"
}

// A port that is in use stops the start before any request to Plex.
func TestRunBindFailureStopsBeforePlex(t *testing.T) {
	buf := captureLog(t)
	var calls atomic.Int32
	target := runTarget(t, http.StatusOK, func(n int32) { calls.Store(n) })
	opts := runOptionsWith(t, target.URL, uiConfig(occupiedAddr(t)))
	ctx, cancel := context.WithCancel(context.Background())

	if err := waitRun(t, goRun(t, ctx, cancel, opts)); err == nil {
		t.Fatal("run = nil, want the bind error")
	}
	if !strings.Contains(buf.String(), `"message":"Failed starting UI listener"`) {
		t.Errorf("no bind error in the log:\n%s", buf)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("Plex requests = %d, want 0", got)
	}
	wantDatabase(t, opts.Database)
}

func TestRunOnceIgnoresUI(t *testing.T) {
	buf := captureLog(t)
	opts := runOptionsWith(t, runTarget(t, http.StatusOK, nil).URL, uiConfig(occupiedAddr(t)))
	opts.Once = true

	if err := run(context.Background(), func() {}, opts, "test"); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	logs := buf.String()
	if strings.Count(logs, `"message":"UI is off with --once or --dry-run"`) != 1 {
		t.Errorf("want one UI is off line:\n%s", logs)
	}
	if strings.Contains(logs, `"message":"UI listening"`) {
		t.Errorf("run started the UI:\n%s", logs)
	}
}

func TestRunDryRunIgnoresUI(t *testing.T) {
	buf := captureLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	// Request 1 is the token check, request 2 the first run.
	target := runTarget(t, http.StatusOK, func(n int32) {
		if n == 2 {
			cancel()
		}
	})
	opts := runOptionsWith(t, target.URL, uiConfig(occupiedAddr(t)))
	opts.DryRun = true

	if err := waitRun(t, goRun(t, ctx, cancel, opts)); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	logs := buf.String()
	if strings.Count(logs, `"message":"UI is off with --once or --dry-run"`) != 1 {
		t.Errorf("want one UI is off line:\n%s", logs)
	}
	for _, msg := range []string{"Failed starting UI listener", "UI listening"} {
		if strings.Contains(logs, `"message":"`+msg+`"`) {
			t.Errorf("unexpected %q line:\n%s", msg, logs)
		}
	}
}

// The UI serves the live status beside the schedule. A signal stops the UI,
// then the schedule, then closes the database.
func TestRunServesUIAndStops(t *testing.T) {
	buf := captureLog(t)
	listeners := recordListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	opts := runOptionsWith(t, runTarget(t, http.StatusOK, nil).URL, uiConfig("127.0.0.1:0"))
	done := goRun(t, ctx, cancel, opts)

	var ln *recordedListener
	select {
	case ln = <-listeners:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not bind the UI listener")
	}
	waitHealthy(t, ln.Addr().String())
	cancel()
	if err := waitRun(t, done); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}

	// The log is safe to read only after run returned.
	logs := buf.String()
	for _, msg := range []string{"UI listening", noAuthWarning, "Stopped"} {
		if !strings.Contains(logs, `"message":"`+msg+`"`) {
			t.Errorf("no %q line in the log:\n%s", msg, logs)
		}
	}
	if strings.Contains(logs, `"message":"Stopping UI failed"`) {
		t.Errorf("stopping the UI failed:\n%s", logs)
	}
	if !ln.closed.Load() {
		t.Error("the UI listener is still open")
	}
	wantDatabase(t, opts.Database)
}

// A signal during the token checks stops the UI, and the schedule never
// starts.
func TestRunStopsUIWhenCanceledDuringTokenChecks(t *testing.T) {
	buf := captureLog(t)
	listeners := recordListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	target := runTarget(t, http.StatusOK, func(n int32) {
		calls.Store(n)
		cancel()
	})
	opts := runOptionsWith(t, target.URL, uiConfig("127.0.0.1:0"))

	if err := waitRun(t, goRun(t, ctx, cancel, opts)); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	select {
	case ln := <-listeners:
		if !ln.closed.Load() {
			t.Error("the UI listener is still open")
		}
	default:
		t.Fatal("run did not bind the UI listener")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("Plex requests = %d, want only the 1 token check", got)
	}
	if logs := buf.String(); !strings.Contains(logs, `"message":"Stopped"`) || strings.Contains(logs, `"message":"Run summary"`) {
		t.Errorf("want Stopped and no Run summary:\n%s", logs)
	}
	wantDatabase(t, opts.Database)
}

// The run API redacts the path prefix of a Plex URL from a stored error
// text, also when the configured path has an empty segment.
func TestUISecretsRedactPrefixedPlexError(t *testing.T) {
	captureLog(t)
	target := runTarget(t, http.StatusInternalServerError, nil)
	cfg, err := config.Decode(strings.NewReader("plex:\n  - name: test\n    url: " + target.URL + "/gateway//sentinel-path/\n    token: token\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Plex[0]
	client := plex.New(s.Name, s.BaseURL, s.Token, s.RequestTimeout())
	_, plexErr := client.Libraries(context.Background())
	if plexErr == nil || !strings.Contains(plexErr.Error(), "sentinel-path") {
		t.Fatalf("Libraries error = %v, want one with the path prefix", plexErr)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "autorefresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := store.RunStart{Server: "test", Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: time.Now()}
	id, err := st.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.FinishRun(id, start, store.RunResult{FinishedAt: time.Now(), Status: store.RunFailed, Error: plexErr.Error()}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(newUIServer(cfg, queueRules(cfg), st, nil, []*plex.Client{client}).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/runs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Runs []struct {
			Error string `json:"error"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Runs) != 1 {
		t.Fatalf("runs = %+v, want 1", body.Runs)
	}
	if got := body.Runs[0].Error; !strings.Contains(got, "<redacted>") || strings.Contains(got, "sentinel-path") {
		t.Errorf("error = %q, want the path prefix redacted", got)
	}
}

// The poster check gets only the credentials, and the redaction gets them
// once with the Plex host and path prefix.
func TestUICredentialsAndSecrets(t *testing.T) {
	cfg, err := config.Decode(strings.NewReader("plex:\n  - name: test\n    url: http://plex:32400/gateway\n" +
		"    token: plex-token\ntmdb-token: tmdb-token\nui:\n  listen: \":0\"\n  username: u\n  password: ui-password\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Plex[0]
	client := plex.New(s.Name, s.BaseURL, s.Token, s.RequestTimeout())

	credentials := uiCredentials(cfg)
	if want := []string{"plex-token", "tmdb-token", "ui-password"}; !slices.Equal(credentials, want) {
		t.Errorf("credentials = %q, want %q", credentials, want)
	}
	secrets := uiSecrets(cfg, credentials, []*plex.Client{client})
	if want := []string{"plex-token", "tmdb-token", "ui-password", "plex:32400", "/gateway"}; !slices.Equal(secrets, want) {
		t.Errorf("secrets = %q, want %q", secrets, want)
	}
}
