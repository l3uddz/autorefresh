package plex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// providerURL is the Plex metadata provider. It describes the plex:// GUID
// of a match candidate, which the server only names. Like the server, it
// takes the server token in the X-Plex-Token header.
var providerURL = url.URL{Scheme: "https", Host: "metadata.provider.plex.tv", Path: "/"}

// showGUID matches the plex:// GUID of a series and captures its ID.
var showGUID = regexp.MustCompile(`^plex://show/([0-9a-f]{24})$`)

// tmdbGUID matches the TMDB ID of an external ID, with at most 12 digits.
var tmdbGUID = regexp.MustCompile(`^tmdb://(\d{1,12})$`)

// ErrInvalidGUID marks a GUID that is not the plex:// GUID of a series.
var ErrInvalidGUID = errors.New("invalid plex show guid")

// Series is what the metadata provider tells about the series of a plex://
// GUID. Each field is empty when the provider does not give it.
type Series struct {
	TMDBID            string
	Seasons, Episodes int
}

// providerSeries holds the fields of a series that Series uses. The answer
// has keys that differ only in case, such as guid and Guid, or summary and
// Summary. Each declared field has its exact key, which wins over a
// case-insensitive match, and undeclared keys are skipped.
type providerSeries struct {
	GUID  string `json:"guid"`
	Guids []struct {
		ID string `json:"id"`
	} `json:"Guid"`
	ChildCount int `json:"childCount"`
	LeafCount  int `json:"leafCount"`
}

// ProviderSeries reads the series of the plex:// GUID guid from the Plex
// metadata provider: its TMDB ID and its season and episode counts. A GUID
// that the provider does not know returns ErrNotFound. Other errors follow
// the requests to the server.
func (c *Client) ProviderSeries(ctx context.Context, guid string) (Series, error) {
	m := showGUID.FindStringSubmatch(guid)
	if m == nil {
		return Series{}, fmt.Errorf("provider series: %w", ErrInvalidGUID)
	}
	var resp struct {
		MediaContainer struct {
			Metadata []providerSeries `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	endpoint := c.provider.JoinPath("library", "metadata", m[1])
	if err := c.send(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return Series{}, fmt.Errorf("provider series: %w", err)
	}
	items := resp.MediaContainer.Metadata
	if len(items) == 0 || items[0].GUID != guid {
		return Series{}, fmt.Errorf("provider series: %w", ErrNotFound)
	}
	s := Series{Seasons: max(items[0].ChildCount, 0), Episodes: max(items[0].LeafCount, 0)}
	for _, g := range items[0].Guids {
		if id := tmdbGUID.FindStringSubmatch(strings.TrimSpace(g.ID)); id != nil {
			s.TMDBID = id[1]
			break
		}
	}
	return s, nil
}
