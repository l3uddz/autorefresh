package main

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l3uddz/autorefresh/internal/store"
)

// legacyDatabase writes a database at file as an older version left it:
// without incremental auto-vacuum.
func legacyDatabase(t *testing.T, file string) {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE legacy (x); INSERT INTO legacy VALUES (1)`); err != nil {
		t.Fatal(err)
	}
}

// databaseState reads auto_vacuum and whether planner statistics exist.
func databaseState(t *testing.T, file string) (autoVacuum int, stats bool) {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'sqlite_stat1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return autoVacuum, n > 0
}

func TestRunOnceConvertsDatabase(t *testing.T) {
	buf := captureLog(t)
	opts := runOptions(t, runTarget(t, http.StatusOK, nil).URL)
	opts.Once = true
	legacyDatabase(t, opts.Database)

	if err := run(context.Background(), func() {}, opts, "test"); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	if mode, _ := databaseState(t, opts.Database); mode != 2 {
		t.Errorf("auto_vacuum = %d, want 2", mode)
	}
	if !strings.Contains(buf.String(), `"message":"Converted database"`) {
		t.Errorf("no Converted database line in the log:\n%s", buf)
	}
}

func TestDryRunLeavesDatabaseLayout(t *testing.T) {
	buf := captureLog(t)
	opts := runOptions(t, runTarget(t, http.StatusOK, nil).URL)
	opts.Once, opts.DryRun = true, true
	legacyDatabase(t, opts.Database)

	if err := run(context.Background(), func() {}, opts, "test"); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	mode, stats := databaseState(t, opts.Database)
	if mode != 0 {
		t.Errorf("auto_vacuum = %d after a dry run, want 0", mode)
	}
	if stats {
		t.Error("a dry run wrote planner statistics")
	}
	if strings.Contains(buf.String(), "Converted database") {
		t.Errorf("a dry run converted the database:\n%s", buf)
	}
}

// The stop updates the planner statistics, unless it ends a dry run. No run
// comes first here, so only the stop can write them.
func TestFinishStoreOptimizesOutsideDryRun(t *testing.T) {
	for _, tt := range []struct {
		name      string
		dryRun    bool
		wantStats bool
	}{
		{"normal stop", false, true},
		{"dry run", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "autorefresh.db")
			st, err := store.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			// PRAGMA optimize analyzes only an indexed table with rows.
			db, err := sql.Open("sqlite", file)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`CREATE TABLE filler (x); CREATE INDEX filler_x ON filler (x);
				WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 1000) INSERT INTO filler SELECT i FROM n`)
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			if _, stats := databaseState(t, file); stats {
				t.Fatal("the database has planner statistics before the stop")
			}
			if err := finishStore(st, tt.dryRun); err != nil {
				t.Fatalf("finishStore = %v", err)
			}
			if _, stats := databaseState(t, file); stats != tt.wantStats {
				t.Errorf("planner statistics = %v, want %v", stats, tt.wantStats)
			}
		})
	}
}
