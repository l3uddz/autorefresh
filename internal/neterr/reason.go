// Package neterr classifies transport errors without exposing request URLs.
package neterr

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"
)

// ErrRedirectRefused marks a redirect rejected by the client's policy.
var ErrRedirectRefused = errors.New("redirect refused")

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
