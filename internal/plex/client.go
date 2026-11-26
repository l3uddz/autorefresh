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

	"github.com/l3uddz/autorefresh/internal/neterr"
)

var (
	// ErrUnavailable marks a 401, 429, 502, 503, or 504 status, a transport
	// error, and a timeout. The caller can retry later.
	ErrUnavailable = errors.New("plex server unavailable")
	// ErrNotFound marks a 404 status.
	ErrNotFound = errors.New("plex item not found")
	// ErrUnauthorized marks a 401 status, which means that the server
	// rejected the token. It also wraps ErrUnavailable.
	ErrUnauthorized = errors.New("plex token rejected")
)

// maxJSONBytes bounds JSON response decoding so a misconfigured proxy cannot
// exhaust memory. Tests may lower it.
var maxJSONBytes int64 = 32 << 20

// maxIdleConnsPerHost keeps connections pooled across dashboard poster bursts.
const maxIdleConnsPerHost = 16

// idPattern matches a rating key or a section ID. Each one is a single path
// segment, so it must not be "." or "..".
var idPattern = regexp.MustCompile(`^[0-9]+$`)

// Client sends requests to one Plex server.
type Client struct {
	name     string
	base     *url.URL
	provider *url.URL // the metadata provider; tests replace it
	token    string
	http     *http.Client
}

// New returns a Client. name is the server name from the configuration. It
// labels trace lines. base is the server URL, which can have a path. timeout
// limits each request, with the response body.
func New(name string, base *url.URL, token string, timeout time.Duration) *Client {
	b, provider := *base, providerURL
	if b.Path == "" {
		b.Path = "/" // the request paths then start with "/"
	}
	var transport http.RoundTripper
	if baseTr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr := baseTr.Clone()
		tr.MaxIdleConnsPerHost = maxIdleConnsPerHost
		transport = tr
	}
	return &Client{
		name:     name,
		base:     &b,
		provider: &provider,
		token:    token,
		http:     &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: neterr.SameOrigin},
	}
}

// endpoint returns the URL of the path segments below the base URL. It
// escapes each segment, so a segment cannot add a level to the path.
func (c *Client) endpoint(segments ...string) *url.URL {
	escaped := make([]string, len(segments))
	for i, seg := range segments {
		escaped[i] = url.PathEscape(seg)
	}
	return c.base.JoinPath(escaped...)
}

// metadataEndpoint checks ratingKey and returns the URL of the item, with
// the segments after it.
func (c *Client) metadataEndpoint(ratingKey string, segments ...string) (*url.URL, error) {
	if !idPattern.MatchString(ratingKey) {
		return nil, errors.New("invalid plex rating key")
	}
	return c.endpoint(append([]string{"library", "metadata", ratingKey}, segments...)...), nil
}

// ValidRatingKey reports whether key is a rating key, by the rule that every
// request of this package applies.
func ValidRatingKey(key string) bool { return idPattern.MatchString(key) }

// PathPrefix returns the path that every request path starts with, without a
// trailing "/". It is "" when the server URL has no path. It cleans the path
// as endpoint does.
func (c *Client) PathPrefix() string {
	return strings.TrimSuffix(c.endpoint().Path, "/")
}

// do sends a request to endpoint with the query. It checks the status. On a
// status error it drains and closes the body. Otherwise the caller reads the
// body, then drains and closes it.
func (c *Client) do(ctx context.Context, method string, endpoint *url.URL, query url.Values, accept string) (*http.Response, error) {
	u := *endpoint
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, errors.New("creating request: invalid server URL")
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", accept)

	start := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		return nil, c.transportError(ctx, req, start, err)
	}

	c.trace(req, start).Int("status", res.StatusCode).Msg("Plex request")

	if err := statusError(res); err != nil {
		neterr.DrainAndClose(res)
		return nil, fmt.Errorf("%s %s: %w", method, req.URL.Path, err)
	}
	return res, nil
}

// send sends a request to endpoint with the query. If out is not nil, it
// decodes the JSON response body into out.
func (c *Client) send(ctx context.Context, method string, endpoint *url.URL, query url.Values, out any) error {
	res, err := c.do(ctx, method, endpoint, query, "application/json")
	if err != nil {
		return err
	}

	if out == nil {
		neterr.DrainAndClose(res)
		return nil
	}
	lr := &io.LimitedReader{R: res.Body, N: maxJSONBytes + 1}
	err = json.NewDecoder(lr).Decode(out)
	if lr.N == 0 {
		res.Body.Close()
		return fmt.Errorf("%s %s: response too large", method, endpoint.Path)
	}
	neterr.DrainAndClose(res)
	if err != nil {
		return bodyError(ctx, method, endpoint.Path, err)
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
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %w: %w", status, ErrUnauthorized, ErrUnavailable)
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("%s: %w", status, ErrUnavailable)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", status, ErrNotFound)
	default:
		return errors.New(status)
	}
}

// bodyError wraps an error from decoding a response body. A context error,
// network error, or truncated body means that the server is unavailable, and
// its text can hold the server address, so the result keeps none of it.
func bodyError(ctx context.Context, method, path string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s %s: reading response failed: %w: %w", method, path, ErrUnavailable, ctxErr)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s %s: reading response failed: %w: %w", method, path, ErrUnavailable, context.DeadlineExceeded)
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%s %s: reading response failed: %w", method, path, ErrUnavailable)
	}
	return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
}

// readError wraps a raw body read error without exposing transport details.
func readError(ctx context.Context, method, path string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s %s: reading response failed: %w: %w", method, path, ErrUnavailable, ctxErr)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s %s: reading response failed: %w: %w", method, path, ErrUnavailable, context.DeadlineExceeded)
	}
	return fmt.Errorf("%s %s: reading response failed: %w", method, path, ErrUnavailable)
}

// trace starts the trace line of one request.
func (c *Client) trace(req *http.Request, start time.Time) *zerolog.Event {
	return log.Trace().
		Str("server", c.name).
		Str("method", req.Method).
		Str("path", req.URL.Path).
		Dur("duration", time.Since(start).Round(time.Millisecond))
}

// transportError traces a failed request and returns a fixed error. It keeps
// no text of err, because that text can hold the server address.
func (c *Client) transportError(ctx context.Context, req *http.Request, start time.Time, err error) error {
	reason := neterr.Reason(err)

	c.trace(req, start).Str("error", reason).Msg("Plex request")

	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s %s: %s: %w: %w", req.Method, req.URL.Path, reason, ErrUnavailable, ctxErr)
	}
	return fmt.Errorf("%s %s: %s: %w", req.Method, req.URL.Path, reason, ErrUnavailable)
}
