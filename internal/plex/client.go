// Package plex is the client for the Plex Media Server API that autorefresh
// uses. It keeps the token and the server address out of errors and logs.
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// ErrUnavailable marks a 401, 502, 503, or 504 status, a transport error,
	// and a timeout. The caller can retry later.
	ErrUnavailable = errors.New("plex server unavailable")
	// ErrNotFound marks a 404 status.
	ErrNotFound = errors.New("plex item not found")
)

// timeout limits each request.
const timeout = 30 * time.Second

var ratingKeyPattern = regexp.MustCompile(`^[0-9]+$`)

// Client sends requests to one Plex server.
type Client struct {
	name    string
	baseURL string
	token   string
	http    *http.Client
}

// New returns a Client. name is the server name from the configuration. It
// labels trace lines.
func New(name, baseURL, token string) *Client {
	return &Client{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: timeout, CheckRedirect: sameOrigin},
	}
}

// sameOrigin refuses a redirect to another origin. Go forwards the
// X-Plex-Token header to the target of a redirect, so a cross-origin redirect
// would leak the token.
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return errors.New("redirect to another origin")
	}
	return nil
}

// send sends a request to endpoint, which is relative to the base URL. If out
// is not nil, it decodes the JSON response body into out.
func (c *Client) send(ctx context.Context, method, endpoint string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, nil)
	if err != nil {
		return errors.New("creating request: invalid server URL")
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		return c.transportError(ctx, req, start, err)
	}
	defer func() {
		// Draining the body lets the connection get reused.
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}()

	c.trace(req, start).Int("status", res.StatusCode).Msg("Plex request")

	if err := statusError(res); err != nil {
		return fmt.Errorf("%s %s: %w", method, req.URL.Path, err)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return bodyError(method, req.URL.Path, err)
	}
	return nil
}

// statusError returns nil for a 2xx response. The message uses the standard
// reason phrase, because the phrase of the response can hold an address.
func statusError(res *http.Response) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}

	status := fmt.Sprintf("%d %s", res.StatusCode, http.StatusText(res.StatusCode))
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("%s: %w", status, ErrUnavailable)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", status, ErrNotFound)
	default:
		return errors.New(status)
	}
}

// bodyError wraps an error from decoding a response body. A network error or
// a body that ends early means that the server is unavailable, and its text can hold the server
// address, so the result keeps none of it.
func bodyError(method, path string, err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%s %s: reading response failed: %w", method, path, ErrUnavailable)
	}
	return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
}

// trace starts the trace line of one request.
func (c *Client) trace(req *http.Request, start time.Time) *zerolog.Event {
	return log.Trace().
		Str("server", c.name).
		Str("method", req.Method).
		Str("path", req.URL.Path).
		Dur("duration", time.Since(start))
}

// transportError traces a failed request and returns a fixed error. It keeps
// no text of err, because that text can hold the server address.
func (c *Client) transportError(ctx context.Context, req *http.Request, start time.Time, err error) error {
	reason := "request failed"
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		reason = "request timed out"
	}

	c.trace(req, start).Str("error", reason).Msg("Plex request")

	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s %s: %s: %w: %w", req.Method, req.URL.Path, reason, ErrUnavailable, ctxErr)
	}
	return fmt.Errorf("%s %s: %s: %w", req.Method, req.URL.Path, reason, ErrUnavailable)
}

// metadataPath checks ratingKey and returns the path of the item with suffix.
func metadataPath(ratingKey, suffix string) (string, error) {
	if !ratingKeyPattern.MatchString(ratingKey) {
		return "", errors.New("invalid plex rating key")
	}
	return "/library/metadata/" + ratingKey + suffix, nil
}
