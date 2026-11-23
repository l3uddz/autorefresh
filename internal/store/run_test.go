package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestStartAndFinishRun(t *testing.T) {
	s := openTest(t)
	start := RunStart{Server: srv, Kind: RunKindRun, Source: RunSourceSchedule, StartedAt: base}
	id, err := s.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := s.Runs(context.Background(), RunQuery{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("Runs = %v, %v", runs, err)
	}
	want := Run{ID: id, Server: srv, Kind: RunKindRun, Source: RunSourceSchedule, Status: RunRunning, StartedAt: base, EventsLinked: true}
	if !reflect.DeepEqual(runs[0], want) {
		t.Fatalf("running = %+v, want %+v", runs[0], want)
	}

	end := base.Add(time.Minute)
	_, err = s.FinishRun(id, start, RunResult{
		FinishedAt: end, Status: RunFailed, Error: "boom",
		ReadItems: 1, Refreshed: 2, Matched: 3, Failed: 4, Completed: 5, Fixed: 6, Incomplete: 7, Due: 8, Selected: 9,
		FullReads: 10, IncrementalReads: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	runs, err = s.Runs(context.Background(), RunQuery{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("Runs = %v, %v", runs, err)
	}
	want = Run{
		ID: id, Server: srv, Kind: RunKindRun, Source: RunSourceSchedule, Status: RunFailed, StartedAt: base,
		FinishedAt: end, Error: "boom",
		ReadItems: 1, Refreshed: 2, Matched: 3, Failed: 4, Completed: 5, Fixed: 6, Incomplete: 7, Due: 8, Selected: 9,
		FullReads: 10, IncrementalReads: 11, EventsLinked: true,
	}
	if !reflect.DeepEqual(runs[0], want) {
		t.Fatalf("finished = %+v, want %+v", runs[0], want)
	}
}

// runStart is how startRun starts a run of server at.
func runStart(server string, at time.Time) RunStart {
	return RunStart{Server: server, Kind: RunKindRun, Source: RunSourceManual, StartedAt: at}
}

func startRun(t *testing.T, s *Store, server string, at time.Time) int64 {
	t.Helper()
	id, err := s.StartRun(runStart(server, at))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRunsOrderAndLimit(t *testing.T) {
	s := openTest(t)
	a := startRun(t, s, srv, base)
	b := startRun(t, s, srv, base)
	c := startRun(t, s, srv, base.Add(time.Hour))

	runs, err := s.Runs(context.Background(), RunQuery{})
	if err != nil || len(runs) != 3 || runs[0].ID != c || runs[1].ID != b || runs[2].ID != a {
		t.Fatalf("Runs = %+v, %v", runs, err)
	}
	runs, err = s.Runs(context.Background(), RunQuery{Limit: 1})
	if err != nil || len(runs) != 1 || runs[0].ID != c {
		t.Fatalf("Runs with Limit 1 = %+v, %v", runs, err)
	}
}

func TestInterruptRunsMarksOnlyRunning(t *testing.T) {
	s := openTest(t)
	done := startRun(t, s, srv, base)
	end := base.Add(time.Minute)
	if _, err := s.FinishRun(done, runStart(srv, base), RunResult{FinishedAt: end, Status: RunFinished}); err != nil {
		t.Fatal(err)
	}
	open := startRun(t, s, srv, base.Add(time.Hour))

	at := base.Add(2 * time.Hour)
	n, err := s.InterruptRuns(at)
	if err != nil || n != 1 {
		t.Fatalf("InterruptRuns = %d, %v", n, err)
	}
	runs, err := s.Runs(context.Background(), RunQuery{})
	if err != nil || len(runs) != 2 {
		t.Fatalf("Runs = %+v, %v", runs, err)
	}
	if r := runs[0]; r.ID != open || r.Status != RunInterrupted || !r.FinishedAt.Equal(at) {
		t.Fatalf("interrupted = %+v", r)
	}
	if r := runs[1]; r.ID != done || r.Status != RunFinished || !r.FinishedAt.Equal(end) {
		t.Fatalf("finished = %+v", r)
	}
}

func count(t *testing.T, s *Store, query string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A run that is still running is never pruned, so its FinishRun still
// finds the row, however long the run takes.
func TestPruneHistoryKeepsRunningRuns(t *testing.T) {
	s := openTest(t)
	id, err := s.StartRun(RunStart{Server: srv, Kind: RunKindRun, Source: RunSourceSchedule, StartedAt: base})
	if err != nil {
		t.Fatal(err)
	}

	_, runs, err := s.PruneHistory(base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("pruned runs = %d, want 0", runs)
	}
	if _, err := s.FinishRun(id, RunStart{Server: srv, Kind: RunKindRun, Source: RunSourceSchedule, StartedAt: base}, RunResult{FinishedAt: base.Add(2 * time.Hour), Status: RunFinished, Refreshed: 3}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Runs(context.Background(), RunQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id || got[0].Status != RunFinished || got[0].Refreshed != 3 {
		t.Fatalf("runs = %+v, want the finished run %d", got, id)
	}

	// Once it ended before the cutoff, the run goes with the next prune.
	if _, runs, err = s.PruneHistory(base.Add(3 * time.Hour)); err != nil || runs != 1 {
		t.Fatalf("second prune = %d runs, %v; want 1, nil", runs, err)
	}
}

// A run that the scheduled service marked interrupted while another
// process (for example --once) still ran it is kept for the whole
// retention after the mark, so that process's FinishRun still finds it.
func TestPruneHistoryKeepsRecentlyInterruptedRuns(t *testing.T) {
	s := openTest(t)
	id := startRun(t, s, srv, base)
	restart := base.Add(800 * time.Hour)
	if _, err := s.InterruptRuns(restart); err != nil {
		t.Fatal(err)
	}

	if _, runs, err := s.PruneHistory(restart.Add(-720 * time.Hour)); err != nil || runs != 0 {
		t.Fatalf("prune = %d runs, %v; want 0, nil", runs, err)
	}
	if _, err := s.FinishRun(id, runStart(srv, base), RunResult{FinishedAt: restart.Add(time.Hour), Status: RunFinished, Refreshed: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Runs(context.Background(), RunQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != RunFinished || got[0].Refreshed != 2 {
		t.Fatalf("runs = %+v, want the finished run %d", got, id)
	}
}

// A run whose row another process removed while it ran (for example the
// retention of the scheduled service after it marked the run interrupted)
// is stored again by FinishRun, under the same ID.
func TestFinishRunRestoresRemovedRow(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	start := runStart(srv, base)
	id := startRun(t, s, srv, base)
	if _, err := s.InterruptRuns(base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, runs, err := s.PruneHistory(base.Add(2 * time.Minute)); err != nil || runs != 1 {
		t.Fatalf("prune = %d runs, %v; want 1, nil", runs, err)
	}

	end := base.Add(5 * time.Minute)
	if _, err := s.FinishRun(id, start, RunResult{FinishedAt: end, Status: RunFinished, Refreshed: 4, FullReads: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Runs(context.Background(), RunQuery{})
	if err != nil {
		t.Fatal(err)
	}
	want := Run{ID: id, Server: srv, Kind: RunKindRun, Source: RunSourceManual, Status: RunFinished,
		StartedAt: base, FinishedAt: end, Refreshed: 4, FullReads: 2, EventsLinked: true}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
	// A later run still gets a new ID.
	if next := startRun(t, s, srv, end); next <= id {
		t.Errorf("next run ID = %d, want above %d", next, id)
	}
}

// The runs of a server that PruneServers removed stay removed: FinishRun
// does not store them again.
func TestFinishRunKeepsRemovedServerRemoved(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	start := runStart(srv, base)
	id := startRun(t, s, srv, base)
	if _, err := s.PruneServers([]string{"other"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.FinishRun(id, start, RunResult{FinishedAt: base.Add(time.Minute), Status: RunFinished}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Runs(context.Background(), RunQuery{}); err != nil || len(got) != 0 {
		t.Fatalf("runs = %+v, %v; want none", got, err)
	}
}

func TestPruneHistoryDeletesOldRowsWithChildren(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	old := base.Add(time.Hour)
	err := s.RecordMatchDecision(MatchDecision{Server: srv, RatingKey: "a", At: old, Source: SourceAuto, Outcome: DecisionMatched,
		Searches: []MatchSearch{{Query: "tmdb-1", Source: "path", Candidates: []MatchCandidate{{GUID: "g", Name: "A"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	fresh := base.Add(48 * time.Hour)
	accept(t, s, "a", fresh, ActionRefresh)
	oldRun := startRun(t, s, srv, old)
	if _, err := s.FinishRun(oldRun, runStart(srv, old), RunResult{FinishedAt: old, Status: RunFinished}); err != nil {
		t.Fatal(err)
	}
	newRun := startRun(t, s, srv, fresh)

	events, runs, err := s.PruneHistory(base.Add(24 * time.Hour))
	if err != nil || events != 1 || runs != 1 {
		t.Fatalf("PruneHistory = %d, %d, %v", events, runs, err)
	}
	if got, _ := s.Timeline(srv, "a"); len(got) != 1 || got[0].Kind != EventRequest {
		t.Fatalf("timeline = %+v", got)
	}
	for _, table := range []string{"match_search", "match_candidate"} {
		if n := count(t, s, `SELECT COUNT(*) FROM `+table); n != 0 {
			t.Errorf("%s rows = %d, want 0", table, n)
		}
	}
	if got, _ := s.Runs(context.Background(), RunQuery{}); len(got) != 1 || got[0].ID != newRun {
		t.Fatalf("runs = %+v", got)
	}
}

func TestCompleteAfterPruneHistory(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	accept(t, s, "a", base.Add(time.Minute), ActionMatch)
	if got := rows(t, s)["a"]; got.LastMatchEventID == 0 {
		t.Fatalf("row = %+v, want a match event", got)
	}

	if _, _, err := s.PruneHistory(base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := rows(t, s)["a"]; got.LastMatchEventID != 0 {
		t.Fatalf("LastMatchEventID = %d, want 0", got.LastMatchEventID)
	}
	apply(t, s, Read{Items: []Item{item("a", 0)}})
	got := completedEvent(t, s, "a")
	if got.FixedMatch != ActionMatch || got.FixedMatchEventID != 0 {
		t.Fatalf("event = %+v", got)
	}
}

func TestPruneServersDeletesRunsOfRemovedServers(t *testing.T) {
	s := openTest(t)
	keep := startRun(t, s, srv, base)
	startRun(t, s, "gone", base)

	if _, err := s.PruneServers([]string{srv}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Runs(context.Background(), RunQuery{}); len(got) != 1 || got[0].ID != keep {
		t.Fatalf("runs = %+v", got)
	}
	if _, err := s.PruneServers(nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Runs(context.Background(), RunQuery{}); err != nil || len(got) != 0 {
		t.Fatalf("runs = %+v, %v", got, err)
	}
}

func runIDs(runs []Run) []int64 {
	out := []int64{}
	for _, r := range runs {
		out = append(out, r.ID)
	}
	return out
}

func TestRunsFiltersAndPages(t *testing.T) {
	s := openTest(t)
	r1 := startRun(t, s, srv, base)
	r2 := startRun(t, s, "srv2", base.Add(time.Hour))
	// r3 starts at the same time as r2, so the id splits the pair.
	r3 := startRun(t, s, srv, base.Add(time.Hour))
	r4 := startRun(t, s, "srv2", base.Add(2*time.Hour))
	if _, err := s.FinishRun(r1, runStart(srv, base), RunResult{FinishedAt: base.Add(time.Minute), Status: RunFinished}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		q    RunQuery
		want []int64
	}{
		{"all", RunQuery{}, []int64{r4, r3, r2, r1}},
		{"servers", RunQuery{Servers: []string{srv}}, []int64{r3, r1}},
		{"several servers", RunQuery{Servers: []string{srv, "srv2"}}, []int64{r4, r3, r2, r1}},
		{"status", RunQuery{Status: RunFinished}, []int64{r1}},
		{"before the second run", RunQuery{Before: &RunCursor{StartedAt: base.Add(time.Hour), ID: r3}}, []int64{r2, r1}},
		{"limit", RunQuery{Limit: 1}, []int64{r4}},
		{"combined", RunQuery{Servers: []string{"srv2"}, Status: RunRunning, Before: &RunCursor{StartedAt: base.Add(2 * time.Hour), ID: r4}}, []int64{r2}},
	}
	for _, tt := range tests {
		runs, err := s.Runs(context.Background(), tt.q)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := runIDs(runs); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: runs = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestFinishJobResultsAreAtomic(t *testing.T) {
	s := openTest(t)
	start := runStart(srv, base)
	start.Kind = RunKindBulkRefresh
	id, err := s.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	result := RunResult{FinishedAt: base, Status: RunFinished, Skipped: 1}
	first := RunItem{Position: 0, RatingKey: "a", Outcome: ItemStopped}
	second := RunItem{Position: 1, RatingKey: "b", Outcome: ItemAccepted}
	if _, err := s.FinishJob(id, start, result, []RunItem{first, first}); err == nil {
		t.Fatal("duplicate positions succeeded")
	}
	rows, err := s.Runs(t.Context(), RunQuery{})
	if err != nil || len(rows) != 1 || rows[0].Status != RunRunning {
		t.Fatalf("runs = %+v, %v", rows, err)
	}
	items, err := s.RunItems(t.Context(), id)
	if err != nil || len(items) != 0 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if written, err := s.FinishJob(id, start, result, []RunItem{second, first}); err != nil || !written {
		t.Fatalf("finish = %v, %v", written, err)
	}
	items, err = s.RunItems(t.Context(), id)
	if err != nil || len(items) != 2 || items[0] != first || items[1] != second {
		t.Fatalf("items = %+v, %v", items, err)
	}
	rows, err = s.Runs(t.Context(), RunQuery{})
	if err != nil || len(rows) != 1 || rows[0].Status != RunFinished || rows[0].Skipped != 1 {
		t.Fatalf("runs = %+v, %v", rows, err)
	}
}

func TestFinishJobAfterServerPrune(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("1", NoSummary)}})
	start := runStart(srv, base)
	start.Kind = RunKindBulkRefresh
	id, err := s.StartRun(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneServers([]string{"other"}); err != nil {
		t.Fatal(err)
	}
	written, err := s.FinishJob(id, start, RunResult{FinishedAt: base, Status: RunFinished}, []RunItem{{RatingKey: "1", Outcome: ItemAccepted}})
	if err != nil || written {
		t.Fatalf("finish after prune = %v, %v", written, err)
	}
	items, err := s.RunItems(t.Context(), id)
	if err != nil || len(items) != 0 {
		t.Fatalf("item results = %+v, %v", items, err)
	}
}

// RunEvents returns the events that a run wrote, oldest first: requests,
// match decisions, and the completions and gone items of its reads.
func TestRunEventsOfRun(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt), item("c", NoArt)}})
	one := startRun(t, s, srv, base)
	two := startRun(t, s, srv, base)
	at := base.Add(time.Hour)
	requests := []Request{
		{Server: srv, RatingKey: "a", At: at, Source: SourceAuto, Action: ActionRefresh, Accepted: true, RunID: one},
		{Server: srv, RatingKey: "b", At: at, Source: SourceAuto, Action: ActionRefresh, Accepted: true, RunID: two},
		{Server: srv, RatingKey: "c", At: at, Source: SourceManual, Action: ActionRefresh, Accepted: true},
	}
	for _, r := range requests {
		if err := s.RecordRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordMatchDecision(MatchDecision{Server: srv, RatingKey: "c", At: at, Source: SourceAuto,
		Outcome: DecisionNoMatch, RunID: one}); err != nil {
		t.Fatal(err)
	}
	later := at.Add(time.Hour)
	apply(t, s, Read{At: later, Full: true, Items: []Item{item("a", 0), item("b", NoArt)}, Gone: []string{"c"}, RunID: one})

	events, err := s.RunEvents(context.Background(), one)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		if e.RunID != one {
			t.Errorf("event %d has run %d, want %d", e.ID, e.RunID, one)
		}
		got = append(got, e.RatingKey+" "+string(e.Kind))
	}
	want := []string{"a request", "c match-decision", "c gone", "a completed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("run events = %v, want %v", got, want)
	}
}

// An event of a run whose row is gone stores no run, and the write succeeds.
func TestEventOfMissingRunStoresNoRun(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	if err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: base, Source: SourceAuto,
		Action: ActionRefresh, Accepted: true, RunID: 999}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Timeline(srv, "a")
	if err != nil || len(events) != 1 || events[0].RunID != 0 {
		t.Fatalf("events = %+v, %v; want one event without a run", events, err)
	}
}

// Pruning a run keeps its events, which lose only the link to it.
func TestPruneRunKeepsItsEvents(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	start := runStart(srv, base)
	id := startRun(t, s, srv, base)
	if _, err := s.FinishRun(id, start, RunResult{FinishedAt: base, Status: RunFinished}); err != nil {
		t.Fatal(err)
	}
	at := base.Add(2 * time.Hour)
	if err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: at, Source: SourceAuto,
		Action: ActionRefresh, Accepted: true, RunID: id}); err != nil {
		t.Fatal(err)
	}
	if _, runs, err := s.PruneHistory(base.Add(time.Hour)); err != nil || runs != 1 {
		t.Fatalf("prune = %d runs, %v; want 1", runs, err)
	}
	events, err := s.Timeline(srv, "a")
	if err != nil || len(events) != 1 || events[0].RunID != 0 {
		t.Fatalf("events = %+v, %v; want the event without its run", events, err)
	}
}

func TestRunByID(t *testing.T) {
	s := openTest(t)
	id := startRun(t, s, srv, base)
	run, found, err := s.Run(context.Background(), id)
	if err != nil || !found || run.ID != id || run.Status != RunRunning {
		t.Fatalf("Run(%d) = %+v, %v, %v", id, run, found, err)
	}
	if _, found, err := s.Run(context.Background(), id+1); err != nil || found {
		t.Fatalf("Run(%d) = found %v, %v; want not found", id+1, found, err)
	}
}

// A run from before runs linked their events says so, and a new run links
// its events, so the dashboard can tell an old run without recorded items
// from a run that changed none.
func TestRunEventsLinkedMarksOnlyNewRuns(t *testing.T) {
	file := openAtVersion(t, 12)
	old, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO run (id, server, kind, source, status, started_at) VALUES (1, ?, 'run', 'schedule', 'finished', ?)`,
		srv, base.Unix()); err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	id, err := s.StartRun(RunStart{Server: srv, Kind: RunKindRun, Source: RunSourceManual, StartedAt: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		id   int64
		want bool
	}{{1, false}, {id, true}} {
		run, found, err := s.Run(context.Background(), tt.id)
		if err != nil || !found {
			t.Fatalf("Run(%d) = %v, %v", tt.id, found, err)
		}
		if run.EventsLinked != tt.want {
			t.Errorf("run %d EventsLinked = %v, want %v", tt.id, run.EventsLinked, tt.want)
		}
	}
}
