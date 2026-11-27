package main

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
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

// databaseState reads auto_vacuum.
func databaseState(t *testing.T, file string) (autoVacuum int) {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	return autoVacuum
}

func TestRunOnceConvertsDatabase(t *testing.T) {
	buf := captureLog(t)
	opts := runOptions(t, runTarget(t, http.StatusOK, nil).URL)
	opts.Once = true
	legacyDatabase(t, opts.Database)

	if err := run(context.Background(), func() {}, opts, "test"); err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
	if mode := databaseState(t, opts.Database); mode != 2 {
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
	if mode := databaseState(t, opts.Database); mode != 0 {
		t.Errorf("auto_vacuum = %d after a dry run, want 0", mode)
	}
	if strings.Contains(buf.String(), "Converted database") {
		t.Errorf("a dry run converted the database:\n%s", buf)
	}
}
