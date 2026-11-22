package store

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

// stopItem sets stopped_at of the row by SQL, until the store has a stop API.
func stopItem(t *testing.T, s *Store, server, key string, at time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE item SET stopped_at = ? WHERE server = ? AND rating_key = ?`, at.Unix(), server, key); err != nil {
		t.Fatal(err)
	}
}

// listAll returns the rows of one ListItems call with Limit 100.
func listAll(t *testing.T, s *Store, q ListQuery) []ListItem {
	t.Helper()
	q.Limit = 100
	items, next, err := s.ListItems(context.Background(), q)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if next != nil {
		t.Fatalf("ListItems with Limit 100 returned next %+v", next)
	}
	return items
}

func listKeys(items []ListItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.RatingKey)
	}
	return out
}

func sortedKeys(items []ListItem) []string {
	out := listKeys(items)
	sort.Strings(out)
	return out
}

func addedAt(key string, at time.Time) Item {
	it := item(key, NoSummary)
	it.AddedAt = at
	return it
}

func TestDueUsesSharedQueueState(t *testing.T) {
	s := openTest(t)
	now := base.Add(1000 * time.Hour)
	dq := DueQuery{
		Server: srv, Sections: []string{sec}, Now: now,
		MinimumAge:      24*time.Hour + 1500*time.Microsecond,
		RefreshInterval: time.Hour, MaxRefreshInterval: 8 * time.Hour,
		Ignored: NoArt,
	}
	// The fraction of MinimumAge moves the cutoff one second before
	// now-24h, so a row added at now-24h is too new.
	cutoff := time.Unix(now.Add(-dq.MinimumAge).Unix(), 0)
	ignored := item("ignored", NoArt)
	apply(t, s, Read{Items: []Item{
		addedAt("old", base),
		addedAt("cutoff", cutoff),
		addedAt("young", cutoff.Add(time.Second)),
		addedAt("end-of-wait", base),
		addedAt("inside-wait", base),
		addedAt("doubled", base),
		addedAt("young-waiting", cutoff.Add(time.Second)),
		addedAt("stopped", base),
		ignored,
	}})
	accept(t, s, "end-of-wait", now.Add(-time.Hour), ActionRefresh)
	accept(t, s, "inside-wait", now.Add(-time.Hour+time.Second), ActionRefresh)
	for range 3 {
		// The wait after three refreshes is 4h.
		accept(t, s, "doubled", now.Add(-3*time.Hour), ActionRefresh)
	}
	accept(t, s, "young-waiting", now.Add(-time.Minute), ActionRefresh)
	stopItem(t, s, srv, "stopped", base)

	rules := dq.rules()
	listed := listAll(t, s, ListQuery{Tab: TabAttention, WithIgnored: true, Rules: rules})
	got := map[string]QueueState{}
	var listedDue []string
	for _, it := range listed {
		got[it.RatingKey] = it.State
		if it.State == StateDue {
			listedDue = append(listedDue, it.RatingKey)
		}
	}
	want := map[string]QueueState{
		"old": StateDue, "cutoff": StateDue, "young": StateTooNew,
		"end-of-wait": StateDue, "inside-wait": StateWaiting, "doubled": StateWaiting,
		"young-waiting": StateTooNew, "ignored": StateIgnored,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}

	dueItems, err := due(s, dq)
	if err != nil {
		t.Fatal(err)
	}
	dueKeys := keys(dueItems)
	sort.Strings(dueKeys)
	sort.Strings(listedDue)
	if !reflect.DeepEqual(dueKeys, listedDue) {
		t.Fatalf("Due = %v, listed due = %v", dueKeys, listedDue)
	}

	stopped := listAll(t, s, ListQuery{Tab: TabStopped, Rules: rules})
	if len(stopped) != 1 || stopped[0].RatingKey != "stopped" || stopped[0].State != StateStopped {
		t.Fatalf("stopped = %+v, want the stopped row with StateStopped", stopped)
	}
}

func TestListNextDue(t *testing.T) {
	s := openTest(t)
	now := base.Add(1000 * time.Hour)
	rules := DueRules{
		Now: now, MinimumAge: 24*time.Hour + 1500*time.Microsecond,
		RefreshInterval: time.Hour, MaxRefreshInterval: 8 * time.Hour,
	}
	young := now.Add(-time.Hour)
	// Too new until now+23h, and waiting for 4h after now-1m.
	both := now.Add(-23 * time.Hour)
	apply(t, s, Read{Items: []Item{
		addedAt("waiting", base), addedAt("young", young), addedAt("both", both), addedAt("due", base),
	}})
	lastWaiting := now.Add(-3 * time.Hour)
	lastBoth := now.Add(-time.Minute)
	for range 3 {
		accept(t, s, "waiting", lastWaiting, ActionRefresh)
		accept(t, s, "both", lastBoth, ActionRefresh)
	}

	byKey := map[string]ListItem{}
	for _, it := range listAll(t, s, ListQuery{Tab: TabAttention, Rules: rules}) {
		byKey[it.RatingKey] = it
	}
	want := map[string]struct {
		state QueueState
		next  time.Time
	}{
		"waiting": {StateWaiting, lastWaiting.Add(4 * time.Hour)},
		"young":   {StateTooNew, young.Add(rules.MinimumAge).Truncate(time.Millisecond)},
		"both":    {StateTooNew, lastBoth.Add(4 * time.Hour)},
		"due":     {StateDue, time.Time{}},
	}
	for key, w := range want {
		it := byKey[key]
		if it.State != w.state || !it.NextDue.Equal(w.next) || it.NextDue.IsZero() != w.next.IsZero() {
			t.Errorf("%s: state %q, NextDue %v, want %q, %v", key, it.State, it.NextDue, w.state, w.next)
		}
	}
}

func TestListAttentionAndStoppedTabs(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{
		item("a", NoSummary), item("b", NoSummary), item("c", NoSummary), item("ignored", NoArt),
		item("s1", NoSummary), item("s2", NoSummary),
	}})
	stopItem(t, s, srv, "s1", base.Add(time.Hour))
	stopItem(t, s, srv, "s2", base.Add(2*time.Hour))
	rules := DueRules{Now: base.Add(1000 * time.Hour), RefreshInterval: time.Hour, Ignored: NoArt}

	if got := listKeys(listAll(t, s, ListQuery{Tab: TabAttention, Rules: rules})); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Attention = %v, want [a b c]", got)
	}
	if got := listKeys(listAll(t, s, ListQuery{Tab: TabAttention, WithIgnored: true, Rules: rules})); !reflect.DeepEqual(got, []string{"a", "b", "c", "ignored"}) {
		t.Errorf("Attention with ignored = %v, want [a b c ignored]", got)
	}
	stopped := listAll(t, s, ListQuery{Tab: TabStopped, Rules: rules})
	if got := listKeys(stopped); !reflect.DeepEqual(got, []string{"s2", "s1"}) {
		t.Fatalf("Stopped = %v, want [s2 s1]", got)
	}
	if !stopped[0].StoppedAt.Equal(base.Add(2*time.Hour)) || !stopped[0].Cached {
		t.Errorf("stopped row = %+v, want StoppedAt %v and Cached", stopped[0], base.Add(2*time.Hour))
	}
}

func TestListFilters(t *testing.T) {
	s := openTest(t)
	mk := func(key, typ, title, path string, reasons Reasons) Item {
		it := item(key, reasons)
		it.Type, it.Title, it.Path = typ, title, path
		return it
	}
	apply(t, s, Read{Items: []Item{
		mk("pure", "movie", "100% Pure", "/m/1", Unmatched),
		mk("pure0", "movie", "1000 Pure", "/m/2", NoSummary),
		mk("ab", "show", "a_b", "/t/1", NoSummary),
		mk("axb", "show", "axb", "/t/2", NoSummary),
		mk("needle", "show", "Plain", "/data/needle/x", NoSummary),
	}})
	apply(t, s, Read{Section: "2", Items: []Item{mk("other", "show", "Other", "/o", NoSummary)}})
	apply(t, s, Read{Server: "srv2", Items: []Item{mk("remote", "show", "Remote", "/r", NoSummary)}})
	if err := s.SaveLibraries(srv, []LibraryInfo{{Section: sec, Title: "Movies"}}); err != nil {
		t.Fatal(err)
	}
	rules := DueRules{Now: base.Add(1000 * time.Hour), RefreshInterval: time.Hour}

	tests := []struct {
		name string
		q    ListQuery
		want []string
	}{
		{"servers", ListQuery{Servers: []string{"srv2"}}, []string{"remote"}},
		{"several servers", ListQuery{Servers: []string{srv, "srv2"}}, []string{"ab", "axb", "needle", "other", "pure", "pure0", "remote"}},
		{"section", ListQuery{Section: "2"}, []string{"other"}},
		{"type", ListQuery{Type: "movie"}, []string{"pure", "pure0"}},
		{"reason", ListQuery{Reason: Unmatched | NoArt}, []string{"pure"}},
		{"search title", ListQuery{Search: "pure"}, []string{"pure", "pure0"}},
		{"search percent", ListQuery{Search: "100%"}, []string{"pure"}},
		{"search underscore", ListQuery{Search: "a_b"}, []string{"ab"}},
		{"search path", ListQuery{Search: "needle/"}, []string{"needle"}},
	}
	for _, tt := range tests {
		tt.q.Tab, tt.q.Rules = TabAttention, rules
		if got := sortedKeys(listAll(t, s, tt.q)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: keys = %v, want %v", tt.name, got, tt.want)
		}
	}

	for _, it := range listAll(t, s, ListQuery{Tab: TabAttention, Rules: rules}) {
		want := ""
		if it.Server == srv && it.Section == sec {
			want = "Movies"
		}
		if it.Library != want {
			t.Errorf("%s: Library = %q, want %q", it.RatingKey, it.Library, want)
		}
	}
}

func TestListPagesWithoutGapsOrRepeats(t *testing.T) {
	s := openTest(t)
	now := base.Add(1000 * time.Hour)
	rules := DueRules{Now: now, MinimumAge: 24 * time.Hour, RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour}
	mk := func(key, title string, added time.Time) Item {
		it := addedAt(key, added)
		it.Title = title
		return it
	}
	apply(t, s, Read{Items: []Item{
		mk("k1", "Alpha", base),                  // due
		mk("k2", "Alpha", base),                  // due
		mk("k3", "Beta", base),                   // waiting until now+30m
		mk("k4", "beta", base),                   // waiting until now+50m
		mk("k5", "Delta", base.Add(time.Hour)),   // due
		mk("k6", "Delta", now.Add(-time.Hour)),   // too new until now+23h
		mk("k7", "Gamma", now.Add(-2*time.Hour)), // too new until now+22h
	}})
	accept(t, s, "k3", now.Add(-30*time.Minute), ActionRefresh)
	accept(t, s, "k4", now.Add(-10*time.Minute), ActionRefresh)

	tests := []struct {
		sort SortKey
		desc bool
		want []string
	}{
		// "Beta" and "beta" are equal without case and fall on both sides
		// of the first page boundary.
		{SortTitle, false, []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7"}},
		{SortTitle, true, []string{"k7", "k6", "k5", "k4", "k3", "k2", "k1"}},
		// The first page ends on the due row k5, whose NextDue is zero.
		{SortDue, false, []string{"k1", "k2", "k5", "k3", "k4", "k7", "k6"}},
		{SortLastAttempt, false, []string{"k1", "k2", "k5", "k6", "k7", "k3", "k4"}},
	}
	for _, tt := range tests {
		q := ListQuery{Tab: TabAttention, Sort: tt.sort, Desc: tt.desc, Rules: rules}
		if got := listKeys(listAll(t, s, q)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s desc=%v: one call = %v, want %v", tt.sort, tt.desc, got, tt.want)
		}

		var paged []string
		q.Limit = 3
		for page := 0; ; page++ {
			if page > 3 {
				t.Fatalf("%s desc=%v: too many pages", tt.sort, tt.desc)
			}
			items, next, err := s.ListItems(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			paged = append(paged, listKeys(items)...)
			if next == nil {
				break
			}
			if len(items) != 3 {
				t.Fatalf("%s desc=%v: page %d has %d rows and a next cursor", tt.sort, tt.desc, page, len(items))
			}
			q.After = next
		}
		if !reflect.DeepEqual(paged, tt.want) {
			t.Errorf("%s desc=%v: pages = %v, want %v", tt.sort, tt.desc, paged, tt.want)
		}
	}
}

func TestListStopsWaitingWhenCanceled(t *testing.T) {
	s := openTest(t)
	// The transaction holds the only connection.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = s.ListItems(ctx, ListQuery{Tab: TabAttention, Limit: 10, Rules: DueRules{Now: base}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListItems = %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("ListItems returned after %v, want within 1s", d)
	}
}

func TestListRejectsInvalidQuery(t *testing.T) {
	s := openTest(t)
	tests := []struct {
		name string
		q    ListQuery
	}{
		{"completed on attention", ListQuery{Tab: TabAttention, Sort: SortCompleted, Limit: 10}},
		{"completed on stopped", ListQuery{Tab: TabStopped, Sort: SortCompleted, Limit: 10}},
		{"unknown sort", ListQuery{Tab: TabAttention, Sort: "bogus", Limit: 10}},
		{"zero limit", ListQuery{Tab: TabAttention}},
		{"unknown tab", ListQuery{Tab: "x", Limit: 10}},
		{"fixed tab", ListQuery{Tab: TabFixed, Limit: 10}},
	}
	for _, tt := range tests {
		if _, _, err := s.ListItems(context.Background(), tt.q); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("%s: err = %v, want ErrInvalidQuery", tt.name, err)
		}
	}
}
