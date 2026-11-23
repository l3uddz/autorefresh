package plex

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const fixtureShow = "plex://show/000000000000000000000001"

// newProvider starts a server that stands in for the metadata provider.
func newProvider(t *testing.T, handle http.HandlerFunc) (*recorder, *Client) {
	t.Helper()
	rec, c := newServer(t, handle)
	c.provider = c.base
	return rec, c
}

func TestProviderSeries(t *testing.T) {
	rec, c := newProvider(t, serveFixture(t, "provider_show.json"))
	got, err := c.ProviderSeries(context.Background(), fixtureShow)
	if err != nil || got != (Series{TMDBID: "48213", Seasons: 1, Episodes: 10}) {
		t.Fatalf("ProviderSeries = %+v, %v", got, err)
	}
	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].URL.Path != "/library/metadata/000000000000000000000001" || reqs[0].URL.RawQuery != "" ||
		reqs[0].Header.Get("X-Plex-Token") != testToken || reqs[0].Header.Get("Accept") != "application/json" {
		t.Fatalf("requests = %v", reqs)
	}
}

// A series without counts or a usable TMDB ID leaves those fields empty.
func TestProviderSeriesWithoutCounts(t *testing.T) {
	_, c := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"guid":"` + fixtureShow + `","childCount":-1,
			"Guid":[{"id":"tmdb://12/../x"},{"id":"tvdb://5"}]}]}}`))
	})
	got, err := c.ProviderSeries(context.Background(), fixtureShow)
	if err != nil || got != (Series{}) {
		t.Fatalf("ProviderSeries = %+v, %v", got, err)
	}
}

func TestProviderSeriesNotFound(t *testing.T) {
	for name, handle := range map[string]http.HandlerFunc{
		"404":         func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"no metadata": func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"MediaContainer":{"size":0}}`)) },
		"another item": func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"guid":"plex://show/000000000000000000000002"}]}}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, c := newProvider(t, handle)
			if _, err := c.ProviderSeries(context.Background(), fixtureShow); !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
	}
}

// Only the GUID of a series reaches the provider, so a GUID cannot add a
// path or a query to the request.
func TestProviderSeriesRejectsOtherGUIDs(t *testing.T) {
	rec, c := newProvider(t, serveFixture(t, "provider_show.json"))
	for _, guid := range []string{
		"", "plex://movie/000000000000000000000001", "plex://show/../000000000000000000000001",
		"plex://show/000000000000000000000001?x=1", "local://1", "plex://show/00000000000000000000000G",
	} {
		if _, err := c.ProviderSeries(context.Background(), guid); !errors.Is(err, ErrInvalidGUID) {
			t.Errorf("%q: err = %v, want ErrInvalidGUID", guid, err)
		}
	}
	if reqs := rec.all(); len(reqs) != 0 {
		t.Fatalf("requests = %v, want none", reqs)
	}
}

// The client sends the token to the provider host, and New sets that host.
func TestNewUsesTheMetadataProvider(t *testing.T) {
	c := New("test", mustURL(t, "http://plex.invalid:32400"), testToken, testTimeout)
	if c.provider.String() != "https://metadata.provider.plex.tv/" {
		t.Fatalf("provider = %s", c.provider)
	}
}
