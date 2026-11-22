package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/l3uddz/autorefresh/internal/plex"
)

// noSrv is the srv of a request without a srv parameter.
const noSrv = -1

// The errors of the query and of the srv and key parameters. Their texts
// are the messages of the 400 answers.
var (
	errInvalidQuery = errors.New("invalid query")
	errInvalidSrv   = errors.New("invalid srv")
	errInvalidKey   = errors.New("invalid key")
)

// queryParams parses the query of r. A malformed pair, such as one with a
// bad percent escape, makes the whole query invalid, so that no dropped
// pair widens or resets a request.
func queryParams(r *http.Request) (url.Values, error) {
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errInvalidQuery
	}
	return v, nil
}

// srvParam returns the srv parameter of the query v: the index of a
// configured server, or noSrv when v has none (an empty value counts as
// none).
func (s *Server) srvParam(v url.Values) (int, error) {
	raw := v.Get("srv")
	if raw == "" {
		return noSrv, nil
	}
	srv, err := strconv.Atoi(raw)
	if err != nil || srv < 0 || srv >= len(s.servers) {
		return 0, errInvalidSrv
	}
	return srv, nil
}

// serverNames returns the name of the server srv, or the names of every
// configured server for noSrv.
func (s *Server) serverNames(srv int) []string {
	if srv != noSrv {
		return []string{s.servers[srv].Name}
	}
	names := make([]string, len(s.servers))
	for i, ps := range s.servers {
		names[i] = ps.Name
	}
	return names
}

// keyParam returns the key parameter of the query v, a Plex rating key.
func keyParam(v url.Values) (string, error) {
	key := v.Get("key")
	if !plex.ValidRatingKey(key) {
		return "", errInvalidKey
	}
	return key, nil
}
