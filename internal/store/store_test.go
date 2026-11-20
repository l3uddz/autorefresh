package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenMigratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open %d: %v", i, err)
		}
		if i == 1 {
			var n int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migration`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			list, err := parseMigrations()
			if err != nil {
				t.Fatal(err)
			}
			if n != len(list) {
				t.Fatalf("schema_migration rows = %d, want %d", n, len(list))
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplyReadIncrementalKeepsFullRead(t *testing.T) {
	s := openTest(t)
	t1 := time.Unix(1000, 0)
	t2 := time.Unix(2000, 0)
	if _, err := s.ApplyRead(Read{Server: "s", Section: "1", At: t1, Full: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyRead(Read{Server: "s", Section: "1", At: t2}); err != nil {
		t.Fatal(err)
	}
	lib, err := s.Library("s", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !lib.LastFullRead.Equal(t1) || !lib.LastRead.Equal(t2) {
		t.Fatalf("library = %+v, want full %v read %v", lib, t1, t2)
	}
}

func TestLibraryMissing(t *testing.T) {
	s := openTest(t)
	lib, err := s.Library("s", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !lib.LastFullRead.IsZero() || !lib.LastRead.IsZero() {
		t.Fatalf("library = %+v, want zero", lib)
	}
}

func TestOpenEnablesForeignKeys(t *testing.T) {
	s := openTest(t)
	var on int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 1 {
		t.Fatalf("foreign_keys = %d, want 1", on)
	}
}

// openVersion1 creates a database with only the first migration and one
// item row of home/9 without a library row. It returns the file path.
func openVersion1(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	script, err := migrations.ReadFile("migrations/1_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		string(script),
		`CREATE TABLE schema_migration (version INTEGER PRIMARY KEY)`,
		`INSERT INTO schema_migration (version) VALUES (1)`,
		`INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons, refreshes)
			VALUES ('home', '9', '101', 'movie', 'Title 101', 2020, 1000, 2, 3)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return file
}

// libraryRows returns the number of library rows of server and section.
func libraryRows(t *testing.T, s *Store, server, section string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM library WHERE server = ? AND section = ?`, server, section).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrationKeepsItemsWithoutLibrary(t *testing.T) {
	s, err := Open(openVersion1(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if n := libraryRows(t, s, "home", "9"); n != 1 {
		t.Fatalf("library rows of home/9 = %d, want 1", n)
	}

	items, err := s.Unseen("home", "9", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RatingKey != "101" || items[0].Refreshes != 3 {
		t.Fatalf("items = %+v, want 101 with 3 refreshes", items)
	}
	lib, err := s.Library("home", "9")
	if err != nil {
		t.Fatal(err)
	}
	if !lib.LastRead.IsZero() || !lib.LastFullRead.IsZero() {
		t.Errorf("library = %+v, want no read times", lib)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("foreign_key_check found a violation")
	}
}

func TestPruneRemovesMigratedLibrary(t *testing.T) {
	s, err := Open(openVersion1(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if n := libraryRows(t, s, "home", "9"); n != 1 {
		t.Fatalf("library rows of home/9 = %d, want 1", n)
	}

	pruned, err := s.PruneSections("home", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pruned{{Server: "home", Section: "9", Items: 1}}; !reflect.DeepEqual(pruned, want) {
		t.Errorf("pruned = %+v, want %+v", pruned, want)
	}
	if items, err := s.Unseen("home", "9", nil); err != nil || len(items) != 0 {
		t.Errorf("items = %+v, %v, want none", items, err)
	}
}

func TestItemNeedsLibrary(t *testing.T) {
	s := openTest(t)
	_, err := s.db.Exec(`INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons)
		VALUES ('home', '9', '101', 'movie', 'Title 101', 2020, 1000, 2)`)
	if err == nil {
		t.Fatal("insert of an item without a library row succeeded")
	}
}

// A "?" in the path ends the file name for the driver, and the rest turns
// into parameters, so foreign keys would be off without an error.
func TestOpenRejectsPathWithoutForeignKeys(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "we?rd.db"))
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded, want an error")
	}
}
