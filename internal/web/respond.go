package web

import (
	"encoding/json"
	"net/http"

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

// writeStoreError logs a failed store read of r and answers 500.
func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	log.Error().Err(err).Str("path", r.URL.Path).Msg("API request failed")
	writeError(w, http.StatusInternalServerError, "internal error")
}
