package store

import (
	"reflect"
	"testing"
)

func TestReasonsString(t *testing.T) {
	tests := []struct {
		in   Reasons
		want string
	}{
		{Unmatched | NoArt, "unmatched,no-art"},
		{0, ""},
		{Unmatched | NoSummary | NoPoster | NoArt | EpisodeNoSummary | EpisodeUnmatched,
			"unmatched,no-summary,no-poster,no-art,episode-no-summary,episode-unmatched"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("Reasons(%d).String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseReasons(t *testing.T) {
	got, err := ParseReasons([]string{"no-art", "episode-unmatched", "no-art"})
	if err != nil || got != NoArt|EpisodeUnmatched {
		t.Errorf("ParseReasons = %v, %v, want no-art,episode-unmatched", got, err)
	}
	if got, err := ParseReasons(nil); err != nil || got != 0 {
		t.Errorf("ParseReasons(nil) = %v, %v, want 0", got, err)
	}
	_, err = ParseReasons([]string{"no-art", "no-banner"})
	if err == nil || !reflect.DeepEqual(err.Error(), `unknown reason "no-banner"`) {
		t.Errorf("ParseReasons with an unknown name: err = %v", err)
	}
}
