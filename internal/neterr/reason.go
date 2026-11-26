// Package neterr classifies transport errors without exposing request URLs
// and provides shared HTTP client helpers.
package neterr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
)

// ErrRedirectRefused marks a redirect rejected by the client's policy.
var ErrRedirectRefused = errors.New("redirect refused")

// maxDrainBytes bounds unread response body draining so normal responses
// reuse connections without letting a misbehaving server stall a request.
const maxDrainBytes = 64 << 10

// SameOrigin refuses a redirect to another origin or beyond 10 hops so
// authentication headers never leave the original host and scheme.
func SameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects: %w", ErrRedirectRefused)
	}
	if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("redirect to another origin: %w", ErrRedirectRefused)
	}
	return nil
}

// DrainAndClose reads a bounded rest of the body, which lets the connection
// get reused, and closes it.
func DrainAndClose(res *http.Response) {
	io.Copy(io.Discard, io.LimitReader(res.Body, maxDrainBytes))
	res.Body.Close()
}

// Reason returns a fixed text for the cause of a transport error.
// The text holds no address, host name, or resolver.
func Reason(err error) string {
	var (
		netErr       net.Error
		dnsErr       *net.DNSError
		verifyErr    *tls.CertificateVerificationError
		authorityErr x509.UnknownAuthorityError
		hostnameErr  x509.HostnameError
		invalidErr   x509.CertificateInvalidError
	)
	switch {
	case errors.Is(err, context.Canceled):
		return "request canceled"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "request timed out"
	case errors.As(err, &dnsErr):
		return "host lookup failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &verifyErr), errors.As(err, &authorityErr),
		errors.As(err, &hostnameErr), errors.As(err, &invalidErr):
		return "certificate verification failed"
	case errors.Is(err, ErrRedirectRefused):
		return "redirect refused"
	default:
		return "request failed"
	}
}
