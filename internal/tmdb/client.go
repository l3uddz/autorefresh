// Package tmdb is the client for the part of the TMDB API that autorefresh
// uses to resolve external IDs and read release years and episode numbers.
// It sends the token only in a header.
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

	"github.com/l3uddz/autorefresh/internal/neterr"
)

// ErrUnauthorized marks a 401 status, which means that TMDB rejected the token.
var ErrUnauthorized = errors.New("tmdb token rejected")

// ErrRateLimited marks a 429 status from TMDB.
var ErrRateLimited = errors.New("tmdb rate limited")

// ErrSeveral marks a Find answer with several entries, so the external ID
// names no single TMDB entry. It is no failed request.
var ErrSeveral = errors.New("tmdb has several entries")

// errNotFound marks a 404 for a missing TMDB entry or season.
var errNotFound = errors.New("tmdb entry not found")

// maxDrainBytes bounds unread response body draining so normal responses
// reuse connections without letting a misbehaving server stall a request.
const maxDrainBytes = 64 << 10

const (
	// maxRateLimitRetries bounds how many times get retries an HTTP 429
	// response before returning ErrRateLimited.
	maxRateLimitRetries = 3
	// baseRateLimitWait is the initial backoff before retrying HTTP 429 when
	// TMDB sends no Retry-After header; each later retry doubles it.
	baseRateLimitWait = 500 * time.Millisecond
	// maxRateLimitWait caps any Retry-After or exponential backoff wait.
	maxRateLimitWait = 10 * time.Second
)

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

// tmdbIDPattern validates a numeric TMDB ID before it becomes a path segment.
var tmdbIDPattern = regexp.MustCompile(`^[0-9]+$`)

// episodeGroupIDPattern validates a hex TMDB episode group ID before it
// becomes a path segment.
var episodeGroupIDPattern = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// Episode group types returned by TMDB's /3/tv/{series_id}/episode_groups.
const (
	EpisodeGroupOriginalAirDate = 1
	EpisodeGroupAbsolute        = 2
	EpisodeGroupDVD             = 3
	EpisodeGroupDigital         = 4
	EpisodeGroupStoryArc        = 5
	EpisodeGroupProduction      = 6
	EpisodeGroupTV              = 7
)

// EpisodeGroup is one alternate episode ordering of a TMDB series.
type EpisodeGroup struct {
	ID           string
	Name         string
	Type         int
	EpisodeCount int
}

// Client sends requests to TMDB.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
	sleep func(context.Context, time.Duration) error
	now   func() time.Time
}

// New returns a Client. token is the API Read Access Token. timeout limits
// each request, with the response body.
func New(token string, timeout time.Duration) *Client {
	return NewWithBaseURL(&url.URL{Scheme: "https", Host: "api.themoviedb.org", Path: "/"}, token, timeout)
}

// NewWithBaseURL returns a Client pointed at base.
func NewWithBaseURL(base *url.URL, token string, timeout time.Duration) *Client {
	return &Client{
		base:  base,
		token: token,
		http: &http.Client{
			Timeout:       timeout,
			Transport:     traceTransport{base: http.DefaultTransport},
			CheckRedirect: sameOrigin,
		},
		sleep: sleepContext,
		now:   time.Now,
	}
}

// traceTransport logs one trace line for each HTTP round trip, including
// redirect hops. It logs only method, path, duration, and status or error.
type traceTransport struct {
	base http.RoundTripper
}

func (t traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	res, err := t.base.RoundTrip(req)
	if err != nil {
		trace(req, start).Err(err).Msg("TMDB request")
		return nil, err
	}
	trace(req, start).Int("status", res.StatusCode).Msg("TMDB request")
	return res, nil
}

// sameOrigin refuses a redirect to another origin so the bearer token stays
// pinned to the TMDB API origin (Go's default client still forwards
// Authorization across subdomains).
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects: %w", neterr.ErrRedirectRefused)
	}
	if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("redirect to another origin: %w", neterr.ErrRedirectRefused)
	}
	return nil
}

// Find returns the TMDB ID of the series (show true) or the movie with an
// external ID. source is "tvdb" or "imdb". ok is false unless TMDB has
// exactly one such entry with a positive ID. Several entries return
// ErrSeveral.
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
	if len(entries) > 1 {
		return "", false, ErrSeveral
	}
	if len(entries) == 0 || entries[0].ID <= 0 {
		return "", false, nil
	}
	return strconv.Itoa(entries[0].ID), true, nil
}

// Check asks TMDB whether it accepts the token.
func (c *Client) Check(ctx context.Context) error {
	return c.get(ctx, []string{"3", "authentication"}, nil, nil)
}

// Year returns the canonical release year of a TMDB entry: for a series
// (show true), the earliest air-date year among regular seasons
// (season_number >= 1, ignoring specials); for a movie, the earliest
// release-date year across all release dates. ok is false if TMDB has no
// valid date for the entry.
func (c *Client) Year(ctx context.Context, id string, show bool) (year int, ok bool, err error) {
	if !tmdbIDPattern.MatchString(id) {
		return 0, false, errors.New("year: invalid tmdb id")
	}
	if show {
		return c.seriesYear(ctx, id)
	}
	return c.movieYear(ctx, id)
}

func (c *Client) seriesYear(ctx context.Context, id string) (int, bool, error) {
	var resp struct {
		Seasons []struct {
			Number  int    `json:"season_number"`
			AirDate string `json:"air_date"`
		} `json:"seasons"`
	}
	if err := c.get(ctx, []string{"3", "tv", id}, nil, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	var earliest int
	for _, s := range resp.Seasons {
		if s.Number < 1 {
			continue
		}
		if y, ok := parseDateYear(s.AirDate); ok {
			earliest = earliestYear(earliest, y)
		}
	}
	return earliest, earliest > 0, nil
}

// SeasonEpisodes returns the catalog episode numbers for one regular season.
// A missing season returns no evidence.
func (c *Client) SeasonEpisodes(ctx context.Context, id string, season int) ([]int, error) {
	if !tmdbIDPattern.MatchString(id) || season <= 0 {
		return nil, errors.New("season episodes: invalid tmdb id or season")
	}
	var resp struct {
		Episodes []struct {
			Number int `json:"episode_number"`
		} `json:"episodes"`
	}
	if err := c.get(ctx, []string{"3", "tv", id, "season", strconv.Itoa(season)}, nil, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}
	numbers := make([]int, 0, len(resp.Episodes))
	for _, e := range resp.Episodes {
		numbers = append(numbers, e.Number)
	}
	return numbers, nil
}

// EpisodeGroups returns the alternate episode groups of a TMDB series.
// A missing series returns no groups.
func (c *Client) EpisodeGroups(ctx context.Context, id string) ([]EpisodeGroup, error) {
	if !tmdbIDPattern.MatchString(id) {
		return nil, errors.New("episode groups: invalid tmdb id")
	}
	var resp struct {
		Results []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Type         int    `json:"type"`
			EpisodeCount int    `json:"episode_count"`
		} `json:"results"`
	}
	if err := c.get(ctx, []string{"3", "tv", id, "episode_groups"}, nil, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}
	groups := make([]EpisodeGroup, 0, len(resp.Results))
	for _, g := range resp.Results {
		if !episodeGroupIDPattern.MatchString(g.ID) {
			continue
		}
		groups = append(groups, EpisodeGroup{
			ID:           g.ID,
			Name:         g.Name,
			Type:         g.Type,
			EpisodeCount: g.EpisodeCount,
		})
	}
	return groups, nil
}

// EpisodeGroupSeasons returns the regular season-to-episode-numbers map of
// one TMDB episode group. Each group with an explicit order >= 1 maps to that
// season number, and each episode's explicit 0-based order maps to episode
// number order+1. Missing or null order fields, negative or duplicate episode
// orders, or duplicate positive group orders return nil so incomplete payloads
// can never confirm a match.
func (c *Client) EpisodeGroupSeasons(ctx context.Context, groupID string) (map[int][]int, error) {
	if !episodeGroupIDPattern.MatchString(groupID) {
		return nil, errors.New("episode group: invalid group id")
	}
	var resp struct {
		Groups []episodeGroupSection `json:"groups"`
	}
	if err := c.get(ctx, []string{"3", "tv", "episode_group", groupID}, nil, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return parseEpisodeGroupSeasons(resp.Groups), nil
}

type episodeGroupSection struct {
	Order    *int `json:"order"`
	Episodes []struct {
		Order *int `json:"order"`
	} `json:"episodes"`
}

func parseEpisodeGroupSeasons(groups []episodeGroupSection) map[int][]int {
	seasons := make(map[int][]int, len(groups))
	for _, g := range groups {
		if g.Order == nil || *g.Order < 0 {
			return nil
		}
		if *g.Order == 0 {
			continue
		}
		if _, dup := seasons[*g.Order]; dup {
			return nil
		}
		nums, ok := parseGroupEpisodes(g.Episodes)
		if !ok {
			return nil
		}
		seasons[*g.Order] = nums
	}
	return seasons
}

func parseGroupEpisodes(episodes []struct {
	Order *int `json:"order"`
}) ([]int, bool) {
	nums := make([]int, 0, len(episodes))
	seen := make(map[int]struct{}, len(episodes))
	for _, ep := range episodes {
		if ep.Order == nil || *ep.Order < 0 {
			return nil, false
		}
		n := *ep.Order + 1
		if _, dup := seen[n]; dup {
			return nil, false
		}
		seen[n] = struct{}{}
		nums = append(nums, n)
	}
	return nums, true
}

func (c *Client) movieYear(ctx context.Context, id string) (int, bool, error) {
	var resp struct {
		Results []struct {
			ReleaseDates []struct {
				ReleaseDate string `json:"release_date"`
			} `json:"release_dates"`
		} `json:"results"`
	}
	if err := c.get(ctx, []string{"3", "movie", id, "release_dates"}, nil, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	var earliest int
	for _, country := range resp.Results {
		for _, rd := range country.ReleaseDates {
			if y, ok := parseDateYear(rd.ReleaseDate); ok {
				earliest = earliestYear(earliest, y)
			}
		}
	}
	return earliest, earliest > 0, nil
}

// parseDateYear reads the leading YYYY of a TMDB date such as "2022-12-05"
// or "1984-12-10T00:00:00.000Z".
func parseDateYear(date string) (int, bool) {
	if len(date) < 4 || (len(date) > 4 && date[4] != '-') {
		return 0, false
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil || y <= 0 {
		return 0, false
	}
	return y, true
}

func earliestYear(cur, y int) int {
	if cur == 0 || y < cur {
		return y
	}
	return cur
}

func trace(req *http.Request, start time.Time) *zerolog.Event {
	return log.Trace().
		Str("method", req.Method).
		Str("path", req.URL.Path).
		Dur("duration", time.Since(start).Round(time.Millisecond))
}

// get sends a GET request to the path segments below the base URL and
// decodes the JSON answer into out, if out is not nil. It escapes each
// segment, so a segment cannot add a level to the path. HTTP 429 responses
// are retried with bounded exponential backoff (honoring Retry-After up to
// maxRateLimitWait). Errors name the path, never the token.
func (c *Client) get(ctx context.Context, segments []string, query url.Values, out any) error {
	escaped := make([]string, len(segments))
	for i, seg := range segments {
		escaped[i] = url.PathEscape(seg)
	}
	u := c.base.JoinPath(escaped...)
	u.RawQuery = query.Encode()
	path := u.Path

	for attempt := 0; ; attempt++ {
		wait, retry, err := c.doGet(ctx, u.String(), path, attempt, out)
		if !retry {
			return err
		}
		log.Debug().Str("path", path).Int("attempt", attempt+1).Dur("wait", wait).Msg("TMDB rate limited, retrying")
		if err := c.sleep(ctx, wait); err != nil {
			return fmt.Errorf("GET %s: %w", path, err)
		}
	}
}

func (c *Client) doGet(ctx context.Context, rawURL, path string, attempt int, out any) (time.Duration, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, false, fmt.Errorf("GET %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return 0, false, formatTransportError(ctx, path, err)
	}
	defer func() {
		// Draining the body lets the connection get reused.
		io.Copy(io.Discard, io.LimitReader(res.Body, maxDrainBytes))
		res.Body.Close()
	}()

	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return 0, false, fmt.Errorf("GET %s: %d %s: %w", path, res.StatusCode, http.StatusText(res.StatusCode), ErrUnauthorized)
	case res.StatusCode == http.StatusTooManyRequests:
		if attempt < maxRateLimitRetries {
			fallback := min(baseRateLimitWait<<attempt, maxRateLimitWait)
			return parseRetryAfter(res.Header.Get("Retry-After"), c.now(), fallback), true, nil
		}
		return 0, false, fmt.Errorf("GET %s: %d %s: %w", path, res.StatusCode, http.StatusText(res.StatusCode), ErrRateLimited)
	case res.StatusCode == http.StatusNotFound:
		return 0, false, fmt.Errorf("GET %s: %d %s: %w", path, res.StatusCode, http.StatusText(res.StatusCode), errNotFound)
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return 0, false, fmt.Errorf("GET %s: %d %s", path, res.StatusCode, http.StatusText(res.StatusCode))
	case out == nil:
		return 0, false, nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return 0, false, fmt.Errorf("GET %s: decoding response: %w", path, err)
	}
	return 0, false, nil
}

func formatTransportError(ctx context.Context, path string, err error) error {
	reason := neterr.Reason(err)
	if ctx.Err() != nil {
		return fmt.Errorf("GET %s: %s: %w", path, reason, ctx.Err())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("GET %s: %s: %w", path, reason, context.DeadlineExceeded)
	}
	return fmt.Errorf("GET %s: %s", path, reason)
}

func parseRetryAfter(header string, now time.Time, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	if secs, err := strconv.ParseInt(header, 10, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		if secs >= int64(maxRateLimitWait/time.Second) {
			return maxRateLimitWait
		}
		return time.Duration(secs) * time.Second
	} else if errors.Is(err, strconv.ErrRange) {
		if header[0] == '-' {
			return 0
		}
		return maxRateLimitWait
	}
	if t, err := http.ParseTime(header); err == nil {
		d := t.Sub(now)
		if d <= 0 {
			return 0
		}
		return min(d, maxRateLimitWait)
	}
	return fallback
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
