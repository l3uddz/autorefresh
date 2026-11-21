// Package tmdb is the client for the part of the TMDB API that autorefresh
// uses: finding the TMDB ID of a TVDB or IMDb ID. It sends the token only in
// a header, so the token never shows in a URL or an error.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrUnauthorized marks a 401 status, which means that TMDB rejected the token.
var ErrUnauthorized = errors.New("tmdb token rejected")

// externalSources maps a path ID source to the external_source value of TMDB.
var externalSources = map[string]string{"tvdb": "tvdb_id", "imdb": "imdb_id"}

// Client sends requests to TMDB.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// New returns a Client. token is the API Read Access Token. timeout limits
// each request, with the response body.
func New(token string, timeout time.Duration) *Client {
	return &Client{
		base:  &url.URL{Scheme: "https", Host: "api.themoviedb.org", Path: "/"},
		token: token,
		http:  &http.Client{Timeout: timeout},
	}
}

// Find returns the TMDB ID of the series (show true) or the movie with an
// external ID. source is "tvdb" or "imdb". ok is false unless TMDB has
// exactly one such entry.
func (c *Client) Find(ctx context.Context, source, id string, show bool) (tmdbID string, ok bool, err error) {
	external, known := externalSources[source]
	if !known {
		return "", false, fmt.Errorf("find: unknown source %q", source)
	}

	var resp struct {
		Movies []struct {
			ID int `json:"id"`
		} `json:"movie_results"`
		Series []struct {
			ID int `json:"id"`
		} `json:"tv_results"`
	}
	query := url.Values{"external_source": {external}}
	if err := c.get(ctx, []string{"3", "find", id}, query, &resp); err != nil {
		return "", false, err
	}

	entries := resp.Movies
	if show {
		entries = resp.Series
	}
	if len(entries) != 1 {
		return "", false, nil
	}
	return strconv.Itoa(entries[0].ID), true, nil
}

// Check asks TMDB whether it accepts the token.
func (c *Client) Check(ctx context.Context) error {
	return c.get(ctx, []string{"3", "authentication"}, nil, nil)
}

// get sends a GET request to the path segments below the base URL and
// decodes the JSON answer into out, if out is not nil. It escapes each
// segment, so a segment cannot add a level to the path. Errors name the
// path, never the token.
func (c *Client) get(ctx context.Context, segments []string, query url.Values, out any) error {
	escaped := make([]string, len(segments))
	for i, seg := range segments {
		escaped[i] = url.PathEscape(seg)
	}
	u := c.base.JoinPath(escaped...)
	u.RawQuery = query.Encode()
	path := u.Path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("GET %s: %d %s: %w", path, res.StatusCode, http.StatusText(res.StatusCode), ErrUnauthorized)
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return fmt.Errorf("GET %s: %d %s", path, res.StatusCode, http.StatusText(res.StatusCode))
	case out == nil:
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("GET %s: decoding response: %w", path, err)
	}
	return nil
}
