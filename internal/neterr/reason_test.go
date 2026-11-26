package neterr

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
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
