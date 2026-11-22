//go:build webui

package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestTaggedDashboardServesBuiltUI(t *testing.T) {
	s := New(Options{})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "./assets/") ||
		strings.Contains(w.Body.String(), "The dashboard UI is not built") {
		t.Fatalf("tagged dashboard did not serve the built UI: status %d", w.Code)
	}
}

func TestTaggedDashboardNegotiatesGzip(t *testing.T) {
	handler := New(Options{}).Handler()
	for _, tc := range []struct {
		accept, encoding string
	}{
		{"gzip", "gzip"},
		{"br, gzip;q=0", ""},
		{"*;q=1, gzip;q=0", ""},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", tc.accept)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if got := w.Header().Get("Content-Encoding"); got != tc.encoding {
			t.Errorf("%q: encoding %q, want %q", tc.accept, got, tc.encoding)
		}
		if got := w.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
			t.Errorf("%q: Vary header %q", tc.accept, got)
		}
	}
}

// get sends GET path to handler with the Accept-Encoding header accept.
func get(handler http.Handler, path, accept string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		r.Header.Set("Accept-Encoding", accept)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestTaggedDashboardServesAssets(t *testing.T) {
	handler := New(Options{}).Handler()
	index := get(handler, "/", "")
	if got := index.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", got)
	}
	script := regexp.MustCompile(`\./(assets/[^"]+\.js)`).FindStringSubmatch(index.Body.String())
	if script == nil {
		t.Fatalf("index.html names no script:\n%s", index.Body.String())
	}
	path := "/" + script[1]

	plain := get(handler, path, "")
	if plain.Code != http.StatusOK || plain.Header().Get("Content-Encoding") != "" {
		t.Fatalf("plain %s: status %d, encoding %q", path, plain.Code, plain.Header().Get("Content-Encoding"))
	}
	if got := plain.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", got)
	}
	if got := plain.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}

	compressed := get(handler, path, "gzip")
	if compressed.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip %s: encoding %q", path, compressed.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	unpacked, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unpacked, plain.Body.Bytes()) {
		t.Error("the gzip copy does not unpack to the plain asset")
	}

	if w := get(handler, path+".gz", "gzip"); w.Code != http.StatusNotFound {
		t.Errorf("GET %s.gz: status %d, want 404", path, w.Code)
	}
}
