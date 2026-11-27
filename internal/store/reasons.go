package store

import (
	"fmt"
	"strings"
)

// Reasons is the set of reasons why an item counts as incomplete.
type Reasons uint16

const (
	Unmatched Reasons = 1 << iota // local:// guid
	NoSummary
	NoPoster
	NoArt
	EpisodeNoSummary // regular episodes with an empty summary
	EpisodeUnmatched // matched series with unmatched regular episodes
	Mismatched       // a matched movie whose path IDs disagree with its Plex IDs
)

// ItemReasons holds the reasons that come from the item itself.
const ItemReasons = Unmatched | NoSummary | NoPoster | NoArt | Mismatched

// ManualReasons never make a row due: a refresh keeps the current match, so
// only a manual fix match helps.
const ManualReasons = Mismatched

var reasonNames = []struct {
	reason Reasons
	name   string
}{
	{Unmatched, "unmatched"},
	{NoSummary, "no-summary"},
	{NoPoster, "no-poster"},
	{NoArt, "no-art"},
	{EpisodeNoSummary, "episode-no-summary"},
	{EpisodeUnmatched, "episode-unmatched"},
	{Mismatched, "mismatched"},
}

// String gives "unmatched,no-summary,no-poster,no-art,episode-no-summary,episode-unmatched,mismatched"
// in this order, for the set flags only. 0 gives "".
func (r Reasons) String() string {
	var names []string
	for _, n := range reasonNames {
		if r&n.reason != 0 {
			names = append(names, n.name)
		}
	}
	return strings.Join(names, ",")
}

// ParseReasons returns the set of the reason names, such as "no-art". An
// unknown name is an error.
func ParseReasons(names []string) (Reasons, error) {
	var r Reasons
	for _, name := range names {
		found := false
		for _, n := range reasonNames {
			if n.name == name {
				r |= n.reason
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("unknown reason %q", name)
		}
	}
	return r, nil
}
