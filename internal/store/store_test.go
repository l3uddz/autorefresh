package store

import (
	"path/filepath"
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
			if n != 1 {
				t.Fatalf("schema_migration rows = %d, want 1", n)
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
