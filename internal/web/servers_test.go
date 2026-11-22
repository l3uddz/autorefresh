package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// testNow is the clock of the API tests.
var testNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

// fakePlex is a web.Plex with a fixed identity answer. details answers
// ItemDetails; without it, ItemDetails gives plex.ErrNotFound.
type fakePlex struct {
	id      string
	err     error
	details func(ctx context.Context, ratingKey string) (plex.Details, error)
	// photo answers Photo; without it, Photo gives plex.ErrNotFound.
	photo func(ctx context.Context, ratingKey, imagePath string, width, height int) ([]byte, error)

	mu    sync.Mutex
	calls int
}

func (f *fakePlex) Photo(ctx context.Context, ratingKey, imagePath string, width, height int) ([]byte, error) {
	if f.photo == nil {
		return nil, plex.ErrNotFound
	}
	return f.photo(ctx, ratingKey, imagePath, width, height)
}

func (f *fakePlex) Identity(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.id, f.err
}

func (f *fakePlex) ItemDetails(ctx context.Context, ratingKey string) (plex.Details, error) {
	if f.details == nil {
		return plex.Details{}, plex.ErrNotFound
	}
	return f.details(ctx, ratingKey)
}

func (f *fakePlex) identityCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// openStore opens a store in a temporary directory.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "autorefresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// newAPIServer returns a Server for st with one PlexServer for each name,
// the rules of the tests, and the clock testNow.
func newAPIServer(st *store.Store, ignored store.Reasons, names ...string) *Server {
	servers := make([]PlexServer, len(names))
	for i, name := range names {
		servers[i] = PlexServer{Name: name, Plex: &fakePlex{id: "id-" + name}}
	}
	return newServerWith(st, ignored, servers)
}

func newServerWith(st *store.Store, ignored store.Reasons, servers []PlexServer) *Server {
	s := New(Options{
		Servers: servers,
		Store:   st,
		Rules: store.DueRules{
			RefreshInterval:    time.Hour,
			MaxRefreshInterval: 8 * time.Hour,
			Ignored:            ignored,
		},
	})
	s.now = func() time.Time { return testNow }
	return s
}

// getJSON sends GET path, checks a 200 JSON answer, and decodes it into v.
// It returns the raw body.
func getJSON(t *testing.T, s *Server, path string, v any) string {
	t.Helper()
	rec := serve(s, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, body %s", path, rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("GET %s: Content-Type = %q", path, got)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("GET %s: %v in %s", path, err, rec.Body)
	}
	return rec.Body.String()
}

// readLibrary stores a full read of section at the time at with items.
func readLibrary(t *testing.T, st *store.Store, server, section string, at time.Time, items ...store.Item) {
	t.Helper()
	for i := range items {
		items[i].Server, items[i].Section = server, section
		if items[i].AddedAt.IsZero() {
			items[i].AddedAt = testNow.Add(-48 * time.Hour)
		}
	}
	if _, err := st.ApplyRead(store.Read{Server: server, Section: section, At: at, Full: true, Items: items}); err != nil {
		t.Fatal(err)
	}
}

// recordRun stores a run of server that ended with status.
func recordRun(t *testing.T, st *store.Store, server string, status store.RunStatus) {
	t.Helper()
	start := store.RunStart{Server: server, Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: testNow.Add(-time.Hour)}
	id, err := st.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.FinishRun(id, start, store.RunResult{FinishedAt: testNow, Status: status}); err != nil {
		t.Fatal(err)
	}
}

type serversAnswer struct {
	Servers []struct {
		Srv               int     `json:"srv"`
		Name              string  `json:"name"`
		MachineIdentifier *string `json:"machineIdentifier"`
		Scanned           bool    `json:"scanned"`
		Libraries         []struct {
			Section      string     `json:"section"`
			Title        string     `json:"title"`
			Type         string     `json:"type"`
			LastRead     *time.Time `json:"lastRead"`
			LastFullRead *time.Time `json:"lastFullRead"`
		} `json:"libraries"`
	} `json:"servers"`
	IgnoreReasons []string `json:"ignoreReasons"`
}

func TestServersEndpoint(t *testing.T) {
	st := openStore(t)
	read := testNow.Add(-time.Hour)
	if err := st.SaveLibraries("Home", []store.LibraryInfo{{Section: "1", Title: "Movies", Type: "movie"}}); err != nil {
		t.Fatal(err)
	}
	readLibrary(t, st, "Home", "1", read)
	if err := st.SaveLibraries("Away", []store.LibraryInfo{{Section: "2", Title: "Shows", Type: "show"}}); err != nil {
		t.Fatal(err)
	}
	home := &fakePlex{id: "home-id"}
	away := &fakePlex{err: errors.New("Plex is down at http://secret-host")}
	s := newServerWith(st, store.NoArt|store.NoPoster, []PlexServer{{Name: "Home", Plex: home}, {Name: "Away", Plex: away}})

	var got serversAnswer
	body := getJSON(t, s, "/api/servers", &got)

	if strings.Contains(body, "secret-host") {
		t.Errorf("body holds the identity error: %s", body)
	}
	if want := []string{"no-poster", "no-art"}; strings.Join(got.IgnoreReasons, ",") != strings.Join(want, ",") {
		t.Errorf("ignoreReasons = %v, want %v", got.IgnoreReasons, want)
	}
	if len(got.Servers) != 2 {
		t.Fatalf("servers = %+v, want 2", got.Servers)
	}
	h, a := got.Servers[0], got.Servers[1]
	if h.Srv != 0 || h.Name != "Home" || a.Srv != 1 || a.Name != "Away" {
		t.Errorf("servers = %+v", got.Servers)
	}
	if h.MachineIdentifier == nil || *h.MachineIdentifier != "home-id" {
		t.Errorf("srv 0 machineIdentifier = %v, want home-id", h.MachineIdentifier)
	}
	if a.MachineIdentifier != nil {
		t.Errorf("srv 1 machineIdentifier = %q, want none", *a.MachineIdentifier)
	}
	if !h.Scanned || a.Scanned {
		t.Errorf("scanned = %v, %v, want true, false", h.Scanned, a.Scanned)
	}
	if len(h.Libraries) != 1 || len(a.Libraries) != 1 {
		t.Fatalf("libraries = %+v, %+v", h.Libraries, a.Libraries)
	}
	hl, al := h.Libraries[0], a.Libraries[0]
	if hl.Section != "1" || hl.Title != "Movies" || hl.Type != "movie" {
		t.Errorf("srv 0 library = %+v", hl)
	}
	if hl.LastRead == nil || !hl.LastRead.Equal(read) || hl.LastFullRead == nil || !hl.LastFullRead.Equal(read) {
		t.Errorf("srv 0 read times = %v, %v, want %v", hl.LastRead, hl.LastFullRead, read)
	}
	if al.Section != "2" || al.Title != "Shows" || al.Type != "show" || al.LastRead != nil || al.LastFullRead != nil {
		t.Errorf("srv 1 library = %+v", al)
	}

	getJSON(t, s, "/api/servers", &got)
	if n := home.identityCalls(); n != 1 {
		t.Errorf("Identity calls of srv 0 = %d, want 1 (cached)", n)
	}
	if n := away.identityCalls(); n != 1 {
		t.Errorf("Identity calls of srv 1 = %d, want 1 during cooldown", n)
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(61 * time.Second) }
	getJSON(t, s, "/api/servers", &got)
	if n := away.identityCalls(); n != 2 {
		t.Errorf("Identity calls after cooldown = %d, want 2", n)
	}
	if got.Servers[0].MachineIdentifier == nil || *got.Servers[0].MachineIdentifier != "home-id" {
		t.Errorf("cached machineIdentifier = %v", got.Servers[0].MachineIdentifier)
	}
}

func TestServersScanned(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, st *store.Store)
		want bool
	}{
		{"no library and no run", func(*testing.T, *store.Store) {}, false},
		{"finished run without a library", func(t *testing.T, st *store.Store) {
			recordRun(t, st, "Home", store.RunFinished)
		}, true},
		{"library with a read", func(t *testing.T, st *store.Store) {
			readLibrary(t, st, "Home", "1", testNow)
		}, true},
		{"only a failed run", func(t *testing.T, st *store.Store) {
			recordRun(t, st, "Home", store.RunFailed)
		}, false},
		{"finished run and an unread library", func(t *testing.T, st *store.Store) {
			if err := st.SaveLibraries("Home", []store.LibraryInfo{{Section: "1", Title: "Movies", Type: "movie"}}); err != nil {
				t.Fatal(err)
			}
			recordRun(t, st, "Home", store.RunFinished)
		}, false},
		{"finished run of another server", func(t *testing.T, st *store.Store) {
			readLibrary(t, st, "Other", "1", testNow)
			recordRun(t, st, "Other", store.RunFinished)
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openStore(t)
			tt.seed(t, st)
			var got serversAnswer
			getJSON(t, newAPIServer(st, 0, "Home"), "/api/servers", &got)
			if len(got.Servers) != 1 || got.Servers[0].Scanned != tt.want {
				t.Errorf("servers = %+v, want scanned %v", got.Servers, tt.want)
			}
		})
	}
}
