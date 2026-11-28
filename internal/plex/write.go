package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/l3uddz/autorefresh/internal/neterr"
)

// Outcome is what the answer of one write request says about the write.
type Outcome string

const (
	// OutcomeAccepted is a 2xx answer: Plex took the write.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRejected is an answer that says that Plex refused the write
	// before it took effect (400, 401, 403, 404, or 405), or a request that
	// was never built, so it never left autorefresh.
	OutcomeRejected Outcome = "rejected"
	// OutcomeUnknown is every other result: a transport error, a timeout, a
	// cancel, a 3xx, a 5xx, or another 4xx such as a proxy's 408 or 429.
	// Plex can have taken the write.
	OutcomeUnknown Outcome = "unknown"
)

// outcomeOf returns the outcome of a write that Plex answered with status.
func outcomeOf(status int) Outcome {
	switch {
	case status >= 200 && status < 300:
		return OutcomeAccepted
	case status == http.StatusBadRequest, status == http.StatusUnauthorized, status == http.StatusForbidden,
		status == http.StatusNotFound, status == http.StatusMethodNotAllowed:
		return OutcomeRejected
	default:
		return OutcomeUnknown
	}
}

// write sends one PUT to endpoint with the query and returns its outcome
// with the error. It follows no redirect, so one write is always one
// request: a 3xx answer is an error that is neither ErrNotFound nor
// ErrUnavailable.
func (c *Client) write(ctx context.Context, endpoint *url.URL, query url.Values) (Outcome, error) {
	res, err := c.roundTrip(ctx, c.writeClient(ctx), http.MethodPut, endpoint, query, "application/json")
	if err != nil {
		return OutcomeUnknown, err
	}
	defer neterr.DrainAndClose(res)
	outcome := outcomeOf(res.StatusCode)
	if err := statusError(res); err != nil {
		return outcome, fmt.Errorf("%s %s: %w", http.MethodPut, res.Request.URL.Path, err)
	}
	return outcome, nil
}

// writeClient returns a copy of the client of c that follows no redirect.
// It is built on each call, so a test that swaps c.http.Transport also
// changes the writes. When ctx ends at or before the timeout of the client,
// the copy has no timeout of its own, so the end of ctx ends the write and
// its error keeps context.DeadlineExceeded.
func (c *Client) writeClient(ctx context.Context) *http.Client {
	wc := *c.http
	wc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if deadline, ok := ctx.Deadline(); ok && wc.Timeout > 0 && time.Until(deadline) <= wc.Timeout {
		wc.Timeout = 0
	}
	return &wc
}
