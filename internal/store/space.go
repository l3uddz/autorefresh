package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// autoVacuumIncremental is the value of PRAGMA auto_vacuum for incremental
// auto-vacuum.
const autoVacuumIncremental = 2

// maxConvertBytes is the largest database that ConvertAutoVacuum converts.
// VACUUM keeps a copy of the database in memory, because temp_store is
// memory. Tests lower it.
var maxConvertBytes int64 = 256 << 20

// upkeepLockWait is the longest wait of a space upkeep step for a lock of
// another process. It is short, because the store has one connection, so a
// long wait would hold up every other query of the service. A step that
// gives up runs again at the next start or run. Tests lower it.
var upkeepLockWait = 2 * time.Second

// Free pages are released only when they are at least 1/reclaimShare of the
// pages and at least reclaimMinBytes, so the few pages that each hourly
// retention frees are reused instead of released and grown again.
const (
	reclaimShare    = 5
	reclaimMinBytes = 1 << 20
)

// stmtRunner runs statements on the database or on one of its connections.
type stmtRunner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

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
func pragmaInt(ctx context.Context, q stmtRunner, pragma string) (int64, error) {
	var v int64
	if err := q.QueryRowContext(ctx, `PRAGMA `+pragma).Scan(&v); err != nil {
		return 0, fmt.Errorf("read %s: %w", pragma, err)
	}
	return v, nil
}

// size returns the size of the database in pages and the page size.
func size(ctx context.Context, q stmtRunner) (pages, pageSize int64, err error) {
	if pages, err = pragmaInt(ctx, q, "page_count"); err != nil {
		return 0, 0, err
	}
	if pageSize, err = pragmaInt(ctx, q, "page_size"); err != nil {
		return 0, 0, err
	}
	return pages, pageSize, nil
}

// withUpkeepLockWait runs fn on the connection of the store with a lock wait
// of upkeepLockWait, and then sets the usual wait again, also after ctx
// ended. When a step fails, it closes the connection instead, because the
// step can have left it in any state, such as with the short wait: SQLite
// applies the pragma while it prepares the statement, so even the setter can
// fail after it changed the connection. The next connection gets the usual
// settings from the data source name.
func (s *Store) withUpkeepLockWait(ctx context.Context, fn func(q stmtRunner) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}
	defer conn.Close()
	defer func() {
		if err != nil || setBusyTimeout(context.Background(), conn, busyTimeout) != nil {
			discard(conn)
		}
	}()
	if err := setBusyTimeout(ctx, conn, upkeepLockWait); err != nil {
		return err
	}
	return fn(conn)
}

// withExclusiveLock runs fn on a connection that holds the exclusive lock of
// the database file, so no other process reads or writes the file until fn
// returns. It waits upkeepLockWait for the lock. The connection is then
// closed, which gives the lock up. The next connection gets the usual
// settings from the data source name.
func (s *Store) withExclusiveLock(ctx context.Context, fn func(q stmtRunner) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}
	defer conn.Close()
	defer discard(conn)
	if err := setBusyTimeout(ctx, conn, upkeepLockWait); err != nil {
		return err
	}
	// In exclusive locking mode, the lock that BEGIN EXCLUSIVE takes stays
	// after COMMIT.
	for _, stmt := range []string{`PRAGMA locking_mode = EXCLUSIVE`, `BEGIN EXCLUSIVE`, `COMMIT`} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("lock database: %w", err)
		}
	}
	return fn(conn)
}

// discard closes the driver connection of conn instead of returning it to
// the pool.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

func setBusyTimeout(ctx context.Context, q stmtRunner, d time.Duration) error {
	if _, err := q.ExecContext(ctx, `PRAGMA busy_timeout = `+strconv.FormatInt(d.Milliseconds(), 10)); err != nil {
		return fmt.Errorf("set lock wait: %w", err)
	}
	return nil
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
// over maxConvertBytes, stays as it is. The conversion holds the exclusive
// lock of the file from the size check to the end, so no other process can
// grow the file over the limit in between. When another process holds a lock
// for longer than upkeepLockWait, the call fails and changes nothing. A call
// that fails before VACUUM commits leaves auto_vacuum unchanged, so a later
// call tries again. The checkpoint and the size after it come after that
// commit, so their error can come with a converted database.
func (s *Store) ConvertAutoVacuum(ctx context.Context) (Conversion, error) {
	// A database that already has it needs no lock.
	mode, err := pragmaInt(ctx, s.db, "auto_vacuum")
	if err != nil || mode == autoVacuumIncremental {
		return Conversion{}, err
	}
	var c Conversion
	err = s.withExclusiveLock(ctx, func(q stmtRunner) error {
		var err error
		c, err = convertAutoVacuum(ctx, q)
		return err
	})
	if err != nil {
		return Conversion{}, err
	}
	return c, nil
}

func convertAutoVacuum(ctx context.Context, q stmtRunner) (Conversion, error) {
	mode, err := pragmaInt(ctx, q, "auto_vacuum")
	if err != nil || mode == autoVacuumIncremental {
		return Conversion{}, err
	}
	pages, pageSize, err := size(ctx, q)
	if err != nil {
		return Conversion{}, err
	}
	c := Conversion{Before: pages * pageSize}
	if c.Before > maxConvertBytes {
		c.TooLarge = true
		return c, nil
	}
	for _, stmt := range []string{`PRAGMA auto_vacuum = INCREMENTAL`, `VACUUM`} {
		if _, err := q.ExecContext(ctx, stmt); err != nil {
			return Conversion{}, fmt.Errorf("convert database: %w", err)
		}
	}
	c.Converted = true
	// The exclusive lock keeps every reader out, so the checkpoint finishes.
	if _, err = checkpoint(ctx, q); err != nil {
		return Conversion{}, err
	}
	if pages, err = pragmaInt(ctx, q, "page_count"); err != nil {
		return Conversion{}, err
	}
	c.After = pages * pageSize
	return c, nil
}

// checkpoint copies the WAL into the database file and truncates it. busy is
// true when another process kept it from finishing.
func checkpoint(ctx context.Context, q stmtRunner) (busy bool, err error) {
	var blocked, logPages, done int64
	if err := q.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&blocked, &logPages, &done); err != nil {
		return false, fmt.Errorf("checkpoint: %w", err)
	}
	return blocked != 0, nil
}

// ReclaimResult is the result of Reclaim.
type ReclaimResult struct {
	Pages          int64 // the fall in the page count
	Bytes          int64 // Pages times the page size
	CheckpointBusy bool  // another process kept the checkpoint from finishing
}

// Reclaim releases the free pages of a database with incremental
// auto-vacuum when they pass the threshold. It then always checkpoints and
// truncates the WAL, also after a failed release, which shrinks the file.
// A checkpoint that another process blocked finishes on a later call. The
// error joins the errors of both steps.
func (s *Store) Reclaim(ctx context.Context) (ReclaimResult, error) {
	var r ReclaimResult
	err := s.withUpkeepLockWait(ctx, func(q stmtRunner) error {
		var releaseErr, checkpointErr error
		r, releaseErr = releaseFreePages(ctx, q)
		if ctx.Err() == nil {
			r.CheckpointBusy, checkpointErr = checkpoint(ctx, q)
		}
		return errors.Join(releaseErr, checkpointErr)
	})
	return r, err
}

// releaseFreePages runs incremental_vacuum when the database has incremental
// auto-vacuum and its free pages pass the threshold, and returns the fall in
// the page count. A first check without a lock skips the write lock in the
// usual case. The release then checks again in a write transaction, because
// another process can reuse the free pages in between.
func releaseFreePages(ctx context.Context, q stmtRunner) (ReclaimResult, error) {
	if due, _, _, err := releaseDue(ctx, q); err != nil || !due {
		return ReclaimResult{}, err
	}
	if _, err := q.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return ReclaimResult{}, fmt.Errorf("release free pages: %w", err)
	}
	r, err := releaseLocked(ctx, q)
	if err == nil {
		if _, err = q.ExecContext(ctx, `COMMIT`); err != nil {
			err = fmt.Errorf("release free pages: %w", err)
		}
	}
	if err != nil {
		// The checkpoint after the release needs the transaction to end.
		_, _ = q.ExecContext(context.Background(), `ROLLBACK`)
		return ReclaimResult{}, err
	}
	return r, nil
}

// releaseDue reports whether the database has incremental auto-vacuum and
// its free pages pass the threshold, with its size.
func releaseDue(ctx context.Context, q stmtRunner) (due bool, pages, pageSize int64, err error) {
	mode, err := pragmaInt(ctx, q, "auto_vacuum")
	if err != nil || mode != autoVacuumIncremental {
		return false, 0, 0, err
	}
	if pages, pageSize, err = size(ctx, q); err != nil {
		return false, 0, 0, err
	}
	free, err := pragmaInt(ctx, q, "freelist_count")
	if err != nil {
		return false, 0, 0, err
	}
	return free*reclaimShare >= pages && free*pageSize >= reclaimMinBytes, pages, pageSize, nil
}

// releaseLocked releases the free pages inside the write transaction of
// releaseFreePages when they still pass the threshold.
func releaseLocked(ctx context.Context, q stmtRunner) (ReclaimResult, error) {
	due, before, pageSize, err := releaseDue(ctx, q)
	if err != nil || !due {
		return ReclaimResult{}, err
	}
	if _, err := q.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
		return ReclaimResult{}, fmt.Errorf("release free pages: %w", err)
	}
	after, err := pragmaInt(ctx, q, "page_count")
	if err != nil {
		return ReclaimResult{}, err
	}
	pages := max(0, before-after)
	return ReclaimResult{Pages: pages, Bytes: pages * pageSize}, nil
}

// Optimize updates the query planner statistics with PRAGMA optimize, with
// the short lock wait of the space upkeep.
func (s *Store) Optimize(ctx context.Context) error {
	return s.withUpkeepLockWait(ctx, func(q stmtRunner) error {
		if _, err := q.ExecContext(ctx, `PRAGMA optimize`); err != nil {
			return fmt.Errorf("optimize: %w", err)
		}
		return nil
	})
}
