//go:build !webui

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFallbackPage checks the page of a binary without the built UI.
func TestFallbackPage(t *testing.T) {
	rec := serve(New(Options{}), httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "not built") {
		t.Errorf("body does not say that the UI is not built:\n%s", body)
	}
	if !strings.Contains(body, `href="api/status"`) {
		t.Errorf("body has no relative link to api/status:\n%s", body)
	}
	if strings.Contains(body, `href="/`) {
		t.Errorf("body has an absolute link:\n%s", body)
	}
}

// TestFallbackHasNoAssets checks that a binary without the built UI answers
// 404 for asset paths.
func TestFallbackHasNoAssets(t *testing.T) {
	handler := New(Options{}).Handler()
	for _, path := range []string{"/assets/index.js", "/assets/"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, rec.Code)
		}
	}
}
