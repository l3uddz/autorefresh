package refresher

import (
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestItemReasons(t *testing.T) {
	tests := []struct {
		name string
		item plex.Item
		want store.Reasons
	}{
		{"unmatched", plex.Item{GUID: "local://1001", Summary: "s", Thumb: "t", Art: "a"}, store.Unmatched},
		{"no summary", plex.Item{GUID: "plex://movie/1", Thumb: "t", Art: "a"}, store.NoSummary},
		{"no poster", plex.Item{GUID: "plex://movie/1", Summary: "s", Art: "a"}, store.NoPoster},
		{"no art", plex.Item{GUID: "plex://movie/1", Summary: "s", Thumb: "t"}, store.NoArt},
		{"complete", plex.Item{GUID: "plex://movie/1", Summary: "s", Thumb: "t", Art: "a"}, 0},
		{
			"unmatched series with nothing",
			plex.Item{GUID: "local://7"},
			store.Unmatched | store.NoSummary | store.NoPoster | store.NoArt,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := itemReasons(tt.item); got != tt.want {
				t.Errorf("itemReasons() = %v, want %v", got, tt.want)
			}
		})
	}
}
