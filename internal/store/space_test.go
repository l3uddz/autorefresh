package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// shortBusyTimeout lowers the usual lock wait of the stores that the test
// opens after the call, and the lock wait of the space upkeep.
func shortBusyTimeout(t *testing.T) {
	t.Helper()
	oldBusy, oldUpkeep := busyTimeout, upkeepLockWait
	busyTimeout, upkeepLockWait = 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { busyTimeout, upkeepLockWait = oldBusy, oldUpkeep })
}

// holdRead opens a second handle on file that keeps an older snapshot of
// the WAL until the returned function ends it.
func holdRead(t *testing.T, file string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// Cleanups run last first, so the transaction ends before db.Close.
	t.Cleanup(func() { _ = tx.Rollback() })
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return func() { tx.Rollback() }
}

func autoVacuum(t *testing.T, s *Store) int64 {
	t.Helper()
	v, err := pragmaInt(context.Background(), s.db, "auto_vacuum")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// holdWrite opens a second handle on file that holds a write transaction
// until the test ends, as another process can.
func holdWrite(t *testing.T, file string) {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS lock_holder (x)`); err != nil {
		t.Fatal(err)
	}
}

func TestOpenNewFileUsesIncrementalAutoVacuum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
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
			t.Fatalf("concurrent Open: %v", err)
		}
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := autoVacuum(t, s); got != autoVacuumIncremental {
		t.Errorf("auto_vacuum = %d, want %d", got, autoVacuumIncremental)
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v, want wal", mode, err)
	}
	c, err := s.ConvertAutoVacuum(context.Background())
	if err != nil || c != (Conversion{}) {
		t.Errorf("ConvertAutoVacuum on a new file = %+v, %v, want nothing to do", c, err)
	}
}

func TestConvertAutoVacuumKeepsRows(t *testing.T) {
	s, err := Open(openVersion1(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := autoVacuum(t, s); got != 0 {
		t.Fatalf("old file auto_vacuum = %d, want 0", got)
	}

	c, err := s.ConvertAutoVacuum(context.Background())
	if err != nil {
		t.Fatalf("ConvertAutoVacuum: %v", err)
	}
	if !c.Converted || c.TooLarge || c.Before <= 0 || c.After <= 0 {
		t.Errorf("ConvertAutoVacuum = %+v, want a conversion with both sizes", c)
	}
	if got := autoVacuum(t, s); got != autoVacuumIncremental {
		t.Errorf("auto_vacuum = %d, want %d", got, autoVacuumIncremental)
	}
	if _, cached, _, err := s.KnownItem("home", "101"); err != nil || !cached {
		t.Errorf("KnownItem after the conversion = %v, %v, want the old row", cached, err)
	}
	if again, err := s.ConvertAutoVacuum(context.Background()); err != nil || again != (Conversion{}) {
		t.Errorf("second ConvertAutoVacuum = %+v, %v, want nothing to do", again, err)
	}
}

func TestConvertAutoVacuumFailsWhileAnotherProcessWrites(t *testing.T) {
	shortBusyTimeout(t)
	path := openVersion1(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	holdWrite(t, path)

	if c, err := s.ConvertAutoVacuum(context.Background()); err == nil {
		t.Fatalf("ConvertAutoVacuum = %+v, want a busy error", c)
	}
	if got := autoVacuum(t, s); got != 0 {
		t.Errorf("auto_vacuum after a failed conversion = %d, want 0", got)
	}
}

func TestConvertAutoVacuumSkipsLargeDatabase(t *testing.T) {
	old := maxConvertBytes
	maxConvertBytes = 1
	t.Cleanup(func() { maxConvertBytes = old })
	s, err := Open(openVersion1(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	c, err := s.ConvertAutoVacuum(context.Background())
	if err != nil || !c.TooLarge || c.Converted || c.Before <= 1 {
		t.Errorf("ConvertAutoVacuum = %+v, %v, want too large with its size", c, err)
	}
	if got := autoVacuum(t, s); got != 0 {
		t.Errorf("auto_vacuum = %d, want 0", got)
	}
}

// fillAndDelete adds rows of about 8 KiB each to a scratch table and then
// deletes the first n of them, which leaves their pages free.
func fillAndDelete(t *testing.T, s *Store, rows, n int) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE scratch (id INTEGER PRIMARY KEY, data BLOB)`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
			INSERT INTO scratch (id, data) SELECT i, randomblob(8000) FROM n`,
	} {
		if _, err := s.db.Exec(stmt, rows); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM scratch WHERE id <= ?`, n); err != nil {
		t.Fatal(err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func TestReclaimReleasesFreePagesAboveThreshold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fillAndDelete(t, s, 400, 400)
	free, _ := pragmaInt(context.Background(), s.db, "freelist_count")

	r, err := s.Reclaim(context.Background())
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if r.Pages < free || r.Bytes != r.Pages*4096 || r.CheckpointBusy {
		t.Errorf("Reclaim = %+v, want at least %d pages and a finished checkpoint", r, free)
	}
	if after, _ := pragmaInt(context.Background(), s.db, "freelist_count"); after != 0 {
		t.Errorf("free pages after Reclaim = %d, want 0", after)
	}
	pages, _ := pragmaInt(context.Background(), s.db, "page_count")
	if got := fileSize(t, path); got != pages*4096 {
		t.Errorf("file size = %d, want %d after the checkpoint", got, pages*4096)
	}
	if got := fileSize(t, path+"-wal"); got != 0 {
		t.Errorf("WAL size = %d, want 0", got)
	}
}

func TestReclaimKeepsFewFreePages(t *testing.T) {
	s := openTest(t)
	fillAndDelete(t, s, 400, 50)
	free, _ := pragmaInt(context.Background(), s.db, "freelist_count")

	r, err := s.Reclaim(context.Background())
	if err != nil || r.Pages != 0 {
		t.Errorf("Reclaim = %+v, %v, want no released page", r, err)
	}
	if after, _ := pragmaInt(context.Background(), s.db, "freelist_count"); after != free {
		t.Errorf("free pages = %d, want %d", after, free)
	}
}

// Each half of the threshold alone releases nothing: a fifth of the pages
// under 1 MiB, and 1 MiB under a fifth of the pages.
func TestReclaimNeedsBothThresholds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rows, deleted int
		share, bytes  bool // which half the setup passes
	}{
		{name: "share under 1 MiB", rows: 100, deleted: 100, share: true},
		{name: "1 MiB under a fifth", rows: 1500, deleted: 250, bytes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			fillAndDelete(t, s, tc.rows, tc.deleted)
			ctx := context.Background()
			free, _ := pragmaInt(ctx, s.db, "freelist_count")
			pages, _ := pragmaInt(ctx, s.db, "page_count")
			if got := free*reclaimShare >= pages; got != tc.share {
				t.Fatalf("setup: %d of %d pages free, share half = %v, want %v", free, pages, got, tc.share)
			}
			if got := free*4096 >= reclaimMinBytes; got != tc.bytes {
				t.Fatalf("setup: %d free pages, bytes half = %v, want %v", free, got, tc.bytes)
			}

			r, err := s.Reclaim(ctx)
			if err != nil || r.Pages != 0 {
				t.Errorf("Reclaim = %+v, %v, want no released page", r, err)
			}
			if after, _ := pragmaInt(ctx, s.db, "freelist_count"); after != free {
				t.Errorf("free pages = %d, want %d", after, free)
			}
		})
	}
}

func TestReclaimNeedsIncrementalAutoVacuum(t *testing.T) {
	s, err := Open(openVersion1(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fillAndDelete(t, s, 400, 400)

	r, err := s.Reclaim(context.Background())
	if err != nil || r.Pages != 0 {
		t.Errorf("Reclaim without auto-vacuum = %+v, %v, want no released page", r, err)
	}
}

func TestReclaimFinishesABlockedCheckpointLater(t *testing.T) {
	shortBusyTimeout(t)
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fillAndDelete(t, s, 400, 400)

	// A reader of another process keeps an older snapshot of the WAL.
	endRead := holdRead(t, path)

	first, err := s.Reclaim(context.Background())
	if err != nil || first.Pages == 0 || !first.CheckpointBusy {
		t.Fatalf("Reclaim with a reader = %+v, %v, want released pages and a busy checkpoint", first, err)
	}
	endRead()

	second, err := s.Reclaim(context.Background())
	if err != nil || second.Pages != 0 || second.CheckpointBusy {
		t.Fatalf("later Reclaim = %+v, %v, want a finished checkpoint", second, err)
	}
	pages, _ := pragmaInt(context.Background(), s.db, "page_count")
	if got := fileSize(t, path); got != pages*4096 {
		t.Errorf("file size = %d, want %d after the later checkpoint", got, pages*4096)
	}
}

func TestOptimize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Optimize(context.Background()); err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	holdWrite(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	err = s.Optimize(ctx)
	// Under the upkeep lock wait, only the end of the context stops it.
	if took := time.Since(began); took >= upkeepLockWait {
		t.Errorf("Optimize under a write lock took %v, want it stopped by its context before %v", took, upkeepLockWait)
	}
	if err == nil {
		t.Error("Optimize under a write lock = nil, want the error of its ended context")
	}
}

// The upkeep waits only briefly for another process, because the store has
// one connection and every other query waits for it meanwhile.
func TestReclaimWaitsBrieflyForAReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fillAndDelete(t, s, 400, 400)
	holdRead(t, path)

	began := time.Now()
	r, err := s.Reclaim(context.Background())
	if took := time.Since(began); took > upkeepLockWait+3*time.Second {
		t.Errorf("Reclaim with a reader took %v, want about %v", took, upkeepLockWait)
	}
	if err != nil || !r.CheckpointBusy {
		t.Errorf("Reclaim = %+v, %v, want a busy checkpoint", r, err)
	}
	if wait, _ := pragmaInt(context.Background(), s.db, "busy_timeout"); wait != busyTimeout.Milliseconds() {
		t.Errorf("busy_timeout after the upkeep = %d, want %d", wait, busyTimeout.Milliseconds())
	}
}

// A failed release still checkpoints the WAL.
func TestReclaimCheckpointsAfterAFailedRelease(t *testing.T) {
	shortBusyTimeout(t)
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fillAndDelete(t, s, 400, 400)
	holdWrite(t, path)

	r, err := s.Reclaim(context.Background())
	if err == nil {
		t.Fatalf("Reclaim under a write lock = %+v, want the error of the release", r)
	}
	if !r.CheckpointBusy {
		t.Errorf("Reclaim = %+v, want the checkpoint tried and reported busy", r)
	}
}

func TestConvertAutoVacuumWaitsForOtherProcesses(t *testing.T) {
	shortBusyTimeout(t)
	path := openVersion1(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	// In WAL mode, an open connection keeps a shared lock of the file also
	// between its transactions.
	var n int
	if err := other.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
		t.Fatal(err)
	}

	if c, err := s.ConvertAutoVacuum(context.Background()); err == nil || c.Converted {
		t.Errorf("ConvertAutoVacuum with another open connection = %+v, %v, want an error", c, err)
	}
	if got := autoVacuum(t, s); got != 0 {
		t.Errorf("auto_vacuum after a deferred conversion = %d, want 0", got)
	}
	other.Close()
	if c, err := s.ConvertAutoVacuum(context.Background()); err != nil || !c.Converted {
		t.Errorf("ConvertAutoVacuum after the other connection closed = %+v, %v, want converted", c, err)
	}
}

func TestExclusiveLockKeepsOtherProcessesOut(t *testing.T) {
	shortBusyTimeout(t)
	// A value apart from upkeepLockWait shows which wait the store has after
	// the lock.
	busyTimeout = 700 * time.Millisecond
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	write := func() error {
		_, err := other.Exec(`CREATE TABLE IF NOT EXISTS other_writer (x)`)
		return err
	}

	err = s.withExclusiveLock(context.Background(), func(q stmtRunner) error {
		// The lock stays after the COMMIT of withExclusiveLock.
		if err := write(); !isBusy(err) {
			t.Errorf("write of another process under the lock = %v, want busy", err)
		}
		var n int
		if err := other.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n); !isBusy(err) {
			t.Errorf("read of another process under the lock = %v, want busy", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := write(); err != nil {
		t.Errorf("write of another process after the lock: %v", err)
	}
	if got, err := pragmaInt(context.Background(), s.db, "busy_timeout"); err != nil || got != busyTimeout.Milliseconds() {
		t.Errorf("busy_timeout after the lock = %d, %v, want %d", got, err, busyTimeout.Milliseconds())
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA locking_mode`).Scan(&mode); err != nil || mode != "normal" {
		t.Errorf("locking_mode after the lock = %q, %v, want normal", mode, err)
	}
}

// beforeBegin runs hook just before the write transaction of the release.
type beforeBegin struct {
	stmtRunner
	hook func()
}

func (b beforeBegin) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if query == `BEGIN IMMEDIATE` {
		b.hook()
	}
	return b.stmtRunner.ExecContext(ctx, query, args...)
}

func TestReleaseChecksAgainUnderTheWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fillAndDelete(t, s, 400, 400)
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	// Another process reuses most free pages after the first check.
	reuse := func() {
		if _, err := other.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 350)
			INSERT INTO scratch (id, data) SELECT i, randomblob(8000) FROM n`); err != nil {
			t.Error(err)
		}
	}

	ctx := context.Background()
	err = s.withUpkeepLockWait(ctx, func(q stmtRunner) error {
		r, err := releaseFreePages(ctx, beforeBegin{q, reuse})
		if err != nil || r.Pages != 0 {
			t.Errorf("release after the reuse = %+v, %v, want no page released", r, err)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if free, _ := pragmaInt(ctx, s.db, "freelist_count"); free == 0 {
		t.Error("the release freed the pages left under the threshold")
	}
}

func TestUpkeepLockWaitComesBackAfterAFailedSetter(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	short := strconv.FormatInt(upkeepLockWait.Milliseconds(), 10)
	// SQLite applies busy_timeout while it prepares the statement, so a
	// cancel in the authorizer fails the setter after the short wait is set.
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Raw(func(driverConn any) error {
		return driverConn.(sqlite.AuthorizerRegisterer).RegisterAuthorizer(
			func(action sqlite.AuthorizerActionCode, arg1, arg2, _, _ string) sqlite.AuthorizerReturnCode {
				if action == sqlite.AuthPragma && arg1 == "busy_timeout" && arg2 == short {
					cancel()
				}
				return sqlite.AuthorizerOK
			})
	})
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Optimize(ctx); err == nil {
		t.Fatal("Optimize with a cancel in the setter succeeded")
	}
	if got, err := pragmaInt(context.Background(), s.db, "busy_timeout"); err != nil || got != busyTimeout.Milliseconds() {
		t.Errorf("busy_timeout after the failed setter = %d, %v, want %d", got, err, busyTimeout.Milliseconds())
	}
}
