package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serve sends r to the handler of s and returns the recorded response.
func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// assertJSONError checks the status, the content type, and the JSON error body.
func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d", rec.Code, status)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got, want := strings.TrimSpace(rec.Body.String()), `{"error":"`+msg+`"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	for _, path := range []string{"/api/", "/api/x", "/api/x/y"} {
		t.Run(path, func(t *testing.T) {
			rec := serve(New(Options{}), httptest.NewRequest(http.MethodGet, path, nil))
			assertJSONError(t, rec, http.StatusNotFound, "not found")
		})
	}
	// Unknown POST routes return 404 even without Content-Type: application/json.
	rec := serve(New(Options{}), httptest.NewRequest(http.MethodPost, "/api/does-not-exist", nil))
	assertJSONError(t, rec, http.StatusNotFound, "not found")
}

func TestSecurityHeaders(t *testing.T) {
	authed := func() *Server { return New(Options{Username: "admin", Password: "secret"}) }
	closed := func() *Server {
		s := New(Options{})
		s.closeAdmission()
		return s
	}
	crossSite := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		return r
	}
	tests := []struct {
		name   string
		server *Server
		req    *http.Request
		status int
	}{
		{"page", New(Options{}), httptest.NewRequest(http.MethodGet, "/", nil), http.StatusOK},
		{"not found", New(Options{}), httptest.NewRequest(http.MethodGet, "/api/x", nil), http.StatusNotFound},
		{"unauthorized", authed(), httptest.NewRequest(http.MethodGet, "/", nil), http.StatusUnauthorized},
		{"cross-origin", New(Options{}), crossSite(), http.StatusForbidden},
		{"shutting down", closed(), httptest.NewRequest(http.MethodGet, "/", nil), http.StatusServiceUnavailable},
	}
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(tt.server, tt.req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			for name, value := range want {
				if got := rec.Header().Get(name); got != value {
					t.Errorf("%s = %q, want %q", name, got, value)
				}
			}
		})
	}
}

func TestShuttingDownIsJSON503(t *testing.T) {
	s := New(Options{})
	s.closeAdmission()

	rec := serve(s, httptest.NewRequest(http.MethodGet, "/", nil))
	assertJSONError(t, rec, http.StatusServiceUnavailable, "shutting down")
}

func TestStopClosesAdmission(t *testing.T) {
	s := New(Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	rec := serve(s, httptest.NewRequest(http.MethodGet, "/", nil))
	assertJSONError(t, rec, http.StatusServiceUnavailable, "shutting down")
}

func TestCrossOriginPostIsRejected(t *testing.T) {
	crossSite := httptest.NewRequest(http.MethodPost, "/", nil)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	assertJSONError(t, serve(New(Options{}), crossSite), http.StatusForbidden, "cross-origin request rejected")

	foreignOrigin := httptest.NewRequest(http.MethodPost, "http://ui.local/", nil)
	foreignOrigin.Header.Set("Origin", "https://evil.example")
	assertJSONError(t, serve(New(Options{}), foreignOrigin), http.StatusForbidden, "cross-origin request rejected")

	sameOrigin := httptest.NewRequest(http.MethodPost, "/", nil)
	sameOrigin.Header.Set("Sec-Fetch-Site", "same-origin")
	if rec := serve(New(Options{}), sameOrigin); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("same-origin POST status = %d, want 405", rec.Code)
	}
}

// startServer listens on a free local port, starts s, and returns the base URL.
func startServer(t *testing.T, s *Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.Start(ln)
	return "http://" + ln.Addr().String()
}

func TestStartAndStop(t *testing.T) {
	s := New(Options{})
	base := startServer(t, s)

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	if resp, err := client.Get(base + "/"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("GET after Stop succeeded")
	}
}

// getInBackground sends a GET to url and ignores the result, since Stop
// closes the connection.
func getInBackground(url string) {
	go func() {
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		if resp, err := client.Get(url); err == nil {
			_ = resp.Body.Close()
		}
	}()
}

// waitEntered waits until the test handler has started.
func waitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
}

func TestStopForcesBlockedHandlers(t *testing.T) {
	s := New(Options{})
	entered := make(chan struct{})
	returned := make(chan struct{})
	s.mux.HandleFunc("GET /block", func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		// Linger after the cancel, so the test sees whether Stop waits
		// for the handler.
		time.Sleep(50 * time.Millisecond)
		close(returned)
	})
	base := startServer(t, s)
	getInBackground(base + "/block")
	waitEntered(t, entered)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.Stop(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stop = %v, want the Shutdown error %v", err, context.DeadlineExceeded)
	}
	if elapsed > time.Second {
		t.Errorf("Stop took %v, want at most 1s", elapsed)
	}
	select {
	case <-returned:
	default:
		t.Error("Stop returned before the blocked handler returned")
	}
	if s.srv.BaseContext(nil).Err() == nil {
		t.Error("Stop did not cancel the base context of the requests")
	}
	if rec := serve(s, httptest.NewRequest(http.MethodGet, "/", nil)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status after a forced Stop = %d, want 503", rec.Code)
	}
}

func TestStopBoundsHandlersThatIgnoreCancel(t *testing.T) {
	s := New(Options{})
	s.forceWait = 50 * time.Millisecond
	entered := make(chan struct{})
	release := make(chan struct{})
	s.mux.HandleFunc("GET /stall", func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	})
	base := startServer(t, s)
	getInBackground(base + "/stall")
	waitEntered(t, entered)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.Stop(ctx)
	elapsed := time.Since(start)
	close(release)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stop = %v, want the Shutdown error %v", err, context.DeadlineExceeded)
	}
	if elapsed > time.Second {
		t.Errorf("Stop took %v, want at most 1s", elapsed)
	}
}
