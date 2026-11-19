package store

import "testing"

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
