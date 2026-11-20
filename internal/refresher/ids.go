package refresher

import (
	"regexp"
	"strings"
)

// pathID is an ID that a library path names, for example {tmdb-747059}.
type pathID struct{ Source, Value string } // Source: "imdb", "tmdb", or "tvdb"

// title gives the match search title, for example "tmdb-747059".
func (id pathID) title() string {
	return id.Source + "-" + id.Value
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
)

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
