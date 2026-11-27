package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// undecided returns a listed item whose wrong match check had nothing to
// compare, so the read keeps the stored value of Mismatched.
func undecided(key string, reasons Reasons) Item {
	it := item(key, reasons)
	it.Keep = Mismatched
	return it
}

func hasCompleted(t *testing.T, s *Store, key string) bool {
	t.Helper()
	events, err := s.Timeline(srv, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == EventCompleted {
			return true
		}
	}
	return false
}

func TestApplyReadKeepsUndecidedMismatch(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", Mismatched)}})

	fixed := apply(t, s, Read{Items: []Item{undecided("a", 0)}})

	if len(fixed) != 0 || rows(t, s)["a"].Reasons != Mismatched {
		t.Fatalf("fixed = %v, row = %+v, want the row kept with Mismatched", keys(fixed), rows(t, s)["a"])
	}
	if hasCompleted(t, s, "a") {
		t.Error("an undecided read recorded a completed event")
	}
}

func TestApplyReadKeepsUndecidedMismatchBesideOtherReasons(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", Mismatched|NoSummary)}})

	apply(t, s, Read{Items: []Item{undecided("a", NoArt)}})

	if got := rows(t, s)["a"].Reasons; got != Mismatched|NoArt {
		t.Errorf("reasons = %v, want mismatched and the new no-art", got)
	}
}

func TestApplyReadClearsDecidedMismatch(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", Mismatched)}})

	fixed := apply(t, s, Read{Items: []Item{item("a", 0)}})

	if len(fixed) != 1 || len(rows(t, s)) != 0 {
		t.Fatalf("fixed = %v, rows = %v, want a removed as complete", keys(fixed), rows(t, s))
	}
	if !hasCompleted(t, s, "a") {
		t.Error("no completed event for the corrected match")
	}
}

func TestApplyReadAddsNoUndecidedMismatch(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{undecided("a", 0)}})
	if got := rows(t, s); len(got) != 0 {
		t.Fatalf("rows = %v, want none for a new undecided movie", got)
	}
}

func TestApplyCheckKeepsUndecidedMismatch(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("a", Mismatched|NoSummary)}})

	fixed, err := s.ApplyCheck(srv, undecided("a", 0), base.Add(time.Minute), 0)

	if err != nil || fixed || rows(t, s)["a"].Reasons != Mismatched {
		t.Fatalf("ApplyCheck = %v, %v, row = %+v, want only Mismatched kept", fixed, err, rows(t, s)["a"])
	}
	if fixed, err := s.ApplyCheck(srv, item("a", 0), base.Add(2*time.Minute), 0); err != nil || !fixed {
		t.Fatalf("decided ApplyCheck = %v, %v, want the row completed", fixed, err)
	}
}

func TestManualQueueState(t *testing.T) {
	s := openTest(t)
	now := base.Add(48 * time.Hour)
	young := item("young", Mismatched)
	young.AddedAt = now.Add(-time.Hour)
	apply(t, s, Read{Items: []Item{
		item("wrong", Mismatched), young, item("wrong-art", Mismatched|NoArt), item("art", NoArt),
	}})
	rules := DueRules{Now: now, MinimumAge: 24 * time.Hour, RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour}

	got := map[string]QueueState{}
	for _, it := range listAll(t, s, ListQuery{Tab: TabAttention, Rules: rules}) {
		got[it.RatingKey] = it.State
		if it.State == StateManual && !it.NextDue.IsZero() {
			t.Errorf("%s has next due %v, want none", it.RatingKey, it.NextDue)
		}
	}
	want := map[string]QueueState{"wrong": StateManual, "young": StateManual, "wrong-art": StateDue, "art": StateDue}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}

	dq := DueQuery{Server: srv, Sections: []string{sec}, Now: now, MinimumAge: 24 * time.Hour,
		RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour}
	dueItems, err := due(s, dq)
	if k := keys(dueItems); err != nil || !reflect.DeepEqual(k, []string{"art", "wrong-art"}) {
		t.Errorf("Due = %v, %v, want only the rows with another reason", k, err)
	}
	if n, err := s.CountIncomplete(srv, []string{sec}, 0); err != nil || n != 4 {
		t.Errorf("CountIncomplete = %d, %v, want 4 with the manual rows", n, err)
	}
	counts, err := s.CountItems(context.Background(), ListQuery{Rules: rules})
	if err != nil || counts.Attention != 4 {
		t.Errorf("attention count = %+v, %v, want 4", counts, err)
	}
	filtered := listKeys(listAll(t, s, ListQuery{Tab: TabAttention, Reason: Mismatched, Rules: rules, Sort: SortTitle}))
	if !reflect.DeepEqual(filtered, []string{"wrong", "wrong-art", "young"}) {
		t.Errorf("reason=mismatched = %v, want the three mismatched rows", filtered)
	}
}

func TestIgnoredMismatchIsIgnoredNotManual(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("wrong", Mismatched)}})
	rules := DueRules{Now: base.Add(48 * time.Hour), Ignored: Mismatched}

	listed := listAll(t, s, ListQuery{Tab: TabAttention, WithIgnored: true, Rules: rules})
	if len(listed) != 1 || listed[0].State != StateIgnored {
		t.Fatalf("listed = %+v, want one row with state ignored", listed)
	}
}

func TestParseMismatchedReason(t *testing.T) {
	r, err := ParseReasons([]string{"mismatched"})
	if err != nil || r != Mismatched || r.String() != "mismatched" {
		t.Errorf("ParseReasons(mismatched) = %v, %v", r, err)
	}
}
