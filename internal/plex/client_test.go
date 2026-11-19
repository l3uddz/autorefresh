package plex

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// TestMain silences the global logger. Tests that read it set their own.
func TestMain(m *testing.M) {
	log.Logger = zerolog.Nop()
	os.Exit(m.Run())
}

const testToken = "secret-token-value"

// fixture returns the bytes of a recorded response.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// recorder is a test server that records every request.
type recorder struct {
	mu       sync.Mutex
	requests []*http.Request
}

func (r *recorder) all() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*http.Request(nil), r.requests...)
}

// newServer starts a server that records each request and calls handle.
func newServer(t *testing.T, handle http.HandlerFunc) (*recorder, *Client) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.requests = append(rec.requests, r)
		rec.mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return rec, New("test", srv.URL, testToken)
}

// serveFixture answers each request with the named recorded response.
func serveFixture(t *testing.T, name string) http.HandlerFunc {
	body := fixture(t, name)
	return func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }
}

// captureLog sets the global logger to trace level with a buffer. The test
// restores the previous logger and level when it ends.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldLogger, oldLevel := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() {
		log.Logger = oldLogger
		zerolog.SetGlobalLevel(oldLevel)
	})
	return &buf
}

func TestRequestsSendTokenHeaderAndAcceptJSON(t *testing.T) {
	rec, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"1"}]}}`))
	})
	ctx := context.Background()

	c.Libraries(ctx)
	c.List(ctx, "1", TypeMovie)
	c.ItemPaths(ctx, "1001")
	c.Refresh(ctx, "1001")
	c.SearchMatches(ctx, "1001", "t", "a", "l")
	c.Match(ctx, "1001", Candidate{GUID: "g", Name: "n"})

	reqs := rec.all()
	if len(reqs) != 6 {
		t.Fatalf("got %d requests, want 6", len(reqs))
	}
	for _, r := range reqs {
		if got := r.Header.Get("X-Plex-Token"); got != testToken {
			t.Errorf("%s %s: token header = %q", r.Method, r.URL.Path, got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("%s %s: Accept = %q", r.Method, r.URL.Path, got)
		}
		if r.URL.Query().Has("X-Plex-Token") || strings.Contains(r.URL.RawQuery, testToken) {
			t.Errorf("%s %s: the URL carries the token", r.Method, r.URL.Path)
		}
	}
}

func TestRejectsInvalidRatingKey(t *testing.T) {
	rec, c := newServer(t, func(http.ResponseWriter, *http.Request) {})
	ctx := context.Background()

	for _, key := range []string{"", "../sections/1", "1/refresh", "12a"} {
		calls := map[string]error{
			"Refresh": c.Refresh(ctx, key),
			"Match":   c.Match(ctx, key, Candidate{GUID: "g", Name: "n"}),
		}
		_, calls["ItemPaths"] = c.ItemPaths(ctx, key)
		_, calls["SearchMatches"] = c.SearchMatches(ctx, key, "t", "a", "l")

		for name, err := range calls {
			if err == nil {
				t.Errorf("%s(%q) returned no error", name, key)
			}
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("the server got %d requests, want 0", n)
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		status      int
		unavailable bool
		notFound    bool
	}{
		{http.StatusUnauthorized, true, false},
		{http.StatusBadGateway, true, false},
		{http.StatusServiceUnavailable, true, false},
		{http.StatusGatewayTimeout, true, false},
		{http.StatusNotFound, false, true},
		{http.StatusInternalServerError, false, false},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			_, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) })

			err := c.Refresh(context.Background(), "1001")
			if err == nil {
				t.Fatal("no error")
			}
			if got := errors.Is(err, ErrUnavailable); got != tt.unavailable {
				t.Errorf("Is ErrUnavailable = %v, want %v", got, tt.unavailable)
			}
			if got := errors.Is(err, ErrNotFound); got != tt.notFound {
				t.Errorf("Is ErrNotFound = %v, want %v", got, tt.notFound)
			}
		})
	}

	t.Run("closed server", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		c := New("test", srv.URL, testToken)
		srv.Close()

		if err := c.Refresh(context.Background(), "1001"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("err = %v, want ErrUnavailable", err)
		}
	})
}

func TestErrorsAndTraceOmitSecrets(t *testing.T) {
	buf := captureLog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/library/metadata/2/refresh" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	host := strings.TrimPrefix(srv.URL, "http://")
	c := New("test", srv.URL, testToken)

	var errs []error
	errs = append(errs, c.Refresh(context.Background(), "1")) // succeeds
	errs = append(errs, c.Refresh(context.Background(), "2")) // fails with 503
	if errs[0] != nil || errs[1] == nil {
		t.Fatalf("errors = %v", errs)
	}
	srv.Close()
	errs = append(errs, c.Refresh(context.Background(), "3")) // transport error

	for _, err := range errs[1:] {
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, secret := range []string{testToken, host, srv.URL} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q contains %q", err, secret)
			}
		}
	}

	out := buf.String()
	for _, secret := range []string{testToken, host} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q: %s", secret, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d trace lines, want 2: %s", len(lines), out)
	}
	for i, want := range []string{
		`"method":"PUT","path":"/library/metadata/1/refresh","status":200,"duration":`,
		`"method":"PUT","path":"/library/metadata/2/refresh","status":503,"duration":`,
	} {
		if !strings.Contains(lines[i], want) || !strings.Contains(lines[i], `"level":"trace"`) {
			t.Errorf("line %d = %s, want %s", i, lines[i], want)
		}
	}
}
