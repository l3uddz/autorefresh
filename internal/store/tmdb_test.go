package store

import "testing"

func TestTMDBIDRoundTrip(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", Unmatched)}})

	if err := s.SaveTMDBID(srv, "a", "tvdb", "5", "77"); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := s.TMDBID(srv, "a", "tvdb", "5"); err != nil || !ok || id != "77" {
		t.Errorf("TMDBID tvdb 5 = %q, %v, %v, want 77", id, ok, err)
	}
	if id, ok, err := s.TMDBID(srv, "a", "imdb", "5"); err != nil || ok {
		t.Errorf("TMDBID imdb 5 = %q, %v, %v, want none", id, ok, err)
	}
	if err := s.SaveTMDBID(srv, "a", "tvdb", "5", "78"); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := s.TMDBID(srv, "a", "tvdb", "5"); id != "78" {
		t.Errorf("TMDBID after a second save = %q, want 78", id)
	}
}

func TestTMDBIDGoesWithItem(t *testing.T) {
	tests := []struct {
		name   string
		remove func(t *testing.T, s *Store)
	}{
		{"gone", func(t *testing.T, s *Store) {
			if err := s.RecordGone(srv, "a", base, SourceAuto, "", 0); err != nil {
				t.Fatal(err)
			}
		}},
		{"complete", func(t *testing.T, s *Store) {
			apply(t, s, Read{Items: []Item{item("a", 0)}})
		}},
		{"prune", func(t *testing.T, s *Store) {
			if _, err := s.PruneSections(srv, nil); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTest(t)
			apply(t, s, Read{Items: []Item{item("a", Unmatched)}})
			if err := s.SaveTMDBID(srv, "a", "tvdb", "5", "77"); err != nil {
				t.Fatal(err)
			}

			tt.remove(t, s)

			if id, ok, err := s.TMDBID(srv, "a", "tvdb", "5"); err != nil || ok {
				t.Errorf("TMDBID = %q, %v, %v, want none", id, ok, err)
			}
		})
	}
}

func TestSaveTMDBIDSkipsMissingItem(t *testing.T) {
	s := openTest(t)
	if err := s.SaveTMDBID(srv, "missing", "tvdb", "5", "77"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.TMDBID(srv, "missing", "tvdb", "5"); err != nil || ok {
		t.Fatalf("TMDBID found = %v, error = %v", ok, err)
	}
}
