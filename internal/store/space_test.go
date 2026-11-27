package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// shortBusyTimeout lowers the lock wait of the stores that the test opens
// after the call.
func shortBusyTimeout(t *testing.T) {
	t.Helper()
	old := busyTimeout
	busyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { busyTimeout = old })
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
