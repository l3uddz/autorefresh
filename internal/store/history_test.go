package store

import (
	"reflect"
	"testing"
	"time"
)

func TestRecordRequestWritesEventAndOutcome(t *testing.T) {
	s := openTest(t)
	it := item("a", NoArt)
	it.Thumb, it.Art, it.Path = "/t", "/a", "/tv/A"
	apply(t, s, Read{Items: []Item{it}})
	at := base.Add(time.Hour)

	if err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: at, Source: SourceAuto, Action: ActionRefresh, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Timeline(srv, "a")
	if err != nil || len(events) != 1 {
		t.Fatalf("Timeline = %v, %v", events, err)
	}
	want := Event{
		ID: events[0].ID, Server: srv, Section: sec, RatingKey: "a", At: at, Kind: EventRequest, Source: SourceAuto,
		Title: "Title a", Year: 2020, Type: "show", Thumb: "/t", Art: "/a", Path: "/tv/A",
		Action: ActionRefresh, Accepted: true, Outcome: "refresh",
	}
	if !reflect.DeepEqual(events[0], want) {
		t.Fatalf("event = %+v, want %+v", events[0], want)
	}
	got := rows(t, s)["a"]
	if got.LastOutcome != "refresh" || got.Refreshes != 1 || got.LastAction != ActionRefresh {
		t.Fatalf("row = %+v", got)
	}
}

func TestRecordRequestFailedMatchKeepsTarget(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	target := &MatchTarget{GUID: "plex://show/1", Title: "Show", Year: 2020}

	err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: base.Add(time.Hour), Source: SourceManual,
		Action: ActionMatch, Error: "boom", Match: target})
	if err != nil {
		t.Fatal(err)
	}
	events, _ := s.Timeline(srv, "a")
	if len(events) != 1 || events[0].MatchGUID != "plex://show/1" || events[0].Error != "boom" ||
		events[0].Accepted || events[0].Outcome != "match-failed" || events[0].Source != SourceManual {
		t.Fatalf("events = %+v", events)
	}
	got := rows(t, s)["a"]
	if got.LastOutcome != "match-failed" || got.LastAction != "" || got.LastMatch != "" || got.Refreshes != 0 ||
		!got.LastAttempt.Equal(base.Add(time.Hour)) || got.LastMatchEventID != 0 {
		t.Fatalf("row = %+v", got)
	}
	var attempts int
	if err := s.db.QueryRow(`SELECT attempts FROM item WHERE rating_key = 'a'`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("attempts = %d, %v", attempts, err)
	}
}

func TestRecordRequestAcceptedMatchSetsMatchEventID(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	target := &MatchTarget{GUID: "g", Title: "Show", Year: 2020, Links: []string{"tvdb-1", "tmdb-2"},
		PathYear: 2019, TMDBYear: 2020, EpisodeCount: 8}

	err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: base.Add(time.Hour), Source: SourceAuto,
		Action: ActionTMDBMatch, Accepted: true, Match: target})
	if err != nil {
		t.Fatal(err)
	}
	events, _ := s.Timeline(srv, "a")
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	got := rows(t, s)["a"]
	if got.LastMatchEventID != events[0].ID || got.LastMatch != ActionTMDBMatch || got.LastOutcome != "tmdb-match" {
		t.Fatalf("row = %+v, event %d", got, events[0].ID)
	}
	e := events[0]
	if !reflect.DeepEqual(e.Links, []string{"tvdb-1", "tmdb-2"}) || e.PathYear != 2019 || e.TMDBYear != 2020 || e.EpisodeCount != 8 {
		t.Fatalf("event = %+v", e)
	}
}

func TestRecordRequestSearchFailedWritesNoEvent(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})

	if err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: base.Add(time.Hour), Source: SourceAuto}); err != nil {
		t.Fatal(err)
	}
	if events, _ := s.Timeline(srv, "a"); len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
	got := rows(t, s)["a"]
	if got.LastOutcome != "search-failed" || !got.LastAttempt.Equal(base.Add(time.Hour)) {
		t.Fatalf("row = %+v", got)
	}
}

func TestRecordRequestMissingRowWritesNothing(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	err := s.RecordRequest(Request{Server: srv, RatingKey: "missing", At: base, Source: SourceAuto, Action: ActionRefresh, Accepted: true})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM event`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("events = %d, %v", n, err)
	}
}

func TestRecordRequestRequiresSource(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	if err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: base, Action: ActionRefresh, Accepted: true}); err == nil {
		t.Fatal("empty source returned no error")
	}
	if events, _ := s.Timeline(srv, "a"); len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
	if got := rows(t, s)["a"]; got.Refreshes != 0 || !got.LastAttempt.IsZero() || got.LastOutcome != "" {
		t.Fatalf("row = %+v", got)
	}
}

func TestRecordGoneWritesEventAndDeletesRow(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	at := base.Add(time.Hour)

	if err := s.RecordGone(srv, "a", at, SourceAuto); err != nil {
		t.Fatal(err)
	}
	if _, ok := rows(t, s)["a"]; ok {
		t.Fatal("row still exists")
	}
	events, _ := s.Timeline(srv, "a")
	if len(events) != 1 || events[0].Kind != EventGone || events[0].Title != "Title a" || events[0].Section != sec ||
		!events[0].At.Equal(at) || events[0].Source != SourceAuto {
		t.Fatalf("events = %+v", events)
	}
	if err := s.RecordGone(srv, "a", at, SourceAuto); err != nil {
		t.Fatalf("missing row: %v", err)
	}
	if events, _ := s.Timeline(srv, "a"); len(events) != 1 {
		t.Fatalf("events after missing row = %+v", events)
	}
}

func TestTimelineOrdersSameSecondByID(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	for _, a := range []Action{ActionRefresh, ActionMatch} {
		err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: base, Source: SourceAuto, Action: a, Accepted: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.Timeline(srv, "a")
	if err != nil || len(events) != 2 || events[0].Action != ActionMatch || events[0].ID <= events[1].ID {
		t.Fatalf("events = %+v, %v", events, err)
	}
}
