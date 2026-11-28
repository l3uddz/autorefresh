package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// matchedItem caches item a with an accepted match and then an accepted
// refresh, as the follow-up of a match leaves it. It returns the ID of the
// request event of the match.
func matchedItem(t *testing.T, s *Store) int64 {
	t.Helper()
	apply(t, s, Read{Items: []Item{item("a", NoSummary)}})
	for _, a := range []Action{ActionTMDBMatch, ActionRefresh} {
		req := Request{Server: srv, RatingKey: "a", At: base, Source: SourceAuto, Action: a, Accepted: true}
		if a != ActionRefresh {
			req.Match = &MatchTarget{GUID: "plex://movie/100", Title: "Title a"}
		}
		if err := s.RecordRequest(req); err != nil {
			t.Fatal(err)
		}
	}
	row := rows(t, s)["a"]
	if row.LastMatch != ActionTMDBMatch || row.LastAction != ActionRefresh || row.LastMatchEventID == 0 {
		t.Fatalf("row = %+v, want a match and a later refresh", row)
	}
	return row.LastMatchEventID
}

// mergeRecorders record that Plex merged item a into item 900 through each
// recorder, and return what it reports.
var mergeRecorders = map[string]func(s *Store, at time.Time, runID int64) (Item, bool, error){
	"gone": func(s *Store, at time.Time, runID int64) (Item, bool, error) {
		return s.RecordGone(srv, "a", at, SourceAuto, ActionRefresh, runID, AttemptResult{}, "900")
	},
	"check": func(s *Store, at time.Time, runID int64) (Item, bool, error) {
		return s.ApplyCheckGone(srv, "a", at, SourceManual, runID, Observation{}, "900")
	},
	"manual": func(s *Store, at time.Time, _ int64) (Item, bool, error) {
		wrote, err := s.RecordManual(ManualRecord{
			Request:  Request{Server: srv, RatingKey: "a", At: at, Source: SourceManual, Action: ActionRefresh},
			Snapshot: item("a", NoSummary), Cached: true, Gone: true, Merged: "900",
		})
		return Item{RatingKey: "a"}, wrote, err
	},
}

func TestMergeRecordsACompletion(t *testing.T) {
	for name, record := range mergeRecorders {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			runID := startRun(t, s, srv, base)
			matchEvent := matchedItem(t, s)
			at := base.Add(time.Hour)

			removed, wroteMerge, err := record(s, at, runID)
			if err != nil || !wroteMerge || removed.RatingKey != "a" {
				t.Fatalf("removed = %+v, wroteMerge = %v, err = %v", removed, wroteMerge, err)
			}
			if _, ok := rows(t, s)["a"]; ok {
				t.Fatal("row still exists")
			}
			events, err := s.Timeline(srv, "a")
			if err != nil {
				t.Fatal(err)
			}
			e := events[0]
			if e.Kind != EventCompleted || e.Source != SourceAuto || e.MergedKey != "900" || !e.At.Equal(at) ||
				e.FixedAction != ActionTMDBMatch || e.FixedMatch != ActionTMDBMatch || e.FixedMatchEventID != matchEvent ||
				e.Title != "Title a" {
				t.Fatalf("event = %+v, want the merged completion", e)
			}
			if name != "manual" && e.RunID != runID {
				t.Errorf("run id = %d, want %d", e.RunID, runID)
			}
			for _, ev := range events {
				if ev.Kind == EventGone {
					t.Errorf("gone event %+v, want none", ev)
				}
			}
		})
	}
}

// A merged key on a row without an accepted match writes the gone event.
func TestMergeWithoutAMatchIsGone(t *testing.T) {
	for name, record := range mergeRecorders {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			runID := startRun(t, s, srv, base)
			apply(t, s, Read{Items: []Item{item("a", NoSummary)}})

			if _, wroteMerge, err := record(s, base, runID); err != nil || wroteMerge {
				t.Fatalf("wroteMerge = %v, err = %v, want a gone event", wroteMerge, err)
			}
			events, _ := s.Timeline(srv, "a")
			if len(events) != 1 || events[0].Kind != EventGone || events[0].MergedKey != "" {
				t.Fatalf("events = %+v, want one gone event", events)
			}
		})
	}
}

// A manual merge whose row left the cache during the action writes nothing.
func TestManualMergeAfterTheRowLeft(t *testing.T) {
	s := openTest(t)
	matchedItem(t, s)
	if _, _, err := s.RecordGone(srv, "a", base, SourceAuto, "", 0, AttemptResult{}, ""); err != nil {
		t.Fatal(err)
	}
	before := countRows(t, s, "event")
	wroteMerge, err := s.RecordManual(ManualRecord{
		Request:  Request{Server: srv, RatingKey: "a", At: base, Source: SourceManual, Action: ActionRefresh},
		Snapshot: item("a", NoSummary), Cached: true, Gone: true, Merged: "900",
	})
	if err != nil || wroteMerge {
		t.Fatalf("wroteMerge = %v, err = %v", wroteMerge, err)
	}
	if n := countRows(t, s, "event"); n != before {
		t.Errorf("events = %d, want %d", n, before)
	}
}

func TestManualMergeNeedsAGoneCachedItem(t *testing.T) {
	for name, m := range map[string]ManualRecord{
		"not cached": {Gone: true},
		"not gone":   {Cached: true},
	} {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			matchedItem(t, s)
			m.Request = Request{Server: srv, RatingKey: "a", At: base, Source: SourceManual, Action: ActionRefresh}
			m.Snapshot, m.Merged = item("a", NoSummary), "900"
			if _, err := s.RecordManual(m); err == nil {
				t.Fatal("no error")
			}
			if _, ok := rows(t, s)["a"]; !ok {
				t.Error("the row was removed")
			}
		})
	}
}

// A check that must yield saves a hold and no merge.
func TestMergedCheckYields(t *testing.T) {
	a, _ := openPair(t)
	matchedItem(t, a)
	fixedClock(a, base)
	obs := observe(t, a, nil, base)
	if _, wroteMerge, err := a.ApplyCheckGone(srv, "a", base, SourceAuto, 0, obs, "900"); !errors.Is(err, ErrCheckYielded) || wroteMerge {
		t.Fatalf("wroteMerge = %v, err = %v, want ErrCheckYielded", wroteMerge, err)
	}
	if _, ok := rows(t, a)["a"]; !ok {
		t.Error("the row was removed")
	}
	events, _ := a.Timeline(srv, "a")
	for _, e := range events {
		if e.Kind == EventCompleted || e.Kind == EventGone {
			t.Errorf("event %+v, want none", e)
		}
	}
}

// Migration 19 gives the events of an older database an empty merged key.
func TestMergedKeyMigration(t *testing.T) {
	file := openAtVersion(t, 18)
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO library (server, section) VALUES ('` + srv + `', '` + sec + `')`,
		`INSERT INTO event (server, section, rating_key, at, kind) VALUES ('` + srv + `', '` + sec + `', 'old', 100, 'gone')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Timeline(srv, "old")
	if err != nil || len(events) != 1 || events[0].MergedKey != "" {
		t.Fatalf("events = %+v, %v", events, err)
	}
}
