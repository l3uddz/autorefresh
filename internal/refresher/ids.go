package refresher

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// pathID is an ID that a library path names, for example {tmdb-747059}.
type pathID struct{ Source, Value string } // Source: "imdb", "tmdb", or "tvdb"

// title gives the match search title, for example "tmdb-747059".
func (id pathID) title() string {
	return id.Source + "-" + id.Value
}

// link returns the web page of the ID: TMDB, IMDb, or TheTVDB. show selects
// the series page of a tmdb or tvdb ID instead of the movie page.
func (id pathID) link(show bool) string {
	var u url.URL
	var segments []string
	switch id.Source {
	case "tmdb":
		u = url.URL{Scheme: "https", Host: "www.themoviedb.org"}
		segments = []string{"movie", id.Value}
		if show {
			segments[0] = "tv"
		}
	case "imdb":
		u = url.URL{Scheme: "https", Host: "www.imdb.com"}
		segments = []string{"title", id.Value}
	case "tvdb":
		u = url.URL{Scheme: "https", Host: "thetvdb.com"}
		segments = []string{"dereferrer", "movie", id.Value}
		if show {
			segments[1] = "series"
		}
	}
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return u.JoinPath(segments...).String()
}

var (
	// groupPattern finds a bracket group with no bracket or path separator inside. The three
	// alternatives keep the opening and closing bracket of the same kind.
	groupPattern = regexp.MustCompile(`\{([^{}\[\]()/\\]*)\}|\[([^{}\[\]()/\\]*)\]|\(([^{}\[\]()/\\]*)\)`)
	// idPattern matches a whole group text that holds one ID.
	idPattern = regexp.MustCompile(`(?i)^(imdb|tmdb|tvdb)(?:id)?[^a-z0-9]+(.+)$`)
	// imdbValue and numberValue match the value of an ID.
	imdbValue   = regexp.MustCompile(`(?i)^tt[0-9]+$`)
	numberValue = regexp.MustCompile(`^[0-9]+$`)
	// parenYearPattern matches a 4-digit year in parentheses, such as (2018).
	parenYearPattern = regexp.MustCompile(`\(([0-9]{4})\)`)
)

// parsePathYear returns the single plausible release year in parentheses
// from the item-owned tail of paths (the show folder for a series, or the
// movie folder and filename for a movie) so library root directories like
// "/media/Backup (2019)/..." never supply a year. It returns 0 if no such
// year exists or multiple different years appear.
func parsePathYear(paths []string, show bool) int {
	segments := 2
	if show {
		segments = 1
	}
	var year int
	for _, path := range paths {
		for _, seg := range tailSegments(path, segments) {
			y, ambiguous := segmentYear(seg)
			if ambiguous || (y != 0 && year != 0 && year != y) {
				return 0
			}
			if y != 0 {
				year = y
			}
		}
	}
	return year
}

// segmentYear returns the single plausible release year in parentheses within
// one path segment. ambiguous is true if the segment holds multiple different
// plausible years.
func segmentYear(seg string) (year int, ambiguous bool) {
	for _, m := range parenYearPattern.FindAllStringSubmatch(seg, -1) {
		y, err := strconv.Atoi(m[1])
		if err != nil || y < 1880 || y > 2100 {
			continue
		}
		if year != 0 && year != y {
			return 0, true
		}
		year = y
	}
	return year, false
}

// tailSegments returns up to n trailing non-empty path segments, splitting
// on both "/" and "\" so Plex server paths from any OS work identically.
func tailSegments(path string, n int) []string {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) <= n {
		return parts
	}
	return parts[len(parts)-n:]
}

// parseIDs reads the IDs in all paths, without duplicates, in the order found.
func parseIDs(paths []string) []pathID {
	var ids []pathID
	seen := map[pathID]bool{}
	for _, path := range paths {
		for _, group := range groupPattern.FindAllStringSubmatch(path, -1) {
			id, ok := parseGroup(group[1] + group[2] + group[3])
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// parseGroup reads the text inside one bracket group as one ID. A bare IMDb
// ID, such as tt0115097, is an imdb ID.
func parseGroup(text string) (pathID, bool) {
	if imdbValue.MatchString(text) {
		return pathID{"imdb", strings.ToLower(text)}, true
	}
	m := idPattern.FindStringSubmatch(text)
	if m == nil {
		return pathID{}, false
	}
	source, value := strings.ToLower(m[1]), m[2]
	if source == "imdb" {
		if !imdbValue.MatchString(value) {
			return pathID{}, false
		}
		return pathID{source, strings.ToLower(value)}, true
	}
	if !numberValue.MatchString(value) {
		return pathID{}, false
	}
	return pathID{source, value}, true
}
