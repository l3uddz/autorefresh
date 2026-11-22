package refresher

import (
	"time"

	"github.com/l3uddz/autorefresh/internal/store"
)

// Progress receives the live state of each server run. Servers run in parallel, so an
// implementation must be safe for concurrent use, and it must not block.
type Progress interface {
	RunStarted(server string, at time.Time)
	LibraryRead(server, library string) // the library title, after its read was applied
	Batch(server string, selected, due int)
	ItemStarted(server, ratingKey, title string)
	ItemDone(server, ratingKey, outcome string)
	RunFinished(server string, res store.RunResult, err error)
}

// The item outcomes that are not the outcome of a store.Request.
const (
	OutcomeGone   = "gone"    // Plex answered 404
	OutcomeDryRun = "dry-run" // a dry run sent nothing
)

// noProgress is the Progress of Options without one: it does nothing.
type noProgress struct{}

func (noProgress) RunStarted(string, time.Time)               {}
func (noProgress) LibraryRead(string, string)                 {}
func (noProgress) Batch(string, int, int)                     {}
func (noProgress) ItemStarted(string, string, string)         {}
func (noProgress) ItemDone(string, string, string)            {}
func (noProgress) RunFinished(string, store.RunResult, error) {}

// progress returns the Progress of the options, or noProgress without one,
// so each caller reports without a check.
func (r *Refresher) progress() Progress {
	if r.opts.Progress == nil {
		return noProgress{}
	}
	return r.opts.Progress
}
