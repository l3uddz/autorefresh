package store

import (
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"
)

const (
	srv = "srv"
	sec = "1"
)

var base = time.Unix(1_700_000_000, 0)

func item(key string, reasons Reasons) Item {
	return Item{
		Server: srv, Section: sec, RatingKey: key, Type: "show",
		Title: "Title " + key, Year: 2020, AddedAt: base, Reasons: reasons,
	}
}

func apply(t *testing.T, s *Store, r Read) []Item {
	t.Helper()
	if r.Server == "" {
		r.Server = srv
	}
	if r.Section == "" {
		r.Section = sec
	}
	if r.At.IsZero() {
		r.At = base
	}
	res, err := s.ApplyRead(r)
	fixed := res.Fixed
	if err != nil {
		t.Fatalf("ApplyRead: %v", err)
	}
	return fixed
}

// readTimes returns the read times of a library.
func readTimes(s *Store, server, section string) (Library, error) {
	st, err := s.ReadStart(server, section)
	return st.Library, err
}

// eventMark returns the mark of the newest event now.
func eventMark(s *Store) (EventMark, error) {
	st, err := s.ReadStart(srv, sec)
	return st.Mark, err
}

// rows returns the rows of the test section by rating key.
func rows(t *testing.T, s *Store) map[string]Item {
	t.Helper()
	got, err := s.Unseen(srv, sec, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Item{}
	for _, it := range got {
		m[it.RatingKey] = it
	}
	return m
}

func keys(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.RatingKey)
	}
	return out
}

// due calls s.Due without a limit and checks that total counts every row.
func due(s *Store, q DueQuery) ([]Item, error) {
	items, total, err := s.Due(q)
	if err == nil && total != len(items) {
		return nil, fmt.Errorf("total = %d, want %d", total, len(items))
	}
	return items, err
}

func refresh(t *testing.T, s *Store, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		accept(t, s, key, base.Add(time.Duration(i+1)*time.Minute), ActionRefresh)
	}
}

// failAttempt records a refresh request of the row at at that Plex did not
// accept.
func failAttempt(t *testing.T, s *Store, key string, at time.Time) {
	t.Helper()
	err := s.RecordRequest(Request{Server: srv, RatingKey: key, At: at, Source: SourceAuto, Action: ActionRefresh, Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyReadStoresOnlyIncomplete(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoSummary), item("b", 0)}})
	got := rows(t, s)
	if len(got) != 1 || got["a"].Reasons != NoSummary {
		t.Fatalf("rows = %+v, want only a with NoSummary", got)
	}
}

func TestApplyReadReturnsFixedItem(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoSummary)}})
	refresh(t, s, "a", 3)

	fixed := apply(t, s, Read{Items: []Item{item("a", 0)}})
	if len(fixed) != 1 || fixed[0].RatingKey != "a" || fixed[0].Refreshes != 3 {
		t.Fatalf("fixed = %+v, want a with Refreshes 3", fixed)
	}
	if len(rows(t, s)) != 0 {
		t.Fatal("row not deleted")
	}

	apply(t, s, Read{Items: []Item{item("a", NoSummary)}})
	got := rows(t, s)["a"]
	if got.Refreshes != 0 || !got.LastAttempt.IsZero() {
		t.Fatalf("row = %+v, want fresh history", got)
	}
}

// A check or a manual action that completes a row or finds it gone after
// the mark of a read is newer than the listing of the read, so the read does
// not add the row back. The mark, not the event time, orders them: a check
// in the first second of the read, or a manual 404 whose request started
// before the read, still counts. A settlement before the mark, or a read
// without a mark, adds the row again.
func TestApplyReadKeepsNewerSettlement(t *testing.T) {
	readAt := base.Add(time.Hour)
	checked := func(t *testing.T, s *Store, at time.Time) {
		if fixed, err := s.ApplyCheck(srv, item("a", 0), at, 0, Observation{}); err != nil || !fixed {
			t.Fatalf("check = %v, %v", fixed, err)
		}
	}
	gone := func(t *testing.T, s *Store, at time.Time) {
		req := Request{Server: srv, RatingKey: "a", At: at, Source: SourceManual, Action: ActionRefresh}
		if _, err := s.RecordManual(ManualRecord{Request: req, Snapshot: item("a", NoSummary), Cached: true, Gone: true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name      string
		settle    func(*testing.T, *Store, time.Time)
		at        time.Time
		afterMark bool
		marked    bool
		kept      bool
	}{
		{"check in the first second of the read", checked, readAt, true, true, false},
		{"404 of a request that started before the read", gone, readAt.Add(-time.Minute), true, true, false},
		{"check before the mark", checked, readAt, false, true, true},
		{"read without a mark", checked, readAt, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			apply(t, s, Read{Items: []Item{item("a", NoSummary), item("b", NoSummary)}})
			if !tc.afterMark {
				tc.settle(t, s, tc.at)
			}
			var mark EventMark
			if tc.marked {
				var err error
				if mark, err = eventMark(s); err != nil {
					t.Fatal(err)
				}
			}
			if tc.afterMark {
				tc.settle(t, s, tc.at)
			}

			fixed := apply(t, s, Read{At: readAt, Mark: mark, Items: []Item{item("a", NoSummary), item("b", NoSummary), item("c", NoPoster)}})
			got := rows(t, s)
			if _, ok := got["a"]; ok != tc.kept {
				t.Errorf("row a stored = %v, want %v", ok, tc.kept)
			}
			if len(fixed) != 0 || got["b"].Reasons != NoSummary || got["c"].Reasons != NoPoster {
				t.Errorf("fixed = %v, rows = %+v; want b and c unchanged by the settlement of a", keys(fixed), got)
			}
		})
	}
}

func TestApplyReadCombinesReasons(t *testing.T) {
	tests := []struct {
		name  string
		setup Read
		read  Read
		want  Reasons
	}{
		{
			"old item bit plus new episode bit",
			Read{Items: []Item{item("a", NoArt)}},
			Read{Shows: true, Items: []Item{item("a", 0)}, EpisodeNoSummary: []Item{item("a", 0)}},
			EpisodeNoSummary,
		},
		{
			"old episode bit plus new item bit",
			Read{Shows: true, EpisodeNoSummary: []Item{item("a", 0)}},
			Read{Shows: true, Items: []Item{item("a", NoArt)}},
			NoArt,
		},
		{
			"old item bit kept when the item is not in Items",
			Read{Items: []Item{item("a", NoArt)}},
			Read{Shows: true, EpisodeNoSummary: []Item{item("a", 0)}},
			NoArt | EpisodeNoSummary,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTest(t)
			apply(t, s, tt.setup)
			refresh(t, s, "a", 2)
			if fixed := apply(t, s, tt.read); len(fixed) != 0 {
				t.Fatalf("fixed = %+v, want none", fixed)
			}
			got, ok := rows(t, s)["a"]
			if !ok || got.Reasons != tt.want || got.Refreshes != 2 {
				t.Fatalf("row = %+v ok=%v, want reasons %v refreshes 2", got, ok, tt.want)
			}
		})
	}
}

func TestApplyReadKeepsSkippedEpisodeReasons(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Shows: true, EpisodeNoSummary: []Item{item("a", 0)}, EpisodeUnmatched: []Item{item("b", 0)}})
	refresh(t, s, "a", 2)

	// The no-summary request was skipped, and the unmatched request found nothing.
	fixed := apply(t, s, Read{Shows: true, EpisodeSkipped: EpisodeNoSummary})
	if k := keys(fixed); !reflect.DeepEqual(k, []string{"b"}) {
		t.Fatalf("fixed = %v, want [b]", k)
	}
	got := rows(t, s)
	if got["a"].Reasons != EpisodeNoSummary || got["a"].Refreshes != 2 {
		t.Errorf("a = %+v, want EpisodeNoSummary and 2 refreshes", got["a"])
	}
}

func TestApplyReadReplacesEpisodeReasons(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Shows: true, EpisodeUnmatched: []Item{item("a", 0), item("b", 0)}})
	apply(t, s, Read{Items: []Item{item("d", NoPoster)}})
	if got := rows(t, s); got["a"].Reasons != EpisodeUnmatched || got["b"].Reasons != EpisodeUnmatched {
		t.Fatalf("setup rows = %+v", got)
	}

	fixed := apply(t, s, Read{Shows: true, EpisodeUnmatched: []Item{item("b", 0), item("c", 0)}})
	if k := keys(fixed); !reflect.DeepEqual(k, []string{"a"}) {
		t.Fatalf("fixed = %v, want [a]", k)
	}
	got := rows(t, s)
	for _, k := range []string{"b", "c"} {
		if got[k].Reasons != EpisodeUnmatched {
			t.Errorf("%s = %+v, want EpisodeUnmatched", k, got[k])
		}
	}
	if _, ok := got["a"]; ok {
		t.Error("a still stored")
	}
	if got["d"].Reasons != NoPoster {
		t.Errorf("d = %+v, want NoPoster kept", got["d"])
	}
}

func TestApplyReadWithoutShowsKeepsEpisodeReasons(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Shows: true, Items: []Item{item("a", NoArt)}, EpisodeNoSummary: []Item{item("a", 0)}})
	apply(t, s, Read{Items: []Item{item("a", 0)}})
	got, ok := rows(t, s)["a"]
	if !ok || got.Reasons != EpisodeNoSummary {
		t.Fatalf("row = %+v ok=%v, want EpisodeNoSummary", got, ok)
	}
}

func TestApplyReadUpdatesMetadataKeepsHistory(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	refresh(t, s, "a", 1)
	n := item("a", NoArt)
	n.Title, n.Year = "New", 2024
	apply(t, s, Read{Items: []Item{n}})
	got := rows(t, s)["a"]
	if got.Title != "New" || got.Year != 2024 || got.Refreshes != 1 || got.LastRefresh.IsZero() {
		t.Fatalf("row = %+v", got)
	}
}

func TestApplyReadDeletesGone(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt)}})
	fixed := apply(t, s, Read{Full: true, Gone: []string{"a"}})
	if len(fixed) != 0 {
		t.Fatalf("fixed = %+v, want none", fixed)
	}
	if got := rows(t, s); len(got) != 1 || got["b"].RatingKey != "b" {
		t.Fatalf("rows = %+v, want only b", got)
	}
}

func TestUnseen(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt), item("c", NoArt)}})
	apply(t, s, Read{Section: "2", Items: []Item{item("z", NoArt)}})
	got, err := s.Unseen(srv, sec, []string{"a", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if k := keys(got); !reflect.DeepEqual(k, []string{"b"}) {
		t.Fatalf("Unseen = %v, want [b]", k)
	}
}

func TestUnseenManyKeys(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	seen := make([]string, 50000)
	for i := range seen {
		seen[i] = fmt.Sprintf("k%d", i)
	}
	got, err := s.Unseen(srv, sec, seen)
	if err != nil || len(got) != 1 {
		t.Fatalf("Unseen = %v, %v", got, err)
	}
}

func TestDueOrder(t *testing.T) {
	s := openTest(t)
	mk := func(key string, added time.Duration) Item {
		it := item(key, NoArt)
		it.AddedAt = base.Add(added)
		return it
	}
	// Within each tie, the larger rating key has the older AddedAt.
	apply(t, s, Read{Items: []Item{
		mk("never-z", 0), mk("never-a", time.Hour),
		mk("old-attempt", 0), mk("new-attempt", 0),
		mk("same-z", 0), mk("same-a", time.Hour),
	}})
	at := base.Add(10 * time.Hour)
	for k, d := range map[string]time.Duration{"old-attempt": 0, "new-attempt": time.Hour, "same-z": 2 * time.Hour, "same-a": 2 * time.Hour} {
		failAttempt(t, s, k, at.Add(d))
	}
	now := base.Add(100 * time.Hour)
	got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: now, RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"never-z", "never-a", "old-attempt", "new-attempt", "same-z", "same-a"}
	if k := keys(got); !reflect.DeepEqual(k, want) {
		t.Fatalf("Due = %v, want %v", k, want)
	}
}

func TestDueLimitKeepsOrderAndTotal(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Full: true, Items: []Item{item("101", NoSummary), item("102", NoSummary), item("103", NoSummary)}})
	refresh(t, s, "101", 1)

	got, total, err := s.Due(DueQuery{Server: srv, Sections: []string{sec}, Now: base.Add(100 * time.Hour),
		RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"102", "103"}; !reflect.DeepEqual(keys(got), want) || total != 3 {
		t.Fatalf("Due = %v, total %d, want %v, total 3", keys(got), total, want)
	}
}

// A failed attempt counts in the order like a refresh, so "failed" and
// "once" tie on one attempt, and the older attempt of "once" goes first.
func TestDuePutsFewerAttemptsFirst(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("never", NoArt), item("failed", NoArt), item("once", NoArt), item("twice", NoArt)}})
	// The last attempts are in the opposite order of the refreshes.
	refresh(t, s, "twice", 2)
	accept(t, s, "once", base.Add(5*time.Hour), ActionRefresh)
	failAttempt(t, s, "failed", base.Add(6*time.Hour))
	got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: base.Add(100 * time.Hour), RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"never", "once", "failed", "twice"}
	if k := keys(got); !reflect.DeepEqual(k, want) {
		t.Fatalf("Due = %v, want %v", k, want)
	}
}

func TestDueBacksOffAfterRefreshes(t *testing.T) {
	s := openTest(t)
	refreshes := map[string]int{"failed": 0, "r1": 1, "r2": 2, "r3": 3, "r5": 5}
	var items []Item
	for k := range refreshes {
		items = append(items, item(k, NoArt))
	}
	apply(t, s, Read{Items: items})
	at := base.Add(time.Hour)
	for k, n := range refreshes {
		if n == 0 {
			failAttempt(t, s, k, at)
		}
		for range n {
			accept(t, s, k, at, ActionRefresh)
		}
	}

	// The wait is 1h, then 1h, 2h, 4h, and then the 4h cap.
	tests := []struct {
		after time.Duration
		want  []string
	}{
		{59 * time.Minute, nil},
		{time.Hour, []string{"failed", "r1"}},
		{2*time.Hour - time.Second, []string{"failed", "r1"}},
		{2 * time.Hour, []string{"failed", "r1", "r2"}},
		{4 * time.Hour, []string{"failed", "r1", "r2", "r3", "r5"}},
	}
	for _, tt := range tests {
		got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: at.Add(tt.after), RefreshInterval: time.Hour, MaxRefreshInterval: 4 * time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if k := keys(got); !reflect.DeepEqual(k, tt.want) {
			t.Errorf("Due after %v = %v, want %v", tt.after, k, tt.want)
		}
	}
}

func TestDueKeepsFractionalSecondWaits(t *testing.T) {
	tests := []struct {
		name      string
		refreshes int
		interval  time.Duration
		wait      time.Duration
	}{
		{"1.5s after one refresh", 1, 1500 * time.Millisecond, 1500 * time.Millisecond},
		{"500ms after three refreshes", 3, 500 * time.Millisecond, 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTest(t)
			apply(t, s, Read{Items: []Item{item("a", NoArt)}})
			at := base.Add(time.Hour)
			for range tt.refreshes {
				accept(t, s, "a", at, ActionRefresh)
			}
			for _, c := range []struct {
				after time.Duration
				want  int
			}{{tt.wait - time.Millisecond, 0}, {tt.wait, 1}} {
				got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: at.Add(c.after), RefreshInterval: tt.interval, MaxRefreshInterval: time.Hour})
				if err != nil || len(got) != c.want {
					t.Errorf("Due after %v = %v, %v, want %d items", c.after, got, err, c.want)
				}
			}
		})
	}
}

func TestDueAndCountSkipIgnoredReasons(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("art", NoArt), item("both", NoArt|NoSummary), item("ep", 0)},
		Shows: true, EpisodeNoSummary: []Item{item("ep", 0)}})
	ignored := NoArt | EpisodeNoSummary
	got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: base, RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour, Ignored: ignored})
	if k := keys(got); err != nil || !reflect.DeepEqual(k, []string{"both"}) {
		t.Fatalf("Due = %v, %v, want [both]", k, err)
	}
	if n, err := s.CountIncomplete(srv, []string{sec}, ignored); err != nil || n != 1 {
		t.Fatalf("CountIncomplete = %d, %v, want 1", n, err)
	}
	if n, err := s.CountIncomplete(srv, []string{sec}, 0); err != nil || n != 3 {
		t.Fatalf("CountIncomplete without ignored reasons = %d, %v, want 3", n, err)
	}
}

func TestLongestWaitRefreshes(t *testing.T) {
	tests := []struct {
		interval, longest time.Duration
		want              int
	}{
		{12 * time.Hour, 168 * time.Hour, 5}, // 12h, 12h, 24h, 48h, 96h, then 168h
		{time.Hour, 4 * time.Hour, 3},        // 1h, 1h, 2h, then 4h
		{time.Hour, time.Hour, 0},
		{time.Hour, 0, 0},
	}
	for _, tt := range tests {
		if got := LongestWaitRefreshes(tt.interval, tt.longest); got != tt.want {
			t.Errorf("LongestWaitRefreshes(%v, %v) = %d, want %d", tt.interval, tt.longest, got, tt.want)
		}
	}
}

func TestDueWithoutBackoffBelowRefreshInterval(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	refresh(t, s, "a", 5)
	got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: base.Add(5*time.Minute + time.Hour), RefreshInterval: time.Hour})
	if err != nil || len(got) != 1 {
		t.Fatalf("Due = %v, %v, want a", got, err)
	}
}

func TestDueCapsLongBackoff(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	refresh(t, s, "a", 200)
	last := base.Add(200 * time.Minute)
	limit := 100 * 365 * 24 * time.Hour
	for _, tt := range []struct {
		after time.Duration
		want  int
	}{{limit - time.Second, 0}, {limit, 1}} {
		got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: last.Add(tt.after), RefreshInterval: time.Hour, MaxRefreshInterval: limit})
		if err != nil || len(got) != tt.want {
			t.Errorf("Due after %v = %v, %v, want %d items", tt.after, got, err, tt.want)
		}
	}
}

func TestApplyReadMovesRowBetweenSections(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	refresh(t, s, "a", 2)

	n := item("a", NoPoster)
	n.Section = "2"
	apply(t, s, Read{Section: "2", Items: []Item{n}})

	moved, err := s.Unseen(srv, "2", nil)
	if err != nil || len(moved) != 1 {
		t.Fatalf("section 2 = %v, %v", moved, err)
	}
	if got := moved[0]; got.Section != "2" || got.Reasons != NoPoster || got.Refreshes != 2 {
		t.Fatalf("row = %+v, want section 2, NoPoster, Refreshes 2", got)
	}
	if len(rows(t, s)) != 0 {
		t.Fatal("row still in section 1")
	}
}

// A second read writes the library row again. The items of the library
// must keep their history, so that write must not delete the row.
func TestApplyReadKeepsHistoryOnSecondRead(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Full: true, Items: []Item{item("a", NoArt)}})
	refresh(t, s, "a", 2)

	apply(t, s, Read{At: base.Add(time.Hour), Items: []Item{item("a", NoArt)}})

	if got := rows(t, s)["a"]; got.Refreshes != 2 {
		t.Fatalf("row = %+v, want Refreshes 2", got)
	}
}

// An incremental read that does not list an existing incomplete row and does
// not change its reasons must not issue a no-op UPDATE on item.
func TestApplyReadSkipsNoOpUpdate(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Full: true, Items: []Item{item("a", NoArt), item("b", NoSummary)}})
	if _, err := s.db.Exec(`CREATE TABLE item_updates (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER count_item_updates AFTER UPDATE ON item
		BEGIN INSERT INTO item_updates VALUES (1); END`); err != nil {
		t.Fatal(err)
	}

	apply(t, s, Read{At: base.Add(time.Hour)})

	var updates int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM item_updates`).Scan(&updates); err != nil {
		t.Fatal(err)
	}
	if updates != 0 {
		t.Fatalf("item updates = %d, want 0", updates)
	}
}

func TestApplyReadReturnsFixedInKeyOrder(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("c", NoArt), item("a", NoArt), item("b", NoArt)}})
	fixed := apply(t, s, Read{Items: []Item{item("c", 0), item("a", 0), item("b", 0)}})
	if k := keys(fixed); !reflect.DeepEqual(k, []string{"a", "b", "c"}) {
		t.Fatalf("fixed = %v, want [a b c]", k)
	}
}

func TestDueSkipsRecentAttemptsAndOtherScopes(t *testing.T) {
	s := openTest(t)
	now := base.Add(48 * time.Hour)
	apply(t, s, Read{Items: []Item{item("recent", NoArt), item("old", NoArt), item("second", NoArt)}})
	apply(t, s, Read{Section: "2", Items: []Item{item("hidden", NoArt)}})
	apply(t, s, Read{Server: "other", Items: []Item{item("elsewhere", NoArt)}})
	failAttempt(t, s, "recent", now.Add(-time.Hour))
	failAttempt(t, s, "old", now.Add(-13*time.Hour))
	got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: now, RefreshInterval: 12 * time.Hour, MaxRefreshInterval: 12 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	k := keys(got)
	sort.Strings(k)
	if want := []string{"old", "second"}; !reflect.DeepEqual(k, want) {
		t.Fatalf("Due = %v, want %v", k, want)
	}
	n, err := s.CountIncomplete(srv, []string{sec}, 0)
	if err != nil || n != 3 {
		t.Fatalf("CountIncomplete = %d, %v, want 3", n, err)
	}
	if got, err := due(s, DueQuery{Server: srv, Now: now}); err != nil || len(got) != 0 {
		t.Fatalf("Due without sections = %v, %v", got, err)
	}
}

func TestDueSkipsYoungItemUntilOldEnough(t *testing.T) {
	s := openTest(t)
	now := base.Add(time.Hour)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: now, MinimumAge: 24 * time.Hour, RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour})
	if err != nil || len(got) != 0 {
		t.Fatalf("young Due = %v, %v", got, err)
	}
	if n, err := s.CountIncomplete(srv, []string{sec}, 0); err != nil || n != 1 {
		t.Fatalf("CountIncomplete = %d, want 1", n)
	}
	got, err = due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: now.Add(24 * time.Hour), MinimumAge: 24 * time.Hour, RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour})
	if err != nil || len(got) != 1 {
		t.Fatalf("old Due = %v, %v", got, err)
	}
}

func TestDueComparesTimesAcrossZones(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt)}})
	east := time.FixedZone("east", 12*3600)
	west := time.FixedZone("west", -12*3600)
	// b is earlier by instant but later by wall clock. The wall clock of a
	// is 12:00 earlier than its instant, and that of b is 12:00 later.
	instant := base.Add(24 * time.Hour)
	a, b := instant.In(west), instant.Add(-time.Hour).In(east)
	if a.Format("2006-01-02T15:04:05") >= b.Format("2006-01-02T15:04:05") {
		t.Fatal("setup: wall-clock order must be opposite to instant order")
	}
	failAttempt(t, s, "a", a)
	failAttempt(t, s, "b", b)
	got, err := due(s, DueQuery{Server: srv, Sections: []string{sec}, Now: instant.Add(48 * time.Hour), RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if k := keys(got); !reflect.DeepEqual(k, []string{"b", "a"}) {
		t.Fatalf("Due = %v, want [b a]", k)
	}
}

// RecordRequest counts each attempt. Only an accepted request sets
// LastRefresh and counts a refresh.
func TestRecordRequestCountsAttempts(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	at1, at2 := base.Add(time.Hour), base.Add(2*time.Hour)

	err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: at1, Source: SourceAuto, Action: ActionRefresh, Accepted: true})
	if err != nil {
		t.Fatal(err)
	}
	got := rows(t, s)["a"]
	if got.Refreshes != 1 || !got.LastRefresh.Equal(at1) || !got.LastAttempt.Equal(at1) {
		t.Fatalf("after refresh = %+v", got)
	}
	err = s.RecordRequest(Request{Server: srv, RatingKey: "a", At: at2, Source: SourceAuto, Action: ActionRefresh, Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	got = rows(t, s)["a"]
	if got.Refreshes != 1 || !got.LastRefresh.Equal(at1) || !got.LastAttempt.Equal(at2) {
		t.Fatalf("after attempt = %+v", got)
	}
	err = s.RecordRequest(Request{Server: srv, RatingKey: "missing", At: at1, Source: SourceAuto, Action: ActionRefresh, Accepted: true})
	if err != nil {
		t.Fatalf("missing row: %v", err)
	}
}

// RecordRequest keeps the last request that Plex accepted, and the last
// match request, which a later refresh does not replace.
func TestRecordRequestKeepsActions(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})

	steps := []struct {
		action          Action
		accepted        bool
		last, lastMatch Action
	}{
		{ActionRefresh, true, ActionRefresh, ""},
		{ActionTMDBMatch, true, ActionTMDBMatch, ActionTMDBMatch},
		{ActionRefresh, true, ActionRefresh, ActionTMDBMatch},
		{ActionMatch, false, ActionRefresh, ActionTMDBMatch}, // a failed attempt changes neither
		{ActionMatch, true, ActionMatch, ActionMatch},
	}
	for i, st := range steps {
		err := s.RecordRequest(Request{Server: srv, RatingKey: "a", At: base.Add(time.Duration(i+1) * time.Hour),
			Source: SourceAuto, Action: st.action, Accepted: st.accepted})
		if err != nil {
			t.Fatal(err)
		}
		got := rows(t, s)["a"]
		if got.LastAction != st.last || got.LastMatch != st.lastMatch {
			t.Fatalf("step %d: LastAction = %q, LastMatch = %q, want %q, %q",
				i, got.LastAction, got.LastMatch, st.last, st.lastMatch)
		}
	}
}

// A read that finds an item complete returns its row with the actions, so
// the refresher can log what fixed it.
func TestApplyReadReturnsActionsOfFixedRows(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	accept(t, s, "a", base.Add(time.Hour), ActionMatch)
	accept(t, s, "a", base.Add(2*time.Hour), ActionRefresh)

	fixed := apply(t, s, Read{At: base.Add(3 * time.Hour), Items: []Item{item("a", 0)}})
	if len(fixed) != 1 || fixed[0].LastAction != ActionRefresh || fixed[0].LastMatch != ActionMatch {
		t.Fatalf("fixed = %+v, want a with LastAction refresh and LastMatch match", fixed)
	}
}

func TestRecordGoneDeletesRow(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	if _, _, err := s.RecordGone(srv, "a", base, SourceAuto, "", 0, AttemptResult{}, ""); err != nil {
		t.Fatal(err)
	}
	if len(rows(t, s)) != 0 {
		t.Fatal("row not deleted")
	}
	if _, _, err := s.RecordGone(srv, "a", base, SourceAuto, "", 0, AttemptResult{}, ""); err != nil {
		t.Fatalf("missing row: %v", err)
	}
}

func TestApplyReadMovedRowKeepsOldReasons(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoPoster)}})
	refresh(t, s, "a", 2)

	moved := item("a", 0)
	moved.Section = "2"
	fixed := apply(t, s, Read{Section: "2", Shows: true, EpisodeNoSummary: []Item{moved}})
	if len(fixed) != 0 {
		t.Fatalf("fixed = %+v, want none", fixed)
	}
	got, err := s.Unseen(srv, "2", nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("section 2 = %v, %v", got, err)
	}
	if a := got[0]; a.Reasons != NoArt|EpisodeNoSummary || a.Refreshes != 2 {
		t.Fatalf("a = %+v, want NoArt|EpisodeNoSummary and Refreshes 2", a)
	}
	if b := rows(t, s)["b"]; b.Reasons != NoPoster {
		t.Fatalf("unmentioned b = %+v, want NoPoster kept in section 1", b)
	}
}

func TestApplyReadMovedCompleteRowIsFixed(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	refresh(t, s, "a", 3)

	moved := item("a", 0)
	moved.Section = "2"
	fixed := apply(t, s, Read{Section: "2", Items: []Item{moved}})
	if len(fixed) != 1 || fixed[0].RatingKey != "a" || fixed[0].Refreshes != 3 {
		t.Fatalf("fixed = %+v, want a with Refreshes 3", fixed)
	}
	if len(rows(t, s)) != 0 {
		t.Fatal("row left in section 1")
	}
}

func TestApplyReadGoneEpisodeOnlyRowIsNotFixed(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Shows: true, EpisodeNoSummary: []Item{item("a", 0)}})
	fixed := apply(t, s, Read{Full: true, Shows: true, Gone: []string{"a"}})
	if len(fixed) != 0 {
		t.Fatalf("fixed = %+v, want none", fixed)
	}
	if len(rows(t, s)) != 0 {
		t.Fatal("row not deleted")
	}
}

// seedLibraries reads one incomplete item into each library, and a second
// one into a/1. Library b/3 has read times but no item row.
func seedLibraries(t *testing.T, s *Store) {
	t.Helper()
	apply(t, s, Read{Server: "a", Section: "1", Full: true, Items: []Item{item("10", NoArt), item("11", NoArt)}})
	apply(t, s, Read{Server: "a", Section: "2", Full: true, Items: []Item{item("20", NoArt)}})
	apply(t, s, Read{Server: "b", Section: "1", Full: true, Items: []Item{item("30", NoArt)}})
	apply(t, s, Read{Server: "b", Section: "3", Full: true})
}

// libraryState returns the item count of each library that has read times,
// as "<server>/<section>". It also fails on an item row without read times.
func libraryState(t *testing.T, s *Store) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, scope := range [][2]string{{"a", "1"}, {"a", "2"}, {"b", "1"}, {"b", "3"}} {
		lib, err := readTimes(s, scope[0], scope[1])
		if err != nil {
			t.Fatal(err)
		}
		items, err := s.Unseen(scope[0], scope[1], nil)
		if err != nil {
			t.Fatal(err)
		}
		if lib.LastRead.IsZero() {
			if len(items) > 0 {
				t.Fatalf("%s/%s has %d item rows but no read times", scope[0], scope[1], len(items))
			}
			continue
		}
		got[scope[0]+"/"+scope[1]] = len(items)
	}
	return got
}

func TestPruneServers(t *testing.T) {
	s := openTest(t)
	seedLibraries(t, s)

	pruned, err := s.PruneServers([]string{"a", "c"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Pruned{{Server: "b", Section: "1", Items: 1}, {Server: "b", Section: "3", Items: 0}}
	if !reflect.DeepEqual(pruned, want) {
		t.Fatalf("pruned = %+v, want %+v", pruned, want)
	}
	if got, want := libraryState(t, s), map[string]int{"a/1": 2, "a/2": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
}

func TestPruneSections(t *testing.T) {
	s := openTest(t)
	seedLibraries(t, s)

	pruned, err := s.PruneSections("a", []string{"1", "9"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pruned{{Server: "a", Section: "2", Items: 1}}; !reflect.DeepEqual(pruned, want) {
		t.Fatalf("pruned = %+v, want %+v", pruned, want)
	}
	if got, want := libraryState(t, s), map[string]int{"a/1": 2, "b/1": 1, "b/3": 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
}

func TestPruneSectionsWithoutSectionsRemovesServer(t *testing.T) {
	s := openTest(t)
	seedLibraries(t, s)

	pruned, err := s.PruneSections("b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pruned{{Server: "b", Section: "1", Items: 1}, {Server: "b", Section: "3", Items: 0}}; !reflect.DeepEqual(pruned, want) {
		t.Fatalf("pruned = %+v, want %+v", pruned, want)
	}
	if got, want := libraryState(t, s), map[string]int{"a/1": 2, "a/2": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
}

func dueAll(t *testing.T, s *Store) ([]Item, int) {
	t.Helper()
	got, total, err := s.Due(DueQuery{Server: srv, Sections: []string{sec}, Now: base.Add(time.Hour), RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return got, total
}

func TestApplyReadStoresImagesAndPath(t *testing.T) {
	s := openTest(t)
	it := item("a", NoArt)
	it.Thumb, it.Art, it.Path = "/library/metadata/1/thumb/9", "/library/metadata/1/art/8", "/movies/A"
	apply(t, s, Read{Items: []Item{it}})
	got := rows(t, s)["a"]
	if got.Thumb != it.Thumb || got.Art != it.Art || got.Path != it.Path {
		t.Fatalf("row = %q %q %q", got.Thumb, got.Art, got.Path)
	}
}

func TestApplyReadKeepsSavedPathWhenReadHasNone(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	if err := s.SetPath(srv, "a", "/tv/Show"); err != nil {
		t.Fatal(err)
	}
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	if got := rows(t, s)["a"].Path; got != "/tv/Show" {
		t.Fatalf("path = %q, want /tv/Show", got)
	}
}

func TestApplyReadReplacesImages(t *testing.T) {
	s := openTest(t)
	it := item("a", NoArt)
	it.Thumb, it.Art = "/t1", "/a1"
	apply(t, s, Read{Items: []Item{it}})
	it.Thumb, it.Art = "", "/a2"
	apply(t, s, Read{Items: []Item{it}})
	got := rows(t, s)["a"]
	if got.Thumb != "" || got.Art != "/a2" {
		t.Fatalf("images = %q %q, want empty /a2", got.Thumb, got.Art)
	}
}

func TestApplyReadKeepsStoppedAt(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	if _, err := s.db.Exec(`UPDATE item SET stopped_at = 5000, last_outcome = 'refreshed', last_match_outcome = 'matched', last_match_event_id = NULL`); err != nil {
		t.Fatal(err)
	}
	apply(t, s, Read{Items: []Item{item("a", NoArt|NoSummary)}})
	got := rows(t, s)["a"]
	if !got.StoppedAt.Equal(time.Unix(5000, 0)) || got.LastOutcome != "refreshed" || got.LastMatchOutcome != "matched" {
		t.Fatalf("row = %+v", got)
	}
}

func TestDueAndCountSkipStoppedItems(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt)}})
	if _, err := s.db.Exec(`UPDATE item SET stopped_at = 5000 WHERE rating_key = 'b'`); err != nil {
		t.Fatal(err)
	}
	got, total := dueAll(t, s)
	if k := keys(got); !reflect.DeepEqual(k, []string{"a"}) || total != 1 {
		t.Fatalf("Due = %v, total %d, want [a], 1", k, total)
	}
	if n, err := s.CountIncomplete(srv, []string{sec}, 0); err != nil || n != 1 {
		t.Fatalf("CountIncomplete = %d, %v, want 1", n, err)
	}
}

func TestMigratedRowIsStillDue(t *testing.T) {
	file := openAtVersion(t, 5)
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO library (server, section) VALUES ('srv', '1')`,
		`INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons)
			VALUES ('srv', '1', 'a', 'movie', 'Title a', 2020, 1000, 2)`,
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
	got, total := dueAll(t, s)
	if len(got) != 1 || total != 1 {
		t.Fatalf("Due = %v, total %d, want one row", keys(got), total)
	}
	if it := got[0]; it.Thumb != "" || !it.StoppedAt.IsZero() || it.LastOutcome != "" || it.LastMatchEventID != 0 {
		t.Fatalf("row = %+v", it)
	}
}

func TestApplyReadWritesStagedFolders(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	apply(t, s, Read{Items: []Item{item("a", NoArt)}, Folders: map[string]string{"a": "/tv/A", "gone": "/tv/G", "empty": ""}})
	if got := rows(t, s); got["a"].Path != "/tv/A" || len(got) != 1 {
		t.Fatalf("rows = %+v", got)
	}
}

// A request that Plex accepted after the mark of a read can have changed the
// item after its listing, such as a manual Fix match to a wrong movie. The
// check after the request is newer, so the read neither completes nor
// changes the row, and adds no row for an item that it does not cache. A
// request that Plex did not accept changes nothing in Plex, so the read
// applies its listing.
func TestApplyReadKeepsRowsOfNewerRequests(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accepted bool
		kept     bool
	}{
		{"accepted request", true, true},
		{"rejected request", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			apply(t, s, Read{Items: []Item{item("a", NoSummary), item("b", NoSummary)}})
			mark, err := eventMark(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"a", "n"} {
				req := Request{Server: srv, RatingKey: key, At: base, Source: SourceManual, Action: ActionMatch, Accepted: tc.accepted}
				_, err := s.RecordManual(ManualRecord{Request: req, Snapshot: item(key, NoArt), Cached: key == "a"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ApplyCheck(srv, item("a", Mismatched), base, 0, Observation{}); err != nil {
				t.Fatal(err)
			}

			// The listing is from before the match: a is complete, n is new.
			fixed := apply(t, s, Read{At: base.Add(time.Hour), Mark: mark,
				Items: []Item{item("a", 0), item("b", 0), item("n", NoArt)}})
			got := rows(t, s)
			a, ok := got["a"]
			if ok != tc.kept || tc.kept && a.Reasons != Mismatched {
				t.Errorf("row a = %+v (stored %v), want stored %v with the reasons of the check", a, ok, tc.kept)
			}
			if _, ok := got["n"]; ok == tc.kept {
				t.Errorf("row n stored = %v, want %v", ok, !tc.kept)
			}
			if _, ok := got["b"]; ok || !slices.Contains(keys(fixed), "b") {
				t.Errorf("fixed = %v, want b completed as usual", keys(fixed))
			}
			if done := hasCompleted(t, s, "a"); done == tc.kept {
				t.Errorf("completed event of a = %v, want %v", done, !tc.kept)
			}
		})
	}
}

// The rule of a request after the mark covers the staged 404s and series
// folders of a full read too.
func TestApplyReadKeepsGoneAndFolderOfNewerRequests(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accepted bool
	}{
		{"accepted request", true},
		{"rejected request", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			apply(t, s, Read{Items: []Item{item("g", NoSummary), item("f", NoSummary)}, Folders: map[string]string{"f": "/tv/Old"}})
			mark, err := eventMark(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"g", "f"} {
				req := Request{Server: srv, RatingKey: key, At: base, Source: SourceManual, Action: ActionMatch, Accepted: tc.accepted}
				if err := s.RecordRequest(req); err != nil {
					t.Fatal(err)
				}
			}

			apply(t, s, Read{At: base.Add(time.Hour), Mark: mark, Full: true, Gone: []string{"g"},
				Items: []Item{item("f", NoSummary)}, Folders: map[string]string{"f": "/tv/New"}})
			got := rows(t, s)
			if _, ok := got["g"]; ok != tc.accepted {
				t.Errorf("row g stored = %v, want %v", ok, tc.accepted)
			}
			want := "/tv/New"
			if tc.accepted {
				want = "/tv/Old"
			}
			if got["f"].Path != want {
				t.Errorf("path of f = %q, want %q", got["f"].Path, want)
			}
		})
	}
}
