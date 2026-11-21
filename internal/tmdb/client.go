// Package tmdb is the client for the part of the TMDB API that autorefresh
// uses: finding the TMDB ID of a TVDB or IMDb ID. It sends the token only in
// a header, so the token never shows in a URL or an error.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ErrUnauthorized marks a 401 status, which means that TMDB rejected the token.
var ErrUnauthorized = errors.New("tmdb token rejected")

// maxDrainBytes bounds unread response body draining so normal responses
// reuse connections without letting a misbehaving server stall a request.
const maxDrainBytes = 64 << 10

type externalSource struct {
	param string
	valid *regexp.Regexp
}

// externalSources maps a path ID source to its TMDB external_source value
// and valid ID shape. Each ID becomes one path segment, so rejecting invalid
// values also prevents "." and ".." traversal in JoinPath.
var externalSources = map[string]externalSource{
	"tvdb": {param: "tvdb_id", valid: regexp.MustCompile(`^[0-9]+$`)},
	"imdb": {param: "imdb_id", valid: regexp.MustCompile(`(?i)^tt[0-9]+$`)},
}

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
		http:  &http.Client{Timeout: timeout, CheckRedirect: sameOrigin},
	}
}

// sameOrigin refuses a redirect to another origin so the bearer token stays
// pinned to the TMDB API origin (Go's default client still forwards
// Authorization across subdomains).
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return errors.New("redirect to another origin refused")
	}
	return nil
}

// Find returns the TMDB ID of the series (show true) or the movie with an
// external ID. source is "tvdb" or "imdb". ok is false unless TMDB has
// exactly one such entry with a positive ID.
func (c *Client) Find(ctx context.Context, source, id string, show bool) (tmdbID string, ok bool, err error) {
	ext, known := externalSources[source]
	if !known {
		return "", false, fmt.Errorf("find: unknown source %q", source)
	}
	if !ext.valid.MatchString(id) {
		return "", false, fmt.Errorf("find: invalid %s id", source)
	}

	var resp struct {
		Movies []struct {
			ID int `json:"id"`
		} `json:"movie_results"`
		Series []struct {
			ID int `json:"id"`
		} `json:"tv_results"`
	}
	query := url.Values{"external_source": {ext.param}}
	if err := c.get(ctx, []string{"3", "find", id}, query, &resp); err != nil {
		return "", false, err
	}

	entries := resp.Movies
	if show {
		entries = resp.Series
	}
	if len(entries) != 1 || entries[0].ID <= 0 {
		return "", false, nil
	}
	return strconv.Itoa(entries[0].ID), true, nil
}

// Check asks TMDB whether it accepts the token.
func (c *Client) Check(ctx context.Context) error {
	return c.get(ctx, []string{"3", "authentication"}, nil, nil)
}

func trace(req *http.Request, start time.Time) *zerolog.Event {
	return log.Trace().
		Str("method", req.Method).
		Str("path", req.URL.Path).
		Dur("duration", time.Since(start).Round(time.Millisecond))
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

	start := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		trace(req, start).Err(err).Msg("TMDB request")
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() {
		// Draining the body lets the connection get reused.
		io.Copy(io.Discard, io.LimitReader(res.Body, maxDrainBytes))
		res.Body.Close()
	}()

	trace(req, start).Int("status", res.StatusCode).Msg("TMDB request")

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
