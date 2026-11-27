package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// The attempts of an older row start at its refreshes, so the order of the
// queue stays the same after the upgrade.
func TestMigrationCountsRefreshesAsAttempts(t *testing.T) {
	s, err := Open(openVersion1(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var attempts int
	if err := s.db.QueryRow(`SELECT attempts FROM item WHERE server = 'home' AND rating_key = '101'`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want the 3 refreshes", attempts)
	}
}

// An older row has refreshes but no recorded action, so its actions stay
// empty instead of a guess.
func TestMigrationLeavesActionsEmpty(t *testing.T) {
	s, err := Open(openVersion1(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	items, err := s.Unseen("home", "9", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Refreshes != 3 || items[0].LastAction != "" || items[0].LastMatch != "" {
		t.Fatalf("items = %+v, want 101 with 3 refreshes and no actions", items)
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

func TestOpenEnablesWALAndIndexesUseQueryPlans(t *testing.T) {
	s := openTest(t)
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var syncMode int
	if err := s.db.QueryRow(`PRAGMA synchronous`).Scan(&syncMode); err != nil {
		t.Fatal(err)
	}
	if syncMode != 1 {
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", syncMode)
	}

	plan := func(query string, args ...any) string {
		t.Helper()
		rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
		if err != nil {
			t.Fatalf("explain %s: %v", query, err)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, detail)
		}
		return strings.Join(lines, "\n")
	}

	cq, cargs := countQuery(ListQuery{Tab: TabFixed, Servers: []string{"home"}})
	if p := plan(cq, cargs...); !strings.Contains(p, "event_completed_item") || strings.Contains(p, "library") {
		t.Errorf("fixed count plan:\n%s", p)
	}
	rq, rargs := runsQuery(RunQuery{Servers: []string{"home"}, Limit: 1})
	if p := plan(rq, rargs...); !strings.Contains(p, "run_server_started") || strings.Contains(p, "TEMP B-TREE") {
		t.Errorf("runs plan:\n%s", p)
	}
	if p := plan(`SELECT `+eventColumns+` FROM event WHERE run_id = ? ORDER BY at, id`, 1); !strings.Contains(p, "event_run") || strings.Contains(p, "TEMP B-TREE") {
		t.Errorf("runEvents plan:\n%s", p)
	}
	if p := plan(`DELETE FROM run WHERE status != ? AND COALESCE(finished_at, started_at) < ?`, RunRunning, 100); !strings.Contains(p, "run_prune") {
		t.Errorf("prune runs plan:\n%s", p)
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
		// The prune reads before it deletes. Without a write lock at the
		// start of the transaction, SQLite returns SQLITE_BUSY at once
		// instead of waiting. It runs last, because it removes the library.
		{"PruneSections", func() error {
			_, err := second.PruneSections("s", nil)
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

// An older binary must not use a database that a newer one migrated.
func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO schema_migration (version) VALUES (9999)`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "9999") {
		t.Fatalf("error = %v, want the schema version", err)
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

// A migration script that fails part way changes nothing: its earlier
// statements roll back, its version is not recorded, and foreign keys are
// on again.
func TestFailedMigrationScriptLeavesNoTrace(t *testing.T) {
	s := openTest(t)
	script := `CREATE TABLE half (a INTEGER); INSERT INTO missing_table VALUES (1);`

	if err := applyMigration(s.db, 99, script); err == nil {
		t.Fatal("applyMigration succeeded, want the error of the script")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'half'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tables named half = %d, %v, want 0 after the rollback", n, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migration WHERE version = 99`).Scan(&n); err != nil || n != 0 {
		t.Errorf("schema_migration rows of 99 = %d, %v, want 0", n, err)
	}
	var on int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Errorf("foreign_keys after the failed migration = %d, %v, want 1", on, err)
	}
}

// Open must not take over a file that is not a SQLite database, such as a
// wrong --database path, and must leave its content as it was.
func TestOpenRejectsFileThatIsNotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	content := []byte(strings.Repeat("These are notes, not a database.\n", 200))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("Open succeeded, want an error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Error("Open changed the file")
	}
}

// Open does not create a missing directory. The CLI creates only the
// default config directory.
func TestOpenFailsInMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")

	if s, err := Open(filepath.Join(dir, "store.db")); err == nil {
		s.Close()
		t.Fatal("Open succeeded, want an error")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("Open created the missing directory")
	}
}

// Two processes opening a fresh or older database at the same time must both
// succeed instead of racing on the same migration version.
func TestOpenConcurrentMigrations(t *testing.T) {
	for _, tt := range []struct {
		name string
		path func(t *testing.T) string
	}{
		{"fresh database", func(t *testing.T) string { return filepath.Join(t.TempDir(), "store.db") }},
		{"upgrade from v1", openVersion1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path(t)
			start := make(chan struct{})
			errs := make(chan error, 2)
			for range 2 {
				go func() {
					<-start
					s, err := Open(path)
					if err == nil {
						err = s.Close()
					}
					errs <- err
				}()
			}
			close(start)
			for range 2 {
				if err := <-errs; err != nil {
					t.Errorf("concurrent Open failed: %v", err)
				}
			}
		})
	}
}

func TestApplyMigrationSkipsAlreadyAppliedVersion(t *testing.T) {
	s := openTest(t)
	if err := applyMigration(s.db, 1, `INVALID SQL THAT MUST NOT RUN`); err != nil {
		t.Fatalf("applyMigration on already-applied version 1 = %v, want nil", err)
	}
}

// A database failure must reach the caller, so the refresher can stop the
// run instead of acting on a cache that it did not read or write.
func TestMethodsReportDatabaseFailure(t *testing.T) {
	s := openTest(t)
	s.Close()

	calls := map[string]func() error{
		"Library":   func() error { _, err := s.Library(srv, sec); return err },
		"EventMark": func() error { _, err := s.EventMark(); return err },
		"ApplyRead": func() error {
			_, err := s.ApplyRead(Read{Server: srv, Section: sec, At: base})
			return err
		},
		"Unseen": func() error { _, err := s.Unseen(srv, sec, nil); return err },
		"Due": func() error {
			_, _, err := s.Due(DueQuery{Server: srv, Sections: []string{sec}, Now: base})
			return err
		},
		"CountIncomplete": func() error { _, err := s.CountIncomplete(srv, []string{sec}, 0); return err },
		"ListItems": func() error {
			_, _, err := s.ListItems(context.Background(), ListQuery{Tab: TabAttention, Limit: 10, Rules: DueRules{Now: base}})
			return err
		},
		"CountItems": func() error {
			_, err := s.CountItems(context.Background(), ListQuery{Rules: DueRules{Now: base}})
			return err
		},
		"ItemDetail": func() error {
			_, _, err := s.ItemDetail(context.Background(), srv, "101", DueRules{Now: base})
			return err
		},
		"RecordRequest": func() error {
			return s.RecordRequest(Request{Server: srv, RatingKey: "101", At: base, Source: SourceAuto, Action: ActionRefresh})
		},
		"RecordGone": func() error { return s.RecordGone(srv, "101", base, SourceAuto, "", 0) },
		"RecordMatchDecision": func() error {
			return s.RecordMatchDecision(MatchDecision{Server: srv, RatingKey: "101", At: base, Source: SourceAuto, Outcome: DecisionNoIDs})
		},
		"StartRun": func() error {
			_, err := s.StartRun(RunStart{Server: srv, Kind: RunKindRun, Source: RunSourceManual, StartedAt: base})
			return err
		},
		"FinishRun": func() error {
			_, err := s.FinishRun(1, RunStart{Server: srv, StartedAt: base}, RunResult{FinishedAt: base, Status: RunFinished})
			return err
		},
		"KnownItem":     func() error { _, _, _, err := s.KnownItem(srv, "101"); return err },
		"KnownItems":    func() error { _, err := s.KnownItems(context.Background(), srv, []string{"101"}); return err },
		"SetStopped":    func() error { _, err := s.SetStopped(context.Background(), srv, "101", base, true); return err },
		"RecordManual":  func() error { return s.RecordManual(ManualRecord{Request: Request{Source: SourceManual}}) },
		"ApplyCheck":    func() error { _, err := s.ApplyCheck(srv, item("101", NoSummary), base, 0); return err },
		"LibraryInfo":   func() error { _, _, err := s.LibraryInfo(srv, sec); return err },
		"FinishJob":     func() error { _, err := s.FinishJob(1, RunStart{}, RunResult{}, nil); return err },
		"RunItems":      func() error { _, err := s.RunItems(context.Background(), 1); return err },
		"RunDetail":     func() error { _, _, err := s.RunDetail(context.Background(), 1); return err },
		"InterruptRuns": func() error { _, err := s.InterruptRuns(base); return err },
		"PruneHistory":  func() error { _, _, err := s.PruneHistory(base); return err },
		"Runs":          func() error { _, err := s.Runs(context.Background(), RunQuery{Limit: 10}); return err },
		"Timeline":      func() error { _, err := s.Timeline(srv, "101"); return err },
		"TimelineContext": func() error {
			_, err := s.TimelineContext(context.Background(), srv, "101")
			return err
		},
		"PruneServers":  func() error { _, err := s.PruneServers(nil); return err },
		"PruneSections": func() error { _, err := s.PruneSections(srv, nil); return err },
		"TMDBID":        func() error { _, _, err := s.TMDBID(srv, "101", "tvdb", "5"); return err },
		"SaveTMDBID":    func() error { return s.SaveTMDBID(srv, "101", "tvdb", "5", "77") },
		"SaveLibraries": func() error { return s.SaveLibraries(srv, []LibraryInfo{{Section: sec, Title: "TV"}}) },
		"SetPath":       func() error { return s.SetPath(srv, "101", "/x") },
		"FillPath":      func() error { _, err := s.FillPath(context.Background(), srv, "101", "/x"); return err },
		"Libraries":     func() error { _, err := s.Libraries(context.Background(), srv); return err },
		"ConvertAutoVacuum": func() error {
			_, err := s.ConvertAutoVacuum(context.Background())
			return err
		},
		"Reclaim":  func() error { _, err := s.Reclaim(context.Background()); return err },
		"Optimize": func() error { return s.Optimize(context.Background()) },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s on a closed database returned no error", name)
		}
	}
}

// openAtVersion creates schema_migration, applies migrations 1..v to a new
// file, and returns its path.
func openAtVersion(t *testing.T, v int) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migration (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	list, err := parseMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m.version > v {
			break
		}
		script, err := migrations.ReadFile("migrations/" + m.file)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyMigration(db, m.version, string(script)); err != nil {
			t.Fatalf("%s: %v", m.file, err)
		}
	}
	return file
}

// indexNames returns the names of the indexes of table.
func indexNames(t *testing.T, s *Store, table string) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM pragma_index_list(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

func TestMigrateAddsDashboardSchema(t *testing.T) {
	s := openTest(t)
	want := map[string][]string{
		"event":           {"event_item", "event_kind", "event_at", "event_library", "event_fixed_match", "event_completed_item", "event_run"},
		"item":            {"item_last_match_event"},
		"match_search":    {"match_search_event"},
		"match_candidate": {"match_candidate_search"},
		"run":             {"run_started", "run_server_started", "run_prune"},
	}
	for table, indexes := range want {
		got := indexNames(t, s, table)
		for _, name := range indexes {
			if !got[name] {
				t.Errorf("table %s has no index %s (have %v)", table, name, got)
			}
		}
	}
}

func TestMigrateFromVersion5KeepsActionHistory(t *testing.T) {
	file := openAtVersion(t, 5)
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO library (server, section) VALUES ('home', '9')`,
		`INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons, refreshes, attempts, last_action, last_match)
			VALUES ('home', '9', '101', 'movie', 'Title 101', 2020, 1000, 2, 2, 3, 'match', 'match')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := Open(file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	var refreshes, attempts int
	var action, match, thumb, outcome string
	var stopped, matchEvent sql.NullInt64
	err = s.db.QueryRow(`SELECT refreshes, attempts, last_action, last_match, thumb, stopped_at, last_outcome, last_match_event_id
		FROM item WHERE server = 'home' AND rating_key = '101'`).
		Scan(&refreshes, &attempts, &action, &match, &thumb, &stopped, &outcome, &matchEvent)
	if err != nil {
		t.Fatal(err)
	}
	if refreshes != 2 || attempts != 3 || action != "match" || match != "match" {
		t.Errorf("old values = %d %d %q %q, want 2 3 match match", refreshes, attempts, action, match)
	}
	if thumb != "" || outcome != "" || stopped.Valid || matchEvent.Valid {
		t.Errorf("new columns = %q %q %v %v, want empty and null", thumb, outcome, stopped, matchEvent)
	}
}

func TestDeletingLibraryCascadesHistory(t *testing.T) {
	s := openTest(t)
	for _, stmt := range []string{
		`INSERT INTO library (server, section) VALUES ('home', '9')`,
		`INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons)
			VALUES ('home', '9', '101', 'movie', 'Title 101', 2020, 1000, 2)`,
		`INSERT INTO event (id, server, section, rating_key, at, kind) VALUES (1, 'home', '9', '101', 2000, 'refresh')`,
		`INSERT INTO match_search (id, event_id, position, query) VALUES (1, 1, 0, 'Title')`,
		`INSERT INTO match_candidate (id, search_id, position, guid) VALUES (1, 1, 0, 'plex://movie/1')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM library`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"library", "item", "event", "match_search", "match_candidate"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after deleting the library, want 0", table, n)
		}
	}
}

func TestDeletingEventClearsFixedMatchReference(t *testing.T) {
	s := openTest(t)
	for _, stmt := range []string{
		`INSERT INTO library (server, section) VALUES ('home', '9')`,
		`INSERT INTO event (id, server, section, rating_key, at, kind) VALUES (1, 'home', '9', '101', 2000, 'refresh')`,
		`INSERT INTO event (id, server, section, rating_key, at, kind, fixed_match_event_id) VALUES (2, 'home', '9', '101', 2001, 'complete', 1)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM event WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var ref sql.NullInt64
	if err := s.db.QueryRow(`SELECT fixed_match_event_id FROM event WHERE id = 2`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if ref.Valid {
		t.Errorf("fixed_match_event_id = %v, want null", ref)
	}

	if _, err := s.db.Exec(`DELETE FROM event WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO event (server, section, rating_key, at, kind) VALUES ('home', '9', '101', 2002, 'refresh')`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if id <= 2 {
		t.Errorf("new event id = %d, want above the deleted highest id 2", id)
	}
}

func TestKnownItemsResolvesCachedHistoryAndMissing(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("cached", NoArt), item("history", NoArt)}})
	// Complete "history" so it leaves the item table but keeps an event snapshot.
	apply(t, s, Read{Items: []Item{item("cached", NoArt), item("history", 0)}, Full: true})

	got, err := s.KnownItems(context.Background(), srv, []string{"cached", "history", "missing", "cached"})
	if err != nil {
		t.Fatalf("KnownItems: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("KnownItems len = %d, want 2: %+v", len(got), got)
	}
	if !got["cached"].Cached || got["cached"].Item.RatingKey != "cached" {
		t.Errorf("cached = %+v", got["cached"])
	}
	if got["history"].Cached || got["history"].Item.RatingKey != "history" || got["history"].Item.Title != "Title history" {
		t.Errorf("history = %+v", got["history"])
	}
	if _, ok := got["missing"]; ok {
		t.Errorf("missing key should not be present: %+v", got["missing"])
	}
}

func TestSetStoppedRejectsZeroTime(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	if _, err := s.SetStopped(context.Background(), srv, "a", time.Time{}, true); err == nil {
		t.Fatal("SetStopped with zero time succeeded, want error")
	}
}

func TestApplyCheckPreservesAddedAtWhenZero(t *testing.T) {
	s := openTest(t)
	orig := item("a", NoArt)
	apply(t, s, Read{Items: []Item{orig}})
	check := Item{RatingKey: "a", Title: "Updated", Reasons: NoSummary}
	if _, err := s.ApplyCheck(srv, check, base.Add(time.Hour), 0); err != nil {
		t.Fatalf("ApplyCheck: %v", err)
	}
	got, cached, known, err := s.KnownItem(srv, "a")
	if err != nil || !cached || !known {
		t.Fatalf("KnownItem = %v, %v, %v", cached, known, err)
	}
	if !got.AddedAt.Equal(orig.AddedAt) {
		t.Errorf("AddedAt = %v, want %v", got.AddedAt, orig.AddedAt)
	}
}

func TestPruneSectionsClearsDanglingMatchEventPointer(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Section: "1", Items: []Item{item("a", Unmatched)}})
	if err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: base.Add(time.Minute), Source: SourceAuto, Action: ActionMatch, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	// Move item "a" into section "2" while its match event stays in section "1".
	apply(t, s, Read{Section: "2", Items: []Item{item("a", NoSummary)}})
	if _, err := s.PruneSections(srv, []string{"2"}); err != nil {
		t.Fatal(err)
	}
	got, cached, known, err := s.KnownItem(srv, "a")
	if err != nil || !cached || !known {
		t.Fatalf("KnownItem = %v, %v, %v", cached, known, err)
	}
	if got.LastMatchEventID != 0 {
		t.Errorf("LastMatchEventID = %d, want 0 after old section was pruned", got.LastMatchEventID)
	}
}
