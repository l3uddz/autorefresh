// Package plex is the client for the Plex Media Server API that autorefresh
// uses. It keeps the token and the server address out of errors and logs.
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

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
		http:    &http.Client{Timeout: timeout},
	}
}

// checkRatingKey makes sure that a rating key cannot change the request path.
func checkRatingKey(ratingKey string) error {
	if !ratingKeyPattern.MatchString(ratingKey) {
		return errors.New("invalid plex rating key")
	}
	return nil
}

// send sends a request to endpoint, which is relative to the base URL. If out
// is not nil, it decodes the JSON response body into out.
func (c *Client) send(ctx context.Context, method, endpoint string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", withoutAddress(err))
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w: %w", method, req.URL.Path, withoutAddress(err), ErrUnavailable)
	}
	defer res.Body.Close()

	log.Trace().
		Str("server", c.name).
		Str("method", method).
		Str("path", req.URL.Path).
		Int("status", res.StatusCode).
		Dur("duration", time.Since(start)).
		Msg("Plex request")

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

// statusError returns nil for a 2xx response.
func statusError(res *http.Response) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}

	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("%s: %w", res.Status, ErrUnavailable)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", res.Status, ErrNotFound)
	default:
		return errors.New(res.Status)
	}
}

// bodyError wraps an error from decoding a response body. A timeout means
// that the server is unavailable.
func bodyError(method, path string, err error) error {
	err = withoutAddress(err)

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%s %s: decoding response: %w: %w", method, path, err, ErrUnavailable)
	}
	return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
}

// withoutAddress returns the cause of a *url.Error or *net.OpError. The error
// then omits the request URL and the server address.
func withoutAddress(err error) error {
	for {
		var urlErr *url.Error
		var opErr *net.OpError
		switch {
		case errors.As(err, &urlErr):
			err = urlErr.Err
		case errors.As(err, &opErr):
			err = opErr.Err
		default:
			return err
		}
	}
}
