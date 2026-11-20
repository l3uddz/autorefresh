// Package store caches the incomplete items of each Plex library and the
// times when each library was read.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the SQLite database of incomplete items.
type Store struct {
	db *sql.DB
}

// Open opens the database file and applies the migrations that are missing.
// Each connection enforces foreign keys.
func Open(file string) (*Store, error) {
	db, err := sql.Open("sqlite", file+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection serializes all statements, so writers never collide.
	db.SetMaxOpenConns(1)

	// The cache cleanup relies on the cascade of the foreign keys. A "?" in
	// the path turns them off without an error, so check them.
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if foreignKeys != 1 {
		db.Close()
		return nil, errors.New(`open database: foreign keys are off; the path must not contain "?"`)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return &Store{db: db}, nil
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
