package store

import (
	"database/sql"
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
	target := &MatchTarget{GUID: "g", Title: "Show", Year: 2020, Links: []string{"tvdb-1", "imdb-tt1,x"},
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
	if !reflect.DeepEqual(e.Links, []string{"tvdb-1", "imdb-tt1,x"}) || e.PathYear != 2019 || e.TMDBYear != 2020 || e.EpisodeCount != 8 {
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

	if err := s.RecordGone(srv, "a", at, SourceAuto, ActionRefresh, 0, AttemptResult{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rows(t, s)["a"]; ok {
		t.Fatal("row still exists")
	}
	events, _ := s.Timeline(srv, "a")
	if len(events) != 1 || events[0].Kind != EventGone || events[0].Title != "Title a" || events[0].Section != sec ||
		!events[0].At.Equal(at) || events[0].Source != SourceAuto || events[0].Action != ActionRefresh {
		t.Fatalf("events = %+v", events)
	}
	if err := s.RecordGone(srv, "a", at, SourceAuto, "", 0, AttemptResult{}); err != nil {
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

// accept records an accepted request of action for the row at at.
func accept(t *testing.T, s *Store, key string, at time.Time, action Action) {
	t.Helper()
	err := s.RecordRequest(Request{Server: srv, RatingKey: key, At: at, Source: SourceAuto, Action: action, Accepted: true})
	if err != nil {
		t.Fatal(err)
	}
}

// completedEvent returns the only completed event of the row.
func completedEvent(t *testing.T, s *Store, key string) Event {
	t.Helper()
	events, err := s.Timeline(srv, key)
	if err != nil {
		t.Fatal(err)
	}
	var got []Event
	for _, e := range events {
		if e.Kind == EventCompleted {
			got = append(got, e)
		}
	}
	if len(got) != 1 {
		t.Fatalf("completed events = %+v, want one", got)
	}
	return got[0]
}

func TestApplyReadWritesCompletedEvent(t *testing.T) {
	s := openTest(t)
	it := item("a", NoArt)
	it.Thumb, it.Art, it.Path = "/t", "/a", "/tv/A"
	apply(t, s, Read{Items: []Item{it}})
	accept(t, s, "a", base.Add(time.Minute), ActionRefresh)

	at := base.Add(time.Hour)
	apply(t, s, Read{At: at, Items: []Item{item("a", 0)}})

	want := Event{
		ID: 0, Server: srv, Section: sec, RatingKey: "a", At: at, Kind: EventCompleted, Source: SourceAuto,
		Title: "Title a", Year: 2020, Type: "show", Thumb: "/t", Art: "/a", Path: "/tv/A", FixedAction: ActionRefresh,
	}
	got := completedEvent(t, s, "a")
	want.ID = got.ID
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
}

func TestCompletedEventCreditsCurrentOccurrence(t *testing.T) {
	s := openTest(t)
	var requestIDs []int64
	for i := 0; i < 2; i++ {
		apply(t, s, Read{Items: []Item{item("a", NoArt)}})
		accept(t, s, "a", base.Add(time.Minute), ActionMatch)
		requestIDs = append(requestIDs, rows(t, s)["a"].LastMatchEventID)
		apply(t, s, Read{At: base.Add(time.Hour), Items: []Item{item("a", 0)}})
	}
	events, _ := s.Timeline(srv, "a")
	var credited []int64
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == EventCompleted {
			credited = append(credited, events[i].FixedMatchEventID)
		}
	}
	if !reflect.DeepEqual(credited, requestIDs) || requestIDs[0] == requestIDs[1] {
		t.Fatalf("credited = %v, want %v", credited, requestIDs)
	}
}

func TestCompletedAfterPlainRefreshHasNoMatch(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	accept(t, s, "a", base.Add(time.Minute), ActionMatch)
	apply(t, s, Read{Items: []Item{item("a", 0)}})
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	accept(t, s, "a", base.Add(2*time.Minute), ActionRefresh)
	apply(t, s, Read{At: base.Add(time.Hour), Items: []Item{item("a", 0)}})

	events, _ := s.Timeline(srv, "a")
	last := events[0]
	if last.Kind != EventCompleted || last.FixedAction != ActionRefresh || last.FixedMatch != "" || last.FixedMatchEventID != 0 {
		t.Fatalf("latest event = %+v", last)
	}
}

func TestCompletedEventPrefersReadMetadata(t *testing.T) {
	s := openTest(t)
	old := item("a", NoArt)
	old.Title, old.Path = "Old", "/tv/A"
	apply(t, s, Read{Items: []Item{old}})

	fresh := item("a", 0)
	fresh.Title, fresh.Thumb = "New", "/t"
	apply(t, s, Read{Items: []Item{fresh}})

	got := completedEvent(t, s, "a")
	if got.Title != "New" || got.Thumb != "/t" || got.Path != "/tv/A" {
		t.Fatalf("event = %+v", got)
	}
}

func TestMovedRowCompletesInNewSection(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	moved := item("a", 0)
	moved.Section = "2"
	apply(t, s, Read{Section: "2", Items: []Item{moved}})

	if got := completedEvent(t, s, "a"); got.Section != "2" {
		t.Fatalf("event = %+v, want section 2", got)
	}
}

func TestLegacyRowCompletesWithoutMatchEvent(t *testing.T) {
	file := openAtVersion(t, 5)
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO library (server, section) VALUES ('srv', '1')`,
		`INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons, refreshes, attempts, last_action, last_match)
			VALUES ('srv', '1', 'a', 'movie', 'Title a', 2020, 1000, 2, 1, 1, 'match', 'match')`,
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
	apply(t, s, Read{Items: []Item{item("a", 0)}})

	got := completedEvent(t, s, "a")
	if got.FixedMatch != ActionMatch || got.FixedAction != ActionMatch || got.FixedMatchEventID != 0 {
		t.Fatalf("event = %+v", got)
	}
}

func TestCompletedEventUsesStagedFolder(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	apply(t, s, Read{Full: true, Items: []Item{item("a", 0)}, Folders: map[string]string{"a": "/tv/A"}})

	if got := completedEvent(t, s, "a"); got.Path != "/tv/A" {
		t.Fatalf("path = %q, want /tv/A", got.Path)
	}
}

func TestCompletesAfterMatchEventExpired(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	accept(t, s, "a", base.Add(time.Minute), ActionMatch)
	if _, err := s.db.Exec(`DELETE FROM event WHERE kind = 'request'`); err != nil {
		t.Fatal(err)
	}
	apply(t, s, Read{Items: []Item{item("a", 0)}})

	got := completedEvent(t, s, "a")
	if got.FixedMatch != ActionMatch || got.FixedMatchEventID != 0 {
		t.Fatalf("event = %+v", got)
	}
}

// An item whose accepted match was in a library that was later pruned
// still completes: the pruned library takes its match event with it, and
// the completion stores no event ID instead of failing the read.
func TestMovedItemCompletesAfterOldLibraryPruned(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	accept(t, s, "a", base.Add(time.Minute), ActionMatch)

	moved := item("a", NoArt)
	moved.Section = "2"
	apply(t, s, Read{Section: "2", Items: []Item{moved}})
	if _, err := s.PruneSections(srv, []string{"2"}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM event WHERE kind = 'request'`); n != 0 {
		t.Fatalf("request events = %d, want 0 after the prune", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM item WHERE rating_key = 'a' AND last_match_event_id IS NOT NULL`); n != 0 {
		t.Fatalf("rows with a stale match event ID = %d, want 0", n)
	}

	done := item("a", 0)
	done.Section = "2"
	apply(t, s, Read{Section: "2", Items: []Item{done}})

	got := completedEvent(t, s, "a")
	if got.Section != "2" || got.FixedMatch != ActionMatch || got.FixedMatchEventID != 0 {
		t.Fatalf("event = %+v", got)
	}
}

func TestApplyReadWritesGoneEvents(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt)}})
	at := base.Add(time.Hour)
	apply(t, s, Read{At: at, Full: true, Items: []Item{item("b", NoArt)}, Gone: []string{"a", "missing"}})

	events, _ := s.Timeline(srv, "a")
	if len(events) != 1 || events[0].Kind != EventGone || events[0].Title != "Title a" || events[0].Source != SourceAuto ||
		!events[0].At.Equal(at) {
		t.Fatalf("events = %+v", events)
	}
	if events, _ := s.Timeline(srv, "missing"); len(events) != 0 {
		t.Fatalf("events of a missing row = %+v", events)
	}
}

func TestDryRunReadWritesNoEvents(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt), item("c", NoArt)}})
	at := base.Add(time.Hour)
	fixed := apply(t, s, Read{At: at, Full: true, DryRun: true, Items: []Item{item("a", 0), item("c", NoArt)}, Gone: []string{"b"}})

	if len(fixed) != 1 || fixed[0].RatingKey != "a" {
		t.Fatalf("fixed = %+v", fixed)
	}
	if got := rows(t, s); len(got) != 1 || got["c"].RatingKey != "c" {
		t.Fatalf("rows = %+v", got)
	}
	if lib, _ := s.Library(srv, sec); !lib.LastFullRead.Equal(at) {
		t.Fatalf("library = %+v", lib)
	}
	for _, key := range []string{"a", "b", "c"} {
		if events, _ := s.Timeline(srv, key); len(events) != 0 {
			t.Fatalf("events of %s = %+v", key, events)
		}
	}
}

func TestRecordMatchDecisionRoundTrips(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	at := base.Add(time.Hour)
	searches := []MatchSearch{
		{Query: "tmdb-1", Source: "path", Candidates: []MatchCandidate{
			{GUID: "plex://show/1", Name: "A", Year: 2020, Type: "show", Summary: "S", Thumb: "/t", Score: 90, Verdict: "picked"},
			{GUID: "plex://show/2", Name: "B", Year: 2019, Score: 40, Verdict: "year-mismatch"},
		}},
		{Query: "tmdb-2", Source: "tmdb", FromID: "tvdb-9", Failed: true},
	}
	evidence := MatchEvidence{TMDBID: "77", TMDBSkip: TMDBFailed, TMDBMissing: []string{"tvdb-9", "imdb-tt1"},
		EpisodeGroup: "DVD Order",
		EpisodeDiff:  EpisodeDiff{Season: 3, Plex: 8, TMDB: 10, Missing: []int{9, 10}, Extra: []int{11}}}
	err := s.RecordMatchDecision(MatchDecision{
		Server: srv, RatingKey: "a", At: at, Source: SourceManual, Outcome: DecisionMatched,
		Conflict: "c", Error: "e", PathYear: 2020, TMDBYear: 2021, EpisodeCount: 8, Searches: searches,
		MatchEvidence: evidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Timeline(srv, "a")
	if err != nil || len(events) != 1 {
		t.Fatalf("Timeline = %v, %v", events, err)
	}
	e := events[0]
	if e.Kind != EventMatchDecision || e.Outcome != "matched" || e.Source != SourceManual || !e.At.Equal(at) ||
		e.Title != "Title a" || e.Conflict != "c" || e.Error != "e" || e.PathYear != 2020 || e.TMDBYear != 2021 || e.EpisodeCount != 8 {
		t.Fatalf("event = %+v", e)
	}
	if !reflect.DeepEqual(e.Searches, searches) {
		t.Fatalf("searches = %+v, want %+v", e.Searches, searches)
	}
	if !reflect.DeepEqual(e.MatchEvidence, evidence) {
		t.Fatalf("evidence = %+v, want %+v", e.MatchEvidence, evidence)
	}
	if got := rows(t, s)["a"]; got.LastMatchOutcome != "matched" {
		t.Fatalf("row = %+v", got)
	}
}

func TestRecordMatchDecisionNoIDs(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	err := s.RecordMatchDecision(MatchDecision{Server: srv, RatingKey: "a", At: base, Source: SourceAuto, Outcome: DecisionNoIDs})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Timeline(srv, "a")
	if err != nil || len(events) != 1 || events[0].Outcome != "no-ids" || events[0].Searches != nil ||
		events[0].TMDBMissing != nil {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if err := s.RecordMatchDecision(MatchDecision{Server: srv, RatingKey: "zz", At: base, Source: SourceAuto, Outcome: DecisionNoIDs}); err != nil {
		t.Fatalf("missing row: %v", err)
	}
	if events, _ := s.Timeline(srv, "zz"); len(events) != 0 {
		t.Fatalf("events for missing row = %+v", events)
	}
}

func TestTimelineWithSeveralDecisions(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt)}})
	cand := func(g string) []MatchCandidate { return []MatchCandidate{{GUID: g, Verdict: "picked"}} }
	decide := func(key string, at time.Time, searches ...MatchSearch) {
		t.Helper()
		err := s.RecordMatchDecision(MatchDecision{Server: srv, RatingKey: key, At: at, Source: SourceAuto,
			Outcome: DecisionMatched, Searches: searches})
		if err != nil {
			t.Fatal(err)
		}
	}
	decide("a", base, MatchSearch{Query: "q1", Candidates: cand("g1")}, MatchSearch{Query: "q2", Candidates: cand("g2")})
	accept(t, s, "a", base.Add(time.Minute), ActionMatch)
	decide("a", base.Add(2*time.Minute), MatchSearch{Query: "q3"})
	decide("b", base, MatchSearch{Query: "other", Candidates: cand("gb")})

	events, err := s.Timeline(srv, "a")
	if err != nil || len(events) != 3 {
		t.Fatalf("Timeline = %+v, %v", events, err)
	}
	if got := events[0].Searches; len(got) != 1 || got[0].Query != "q3" || got[0].Candidates != nil {
		t.Fatalf("newest searches = %+v", got)
	}
	if events[1].Kind != EventRequest || events[1].Searches != nil {
		t.Fatalf("request event = %+v", events[1])
	}
	got := events[2].Searches
	if len(got) != 2 || got[0].Query != "q1" || got[0].Candidates[0].GUID != "g1" || got[1].Query != "q2" || got[1].Candidates[0].GUID != "g2" {
		t.Fatalf("oldest searches = %+v", got)
	}
}

func TestManualStoreLifecycle(t *testing.T) {
	s := openTest(t)
	it := item("a", NoSummary)
	apply(t, s, Read{Items: []Item{it}})
	if changed, err := s.SetStopped(t.Context(), srv, "a", base, true); err != nil || !changed {
		t.Fatalf("stop = %v, %v", changed, err)
	}
	if changed, err := s.SetStopped(t.Context(), srv, "a", base, true); err != nil || changed {
		t.Fatalf("repeated stop = %v, %v", changed, err)
	}
	if changed, err := s.SetStopped(t.Context(), srv, "a", base, false); err != nil || !changed {
		t.Fatalf("resume = %v, %v", changed, err)
	}
	request := Request{Server: srv, RatingKey: "a", At: base, Source: SourceManual, Action: ActionRefresh, Accepted: true}
	if err := s.RecordManual(ManualRecord{Request: request, Snapshot: it, Cached: true}); err != nil {
		t.Fatal(err)
	}
	row, cached, found, err := s.KnownItem(srv, "a")
	if err != nil || !cached || !found || row.Attempts != 1 || !row.StoppedAt.IsZero() {
		t.Fatalf("cached item = %+v, %v, %v, %v", row, cached, found, err)
	}
	it.Reasons, it.Title = 0, "Updated"
	if completed, err := s.ApplyCheck(srv, it, base, 0); err != nil || !completed {
		t.Fatalf("check = %v, %v", completed, err)
	}
	row, cached, found, err = s.KnownItem(srv, "a")
	if err != nil || cached || !found || row.Title != "Updated" {
		t.Fatalf("snapshot = %+v, %v, %v, %v", row, cached, found, err)
	}
	events, err := s.Timeline(srv, "a")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []EventKind
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	if want := []EventKind{EventCompleted, EventRequest, EventResumed, EventStopped}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
}

func TestRecordManualUsesAdmissionSnapshotAcrossRowChanges(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		cached, gone, rowExists bool
		kind                    EventKind
		leavesRow               bool
	}{
		{"history admission with new row", false, false, true, EventRequest, true},
		{"cached admission after row removed", true, false, false, EventRequest, false},
		{"cached gone with row", true, true, true, EventGone, false},
		{"cached gone after row removed", true, true, false, EventGone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			snapshot := item("1", NoSummary)
			apply(t, s, Read{Items: []Item{snapshot}})
			if !tc.rowExists {
				complete := snapshot
				complete.Reasons = 0
				apply(t, s, Read{Items: []Item{complete}, DryRun: true})
			}
			req := Request{Server: srv, RatingKey: "1", At: base, Source: SourceManual, Action: ActionRefresh, Accepted: !tc.gone}
			if err := s.RecordManual(ManualRecord{Request: req, Snapshot: snapshot, Cached: tc.cached, Gone: tc.gone}); err != nil {
				t.Fatal(err)
			}
			row, cached, found, err := s.KnownItem(srv, "1")
			if err != nil || !found || cached != tc.leavesRow || row.Attempts != 0 {
				t.Fatalf("known item = %+v, cached %v, found %v, %v", row, cached, found, err)
			}
			events, err := s.Timeline(srv, "1")
			if err != nil || len(events) != 1 || events[0].Kind != tc.kind || events[0].Title != snapshot.Title || events[0].Source != SourceManual {
				t.Fatalf("events = %+v, %v", events, err)
			}
			// Each event keeps the action of the request, also a gone event.
			if events[0].Action != ActionRefresh {
				t.Errorf("event action = %q, want %q", events[0].Action, ActionRefresh)
			}
		})
	}
}

// A decision from before migration 14 reads with empty evidence.
func TestEvidenceMigrationKeepsOldDecisions(t *testing.T) {
	file := openAtVersion(t, 13)
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO library (server, section) VALUES ('` + srv + `', '` + sec + `')`,
		`INSERT INTO event (server, section, rating_key, at, kind, outcome, conflict, path_year)
			VALUES ('` + srv + `', '` + sec + `', 'old', 100, 'match-decision', 'no-match', 'year differs', 2020)`,
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
	if err != nil || len(events) != 1 {
		t.Fatalf("Timeline = %+v, %v", events, err)
	}
	if e := events[0]; e.PathYear != 2020 || !reflect.DeepEqual(e.MatchEvidence, MatchEvidence{}) {
		t.Fatalf("event = %+v, want the old fields and empty evidence", e)
	}
}
