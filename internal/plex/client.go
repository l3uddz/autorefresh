// Package plex is the client for the Plex Media Server API that autorefresh
// uses. It keeps the token and the server address out of errors and logs.
package plex

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"syscall"
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
	// ErrUnauthorized marks a 401 status, which means that the server
	// rejected the token. It also wraps ErrUnavailable.
	ErrUnauthorized = errors.New("plex token rejected")
)

// idPattern matches a rating key or a section ID. Each one is a single path
// segment, so it must not be "." or "..".
var idPattern = regexp.MustCompile(`^[0-9]+$`)

// Client sends requests to one Plex server.
type Client struct {
	name  string
	base  *url.URL
	token string
	http  *http.Client
}

// New returns a Client. name is the server name from the configuration. It
// labels trace lines. base is the server URL, which can have a path. timeout
// limits each request, with the response body.
func New(name string, base *url.URL, token string, timeout time.Duration) *Client {
	b := *base
	if b.Path == "" {
		b.Path = "/" // the request paths then start with "/"
	}
	return &Client{
		name:  name,
		base:  &b,
		token: token,
		http:  &http.Client{Timeout: timeout, CheckRedirect: sameOrigin},
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

// errRedirectRefused marks a redirect that sameOrigin refuses.
var errRedirectRefused = errors.New("redirect refused")

// sameOrigin refuses a redirect to another origin. Go forwards the
// X-Plex-Token header to the target of a redirect, so a cross-origin redirect
// would leak the token.
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects: %w", errRedirectRefused)
	}
	if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("redirect to another origin: %w", errRedirectRefused)
	}
	return nil
}

// maxDrainBytes bounds unread response body draining so normal responses
// reuse connections without letting a misbehaving server stall a request.
const maxDrainBytes = 64 << 10

// send sends a request to endpoint with the query. If out is not nil, it
// decodes the JSON response body into out.
func (c *Client) send(ctx context.Context, method string, endpoint *url.URL, query url.Values, out any) error {
	u := *endpoint
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return errors.New("creating request: invalid server URL")
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		return c.transportError(ctx, req, start, err)
	}
	defer func() {
		// Draining the body lets the connection get reused.
		io.Copy(io.Discard, io.LimitReader(res.Body, maxDrainBytes))
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
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %w: %w", status, ErrUnauthorized, ErrUnavailable)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
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
		Dur("duration", time.Since(start).Round(time.Millisecond))
}

// transportError traces a failed request and returns a fixed error. It keeps
// no text of err, because that text can hold the server address.
func (c *Client) transportError(ctx context.Context, req *http.Request, start time.Time, err error) error {
	reason := transportReason(err)

	c.trace(req, start).Str("error", reason).Msg("Plex request")

	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s %s: %s: %w: %w", req.Method, req.URL.Path, reason, ErrUnavailable, ctxErr)
	}
	return fmt.Errorf("%s %s: %s: %w", req.Method, req.URL.Path, reason, ErrUnavailable)
}

// transportReason returns a fixed text for the cause of a transport error.
// The text holds no address, host name, or resolver.
func transportReason(err error) string {
	var (
		netErr       net.Error
		dnsErr       *net.DNSError
		verifyErr    *tls.CertificateVerificationError
		authorityErr x509.UnknownAuthorityError
		hostnameErr  x509.HostnameError
		invalidErr   x509.CertificateInvalidError
	)
	switch {
	case errors.As(err, &netErr) && netErr.Timeout():
		return "request timed out"
	case errors.As(err, &dnsErr):
		return "host lookup failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &verifyErr), errors.As(err, &authorityErr),
		errors.As(err, &hostnameErr), errors.As(err, &invalidErr):
		return "certificate verification failed"
	case errors.Is(err, errRedirectRefused):
		return "redirect refused"
	default:
		return "request failed"
	}
}
