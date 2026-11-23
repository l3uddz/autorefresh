package refresher

import (
	"time"

	"github.com/l3uddz/autorefresh/internal/store"
)

// Progress receives the live state of each server run. Servers run in parallel, so an
// implementation must be safe for concurrent use, and it must not block.
type Progress interface {
	RunStarted(id int64, start store.RunStart)
	LibraryRead(server, library string) // the library title, after its read was applied
	Batch(server string, selected, due int)
	// ItemNext reports the item that the run works on after the pace wait,
	// which ends at at. A run without a wait before an item does not call it.
	ItemNext(server string, item ProgressItem, at time.Time)
	ItemStarted(server string, item ProgressItem)
	// ItemDone reports a finished item with the request counts of the run
	// so far.
	ItemDone(server, ratingKey, outcome string, counts Counts)
	RunFinished(server string, res store.RunResult, err error, recorded bool)
}

// ProgressItem is an item of a run as the live status shows it. Thumb is
// the Plex image path, and Library the title of its library, "" if unknown.
type ProgressItem struct {
	RatingKey, Title, Type, Library, Thumb string
	Year                                   int
	Reasons                                store.Reasons
}

// progressItem returns the ProgressItem of the cached row it in the library
// with the title library.
func progressItem(it store.Item, library string) ProgressItem {
	return ProgressItem{
		RatingKey: it.RatingKey, Title: it.Title, Type: it.Type, Library: library,
		Thumb: it.Thumb, Year: it.Year, Reasons: it.Reasons,
	}
}

// Counts are the request counts of a run so far. They are the counts that
// the stored run gets (store.RunResult): Refreshed and Matched count the sent
// requests, also the failed ones and those that Plex answered with 404, and
// Failed counts the failed requests and match searches and each TMDB lookup
// failure that the fix match handled. Such a TMDB failure shows in the
// counts of the next ItemDone call.
type Counts struct{ Refreshed, Matched, Failed, Skipped int }

// The item outcomes that are not the outcome of a store.Request.
const (
	OutcomeGone    = "gone" // Plex answered 404
	OutcomeSkipped = "skipped"
	OutcomeDryRun  = "dry-run" // a dry run sent nothing
)

// noProgress is the Progress of Options without one: it does nothing.
type noProgress struct{}

func (noProgress) RunStarted(int64, store.RunStart)                 {}
func (noProgress) LibraryRead(string, string)                       {}
func (noProgress) Batch(string, int, int)                           {}
func (noProgress) ItemNext(string, ProgressItem, time.Time)         {}
func (noProgress) ItemStarted(string, ProgressItem)                 {}
func (noProgress) ItemDone(string, string, string, Counts)          {}
func (noProgress) RunFinished(string, store.RunResult, error, bool) {}

// progress returns the Progress of the options, or noProgress without one,
// so each caller reports without a check.
func (r *Refresher) progress() Progress {
	if r.opts.Progress == nil {
		return noProgress{}
	}
	return r.opts.Progress
}

// itemDone reports a finished item of run with the request counts of the
// run so far.
func (r *Refresher) itemDone(run *serverRun, ratingKey, outcome string) {
	r.progress().ItemDone(run.server.Name, ratingKey, outcome, run.counts())
}
