package web

import (
	"embed"
	"net/http"
)

// fallback holds the placeholder page that the server shows when the
// dashboard UI is not built into the binary.
//
//go:embed fallback
var fallback embed.FS

// servePlaceholder answers with the embedded placeholder page.
func servePlaceholder(w http.ResponseWriter, _ *http.Request) {
	page, err := fallback.ReadFile("fallback/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}
