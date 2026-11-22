package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

// basicAuthChallenge is the WWW-Authenticate value of a 401 response.
const basicAuthChallenge = `Basic realm="autorefresh", charset="UTF-8"`

// basicAuth requires the configured username and password on every request.
// It returns next unchanged when username is empty.
func basicAuth(username, password string, next http.Handler) http.Handler {
	if username == "" {
		return next
	}
	wantUser := sha256.Sum256([]byte(username))
	wantPass := sha256.Sum256([]byte(password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotUser := sha256.Sum256([]byte(user))
		gotPass := sha256.Sum256([]byte(pass))
		// Compare both values before deciding, so the time does not show
		// which one was wrong.
		userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:])
		passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:])
		if !ok || userOK&passOK != 1 {
			w.Header().Set("WWW-Authenticate", basicAuthChallenge)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
