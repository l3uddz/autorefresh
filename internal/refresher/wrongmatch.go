package refresher

import (
	"slices"
	"strings"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// WrongMatchResult is the outcome of CompareMovieIDs.
type WrongMatchResult string

const (
	WrongMatchUnmatched  WrongMatchResult = "unmatched"   // Plex has no match, so nothing is compared
	WrongMatchMismatched WrongMatchResult = "mismatched"  // no compared source agrees
	WrongMatchAgrees     WrongMatchResult = "agrees"      // a compared source agrees
	WrongMatchUndecided  WrongMatchResult = "undecided"   // the paths name IDs, but no source can be compared
	WrongMatchNoPathIDs  WrongMatchResult = "no-path-ids" // the paths name no ID
)

// ComparedSource is one source that both the paths and Plex name.
type ComparedSource struct {
	Source string   // "tmdb", "imdb", or "tvdb"
	Path   string   // the ID that the paths name
	Plex   []string // the IDs that Plex lists
}

// WrongMatch is the comparison of the path IDs of a movie with its Plex IDs.
type WrongMatch struct {
	Result  WrongMatchResult
	Sources []ComparedSource // in the order of idSources
}

// idSources is the fixed order of the compared sources.
var idSources = []string{"tmdb", "imdb", "tvdb"}

// CompareMovieIDs compares the IDs that the file paths of a movie name with
// the external IDs that Plex lists, such as "tmdb://100". A source is
// compared when the paths name one ID of it and Plex lists at least one. The
// movie is mismatched when no compared source agrees. Both sides follow the
// ID rules of fix match. Without files, the paths are unknown, so the result
// is undecided.
func CompareMovieIDs(guid string, files, externalIDs []string) WrongMatch {
	if strings.HasPrefix(guid, "local://") {
		return WrongMatch{Result: WrongMatchUnmatched}
	}
	if len(files) == 0 {
		return WrongMatch{Result: WrongMatchUndecided}
	}
	paths := pathValues(files)
	if len(paths) == 0 {
		return WrongMatch{Result: WrongMatchNoPathIDs}
	}
	plexIDs := plexValues(externalIDs)
	wm := WrongMatch{Result: WrongMatchMismatched}
	for _, source := range idSources {
		path, listed := paths[source], plexIDs[source]
		if len(path) != 1 || len(listed) == 0 {
			continue
		}
		wm.Sources = append(wm.Sources, ComparedSource{Source: source, Path: path[0], Plex: listed})
		if slices.Contains(listed, path[0]) {
			wm.Result = WrongMatchAgrees
		}
	}
	if len(wm.Sources) == 0 {
		wm.Result = WrongMatchUndecided
	}
	return wm
}

// pathValues returns the path IDs of files by source.
func pathValues(files []string) map[string][]string {
	values := map[string][]string{}
	for _, id := range parseIDs(files) {
		values[id.Source] = append(values[id.Source], id.Value)
	}
	return values
}

// plexValues returns the external IDs by source, normalized and without
// duplicates, in first-seen order. It keeps only the schemes imdb://,
// tmdb://, and tvdb://, and drops a value that the ID rules reject.
func plexValues(externalIDs []string) map[string][]string {
	values := map[string][]string{}
	for _, id := range externalIDs {
		scheme, value, ok := strings.Cut(id, "://")
		source := strings.ToLower(scheme)
		if !ok || !slices.Contains(idSources, source) {
			continue
		}
		norm, ok := normalizeIDValue(source, value)
		if ok && !slices.Contains(values[source], norm) {
			values[source] = append(values[source], norm)
		}
	}
	return values
}

// flagWrongMatch sets the mismatched reason of the row of a listed or
// checked movie, or marks it as undecided so the store keeps its value. It
// returns the comparison. A series is not compared.
func flagWrongMatch(row *store.Item, itemType string, it plex.Item) WrongMatch {
	if itemType != plex.MovieLibrary {
		return WrongMatch{}
	}
	wm := CompareMovieIDs(it.GUID, it.Files, it.ExternalIDs)
	switch wm.Result {
	case WrongMatchMismatched:
		row.Reasons |= store.Mismatched
	case WrongMatchUndecided:
		row.Keep |= store.Mismatched
	}
	return wm
}

// wrongMatchFields returns the path IDs and the Plex IDs of the compared
// sources for a log line, such as "tmdb-100" and "tmdb-200".
func wrongMatchFields(wm WrongMatch) (pathIDs, plexIDs []string) {
	for _, s := range wm.Sources {
		pathIDs = append(pathIDs, s.Source+"-"+s.Path)
		for _, v := range s.Plex {
			plexIDs = append(plexIDs, s.Source+"-"+v)
		}
	}
	return pathIDs, plexIDs
}
