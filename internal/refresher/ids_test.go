package refresher

import (
	"reflect"
	"testing"
)

func TestParseIDs(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  []pathID
	}{
		{"braces", []string{"/m/Film (2020) {tmdb-747059}/Film.mkv"}, []pathID{{"tmdb", "747059"}}},
		{"brackets and colon", []string{"/m/Film [imdb:tt19120100]/f.mkv"}, []pathID{{"imdb", "tt19120100"}}},
		{"parentheses", []string{"/tv/Show (tvdb-463649)"}, []pathID{{"tvdb", "463649"}}},
		{"id suffix", []string{"/m/Film {imdbid-tt31289994}/f.mkv"}, []pathID{{"imdb", "tt31289994"}}},
		{"other separators and case", []string{"/a {tvdb=1}", "/b {TMDB_2}"}, []pathID{{"tvdb", "1"}, {"tmdb", "2"}}},
		{"upper case imdb value", []string{"/m/Film {IMDB-TT123}"}, []pathID{{"imdb", "tt123"}}},
		{"year only", []string{"/tv/Show (2020)"}, nil},
		{"imdb without tt", []string{"/m/Film {imdb-12345}"}, nil},
		{"tmdb with tt", []string{"/m/Film {tmdb-tt1}"}, nil},
		{"outside brackets", []string{"/m/Film tmdb-1 (2020)/f.mkv"}, nil},
		{"two IDs in one group", []string{"/m/Film {tmdb-1 imdb-tt2}"}, nil},
		{"mismatched brackets", []string{"/m/Film {tmdb-1]"}, nil},
		{"same ID in folder and file", []string{"/m/Film {tmdb-5}/Film {tmdb-5}.mkv"}, []pathID{{"tmdb", "5"}}},
		{"order across paths", []string{"/m/A {tvdb-9}", "/m/B {imdb-tt1}"}, []pathID{{"tvdb", "9"}, {"imdb", "tt1"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIDs(tt.paths); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseIDs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPathIDTitle(t *testing.T) {
	if got := (pathID{"tmdb", "747059"}).title(); got != "tmdb-747059" {
		t.Errorf("title() = %q", got)
	}
}
