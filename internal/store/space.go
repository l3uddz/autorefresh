package store

import (
	"context"
	"database/sql"
	"fmt"
)

// autoVacuumIncremental is the value of PRAGMA auto_vacuum for incremental
// auto-vacuum.
const autoVacuumIncremental = 2

// maxConvertBytes is the largest database that ConvertAutoVacuum converts.
// VACUUM keeps a copy of the database in memory, because temp_store is
// memory. Tests lower it.
var maxConvertBytes int64 = 256 << 20

// setNewAutoVacuum turns on incremental auto-vacuum for a new, empty file. It
// must run before the switch to WAL mode.
func setNewAutoVacuum(db *sql.DB) error {
	return retryBusy(func() error {
		pages, err := pragmaInt(context.Background(), db, "page_count")
		if err != nil || pages != 0 {
			return err
		}
		_, err = db.Exec(`PRAGMA auto_vacuum = INCREMENTAL`)
		return err
	})
}

// pragmaInt reads a PRAGMA that answers one integer.
func pragmaInt(ctx context.Context, db *sql.DB, pragma string) (int64, error) {
	var v int64
	if err := db.QueryRowContext(ctx, `PRAGMA `+pragma).Scan(&v); err != nil {
		return 0, fmt.Errorf("read %s: %w", pragma, err)
	}
	return v, nil
}

// size returns the size of the database in pages and bytes.
func (s *Store) size(ctx context.Context) (pages, pageSize int64, err error) {
	if pages, err = pragmaInt(ctx, s.db, "page_count"); err != nil {
		return 0, 0, err
	}
	if pageSize, err = pragmaInt(ctx, s.db, "page_size"); err != nil {
		return 0, 0, err
	}
	return pages, pageSize, nil
}

// Conversion is the result of ConvertAutoVacuum. Before and After are page
// count times page size.
type Conversion struct {
	Converted bool  // the database now uses incremental auto-vacuum
	TooLarge  bool  // Before is over the limit, so nothing changed
	Before    int64 // bytes before the conversion; set unless it was not needed
	After     int64 // bytes after the conversion
}

// ConvertAutoVacuum switches a database without incremental auto-vacuum to it
// with VACUUM, then truncates the WAL. A database that already has it, or one
// over maxConvertBytes, stays as it is. A failed VACUUM leaves auto_vacuum
// unchanged, so a later call tries again.
func (s *Store) ConvertAutoVacuum(ctx context.Context) (Conversion, error) {
	mode, err := pragmaInt(ctx, s.db, "auto_vacuum")
	if err != nil || mode == autoVacuumIncremental {
		return Conversion{}, err
	}
	pages, pageSize, err := s.size(ctx)
	if err != nil {
		return Conversion{}, err
	}
	c := Conversion{Before: pages * pageSize}
	if c.Before > maxConvertBytes {
		c.TooLarge = true
		return c, nil
	}
	for _, stmt := range []string{`PRAGMA auto_vacuum = INCREMENTAL`, `VACUUM`} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return Conversion{}, fmt.Errorf("convert database: %w", err)
		}
	}
	if _, err := s.checkpoint(ctx); err != nil {
		return Conversion{}, err
	}
	if pages, err = pragmaInt(ctx, s.db, "page_count"); err != nil {
		return Conversion{}, err
	}
	c.Converted, c.After = true, pages*pageSize
	return c, nil
}

// checkpoint copies the WAL into the database file and truncates it. busy is
// true when a reader of another process kept it from finishing.
func (s *Store) checkpoint(ctx context.Context) (busy bool, err error) {
	var blocked, logPages, done int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&blocked, &logPages, &done); err != nil {
		return false, fmt.Errorf("checkpoint: %w", err)
	}
	return blocked != 0, nil
}
