package store

import (
	"database/sql"
	"os"
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

// The data source name escapes the path, so a character such as "?" or "#"
// stays part of the file name and the settings still apply.
func TestOpenAcceptsAnyPath(t *testing.T) {
	for _, name := range []string{"we?rd.db", "per%cent.db", "hash#tag.db", "with space.db"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			s.Close()
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatalf("database file: %v", err)
			}
		})
	}
}

func TestOpenAcceptsRelativePath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s, err := Open(filepath.Join("sub", "..", "store.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, "store.db")); err != nil {
		t.Fatalf("database file: %v", err)
	}
}

// The container image has no writable temporary directory, so a large sort
// must stay in memory.
func TestOpenKeepsTemporaryDataInMemory(t *testing.T) {
	s := openTest(t)
	var store int
	if err := s.db.QueryRow(`PRAGMA temp_store`).Scan(&store); err != nil {
		t.Fatal(err)
	}
	if store != 2 {
		t.Fatalf("temp_store = %d, want 2 (memory)", store)
	}
}

// A second process, such as a dry run, waits for the write lock of the
// first one instead of failing at once.
func TestSecondStoreWaitsForLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.ApplyRead(Read{Server: "s", Section: "1", At: base, Full: true}); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		call func() error
	}{
		{"ApplyRead", func() error {
			_, err := second.ApplyRead(Read{Server: "s", Section: "1", At: base, Full: true})
			return err
		}},
		{"PruneSections", func() error {
			_, err := second.PruneSections("s", []string{"1"})
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := first.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO library (server, section) VALUES ('other', '1')
				ON CONFLICT DO NOTHING`); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- tt.call() }()
			time.Sleep(200 * time.Millisecond)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf("%s while another store held the lock: %v", tt.name, err)
			}
		})
	}
}

// rebuildLibrary rebuilds the library table the way SQLite needs it for a
// schema change: create a new table, copy the rows, drop the old table, and
// rename the new one.
const rebuildLibrary = `
CREATE TABLE library_new (
    server TEXT NOT NULL,
    section TEXT NOT NULL,
    last_full_read INTEGER,
    last_read INTEGER,
    PRIMARY KEY (server, section)
);
INSERT INTO library_new SELECT server, section, last_full_read, last_read FROM library;
DROP TABLE library;
ALTER TABLE library_new RENAME TO library;
`

// A migration that rebuilds a parent table must not cascade-delete the rows
// of its child tables.
func TestMigrationRebuildKeepsChildRows(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", Unmatched)}})
	if err := s.SaveTMDBID(srv, "a", "tvdb", "5", "77"); err != nil {
		t.Fatal(err)
	}

	if err := applyMigration(s.db, 99, rebuildLibrary); err != nil {
		t.Fatalf("applyMigration: %v", err)
	}

	if got := rows(t, s); len(got) != 1 {
		t.Errorf("item rows = %v, want a", got)
	}
	if _, ok, err := s.TMDBID(srv, "a", "tvdb", "5"); err != nil || !ok {
		t.Errorf("cached TMDB ID found = %v, %v, want it kept", ok, err)
	}
	var on int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Errorf("foreign_keys after the migration = %d, %v, want 1", on, err)
	}
}

func TestMigrationRejectsBrokenForeignKey(t *testing.T) {
	s := openTest(t)
	script := `INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons)
		VALUES ('home', '9', '101', 'movie', 'Title 101', 2020, 1000, 2)`

	if err := applyMigration(s.db, 99, script); err == nil {
		t.Fatal("applyMigration succeeded, want a foreign key error")
	}
	if items, err := s.Unseen("home", "9", nil); err != nil || len(items) != 0 {
		t.Errorf("items = %v, %v, want none after the rollback", items, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migration WHERE version = 99`).Scan(&n); err != nil || n != 0 {
		t.Errorf("schema_migration rows of 99 = %d, %v, want 0", n, err)
	}
}
