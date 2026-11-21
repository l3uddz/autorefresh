package tmdb

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
)

func TestMain(m *testing.M) {
	log.Logger = zerolog.Nop()
	os.Exit(m.Run())
}

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

const token = "secret-token"

// serve returns a client for a server that checks the token and answers each
// request with handler.
func serve(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q, want the bearer token", got)
		}
		if strings.Contains(r.URL.String(), token) {
			t.Errorf("URL %q holds the token", r.URL)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New(token, 5*time.Second)
	c.base = mustURL(t, srv.URL)
	return c
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

// file answers with a file of testdata.
func file(t *testing.T, name string) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

func TestFindSeries(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/find/81189" || r.URL.Query().Get("external_source") != "tvdb_id" {
			t.Errorf("request = %s, want /3/find/81189 with external_source=tvdb_id", r.URL)
		}
		file(t, "find_tv.json")(w, r)
	})

	id, ok, err := c.Find(context.Background(), "tvdb", "81189", true)
	if err != nil || !ok || id != "1396" {
		t.Fatalf("Find = %q, %v, %v, want 1396", id, ok, err)
	}
}

func TestFindMovieByIMDb(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/find/tt0133093" || r.URL.Query().Get("external_source") != "imdb_id" {
			t.Errorf("request = %s, want /3/find/tt0133093 with external_source=imdb_id", r.URL)
		}
		file(t, "find_movie.json")(w, r)
	})

	id, ok, err := c.Find(context.Background(), "imdb", "tt0133093", false)
	if err != nil || !ok || id != "603" {
		t.Fatalf("Find = %q, %v, %v, want 603", id, ok, err)
	}
}

func TestFindNothing(t *testing.T) {
	tests := []struct {
		name, file string
		show       bool
	}{
		{"no entry", "find_empty.json", true},
		{"two series", "find_two_tv.json", true},
		{"series for a movie", "find_tv.json", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := serve(t, file(t, tt.file))
			id, ok, err := c.Find(context.Background(), "tvdb", "81189", tt.show)
			if err != nil || ok || id != "" {
				t.Fatalf("Find = %q, %v, %v, want nothing", id, ok, err)
			}
		})
	}
}

func TestFindErrors(t *testing.T) {
	status := func(code int) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
	}
	tests := []struct {
		name         string
		handler      func(http.ResponseWriter, *http.Request)
		unauthorized bool
	}{
		{"401", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			file(t, "unauthorized.json")(w, r)
		}, true},
		{"404", status(http.StatusNotFound), false},
		{"429", status(http.StatusTooManyRequests), false},
		{"500", status(http.StatusInternalServerError), false},
		{"bad JSON", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("{")) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := serve(t, tt.handler)
			_, _, err := c.Find(context.Background(), "tvdb", "81189", true)
			checkError(t, err, tt.unauthorized)
		})
	}

	t.Run("closed server", func(t *testing.T) {
		c := serve(t, nil)
		c.base = mustURL(t, "http://127.0.0.1:1")
		_, _, err := c.Find(context.Background(), "tvdb", "81189", true)
		checkError(t, err, false)
	})
}

// checkError fails unless err is a TMDB error that the refresher cannot take
// for a Plex error, and that does not show the token.
func checkError(t *testing.T, err error, unauthorized bool) {
	t.Helper()
	switch {
	case err == nil:
		t.Fatal("err = nil, want an error")
	case errors.Is(err, ErrUnauthorized) != unauthorized:
		t.Errorf("errors.Is(%v, ErrUnauthorized) = %v, want %v", err, !unauthorized, unauthorized)
	case errors.Is(err, plex.ErrUnavailable), errors.Is(err, plex.ErrNotFound):
		t.Errorf("err %v wraps a Plex error", err)
	case strings.Contains(err.Error(), token):
		t.Errorf("err %v holds the token", err)
	}
}

func TestCheck(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// A request without a query has no "?".
		if r.RequestURI != "/3/authentication" {
			t.Errorf("request URI = %s, want /3/authentication", r.RequestURI)
		}
		w.Write([]byte(`{"success":true}`))
	})
	if err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}

	c = serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		file(t, "unauthorized.json")(w, r)
	})
	if err := c.Check(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Check = %v, want ErrUnauthorized", err)
	}
}

func TestFindRejectsInvalidID(t *testing.T) {
	var hits atomic.Int32
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) })
	tests := []struct {
		source, id string
	}{
		{"tvdb", ""},
		{"tvdb", ".."},
		{"tvdb", "1/2"},
		{"tvdb", "tt123"},
		{"imdb", ""},
		{"imdb", ".."},
		{"imdb", "12345"},
		{"imdb", "tt"},
		{"unknown", "123"},
	}
	for _, tt := range tests {
		t.Run(tt.source+"_"+tt.id, func(t *testing.T) {
			if _, _, err := c.Find(context.Background(), tt.source, tt.id, true); err == nil {
				t.Errorf("Find(%q, %q) returned no error", tt.source, tt.id)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("server received %d requests, want 0", n)
	}
}

func TestFindIgnoresNonPositiveID(t *testing.T) {
	for _, body := range []string{`{"tv_results":[{}]}`, `{"tv_results":[{"id":0}]}`, `{"tv_results":[{"id":-5}]}`} {
		t.Run(body, func(t *testing.T) {
			c := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
			id, ok, err := c.Find(context.Background(), "tvdb", "81189", true)
			if err != nil || ok || id != "" {
				t.Fatalf("Find = %q, %v, %v, want nothing", id, ok, err)
			}
		})
	}
}

func TestRefusesCrossOriginRedirect(t *testing.T) {
	var other atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other.Add(1) }))
	t.Cleanup(target.Close)
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	})

	err := c.Check(context.Background())
	if err == nil {
		t.Fatal("Check succeeded, want redirect error")
	}
	if other.Load() != 0 {
		t.Errorf("other origin received %d requests, want 0", other.Load())
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("error %q holds the token", err)
	}
}

func TestTraceLogsRequestsWithoutToken(t *testing.T) {
	buf := captureLog(t)
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"success":true}`))
	})

	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.base = mustURL(t, "http://127.0.0.1:1")
	_ = c.Check(context.Background())

	out := buf.String()
	if strings.Contains(out, token) {
		t.Fatalf("trace log holds the token: %s", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d trace lines, want 2: %s", len(lines), out)
	}
	for _, want := range []string{
		`"level":"trace"`,
		`"method":"GET","path":"/3/authentication"`,
	} {
		for i, line := range lines {
			if !strings.Contains(line, want) {
				t.Errorf("line %d missing %s: %s", i, want, line)
			}
		}
	}
	if !strings.Contains(lines[0], `"status":200`) {
		t.Errorf("line 0 = %s, want status 200", lines[0])
	}
	if !strings.Contains(lines[1], `"error":`) {
		t.Errorf("line 1 = %s, want error field", lines[1])
	}
}
