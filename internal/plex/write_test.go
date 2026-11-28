package plex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestWriteOutcomeOfEachStatus(t *testing.T) {
	tests := []struct {
		status int
		want   Outcome
	}{
		{http.StatusOK, OutcomeAccepted},
		{http.StatusNoContent, OutcomeAccepted},
		{http.StatusBadRequest, OutcomeRejected},
		{http.StatusUnauthorized, OutcomeRejected},
		{http.StatusForbidden, OutcomeRejected},
		{http.StatusNotFound, OutcomeRejected},
		{http.StatusMethodNotAllowed, OutcomeRejected},
		{http.StatusRequestTimeout, OutcomeUnknown},
		{http.StatusTooManyRequests, OutcomeUnknown},
		{http.StatusInternalServerError, OutcomeUnknown},
		{http.StatusServiceUnavailable, OutcomeUnknown},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			_, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) })

			refresh, refreshErr := c.Refresh(context.Background(), "1001")
			match, matchErr := c.Match(context.Background(), "1001", Candidate{GUID: "g", Name: "n"})
			if refresh != tt.want || match != tt.want {
				t.Errorf("outcomes = %s and %s, want %s", refresh, match, tt.want)
			}
			if (refreshErr == nil) != (tt.want == OutcomeAccepted) || (matchErr == nil) != (tt.want == OutcomeAccepted) {
				t.Errorf("errors = %v and %v, want an error unless accepted", refreshErr, matchErr)
			}
		})
	}
}

func TestWriteOutcomeWithoutAnswer(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
		t.Cleanup(srv.Close)
		t.Cleanup(func() { close(release) })
		c := New("test", mustURL(t, srv.URL), testToken, 50*time.Millisecond)

		outcome, err := c.Refresh(context.Background(), "1001")
		if outcome != OutcomeUnknown || !errors.Is(err, ErrUnavailable) {
			t.Errorf("outcome = %s, err = %v, want unknown and ErrUnavailable", outcome, err)
		}
	})

	t.Run("refused connection", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		c := New("test", mustURL(t, srv.URL), testToken, testTimeout)
		srv.Close()

		outcome, err := c.Refresh(context.Background(), "1001")
		if outcome != OutcomeUnknown || !errors.Is(err, ErrUnavailable) {
			t.Errorf("outcome = %s, err = %v, want unknown and ErrUnavailable", outcome, err)
		}
	})

	t.Run("invalid rating key", func(t *testing.T) {
		rec, c := newServer(t, func(http.ResponseWriter, *http.Request) {})

		outcome, err := c.Refresh(context.Background(), "../1")
		if outcome != OutcomeRejected || err == nil {
			t.Errorf("outcome = %s, err = %v, want rejected with an error", outcome, err)
		}
		if n := len(rec.all()); n != 0 {
			t.Errorf("the server got %d requests, want 0", n)
		}
	})
}

func TestWriteFollowsNoRedirect(t *testing.T) {
	for _, code := range []int{http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			rec, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/moved" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				http.Redirect(w, r, "/moved", code)
			})

			outcome, err := c.Refresh(context.Background(), "1001")
			if outcome != OutcomeUnknown || err == nil {
				t.Fatalf("outcome = %s, err = %v, want unknown with an error", outcome, err)
			}
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
				t.Errorf("err = %v, want neither ErrNotFound nor ErrUnavailable", err)
			}
			reqs := rec.all()
			if len(reqs) != 1 || reqs[0].Method != http.MethodPut {
				t.Errorf("the server got %d requests, want one PUT", len(reqs))
			}
		})
	}
}

func TestWriteKeepsTheDeadlineOfItsContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	// The deadline of the context and the timeout of the client end at the
	// same time, so the end of the context must decide the error.
	c := New("test", mustURL(t, srv.URL), testToken, 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	outcome, err := c.Match(ctx, "1001", Candidate{GUID: "g", Name: "n"})
	if outcome != OutcomeUnknown || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrUnavailable) {
		t.Errorf("outcome = %s, err = %v, want unknown with DeadlineExceeded and ErrUnavailable", outcome, err)
	}
}
