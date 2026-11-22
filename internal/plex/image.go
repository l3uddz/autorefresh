package plex

import (
	"net/url"
	"regexp"
	"strings"
)

var digitsPattern = regexp.MustCompile(`^[0-9]+$`)

// ItemImagePath returns p when it is the thumb or art path of the item, in
// the form that Plex gives. Otherwise it returns "".
func ItemImagePath(ratingKey, p string) string {
	if !idPattern.MatchString(ratingKey) {
		return ""
	}
	base := "/library/metadata/" + ratingKey + "/"
	for _, kind := range []string{"thumb/", "art/"} {
		if rest, ok := strings.CutPrefix(p, base+kind); ok && digitsPattern.MatchString(rest) {
			return p
		}
	}
	return ""
}

// SafeRemoteImage returns raw when it is an https URL with a host, without
// user info, fragment, or a token in its query. Otherwise it returns "".
func SafeRemoteImage(raw string) string {
	if strings.Contains(raw, "#") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	for key := range query {
		if strings.Contains(strings.ToLower(key), "token") {
			return ""
		}
	}
	return raw
}
