package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func libraryMeta(t *testing.T, s *Store) (title, typ, agent, lang string, full, read int64) {
	t.Helper()
	err := s.db.QueryRow(`SELECT title, type, agent, language, COALESCE(last_full_read, 0), COALESCE(last_read, 0)
		FROM library WHERE server = ? AND section = ?`, srv, sec).Scan(&title, &typ, &agent, &lang, &full, &read)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestSaveLibrariesUpsertsMetadataKeepsReadTimes(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Full: true})
	for _, title := range []string{"Old", "TV"} {
		err := s.SaveLibraries(srv, []LibraryInfo{{Section: sec, Title: title, Type: "show", Agent: "tv.plex.agents.series", Language: "en-US"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	title, typ, agent, lang, full, read := libraryMeta(t, s)
	if title != "TV" || typ != "show" || agent != "tv.plex.agents.series" || lang != "en-US" {
		t.Fatalf("metadata = %q %q %q %q", title, typ, agent, lang)
	}
	if full != base.Unix() || read != base.Unix() {
		t.Fatalf("read times = %d %d, want %d", full, read, base.Unix())
	}
}

func TestSaveLibrariesInsertsMissingRowWithoutReadTimes(t *testing.T) {
	s := openTest(t)
	if err := s.SaveLibraries(srv, []LibraryInfo{{Section: sec, Title: "Movies"}}); err != nil {
		t.Fatal(err)
	}
	lib, err := s.Library(srv, sec)
	if err != nil || !lib.LastFullRead.Equal(time.Time{}) || !lib.LastRead.Equal(time.Time{}) {
		t.Fatalf("Library = %+v, %v, want zero times", lib, err)
	}
	if title, _, _, _, _, _ := libraryMeta(t, s); title != "Movies" {
		t.Fatalf("title = %q", title)
	}
}

func TestSetPathIgnoresMissingRow(t *testing.T) {
	s := openTest(t)
	if err := s.SetPath(srv, "nope", "/x"); err != nil {
		t.Fatalf("SetPath = %v, want nil", err)
	}
}

func TestLibrariesListsMetadataAndReadTimes(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Full: true})
	err := s.SaveLibraries(srv, []LibraryInfo{
		{Section: "2", Title: "Movies", Type: "movie", Agent: "tv.plex.agents.movie", Language: "en-US"},
		{Section: sec, Title: "TV", Type: "show", Agent: "tv.plex.agents.series", Language: "de-DE"},
		{Section: "3", Title: "Movies", Type: "movie"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveLibraries("srv2", []LibraryInfo{{Section: "9", Title: "Other"}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Libraries(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	want := []LibraryEntry{
		{LibraryInfo{Section: "2", Title: "Movies", Type: "movie", Agent: "tv.plex.agents.movie", Language: "en-US"}, Library{}},
		{LibraryInfo{Section: "3", Title: "Movies", Type: "movie"}, Library{}},
		{LibraryInfo{Section: sec, Title: "TV", Type: "show", Agent: "tv.plex.agents.series", Language: "de-DE"}, Library{LastFullRead: base, LastRead: base}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Libraries = %+v, want %+v", got, want)
	}

	none, err := s.Libraries(context.Background(), "unknown")
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("Libraries of an unknown server = %#v, %v, want an empty slice", none, err)
	}
}

func TestFillPath(t *testing.T) {
	s := openTest(t)
	kept := item("kept", NoArt)
	kept.Path = "/old"
	apply(t, s, Read{Items: []Item{item("empty", NoArt), kept}})

	for _, tt := range []struct{ key, want string }{
		{"empty", "/new"}, // fills the empty path
		{"kept", "/old"},  // keeps the path that a run saved
		{"missing", ""},   // no row, no error
	} {
		got, err := s.FillPath(context.Background(), srv, tt.key, "/new")
		if err != nil || got != tt.want {
			t.Errorf("FillPath %s = %q, %v, want %q, nil", tt.key, got, err, tt.want)
		}
	}
	got := rows(t, s)
	if got["empty"].Path != "/new" || got["kept"].Path != "/old" {
		t.Fatalf("paths = %q, %q, want /new, /old", got["empty"].Path, got["kept"].Path)
	}
	if _, ok := got["missing"]; ok {
		t.Fatal("FillPath created a row")
	}
}

func TestFillPathStopsBusyWaitWhenCanceled(t *testing.T) {
	file := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	other, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	// The DSN begins each transaction immediate, so this one holds the
	// write lock of the file.
	tx, err := other.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = s.FillPath(ctx, srv, "a", "/x")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FillPath = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("FillPath returned after %v, want within 2s", d)
	}
}
