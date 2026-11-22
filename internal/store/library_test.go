package store

import (
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
