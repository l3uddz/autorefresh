package refresher

import (
	"strings"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// itemReasons returns the item-level reasons of a movie or series.
func itemReasons(it plex.Item) store.Reasons {
	var r store.Reasons
	if strings.HasPrefix(it.GUID, "local://") {
		r |= store.Unmatched
	}
	if it.Summary == "" {
		r |= store.NoSummary
	}
	if it.Thumb == "" {
		r |= store.NoPoster
	}
	if it.Art == "" {
		r |= store.NoArt
	}
	return r
}
