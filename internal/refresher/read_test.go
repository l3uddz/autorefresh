package refresher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

const (
	testServer = "home"
	minimumAge = 2 * time.Hour
	oversample = 48 * time.Hour
)

var (
	moviesLib = plex.Library{ID: "1", Title: "Movies", Type: "movie", Agent: "tv.plex.agents.movie", Language: "en-US"}
	showsLib  = plex.Library{ID: "2", Title: "TV", Type: "show", Agent: "tv.plex.agents.series", Language: "en-US"}

	// t0 is a Saturday. The cache-refresh schedule "0 3 * * 0" next runs on
	// Sunday at 03:00.
	t0         = time.Date(2026, 11, 21, 12, 0, 0, 0, time.Local)
	sunday0259 = time.Date(2026, 11, 22, 2, 59, 0, 0, time.Local)
	sunday0300 = time.Date(2026, 11, 22, 3, 0, 0, 0, time.Local)
	addedAt    = t0.Add(-30 * 24 * time.Hour)
)

func completeItem(key string) plex.Item {
	return plex.Item{
		RatingKey: key, GUID: "plex://item/" + key, Title: "Title " + key, Year: 2020,
		Summary: "A summary.", Thumb: "/thumb/" + key, Art: "/art/" + key, AddedAt: addedAt,
	}
}

func incompleteItem(key string) plex.Item {
	it := completeItem(key)
	it.Summary = ""
	return it
}

// episodeKeys returns the listing keys of the two episode requests of the
// show library at the given run time.
func episodeKeys(at time.Time) (noSummary, unmatched string) {
	a := unixString(at.Add(-minimumAge))
	noSummary = "2?type=2&unmatched=0&episode.unmatched=0&episode.summary=&episode.addedAt<<=" + a + "&season.index>>=1"
	unmatched = "2?type=2&unmatched=0&episode.unmatched=1&episode.addedAt<<=" + a + "&season.index>>=1"
	return noSummary, unmatched
}

// captureLogs sends the global logger to a buffer until the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Logger
	// The servers of a run log from their own goroutines.
	log.Logger = zerolog.New(zerolog.SyncWriter(&buf))
	t.Cleanup(func() { log.Logger = prev })
	return &buf
}

type fixture struct {
	t      *testing.T
	store  *store.Store
	r      *Refresher
	logs   *bytes.Buffer
	now    time.Time
	mu     sync.Mutex      // guards sleeps
	sleeps []time.Duration // the durations passed to sleep
}

func newFixture(t *testing.T, servers ...Server) *fixture {
	t.Helper()
	logs := captureLogs(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	schedule, err := cron.ParseStandard("0 3 * * 0")
	if err != nil {
		t.Fatalf("parse schedule: %v", err)
	}

	f := &fixture{t: t, store: st, logs: logs}
	// The read tests use a dry run, so the refresh step after the reads sends
	// no request and changes no row. newRefreshFixture turns it off.
	f.r = New(servers, st, Options{
		MinimumAge:      minimumAge,
		RefreshInterval: 24 * time.Hour,
		Oversample:      oversample,
		CacheRefresh:    schedule,
		DryRun:          true,
	})
	f.r.now = func() time.Time { return f.now }
	f.r.sleep = func(_ context.Context, d time.Duration) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sleeps = append(f.sleeps, d)
		return nil
	}
	return f
}

func (f *fixture) run(at time.Time) {
	f.t.Helper()
	f.now = at
	if err := f.r.Run(context.Background()); err != nil {
		f.t.Fatalf("Run: %v", err)
	}
}

// rows returns the stored rows of a section, ordered by rating key.
func (f *fixture) rows(server, section string) []store.Item {
	f.t.Helper()
	rows, err := f.store.Unseen(server, section, nil)
	if err != nil {
		f.t.Fatalf("read rows: %v", err)
	}
	return rows
}

func (f *fixture) reasons(section string) map[string]store.Reasons {
	f.t.Helper()
	got := map[string]store.Reasons{}
	for _, it := range f.rows(testServer, section) {
		got[it.RatingKey] = it.Reasons
	}
	return got
}

func (f *fixture) library(server, section string) store.Library {
	f.t.Helper()
	lib, err := f.store.Library(server, section)
	if err != nil {
		f.t.Fatalf("read library: %v", err)
	}
	return lib
}

func (f *fixture) refreshed(key string, times int) {
	f.t.Helper()
	for range times {
		if err := f.store.RecordAttempt(testServer, key, t0, true); err != nil {
			f.t.Fatalf("record attempt: %v", err)
		}
	}
}

// logLines returns the logged events with the level and the message.
func (f *fixture) logLines(level, message string) []map[string]any {
	f.t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(f.logs.Bytes()))
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			f.t.Fatalf("decode log line %q: %v", sc.Text(), err)
		}
		if ev["level"] == level && ev["message"] == message {
			lines = append(lines, ev)
		}
	}
	return lines
}

func keys(rows []store.Item) []string {
	var got []string
	for _, it := range rows {
		got = append(got, it.RatingKey)
	}
	return got
}

// listedSections returns the sections of the recorded List calls.
func listedSections(calls []string) map[string]bool {
	sections := map[string]bool{}
	for _, c := range calls {
		if rest, ok := strings.CutPrefix(c, "List "); ok {
			section, _, _ := strings.Cut(rest, "?")
			sections[section] = true
		}
	}
	return sections
}

func wantCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls:\n got %q\nwant %q", got, want)
	}
}

func wantField(t *testing.T, ev map[string]any, field string, want any) {
	t.Helper()
	if got := ev[field]; got != want {
		t.Errorf("log field %s = %v (%T), want %v (%T)", field, got, got, want, want)
	}
}

func TestRunSelectsSupportedLibraries(t *testing.T) {
	personal := plex.Library{ID: "3", Title: "Home Videos", Type: "movie", Agent: "tv.plex.agents.none"}
	fp := newFakePlex(moviesLib, showsLib, personal)
	f := newFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)
	f.run(t0.Add(time.Hour))

	if got, want := listedSections(fp.calls), map[string]bool{"1": true, "2": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("listed sections = %v, want %v", got, want)
	}
	warnings := f.logLines("warn", "Skipping library with unsupported agent")
	if len(warnings) != 1 {
		t.Fatalf("got %d unsupported agent warnings, want 1", len(warnings))
	}
	wantField(t, warnings[0], "server", testServer)
	wantField(t, warnings[0], "library", "Home Videos")
	wantField(t, warnings[0], "agent", "tv.plex.agents.none")
}

func TestRunUsesConfiguredLibraries(t *testing.T) {
	fp := newFakePlex(moviesLib, showsLib)
	f := newFixture(t, Server{Name: testServer, Plex: fp, Libraries: []string{"Movies", "Anime"}})

	f.run(t0)
	f.run(t0.Add(time.Hour))

	if got, want := listedSections(fp.calls), map[string]bool{"1": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("listed sections = %v, want %v", got, want)
	}
	warnings := f.logLines("warn", "Configured library not found")
	if len(warnings) != 1 {
		t.Fatalf("got %d missing library warnings, want 1", len(warnings))
	}
	wantField(t, warnings[0], "library", "Anime")
}

func TestFirstRunDoesFullRead(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), completeItem("102")}
	f := newFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	wantCalls(t, fp.calls, "Libraries", "List 1?type=1")
	want := []store.Item{{
		Server: testServer, Section: "1", RatingKey: "101", Type: "movie",
		Title: "Title 101", Year: 2020, AddedAt: addedAt, Reasons: store.NoSummary,
	}}
	if got := f.rows(testServer, "1"); !reflect.DeepEqual(got, want) {
		t.Errorf("rows:\n got %+v\nwant %+v", got, want)
	}
	lib := f.library(testServer, "1")
	if !lib.LastFullRead.Equal(t0) || !lib.LastRead.Equal(t0) {
		t.Errorf("library times = %+v, want both %v", lib, t0)
	}

	reads := f.logLines("debug", "Read library")
	if len(reads) != 1 {
		t.Fatalf("got %d read lines, want 1", len(reads))
	}
	wantField(t, reads[0], "server", testServer)
	wantField(t, reads[0], "library", "Movies")
	wantField(t, reads[0], "kind", "full")
	wantField(t, reads[0], "items", float64(2))
	wantField(t, reads[0], "incomplete", float64(1))
	if _, ok := reads[0]["duration"]; !ok {
		t.Error("read line has no duration")
	}
}

func TestFullReadChecksUnseenRows(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{
		incompleteItem("101"), incompleteItem("102"), incompleteItem("103"), incompleteItem("104"),
	}
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.run(t0)

	noPoster := completeItem("103")
	noPoster.Thumb = ""
	fp.lists["1?type=1"] = []plex.Item{noPoster}
	fp.pathErrs["101"] = fmt.Errorf("item paths: %w", plex.ErrNotFound)
	fp.paths["102"] = []string{"/movies/Title 102/Title 102.mkv"}
	fp.pathErrs["104"] = errors.New("item paths: GET /library/metadata/104: 500 Internal Server Error")
	fp.calls = nil
	f.run(sunday0300)

	wantCalls(t, fp.calls, "Libraries", "List 1?type=1", "ItemPaths 101", "ItemPaths 102", "ItemPaths 104")
	want := map[string]store.Reasons{"102": store.NoSummary, "103": store.NoPoster, "104": store.NoSummary}
	if got := f.reasons("1"); !reflect.DeepEqual(got, want) {
		t.Errorf("reasons = %v, want %v", got, want)
	}
	if lib := f.library(testServer, "1"); !lib.LastFullRead.Equal(sunday0300) {
		t.Errorf("LastFullRead = %v, want %v", lib.LastFullRead, sunday0300)
	}
	if fixed := f.logLines("info", "Item complete"); len(fixed) != 0 {
		t.Errorf("got complete lines %v, want none", fixed)
	}
	warnings := f.logLines("warn", "Checking unseen item failed")
	if len(warnings) != 1 {
		t.Fatalf("got %d unseen item warnings, want 1", len(warnings))
	}
	wantField(t, warnings[0], "title", "Title 104")
}

func TestIncrementalReadFilters(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.run(t0)

	since := unixString(t0.Add(-48 * time.Hour))
	unmatched := incompleteItem("105")
	unmatched.GUID = "local://105"
	fp.lists["1?type=1&updatedAt>>="+since] = []plex.Item{incompleteItem("104")}
	fp.lists["1?type=1&addedAt>>="+since] = []plex.Item{incompleteItem("104")}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{unmatched}
	fp.calls = nil
	f.run(t0.Add(time.Hour))

	wantCalls(t, fp.calls,
		"Libraries",
		"List 1?type=1&updatedAt>>="+since,
		"List 1?type=1&addedAt>>="+since,
		"List 1?type=1&unmatched=1",
	)
	lib := f.library(testServer, "1")
	if !lib.LastFullRead.Equal(t0) || !lib.LastRead.Equal(t0.Add(time.Hour)) {
		t.Errorf("library times = %+v, want full %v and read %v", lib, t0, t0.Add(time.Hour))
	}
	if got, want := keys(f.rows(testServer, "1")), []string{"101", "104", "105"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	reads := f.logLines("debug", "Read library")
	if len(reads) != 2 {
		t.Fatalf("got %d read lines, want 2", len(reads))
	}
	last := reads[1]
	wantField(t, last, "kind", "incremental")
	wantField(t, last, "items", float64(2))
}

func TestFullReadAfterCacheRefresh(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.run(t0)

	fp.calls = nil
	f.run(sunday0259)
	since := unixString(t0.Add(-oversample))
	wantCalls(t, fp.calls,
		"Libraries",
		"List 1?type=1&updatedAt>>="+since,
		"List 1?type=1&addedAt>>="+since,
		"List 1?type=1&unmatched=1",
	)
	if lib := f.library(testServer, "1"); !lib.LastFullRead.Equal(t0) {
		t.Errorf("LastFullRead after 02:59 = %v, want %v", lib.LastFullRead, t0)
	}

	fp.calls = nil
	f.run(sunday0300)
	wantCalls(t, fp.calls, "Libraries", "List 1?type=1")
	if lib := f.library(testServer, "1"); !lib.LastFullRead.Equal(sunday0300) {
		t.Errorf("LastFullRead after 03:00 = %v, want %v", lib.LastFullRead, sunday0300)
	}
}

func TestEpisodeRequestFilters(t *testing.T) {
	fp := newFakePlex(showsLib)
	noSummary, unmatched := episodeKeys(t0)
	fp.lists["2?type=2"] = []plex.Item{completeItem("201"), completeItem("202")}
	fp.lists[noSummary] = []plex.Item{completeItem("201"), completeItem("202")}
	fp.lists[unmatched] = []plex.Item{completeItem("202")}
	f := newFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	a := unixString(t0.Add(-minimumAge))
	wantCalls(t, fp.calls,
		"Libraries",
		"List 2?type=2",
		"List 2?type=2&unmatched=0&episode.unmatched=0&episode.summary=&episode.addedAt<<="+a+"&season.index>>=1",
		"List 2?type=2&unmatched=0&episode.unmatched=1&episode.addedAt<<="+a+"&season.index>>=1",
	)
	want := map[string]store.Reasons{
		"201": store.EpisodeNoSummary,
		"202": store.EpisodeNoSummary | store.EpisodeUnmatched,
	}
	if got := f.reasons("2"); !reflect.DeepEqual(got, want) {
		t.Errorf("reasons after first run = %v, want %v", got, want)
	}

	next := t0.Add(time.Hour)
	noSummary, unmatched = episodeKeys(next)
	fp.lists[noSummary] = []plex.Item{completeItem("201")}
	fp.lists[unmatched] = []plex.Item{completeItem("202")}
	fp.calls = nil
	f.run(next)

	since := unixString(t0.Add(-oversample))
	wantCalls(t, fp.calls,
		"Libraries",
		"List 2?type=2&updatedAt>>="+since,
		"List 2?type=2&addedAt>>="+since,
		"List 2?type=2&unmatched=1",
		"List "+noSummary,
		"List "+unmatched,
	)
	want = map[string]store.Reasons{"201": store.EpisodeNoSummary, "202": store.EpisodeUnmatched}
	if got := f.reasons("2"); !reflect.DeepEqual(got, want) {
		t.Errorf("reasons after second run = %v, want %v", got, want)
	}
}

func TestFailedReadKeepsState(t *testing.T) {
	noSummaryAt0, _ := episodeKeys(t0)
	noSummaryAtRefresh, unmatchedAtRefresh := episodeKeys(sunday0300)
	hourLater := t0.Add(time.Hour)
	since := unixString(t0.Add(-oversample))
	tests := []struct {
		name          string
		at            time.Time // sunday0300 gives a full read, hourLater an incremental read
		failKey       string
		checkedUnseen bool
	}{
		{"full listing fails", sunday0300, "2?type=2", false},
		{"first episode request fails", sunday0300, noSummaryAtRefresh, true},
		{"second episode request fails", sunday0300, unmatchedAtRefresh, true},
		{"second incremental listing fails", hourLater, "2?type=2&addedAt>>=" + since, false},
		{"third incremental listing fails", hourLater, "2?type=2&unmatched=1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := newFakePlex(showsLib, moviesLib)
			fp.lists["2?type=2"] = []plex.Item{incompleteItem("201"), incompleteItem("202")}
			fp.lists[noSummaryAt0] = []plex.Item{completeItem("202")}
			fp.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
			f := newFixture(t, Server{Name: testServer, Plex: fp})
			f.run(t0)
			rowsBefore := f.rows(testServer, "2")
			libBefore := f.library(testServer, "2")

			// A partial apply would fix 201, delete the unseen row 202 on a
			// full read, and drop the episode reason of 202.
			fp.lists["2?type=2"] = []plex.Item{completeItem("201")}
			fp.lists["2?type=2&updatedAt>>="+since] = []plex.Item{completeItem("201")}
			fp.pathErrs["202"] = fmt.Errorf("item paths: %w", plex.ErrNotFound)
			fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
			fp.lists["1?type=1&updatedAt>>="+since] = []plex.Item{incompleteItem("102")}
			fp.listErrs[tt.failKey] = errors.New("listing section 2: decoding response: unexpected token")
			fp.calls = nil
			f.run(tt.at)

			if !containsCall(fp.calls, "List "+tt.failKey) {
				t.Fatalf("the failing request was not sent: %q", fp.calls)
			}
			if got := f.rows(testServer, "2"); !reflect.DeepEqual(got, rowsBefore) {
				t.Errorf("rows of the failed library:\n got %+v\nwant %+v", got, rowsBefore)
			}
			lib := f.library(testServer, "2")
			if !lib.LastFullRead.Equal(libBefore.LastFullRead) || !lib.LastRead.Equal(libBefore.LastRead) {
				t.Errorf("library times = %+v, want %+v", lib, libBefore)
			}
			if got := containsCall(fp.calls, "ItemPaths 202"); got != tt.checkedUnseen {
				t.Errorf("unseen row checked = %v, want %v", got, tt.checkedUnseen)
			}
			if fixed := f.logLines("info", "Item complete"); len(fixed) != 0 {
				t.Errorf("got complete lines %v, want none", fixed)
			}
			warnings := f.logLines("warn", "Reading library failed")
			if len(warnings) != 1 {
				t.Fatalf("got %d read failure warnings, want 1", len(warnings))
			}
			wantField(t, warnings[0], "library", "TV")

			if got, want := keys(f.rows(testServer, "1")), []string{"101", "102"}; !reflect.DeepEqual(got, want) {
				t.Errorf("rows of the next library = %v, want %v", got, want)
			}
			if lib := f.library(testServer, "1"); !lib.LastRead.Equal(tt.at) {
				t.Errorf("LastRead of the next library = %v, want %v", lib.LastRead, tt.at)
			}
		})
	}
}

func containsCall(calls []string, call string) bool {
	for _, c := range calls {
		if c == call {
			return true
		}
	}
	return false
}

func TestUnavailableSkipsServer(t *testing.T) {
	tests := []struct {
		name string
		fail func(*fakePlex)
	}{
		{"libraries", func(fp *fakePlex) { fp.librariesErr = fmt.Errorf("libraries: %w", plex.ErrUnavailable) }},
		{"list", func(fp *fakePlex) {
			fp.listErrs["1?type=1"] = fmt.Errorf("listing section 1: %w", plex.ErrUnavailable)
		}},
		{"item paths", func(fp *fakePlex) {
			fp.pathErrs["100"] = fmt.Errorf("item paths: %w", plex.ErrUnavailable)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			down := newFakePlex(moviesLib, showsLib)
			tt.fail(down)
			up := newFakePlex(moviesLib)
			up.lists["1?type=1"] = []plex.Item{incompleteItem("101")}
			f := newFixture(t, Server{Name: "down", Plex: down}, Server{Name: "up", Plex: up})

			// A full read a week ago stored row 100. The listing of this run
			// does not return it, so the full read checks it with ItemPaths.
			_, err := f.store.ApplyRead(store.Read{
				Server: "down", Section: "1", At: t0.Add(-7 * 24 * time.Hour), Full: true,
				Items: []store.Item{{
					Server: "down", Section: "1", RatingKey: "100", Type: "movie",
					Title: "Title 100", AddedAt: addedAt, Reasons: store.NoSummary,
				}},
			})
			if err != nil {
				t.Fatalf("seed store: %v", err)
			}
			rowsBefore := f.rows("down", "1")
			libBefore := f.library("down", "1")

			f.run(t0)

			if listedSections(down.calls)["2"] {
				t.Errorf("the unavailable server read its second library: %q", down.calls)
			}
			if got := f.rows("down", "1"); !reflect.DeepEqual(got, rowsBefore) {
				t.Errorf("rows of the unavailable server:\n got %+v\nwant %+v", got, rowsBefore)
			}
			if lib := f.library("down", "1"); !lib.LastFullRead.Equal(libBefore.LastFullRead) || !lib.LastRead.Equal(libBefore.LastRead) {
				t.Errorf("library times of the unavailable server = %+v, want %+v", lib, libBefore)
			}
			errs := f.logLines("error", "Plex server unavailable")
			if len(errs) != 1 {
				t.Fatalf("got %d unavailable errors, want 1", len(errs))
			}
			wantField(t, errs[0], "server", "down")
			if got := keys(f.rows("up", "1")); !reflect.DeepEqual(got, []string{"101"}) {
				t.Errorf("rows of the next server = %v, want [101]", got)
			}
		})
	}
}

func TestCancelStopsRunQuietly(t *testing.T) {
	firstIncremental := "List 1?type=1&updatedAt>>=" + unixString(t0.Add(-oversample))
	noSummary, _ := episodeKeys(t0)
	tests := []struct {
		name       string
		prior      map[string][]plex.Item // if set, the listings of a run at t0 before the canceled run
		at         time.Time              // the time of the canceled run
		cancelCall string                 // the call that cancels the context
		listErr    error                  // the error of that call, if it is a List call
		want       []string
	}{
		{
			name: "between libraries", at: t0, cancelCall: "List 1?type=1",
			want: []string{"Libraries", "List 1?type=1"},
		},
		{
			name: "during a request", at: t0, cancelCall: "List 1?type=1",
			listErr: fmt.Errorf("listing section 1: %w: %w", plex.ErrUnavailable, context.Canceled),
			want:    []string{"Libraries", "List 1?type=1"},
		},
		{
			name: "plain error after cancel", at: t0, cancelCall: "List 1?type=1",
			listErr: errors.New("listing section 1: decoding response: unexpected EOF"),
			want:    []string{"Libraries", "List 1?type=1"},
		},
		{
			name: "after the first incremental listing", prior: map[string][]plex.Item{},
			at: t0.Add(time.Hour), cancelCall: firstIncremental,
			want: []string{"Libraries", firstIncremental},
		},
		{
			name: "after the first episode request", at: t0, cancelCall: "List " + noSummary,
			want: []string{"Libraries", "List 1?type=1", "List 2?type=2", "List " + noSummary},
		},
		{
			name:  "after the first unseen check",
			prior: map[string][]plex.Item{"1?type=1": {incompleteItem("101"), incompleteItem("102")}},
			at:    sunday0300, cancelCall: "ItemPaths 101",
			want: []string{"Libraries", "List 1?type=1", "ItemPaths 101"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := newFakePlex(moviesLib, showsLib)
			f := newFixture(t, Server{Name: "first", Plex: first})
			if tt.prior != nil {
				first.lists = tt.prior
				f.run(t0)
				first.lists = map[string][]plex.Item{}
				first.calls = nil
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first.onCall = func(call string) {
				if call == tt.cancelCall {
					cancel()
				}
			}
			if tt.listErr != nil {
				first.listErrs[strings.TrimPrefix(tt.cancelCall, "List ")] = tt.listErr
			}
			f.now = tt.at
			if err := f.r.Run(ctx); err != nil {
				t.Fatalf("Run = %v, want nil", err)
			}

			wantCalls(t, first.calls, tt.want...)
			if strings.Contains(f.logs.String(), `"level":"error"`) {
				t.Errorf("got error lines, want none:\n%s", f.logs)
			}
			if warnings := f.logLines("warn", "Reading library failed"); len(warnings) != 0 {
				t.Errorf("got read failure warnings %v, want none", warnings)
			}
		})
	}
}

func TestCompleteItemLogsFixed(t *testing.T) {
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{incompleteItem("101"), incompleteItem("102")}
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.run(t0)
	f.refreshed("101", 2)

	fp.lists["1?type=1&updatedAt>>="+unixString(t0.Add(-oversample))] = []plex.Item{completeItem("101"), completeItem("102")}
	f.run(t0.Add(time.Hour))

	if rows := f.rows(testServer, "1"); len(rows) != 0 {
		t.Errorf("rows = %+v, want none", rows)
	}
	fixed := f.logLines("info", "Item complete")
	if len(fixed) != 2 {
		t.Fatalf("got %d complete lines, want 2", len(fixed))
	}
	wantField(t, fixed[0], "server", testServer)
	wantField(t, fixed[0], "library", "Movies")
	wantField(t, fixed[0], "title", "Title 101")
	wantField(t, fixed[0], "refreshes", float64(2))
	// Only 101 had a request from autorefresh.
	summary := f.lastSummary(testServer)
	wantField(t, summary, "completed_items", float64(2))
	wantField(t, summary, "completed_after_request", float64(1))
}

func TestCompleteItemKeepsEpisodeReason(t *testing.T) {
	fp := newFakePlex(showsLib)
	noArt := completeItem("201")
	noArt.Art = ""
	fp.lists["2?type=2"] = []plex.Item{noArt}
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.run(t0)
	f.refreshed("201", 2)

	noSummary, _ := episodeKeys(sunday0300)
	fp.lists["2?type=2"] = []plex.Item{completeItem("201")}
	fp.lists[noSummary] = []plex.Item{completeItem("201")}
	f.run(sunday0300)

	rows := f.rows(testServer, "2")
	if len(rows) != 1 || rows[0].Reasons != store.EpisodeNoSummary || rows[0].Refreshes != 2 {
		t.Errorf("rows = %+v, want 201 with episode-no-summary and 2 refreshes", rows)
	}
	if fixed := f.logLines("info", "Item complete"); len(fixed) != 0 {
		t.Errorf("got complete lines %v, want none", fixed)
	}
}

func TestYoungItemLogsDebug(t *testing.T) {
	young := incompleteItem("101")
	young.AddedAt = t0.Add(-30 * time.Minute)
	youngComplete := completeItem("103")
	youngComplete.AddedAt = t0.Add(-30 * time.Minute)
	fp := newFakePlex(moviesLib)
	fp.lists["1?type=1"] = []plex.Item{young, incompleteItem("102"), youngComplete}
	f := newFixture(t, Server{Name: testServer, Plex: fp})

	f.run(t0)

	if got, want := keys(f.rows(testServer, "1")), []string{"101", "102"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	lines := f.logLines("debug", "Item too young to refresh")
	if len(lines) != 1 {
		t.Fatalf("got %d young item lines, want 1", len(lines))
	}
	wantField(t, lines[0], "title", "Title 101")
}

func TestRunChecksServersInParallel(t *testing.T) {
	a, b := newFakePlex(moviesLib), newFakePlex(moviesLib)
	aCalled, bCalled := make(chan struct{}), make(chan struct{})
	// Each server waits in its Libraries call until the other server makes
	// its own Libraries call. Servers that run one after another time out.
	wait := func(own, other chan struct{}) func(string) {
		return func(call string) {
			if call != "Libraries" {
				return
			}
			close(own)
			select {
			case <-other:
			case <-time.After(5 * time.Second):
				t.Error("the other server did not start while this server was busy")
			}
		}
	}
	a.onCall, b.onCall = wait(aCalled, bCalled), wait(bCalled, aCalled)
	f := newFixture(t, Server{Name: "a", Plex: a}, Server{Name: "b", Plex: b})

	f.run(t0)

	if got := len(f.logLines("info", "Run summary")); got != 2 {
		t.Errorf("got %d summary lines, want 2", got)
	}
}

func TestStoreErrorEndsRun(t *testing.T) {
	fp := newFakePlex(moviesLib)
	f := newFixture(t, Server{Name: testServer, Plex: fp})
	f.store.Close()

	f.now = t0
	if err := f.r.Run(context.Background()); err == nil {
		t.Error("Run with a closed store returned no error")
	}
}
