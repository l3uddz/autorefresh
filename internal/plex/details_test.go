package plex

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIdentity(t *testing.T) {
	rec, c := newServer(t, serveFixture(t, "server_root.json"))
	got, err := c.Identity(context.Background())
	if err != nil || got != "0000000000000000000000000000000000000001" {
		t.Fatalf("Identity = %q, %v", got, err)
	}
	reqs := rec.all()
	if len(reqs) != 1 || reqs[0].URL.Path != "/" || reqs[0].Header.Get("X-Plex-Token") != testToken {
		t.Fatalf("requests = %v", reqs)
	}
}

func TestIdentityRejectsInvalidIdentifier(t *testing.T) {
	for _, id := range []string{"", "a/b"} {
		_, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"` + id + `"}}`))
		})
		_, err := c.Identity(context.Background())
		if err == nil || !strings.HasPrefix(err.Error(), "identity: ") {
			t.Fatalf("id %q: err = %v", id, err)
		}
	}
}

func TestItemDetails(t *testing.T) {
	_, c := newServer(t, serveFixture(t, "item_show.json"))
	got, err := c.ItemDetails(context.Background(), "1004")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := c.ItemPaths(context.Background(), "1004")
	if err != nil {
		t.Fatal(err)
	}
	want := Details{GUID: "local://1004", ChildCount: 2, LeafCount: 12, Paths: paths}
	if !reflect.DeepEqual(got, want) || len(got.Paths.Locations) != 1 {
		t.Fatalf("ItemDetails = %+v, want %+v", got, want)
	}
}

// A matched item lists its external guids in "Guid", an array. The key must
// not fall into the string field "guid".
func TestItemDetailsReadsItemWithExternalGuids(t *testing.T) {
	_, c := newServer(t, serveFixture(t, "item_show_guids.json"))
	got, err := c.ItemDetails(context.Background(), "1004")
	if err != nil {
		t.Fatal(err)
	}
	if got.GUID != "plex://show/fixture0000000000000001" || len(got.Paths.Locations) != 1 {
		t.Fatalf("ItemDetails = %+v", got)
	}
}

func TestItemDetailsErrors(t *testing.T) {
	_, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"MediaContainer":{}}`))
	})
	_, err := c.ItemDetails(context.Background(), "1004")
	if err == nil || !strings.HasPrefix(err.Error(), "item details: ") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.ItemDetails(context.Background(), "../x"); err == nil {
		t.Fatal("no error for an invalid key")
	}
}

const thumb = "/library/metadata/1004/thumb/1700000300"

func TestPhotoUsesTranscoder(t *testing.T) {
	rec, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("jpeg-bytes")) })
	got, err := c.Photo(context.Background(), "1004", thumb, 240, 360)
	if err != nil || string(got) != "jpeg-bytes" {
		t.Fatalf("Photo = %q, %v", got, err)
	}
	r := rec.all()[0]
	want := url.Values{"url": {thumb}, "width": {"240"}, "height": {"360"}, "minSize": {"1"}, "upscale": {"1"}}
	if r.URL.Path != "/photo/:/transcode" || !reflect.DeepEqual(r.URL.Query(), want) ||
		r.Header.Get("Accept") != "image/*" || r.Header.Get("X-Plex-Token") != testToken {
		t.Fatalf("request = %s %v %v", r.URL.Path, r.URL.Query(), r.Header)
	}

	rec, c = newServer(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("x")) })
	c.base = mustURL(t, c.base.String()+"/plex")
	if _, err := c.Photo(context.Background(), "1004", thumb, 1, 1); err != nil {
		t.Fatal(err)
	}
	if p := rec.all()[0].URL.Path; p != "/plex/photo/:/transcode" {
		t.Fatalf("path = %s", p)
	}
}

func TestPhotoRejectsForeignPath(t *testing.T) {
	rec, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {})
	_, err := c.Photo(context.Background(), "1004", "/library/metadata/999/thumb/1", 1, 1)
	if err == nil || len(rec.all()) != 0 {
		t.Fatalf("err = %v, requests = %d", err, len(rec.all()))
	}
}

func TestPhotoLimitsSize(t *testing.T) {
	for _, tt := range []struct {
		size int
		ok   bool
	}{{MaxPhotoBytes + 1, false}, {MaxPhotoBytes, true}} {
		_, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write(bytes.Repeat([]byte("a"), tt.size))
		})
		got, err := c.Photo(context.Background(), "1004", thumb, 1, 1)
		if (err == nil) != tt.ok || (tt.ok && len(got) != tt.size) {
			t.Fatalf("size %d: len %d, err %v", tt.size, len(got), err)
		}
	}
}

func TestPhotoTooLargeDoesNotWaitForStalledBody(t *testing.T) {
	release := make(chan struct{})
	_, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(bytes.Repeat([]byte("a"), MaxPhotoBytes+1))
		w.(http.Flusher).Flush()
		<-release
	})
	// Cleanups run last in, first out: release the handler before the server closes.
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.Photo(ctx, "1004", thumb, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "larger than 10 MB") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestPhotoNotFound(t *testing.T) {
	_, c := newServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	if _, err := c.Photo(context.Background(), "1004", thumb, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPathPrefix(t *testing.T) {
	for base, want := range map[string]string{
		"http://h": "", "http://h/": "", "http://h/plex/": "/plex",
		"http://h/gateway//sentinel-path/": "/gateway/sentinel-path",
	} {
		if got := New("t", mustURL(t, base), "", time.Second).PathPrefix(); got != want {
			t.Errorf("PathPrefix(%q) = %q, want %q", base, got, want)
		}
	}
	rec, c := newServer(t, serveFixture(t, "sections.json"))
	c.base = mustURL(t, c.base.String()+"/gateway//sentinel-path/")
	c.Libraries(context.Background())
	if p := rec.all()[0].URL.Path; !strings.HasPrefix(p, c.PathPrefix()) || c.PathPrefix() != "/gateway/sentinel-path" {
		t.Fatalf("path = %s, prefix = %s", p, c.PathPrefix())
	}
}

func TestBodyErrorsKeepTheRequestPathAfterRedirect(t *testing.T) {
	_, c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			w.Write([]byte("not json"))
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	c.base = mustURL(t, c.base.String()+"/plex")
	_, err := c.Libraries(context.Background())
	if err == nil || !strings.Contains(err.Error(), "/plex/library/sections") || strings.Contains(err.Error(), "/elsewhere") {
		t.Fatalf("err = %v", err)
	}
}
