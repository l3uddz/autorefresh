package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
)

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
