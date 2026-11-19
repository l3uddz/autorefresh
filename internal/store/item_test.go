package store

import (
	"fmt"
	"reflect"
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
	fixed, err := s.ApplyRead(r)
	if err != nil {
		t.Fatalf("ApplyRead: %v", err)
	}
	return fixed
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

func refresh(t *testing.T, s *Store, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.RecordAttempt(srv, key, base.Add(time.Duration(i+1)*time.Minute), true); err != nil {
			t.Fatal(err)
		}
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
	apply(t, s, Read{Items: []Item{
		mk("old-attempt", 0), mk("new-attempt", 0), mk("never-late", 2*time.Hour),
		mk("never-early", time.Hour), mk("tie-b", 0), mk("tie-a", 0),
	}})
	at := base.Add(10 * time.Hour)
	for k, d := range map[string]time.Duration{"old-attempt": 0, "new-attempt": time.Hour, "tie-a": 2 * time.Hour, "tie-b": 2 * time.Hour} {
		if err := s.RecordAttempt(srv, k, at.Add(d), false); err != nil {
			t.Fatal(err)
		}
	}
	now := base.Add(100 * time.Hour)
	got, err := s.Due(srv, []string{sec}, now, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"never-early", "never-late", "old-attempt", "new-attempt", "tie-a", "tie-b"}
	if k := keys(got); !reflect.DeepEqual(k, want) {
		t.Fatalf("Due = %v, want %v", k, want)
	}
}

func TestDueSkipsRecentAttemptsAndOtherScopes(t *testing.T) {
	s := openTest(t)
	now := base.Add(48 * time.Hour)
	apply(t, s, Read{Items: []Item{item("recent", NoArt), item("old", NoArt), item("other-section", NoArt)}})
	apply(t, s, Read{Section: "2", Items: []Item{item("hidden", NoArt)}})
	apply(t, s, Read{Server: "other", Items: []Item{item("elsewhere", NoArt)}})
	if err := s.RecordAttempt(srv, "recent", now.Add(-time.Hour), false); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttempt(srv, "old", now.Add(-13*time.Hour), false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Due(srv, []string{sec}, now, 0, 12*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	k := keys(got)
	sort.Strings(k)
	if want := []string{"old", "other-section"}; !reflect.DeepEqual(k, want) {
		t.Fatalf("Due = %v, want %v", k, want)
	}
	n, err := s.CountIncomplete(srv, []string{sec})
	if err != nil || n != 3 {
		t.Fatalf("CountIncomplete = %d, %v, want 3", n, err)
	}
	if got, err := s.Due(srv, nil, now, 0, 0); err != nil || len(got) != 0 {
		t.Fatalf("Due without sections = %v, %v", got, err)
	}
}

func TestDueSkipsYoungItemUntilOldEnough(t *testing.T) {
	s := openTest(t)
	now := base.Add(time.Hour)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	got, err := s.Due(srv, []string{sec}, now, 24*time.Hour, time.Hour)
	if err != nil || len(got) != 0 {
		t.Fatalf("young Due = %v, %v", got, err)
	}
	if n, _ := s.CountIncomplete(srv, []string{sec}); n != 1 {
		t.Fatalf("CountIncomplete = %d, want 1", n)
	}
	got, err = s.Due(srv, []string{sec}, now.Add(24*time.Hour), 24*time.Hour, time.Hour)
	if err != nil || len(got) != 1 {
		t.Fatalf("old Due = %v, %v", got, err)
	}
}

func TestDueComparesTimesAcrossZones(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt), item("b", NoArt)}})
	east := time.FixedZone("east", 12*3600)
	west := time.FixedZone("west", -12*3600)
	// b is earlier by instant but later by wall clock.
	instant := base.Add(24 * time.Hour)
	if err := s.RecordAttempt(srv, "a", instant.In(east), false); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttempt(srv, "b", instant.Add(-time.Hour).In(west), false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Due(srv, []string{sec}, instant.Add(48*time.Hour), 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if k := keys(got); !reflect.DeepEqual(k, []string{"b", "a"}) {
		t.Fatalf("Due = %v, want [b a]", k)
	}
}

func TestRecordAttempt(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	at1, at2 := base.Add(time.Hour), base.Add(2*time.Hour)

	if err := s.RecordAttempt(srv, "a", at1, true); err != nil {
		t.Fatal(err)
	}
	got := rows(t, s)["a"]
	if got.Refreshes != 1 || !got.LastRefresh.Equal(at1) || !got.LastAttempt.Equal(at1) {
		t.Fatalf("after refresh = %+v", got)
	}
	if err := s.RecordAttempt(srv, "a", at2, false); err != nil {
		t.Fatal(err)
	}
	got = rows(t, s)["a"]
	if got.Refreshes != 1 || !got.LastRefresh.Equal(at1) || !got.LastAttempt.Equal(at2) {
		t.Fatalf("after attempt = %+v", got)
	}
	if err := s.RecordAttempt(srv, "missing", at1, true); err != nil {
		t.Fatalf("missing row: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", NoArt)}})
	if err := s.Delete(srv, "a"); err != nil {
		t.Fatal(err)
	}
	if len(rows(t, s)) != 0 {
		t.Fatal("row not deleted")
	}
	if err := s.Delete(srv, "a"); err != nil {
		t.Fatalf("missing row: %v", err)
	}
}
