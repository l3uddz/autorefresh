// Package store caches the incomplete items of each Plex library and the
// times when each library was read.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the SQLite database of incomplete items.
type Store struct {
	db *sql.DB
}

// Open opens the database file and applies the migrations that are missing.
func Open(file string) (*Store, error) {
	name, err := dataSourceName(file)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection serializes all statements, so writers never collide.
	db.SetMaxOpenConns(1)

	// The driver ignores a setting that it does not apply, so check the
	// settings that the cache relies on.
	if err := checkPragma(db, "foreign_keys", 1); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := checkPragma(db, "temp_store", 2); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := checkPragma(db, "synchronous", 1); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := enableWAL(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return &Store{db: db}, nil
}

// dataSourceName returns the SQLite URI of file with the settings of each
// connection:
//
//   - Foreign keys are on, because the cache cleanup relies on their cascade.
//   - Temporary data, such as a large sort, stays in memory. The container
//     image has no writable temporary directory.
//   - A second process, such as a dry run, can use the same file. A
//     connection waits for a lock that the other process holds, and a write
//     transaction takes the write lock at its start. A transaction that
//     reads first and then writes can otherwise fail at once.
//
// The URI escapes the path, so a path can hold any character.
func dataSourceName(file string) (string, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	query := url.Values{
		"_foreign_keys": {"1"},
		"_pragma":       {"temp_store(memory)", "synchronous(NORMAL)"},
		"_busy_timeout": {strconv.Itoa(int(busyTimeout.Milliseconds()))},
		"_txlock":       {"immediate"},
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: query.Encode()}
	return u.String(), nil
}

// busyTimeout is the longest wait for a lock that another process holds.
const busyTimeout = 30 * time.Second

// checkPragma returns an error unless the pragma has the value want.
func checkPragma(db *sql.DB, pragma string, want int) error {
	var got int
	if err := db.QueryRow(`PRAGMA ` + pragma).Scan(&got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s is %d, want %d", pragma, got, want)
	}
	return nil
}

// enableWAL switches the database to WAL journal mode if it is not already.
// Two processes switching a rollback-journal database to WAL at the same time
// can hit SQLITE_BUSY without waiting for busy_timeout, so it retries briefly.
func enableWAL(db *sql.DB) error {
	deadline := time.Now().Add(busyTimeout)
	for {
		var mode string
		err := db.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&mode)
		if err == nil {
			if !strings.EqualFold(mode, "wal") {
				return fmt.Errorf("journal_mode is %q, want %q", mode, "wal")
			}
			return nil
		}
		if !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isBusy(err error) bool {
	return strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked")
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

type migration struct {
	version int
	file    string
}

// migrate runs each embedded migration that is not yet recorded, in version
// order and in one transaction for each file.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migration (version INTEGER PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create schema_migration: %w", err)
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}
	list, err := parseMigrations()
	if err != nil {
		return err
	}
	// An older binary cannot know the schema of a newer one.
	latest := list[len(list)-1].version
	for v := range applied {
		if v > latest {
			return fmt.Errorf("the database has schema version %d, but this version of autorefresh knows only up to %d", v, latest)
		}
	}

	for _, m := range list {
		if applied[m.version] {
			continue
		}
		script, err := migrations.ReadFile(path.Join("migrations", m.file))
		if err != nil {
			return err
		}
		if err := applyMigration(db, m.version, string(script)); err != nil {
			return fmt.Errorf("%s: %w", m.file, err)
		}
	}
	return nil
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT version FROM schema_migration`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migration: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_migration: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// parseMigrations lists the embedded files, named <version>_<name>.sql, by version.
func parseMigrations() ([]migration, error) {
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}

	var list []migration
	for _, f := range files {
		prefix, _, _ := strings.Cut(f.Name(), "_")
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q has no version prefix", f.Name())
		}
		list = append(list, migration{version: v, file: f.Name()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	return list, nil
}

// applyMigration runs one migration script in a transaction. Foreign keys are
// off while it runs, so a script can rebuild a parent table without deleting
// the rows of its child tables. SQLite ignores that setting inside a
// transaction, so it changes on the connection first. Before the commit,
// every row must still satisfy its foreign keys.
func applyMigration(db *sql.DB, version int, script string) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	err = migrateInTx(ctx, conn, version, script)
	if _, onErr := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); onErr != nil && err == nil {
		err = onErr
	}
	return err
}

func migrateInTx(ctx context.Context, conn *sql.Conn, version int, script string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Another process can apply this version between appliedVersions and the
	// immediate write lock of this transaction.
	var existing int
	switch err := tx.QueryRow(`SELECT version FROM schema_migration WHERE version = ?`, version).Scan(&existing); {
	case err == nil:
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check schema_migration: %w", err)
	}

	if _, err := tx.Exec(script); err != nil {
		return err
	}
	var table string
	switch err := tx.QueryRow(`PRAGMA foreign_key_check`).Scan(&table, new(any), new(any), new(any)); {
	case err == nil:
		return fmt.Errorf("a row of %s breaks a foreign key", table)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("foreign key check: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migration (version) VALUES (?)`, version); err != nil {
		return err
	}
	return tx.Commit()
}
