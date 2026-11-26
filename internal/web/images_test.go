package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

const (
	thumbPath = "/library/metadata/101/thumb/1700000000"
	artPath   = "/library/metadata/101/art/1700000001"
)

// photoCall is one Photo request of the fake.
type photoCall struct {
	key, path     string
	width, height int
}

// photoRecorder is the Photo of a fake Plex that records its calls.
type photoRecorder struct {
	mu    sync.Mutex
	calls []photoCall
	body  []byte
	err   error
}

func (p *photoRecorder) photo(_ context.Context, key, path string, w, h int) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, photoCall{key, path, w, h})
	return p.body, p.err
}

func (p *photoRecorder) got() []photoCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]photoCall(nil), p.calls...)
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// imageServer returns a Server with the item 101 of server Home, with the
// thumb and art paths, and the recorder of its Photo calls.
func imageServer(t *testing.T, thumb, art string) (*Server, *store.Store, *photoRecorder) {
	t.Helper()
	st := openStore(t)
	it := movie("101", "Alien", store.NoSummary, time.Time{})
	it.Thumb, it.Art = thumb, art
	readLibrary(t, st, "Home", "1", testNow, it)
	rec := &photoRecorder{body: pngBytes(t)}
	s := newServerWith(st, 0, []PlexServer{{Name: "Home", Plex: &fakePlex{photo: rec.photo}}})
	return s, st, rec
}

func getImage(s *Server, path string) *httptest.ResponseRecorder {
	return serve(s, httptest.NewRequest(http.MethodGet, path, nil))
}

func TestThumbServesImage(t *testing.T) {
	s, _, rec := imageServer(t, thumbPath, artPath)
	want := rec.body

	res := getImage(s, "/api/items/thumb?srv=0&key=101&v=7")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Code, res.Body)
	}
	for h, w := range map[string]string{
		"Content-Type":                 "image/png",
		"Content-Length":               strconv.Itoa(len(want)),
		"Cache-Control":                "private, max-age=86400",
		"Cross-Origin-Resource-Policy": "same-origin",
		"X-Content-Type-Options":       "nosniff",
	} {
		if got := res.Header().Get(h); got != w {
			t.Errorf("%s = %q, want %q", h, got, w)
		}
	}
	if !bytes.Equal(res.Body.Bytes(), want) {
		t.Error("body differs from the Plex image")
	}
	if got := rec.got(); len(got) != 1 || got[0] != (photoCall{"101", thumbPath, 240, 360}) {
		t.Errorf("Photo calls = %v", got)
	}
}

func TestImageSizes(t *testing.T) {
	for _, tt := range []struct {
		query         string
		width, height int
		status        int
	}{
		{"thumb?srv=0&key=101&size=sm", 120, 180, 200},
		{"thumb?srv=0&key=101&size=md", 240, 360, 200},
		{"thumb?srv=0&key=101&size=lg", 480, 720, 200},
		{"art?srv=0&key=101&size=sm", 480, 270, 200},
		{"art?srv=0&key=101", 960, 540, 200},
		{"art?srv=0&key=101&size=lg", 1920, 1080, 200},
		{"art?srv=0&key=101&size=xl", 0, 0, 400},
		{"thumb?srv=0&key=101&size=", 0, 0, 400},
	} {
		t.Run(tt.query, func(t *testing.T) {
			s, _, rec := imageServer(t, thumbPath, artPath)
			res := getImage(s, "/api/items/"+tt.query)
			if res.Code != tt.status {
				t.Fatalf("status = %d, want %d", res.Code, tt.status)
			}
			got := rec.got()
			if tt.status != 200 {
				assertJSONError(t, res, 400, "invalid size")
				if len(got) != 0 {
					t.Errorf("Photo calls = %v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].width != tt.width || got[0].height != tt.height {
				t.Errorf("Photo calls = %v, want %dx%d", got, tt.width, tt.height)
			}
		})
	}
}

func TestImageEndpointsUseTheirField(t *testing.T) {
	s, _, rec := imageServer(t, thumbPath, artPath)

	getImage(s, "/api/items/thumb?srv=0&key=101")
	getImage(s, "/api/items/art?srv=0&key=101")

	got := rec.got()
	if len(got) != 2 || got[0].path != thumbPath || got[1].path != artPath {
		t.Errorf("Photo calls = %v, want the thumb path then the art path", got)
	}
}

func TestImageTooLarge(t *testing.T) {
	s, _, rec := imageServer(t, thumbPath, artPath)
	rec.body, rec.err = nil, errors.New("photo: larger than 10 MB")

	res := getImage(s, "/api/items/thumb?srv=0&key=101")

	assertJSONError(t, res, http.StatusBadGateway, "image unavailable")
}

func TestImageRejectsForeignPath(t *testing.T) {
	for _, stored := range []string{"/library/metadata/999/thumb/1", "https://h/x.jpg", ""} {
		t.Run(stored, func(t *testing.T) {
			s, _, rec := imageServer(t, stored, stored)
			for _, route := range []string{"thumb", "art"} {
				res := getImage(s, "/api/items/"+route+"?srv=0&key=101")
				assertJSONError(t, res, http.StatusNotFound, "not found")
			}
			if got := rec.got(); len(got) != 0 {
				t.Errorf("Photo calls = %v, want none", got)
			}
		})
	}
}

func TestImageRejectsNonImage(t *testing.T) {
	for name, body := range map[string][]byte{
		"html":  []byte("<html><script>alert(1)</script></html>"),
		"gif":   []byte("GIF89a\x01\x00\x01\x00"),
		"empty": nil,
	} {
		t.Run(name, func(t *testing.T) {
			s, _, rec := imageServer(t, thumbPath, artPath)
			rec.body = body

			res := getImage(s, "/api/items/thumb?srv=0&key=101")

			assertJSONError(t, res, http.StatusBadGateway, "image unavailable")
			if strings.Contains(res.Body.String(), "html") {
				t.Errorf("body = %s", res.Body)
			}
		})
	}
}

func TestImageServesJPEGAndWebP(t *testing.T) {
	for typ, body := range map[string][]byte{
		"image/jpeg": {0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0},
		"image/webp": []byte("RIFF\x04\x00\x00\x00WEBPVP8 "),
	} {
		t.Run(typ, func(t *testing.T) {
			s, _, rec := imageServer(t, thumbPath, artPath)
			rec.body = body

			res := getImage(s, "/api/items/art?srv=0&key=101")

			if res.Code != http.StatusOK || res.Header().Get("Content-Type") != typ {
				t.Errorf("status = %d, Content-Type = %q, want 200 %s", res.Code, res.Header().Get("Content-Type"), typ)
			}
		})
	}
}

func TestImageNotFound(t *testing.T) {
	s, _, rec := imageServer(t, thumbPath, artPath)

	assertJSONError(t, getImage(s, "/api/items/thumb?srv=0&key=999"), http.StatusNotFound, "not found")
	if got := rec.got(); len(got) != 0 {
		t.Errorf("Photo calls = %v, want none", got)
	}

	rec.body, rec.err = nil, fmt.Errorf("photo: %w", plex.ErrNotFound)
	assertJSONError(t, getImage(s, "/api/items/thumb?srv=0&key=101"), http.StatusNotFound, "not found")
	assertJSONError(t, getImage(s, "/api/items/thumb?key=101"), http.StatusBadRequest, "invalid srv")
	assertJSONError(t, getImage(s, "/api/items/thumb?srv=0&key=1/2"), http.StatusBadRequest, "invalid key")
}

func TestImageFromHistory(t *testing.T) {
	s, st, rec := imageServer(t, thumbPath, artPath)
	// The read completes the item, so its row is gone.
	readLibrary(t, st, "Home", "1", testNow.Add(time.Hour), movie("101", "Alien", 0, time.Time{}))
	if _, found := storedPath(t, st, "101"); found {
		t.Fatal("the row of the completed item still exists")
	}

	res := getImage(s, "/api/items/art?srv=0&key=101")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", res.Code, res.Body)
	}
	if got := rec.got(); len(got) != 1 || got[0].path != artPath {
		t.Errorf("Photo calls = %v, want the art path of the snapshot", got)
	}
}

func TestImageNeedsAuth(t *testing.T) {
	st := openStore(t)
	it := movie("101", "Alien", store.NoSummary, time.Time{})
	it.Thumb = thumbPath
	readLibrary(t, st, "Home", "1", testNow, it)
	rec := &photoRecorder{body: pngBytes(t)}
	s := New(Options{
		Username: "admin", Password: "secret", Store: st,
		Servers: []PlexServer{{Name: "Home", Plex: &fakePlex{photo: rec.photo}}},
	})

	res := getImage(s, "/api/items/thumb?srv=0&key=101")

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
	if got := rec.got(); len(got) != 0 {
		t.Errorf("Photo calls = %v, want none", got)
	}
}

func TestImageCoalescesDuplicateConcurrentRequests(t *testing.T) {
	st := openStore(t)
	it := movie("101", "Alien", store.NoSummary, time.Time{})
	it.Thumb = thumbPath
	readLibrary(t, st, "Home", "1", testNow, it)

	want := pngBytes(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	fp := &fakePlex{photo: func(_ context.Context, _, _ string, _, _ int) ([]byte, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return want, nil
	}}
	s := newServerWith(st, 0, []PlexServer{{Name: "Home", Plex: fp}})

	var wg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, 2)
	for i := range 2 {
		wg.Go(func() {
			recs[i] = getImage(s, "/api/items/thumb?srv=0&key=101")
		})
	}
	<-entered
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("Photo calls = %d, want 1", gotCalls)
	}
	for i, rec := range recs {
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
			t.Errorf("response[%d] = status %d len %d, want 200 len %d", i, rec.Code, rec.Body.Len(), len(want))
		}
	}
}

func TestImageBoundsConcurrentFetchesAndRespectsCancel(t *testing.T) {
	st := openStore(t)
	it1 := movie("101", "One", store.NoSummary, time.Time{})
	it1.Thumb = thumbPath
	it2 := movie("102", "Two", store.NoSummary, time.Time{})
	it2.Thumb = "/library/metadata/102/thumb/1700000002"
	readLibrary(t, st, "Home", "1", testNow, it1, it2)

	want := pngBytes(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var keys []string
	fp := &fakePlex{photo: func(_ context.Context, key, _ string, _, _ int) ([]byte, error) {
		mu.Lock()
		keys = append(keys, key)
		mu.Unlock()
		if key == "101" {
			close(entered)
			<-release
		}
		return want, nil
	}}
	s := newServerWith(st, 0, []PlexServer{{Name: "Home", Plex: fp}})
	s.imageSem = make(chan struct{}, 1)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		getImage(s, "/api/items/thumb?srv=0&key=101")
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/items/thumb?srv=0&key=102", nil)
	rec := serve(s, req)
	if rec.Body.Len() != 0 {
		t.Errorf("canceled request wrote body %q, want empty", rec.Body.String())
	}

	close(release)
	<-firstDone
	mu.Lock()
	gotKeys := append([]string(nil), keys...)
	mu.Unlock()
	if len(gotKeys) != 1 || gotKeys[0] != "101" {
		t.Errorf("Photo keys = %v, want only [101]", gotKeys)
	}
}

func TestImageLogsFailureRedacted(t *testing.T) {
	const secret = "fake-private-token"
	logs := captureLog(t)
	st := openStore(t)
	it := movie("101", "Alien", store.NoSummary, time.Time{})
	it.Thumb = thumbPath
	readLibrary(t, st, "Home", "1", testNow, it)
	s := New(Options{
		Store:   st,
		Secrets: []string{secret},
		Servers: []PlexServer{{Name: "Home", Plex: &fakePlex{photo: func(context.Context, string, string, int, int) ([]byte, error) {
			return nil, errors.New("plex failed with " + secret)
		}}}},
	})

	res := getImage(s, "/api/items/thumb?srv=0&key=101")
	assertJSONError(t, res, http.StatusBadGateway, "image unavailable")
	out := logs.String()
	if !strings.Contains(out, `"level":"warn"`) || !strings.Contains(out, redacted) || strings.Contains(out, secret) {
		t.Errorf("log = %q, want redacted warn without secret", out)
	}
}

func TestImageCoalescesConcurrentRequestsWhileSemaphoreFull(t *testing.T) {
	st := openStore(t)
	it1 := movie("101", "One", store.NoSummary, time.Time{})
	it1.Thumb = thumbPath
	it2 := movie("102", "Two", store.NoSummary, time.Time{})
	it2.Thumb = "/library/metadata/102/thumb/1700000002"
	readLibrary(t, st, "Home", "1", testNow, it1, it2)

	want := pngBytes(t)
	firstEntered := make(chan struct{})
	firstRelease := make(chan struct{})
	var mu sync.Mutex
	callsByKey := map[string]int{}
	fp := &fakePlex{photo: func(_ context.Context, key, _ string, _, _ int) ([]byte, error) {
		mu.Lock()
		callsByKey[key]++
		mu.Unlock()
		if key == "101" {
			close(firstEntered)
			<-firstRelease
		}
		return want, nil
	}}
	s := newServerWith(st, 0, []PlexServer{{Name: "Home", Plex: fp}})
	s.imageSem = make(chan struct{}, 1)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		getImage(s, "/api/items/thumb?srv=0&key=101")
	}()
	<-firstEntered

	// Saturate with 101 holding the only slot, then send two concurrent
	// requests for 102 before releasing 101. Both must share one fetch.
	var wg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, 2)
	for i := range 2 {
		wg.Go(func() {
			recs[i] = getImage(s, "/api/items/thumb?srv=0&key=102")
		})
	}
	coalesced := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.imageMu.Lock()
		call := s.imageCalls[imageKey{srv: 0, ratingKey: "102", path: it2.Thumb, width: 240, height: 360}]
		waiters := 0
		if call != nil {
			waiters = call.waiters
		}
		s.imageMu.Unlock()
		if waiters == 2 {
			coalesced = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(firstRelease)
	<-firstDone
	wg.Wait()
	if !coalesced {
		t.Fatal("concurrent requests for key 102 did not join before timeout")
	}

	mu.Lock()
	got102 := callsByKey["102"]
	mu.Unlock()
	if got102 != 1 {
		t.Fatalf("Photo calls for key 102 = %d, want 1", got102)
	}
	for i, rec := range recs {
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
			t.Errorf("response[%d] = status %d len %d, want 200 len %d", i, rec.Code, rec.Body.Len(), len(want))
		}
	}
}
