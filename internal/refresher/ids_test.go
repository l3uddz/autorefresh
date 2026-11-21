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
		{"bare imdb in braces", []string{"/m/Film (1996)/Film (1996) {tt0115097}.mkv"}, []pathID{{"imdb", "tt0115097"}}},
		{"bare imdb in brackets and parentheses", []string{"/m/A [TT1]", "/m/B (tt2)"}, []pathID{{"imdb", "tt1"}, {"imdb", "tt2"}}},
		{"bare and prefixed are one ID", []string{"/m/Film {imdb-tt5}/Film {tt5}.mkv"}, []pathID{{"imdb", "tt5"}}},
		{"bare tt without digits", []string{"/m/Film {tt}"}, nil},
		{"bare number", []string{"/m/Film {12345}"}, nil},
		{"year only", []string{"/tv/Show (2020)"}, nil},
		{"imdb without tt", []string{"/m/Film {imdb-12345}"}, nil},
		{"tmdb with tt", []string{"/m/Film {tmdb-tt1}"}, nil},
		{"outside brackets", []string{"/m/Film tmdb-1 (2020)/f.mkv"}, nil},
		{"group spans path parts", []string{"/m/Film {tmdb-/5}"}, nil},
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

func TestPathIDLink(t *testing.T) {
	tests := []struct {
		id   pathID
		show bool
		want string
	}{
		{pathID{"tmdb", "747059"}, false, "https://www.themoviedb.org/movie/747059"},
		{pathID{"tmdb", "77"}, true, "https://www.themoviedb.org/tv/77"},
		{pathID{"imdb", "tt0115097"}, false, "https://www.imdb.com/title/tt0115097"},
		{pathID{"tvdb", "5"}, true, "https://thetvdb.com/dereferrer/series/5"},
		{pathID{"tvdb", "5"}, false, "https://thetvdb.com/dereferrer/movie/5"},
		// A value from outside stays one escaped segment.
		{pathID{"tmdb", "1/../x"}, false, "https://www.themoviedb.org/movie/1%2F..%2Fx"},
	}
	for _, tt := range tests {
		if got := tt.id.link(tt.show); got != tt.want {
			t.Errorf("link(%v, %v) = %q, want %q", tt.id, tt.show, got, tt.want)
		}
	}
}

func TestParsePathYear(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		show  bool
		want  int
	}{
		{"bare year in title and parenthesized release year", []string{"/m/Concert 2000 (2018) {imdb-tt1}/Concert 2000 (2018).mkv"}, false, 2018},
		{"same year repeated across folder and file", []string{"/m/Film (2020)/Film (2020).mkv"}, false, 2020},
		{"ignores year in movie library root", []string{"/media/Backup (2019)/Movies/Film/Film.mkv"}, false, 0},
		{"ignores year in show library root", []string{"/media/TV (2019)/Show"}, true, 0},
		{"reads year from show folder", []string{"/media/TV (2019)/Show (2022) [tvdbid-5]"}, true, 2022},
		{"ambiguous parenthesized years", []string{"/m/Film (1984) (2018)/Film.mkv"}, false, 0},
		{"different years across paths", []string{"/m/A (2018).mkv", "/m/B (2020).mkv"}, false, 0},
		{"out of plausible year range", []string{"/m/Film (0001) (2640)/Film.mkv"}, false, 0},
		{"non-four-digit parens and square brackets", []string{"/m/Film [2020] (1080p)/Film.mkv"}, false, 0},
		{"no year", []string{"/m/Film/Film.mkv"}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePathYear(tt.paths, tt.show); got != tt.want {
				t.Errorf("parsePathYear(%v, %v) = %d, want %d", tt.paths, tt.show, got, tt.want)
			}
		})
	}
}
