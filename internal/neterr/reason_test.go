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
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestReasonCancel(t *testing.T) {
	if got := Reason(context.Canceled); got != "request canceled" {
		t.Errorf("Reason(context.Canceled) = %q, want %q", got, "request canceled")
	}
	if got := Reason(context.DeadlineExceeded); got != "request timed out" {
		t.Errorf("Reason(context.DeadlineExceeded) = %q, want %q", got, "request timed out")
	}
}

// transportError wraps err as net/http does for a failed request, with the
// host and the address in the outer errors.
func transportError(err error) error {
	opErr := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(192, 0, 2, 7), Port: 32400}, Err: err}
	return &url.Error{Op: "Get", URL: "https://plex.example.invalid:32400/library/sections", Err: opErr}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestReasonClassifiesTransportErrors(t *testing.T) {
	cert := &x509.Certificate{}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"wrapped cancel", fmt.Errorf("list sections: %w", context.Canceled), "request canceled"},
		{"network timeout", transportError(timeoutError{}), "request timed out"},
		{"host lookup", transportError(&net.DNSError{Err: "no such host", Name: "plex.example.invalid", Server: "192.0.2.53:53"}), "host lookup failed"},
		{"connection refused", transportError(os.NewSyscallError("connect", syscall.ECONNREFUSED)), "connection refused"},
		{"certificate verification", transportError(&tls.CertificateVerificationError{Err: errors.New("x509: expired")}), "certificate verification failed"},
		{"unknown authority", transportError(x509.UnknownAuthorityError{Cert: cert}), "certificate verification failed"},
		{"host name", transportError(x509.HostnameError{Certificate: cert, Host: "plex.example.invalid"}), "certificate verification failed"},
		{"invalid certificate", transportError(x509.CertificateInvalidError{Cert: cert, Reason: x509.Expired}), "certificate verification failed"},
		{"redirect refused", &url.Error{Op: "Get", URL: "https://plex.example.invalid/", Err: fmt.Errorf("redirect to another origin: %w", ErrRedirectRefused)}, "redirect refused"},
		{"other", transportError(errors.New("connection reset by peer")), "request failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Reason(tt.err)
			if got != tt.want {
				t.Errorf("Reason() = %q, want %q", got, tt.want)
			}
			for _, private := range []string{"plex.example.invalid", "192.0.2"} {
				if strings.Contains(got, private) {
					t.Errorf("Reason() = %q, holds %q", got, private)
				}
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	origin := &http.Request{URL: &url.URL{Scheme: "https", Host: "example.invalid:443", Path: "/a"}}
	same := &http.Request{URL: &url.URL{Scheme: "https", Host: "example.invalid:443", Path: "/b"}}
	otherHost := &http.Request{URL: &url.URL{Scheme: "https", Host: "other.invalid", Path: "/b"}}
	otherScheme := &http.Request{URL: &url.URL{Scheme: "http", Host: "example.invalid:443", Path: "/b"}}

	if err := SameOrigin(same, []*http.Request{origin}); err != nil {
		t.Errorf("same origin rejected: %v", err)
	}
	for _, req := range []*http.Request{otherHost, otherScheme} {
		if err := SameOrigin(req, []*http.Request{origin}); !errors.Is(err, ErrRedirectRefused) {
			t.Errorf("SameOrigin(%s) = %v, want ErrRedirectRefused", req.URL, err)
		}
	}
	tooMany := make([]*http.Request, 10)
	for i := range tooMany {
		tooMany[i] = origin
	}
	if err := SameOrigin(same, tooMany); !errors.Is(err, ErrRedirectRefused) {
		t.Errorf("SameOrigin(10 hops) = %v, want ErrRedirectRefused", err)
	}
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func TestDrainAndCloseBoundsRead(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader(strings.Repeat("x", maxDrainBytes+1024))}
	DrainAndClose(&http.Response{Body: body})
	if !body.closed {
		t.Fatal("body was not closed")
	}
	rem, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rem) != 1024 {
		t.Errorf("remaining unread bytes = %d, want 1024", len(rem))
	}
}
