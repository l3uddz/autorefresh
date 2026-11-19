package store

import "strings"

// Reasons is the set of reasons why an item counts as incomplete.
type Reasons uint16

const (
	Unmatched Reasons = 1 << iota // local:// guid
	NoSummary
	NoPoster
	NoArt
	EpisodeNoSummary // regular episodes with an empty summary
	EpisodeUnmatched // matched series with unmatched regular episodes
)

// ItemReasons holds the reasons that come from the item itself.
const ItemReasons = Unmatched | NoSummary | NoPoster | NoArt

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
}

// String gives "unmatched,no-summary,no-poster,no-art,episode-no-summary,episode-unmatched"
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
