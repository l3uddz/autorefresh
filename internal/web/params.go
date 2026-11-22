package web

import (
	"errors"
	"net/url"
	"strconv"
)

// noSrv is the srv of a request without a srv parameter.
const noSrv = -1

// errInvalidSrv is the error of a srv that is not a configured index. Its
// text is the message of the 400 answer.
var errInvalidSrv = errors.New("invalid srv")

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
