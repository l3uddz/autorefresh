package web

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"
)

// errorJSON is the body of every error response.
type errorJSON struct {
	Error string `json:"error"`
}

// writeJSON answers with status and v encoded as JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is sent, so an encoding or write error cannot change the
	// response any more.
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers with status and {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorJSON{Error: msg})
}

// writeStoreError logs a failed store read of r and answers 500. When the
// context of r is done, the client left or the server stops: the read
// failed for that reason and not in the store, so it logs at debug and
// answers nothing.
func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		log.Debug().Err(err).Str("path", r.URL.Path).Msg("API request failed")
		return
	}
	log.Error().Err(err).Str("path", r.URL.Path).Msg("API request failed")
	writeError(w, http.StatusInternalServerError, "internal error")
}

// redacted replaces each secret in a stored error text.
const redacted = "<redacted>"

// span is the byte range [start, end) of a text.
type span struct{ start, end int }

// redact returns text with every occurrence of each configured secret
// replaced by "<redacted>". It finds the matches of all secrets in the
// original text, merges the overlapping and touching ones, and replaces each
// merged span once, so overlapping secrets leave no part of either.
func (s *Server) redact(text string) string {
	spans := secretSpans(text, s.secrets)
	if len(spans) == 0 {
		return text
	}
	slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.start, b.start) })
	var b strings.Builder
	written := 0
	cur := spans[0]
	for _, sp := range spans[1:] {
		if sp.start <= cur.end {
			cur.end = max(cur.end, sp.end)
			continue
		}
		b.WriteString(text[written:cur.start])
		b.WriteString(redacted)
		written, cur = cur.end, sp
	}
	b.WriteString(text[written:cur.start])
	b.WriteString(redacted)
	b.WriteString(text[cur.end:])
	return b.String()
}

// secretSpans returns the span of every match of each non-empty secret in
// text. It advances one byte after each match, so it also finds the
// overlapping matches of one secret.
func secretSpans(text string, secrets []string) []span {
	var spans []span
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for from := 0; ; {
			i := strings.Index(text[from:], secret)
			if i < 0 {
				break
			}
			start := from + i
			spans = append(spans, span{start, start + len(secret)})
			from = start + 1
		}
	}
	return spans
}
