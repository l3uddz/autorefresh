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

// listPages follows the pages of q with limit rows each and returns the
// keys of all pages.
func listPages(t *testing.T, s *Store, q ListQuery, limit int) []string {
	t.Helper()
	paged := []string{}
	q.Limit = limit
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatalf("%s %s desc=%v: too many pages", q.Tab, q.Sort, q.Desc)
		}
		items, next, err := s.ListItems(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, listKeys(items)...)
		if next == nil {
			return paged
		}
		if len(items) != limit {
			t.Fatalf("%s %s desc=%v: page %d has %d rows and a next cursor", q.Tab, q.Sort, q.Desc, page, len(items))
		}
		q.After = next
	}
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
	// Too new until now+1h, and waiting for 4h after now-1m.
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
	accept(t, s, "a", base, ActionRefresh)
	rules := DueRules{Now: base.Add(1000 * time.Hour), RefreshInterval: time.Hour, Ignored: NoArt}

	// The default order lists the most tries first. Equal tries follow the
	// key, in the same direction.
	if got := listKeys(listAll(t, s, ListQuery{Tab: TabAttention, Rules: rules})); !reflect.DeepEqual(got, []string{"a", "c", "b"}) {
		t.Errorf("Attention = %v, want [a c b]", got)
	}
	if got := listKeys(listAll(t, s, ListQuery{Tab: TabAttention, WithIgnored: true, Rules: rules})); !reflect.DeepEqual(got, []string{"a", "ignored", "c", "b"}) {
		t.Errorf("Attention with ignored = %v, want [a ignored c b]", got)
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

		if paged := listPages(t, s, q, 3); !reflect.DeepEqual(paged, tt.want) {
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
		{"due on fixed", ListQuery{Tab: TabFixed, Sort: SortDue, Limit: 10}},
		{"zero limit on fixed", ListQuery{Tab: TabFixed}},
	}
	for _, tt := range tests {
		if _, _, err := s.ListItems(context.Background(), tt.q); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("%s: err = %v, want ErrInvalidQuery", tt.name, err)
		}
	}
}

func TestDefaultSort(t *testing.T) {
	tests := []struct {
		tab  Tab
		key  SortKey
		desc bool
	}{
		{TabAttention, SortTries, true},
		{TabStopped, SortStopped, true},
		{TabFixed, SortCompleted, true},
		{"x", "", false},
	}
	for _, tt := range tests {
		key, desc := DefaultSort(tt.tab)
		if key != tt.key || desc != tt.desc {
			t.Errorf("DefaultSort(%q) = %q, %v, want %q, %v", tt.tab, key, desc, tt.key, tt.desc)
		}
		if tt.key != "" && !ValidSort(tt.tab, key) {
			t.Errorf("default sort %q of %q is not valid", key, tt.tab)
		}
	}
}

func TestValidSort(t *testing.T) {
	queue := []SortKey{SortTitle, SortYear, SortAdded, SortDue, SortTries, SortLastAttempt, SortStopped}
	fixed := []SortKey{SortTitle, SortYear, SortCompleted}
	every := append(append([]SortKey{}, queue...), SortCompleted, "bogus", "")
	for tab, valid := range map[Tab][]SortKey{TabAttention: queue, TabStopped: queue, TabFixed: fixed, "x": nil} {
		for _, key := range every {
			want := false
			for _, v := range valid {
				want = want || v == key
			}
			if got := ValidSort(tab, key); got != want {
				t.Errorf("ValidSort(%q, %q) = %v, want %v", tab, key, got, want)
			}
		}
	}
}

// complete completes the row of key at at, with the metadata of it.
func complete(t *testing.T, s *Store, it Item, at time.Time) {
	t.Helper()
	it.Reasons = 0
	apply(t, s, Read{At: at, Items: []Item{it}})
}

// insertCompleted writes a completed event of key by SQL.
func insertCompleted(t *testing.T, s *Store, key string, at time.Time, action Action) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO event (server, section, rating_key, at, kind, source, title, fixed_action)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, srv, sec, key, at.Unix(), EventCompleted, SourceAuto, "Title "+key, action)
	if err != nil {
		t.Fatal(err)
	}
}

func TestListFixedShowsNewestCompletionOnce(t *testing.T) {
	s := openTest(t)
	if err := s.SaveLibraries(srv, []LibraryInfo{{Section: sec, Title: "TV"}}); err != nil {
		t.Fatal(err)
	}
	a := item("a", NoArt)
	a.Thumb, a.Art, a.Path = "/t", "/a", "/tv/A"
	apply(t, s, Read{Items: []Item{a, item("u", NoArt)}})
	accept(t, s, "a", base.Add(time.Minute), ActionRefresh)
	complete(t, s, a, base.Add(time.Hour))
	complete(t, s, item("u", NoArt), base.Add(30*time.Minute))
	apply(t, s, Read{At: base.Add(2 * time.Hour), Items: []Item{a}})
	accept(t, s, "a", base.Add(2*time.Hour+time.Minute), ActionMatch)
	complete(t, s, a, base.Add(3*time.Hour))

	rules := DueRules{Now: base.Add(1000 * time.Hour)}
	got := listAll(t, s, ListQuery{Tab: TabFixed, Rules: rules})
	want := []ListItem{{
		Server: srv, Section: sec, RatingKey: "a", Library: "TV", Type: "show", Title: "Title a", Year: 2020,
		Thumb: "/t", Art: "/a", Path: "/tv/A",
		CompletedAt: base.Add(3 * time.Hour), FixedAction: ActionMatch, FixedMatch: ActionMatch,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fixed = %+v, want %+v", got, want)
	}

	all := listAll(t, s, ListQuery{Tab: TabFixed, WithUnrequested: true, Rules: rules})
	if keys := listKeys(all); !reflect.DeepEqual(keys, []string{"a", "u"}) {
		t.Fatalf("Fixed with unrequested = %v, want [a u]", keys)
	}
	if u := all[1]; u.FixedAction != "" || !u.CompletedAt.Equal(base.Add(30*time.Minute)) {
		t.Errorf("unrequested row = %+v, want no FixedAction and CompletedAt %v", u, base.Add(30*time.Minute))
	}

	apply(t, s, Read{At: base.Add(4 * time.Hour), Items: []Item{a}})
	again := listAll(t, s, ListQuery{Tab: TabFixed, Rules: rules})
	if len(again) != 1 || !again[0].Cached || !again[0].CompletedAt.Equal(base.Add(3*time.Hour)) {
		t.Fatalf("Fixed while cached again = %+v, want the row of a with Cached", again)
	}
}

func TestListFixedOrdersCompletionsByTime(t *testing.T) {
	s := openTest(t)
	apply(t, s, Read{Items: []Item{item("late", NoArt)}})
	accept(t, s, "late", base.Add(time.Minute), ActionRefresh)
	complete(t, s, item("late", NoArt), base.Add(2*time.Hour))
	// The last event of late has the highest id but an older time.
	insertCompleted(t, s, "late", base.Add(time.Hour), ActionMatch)
	insertCompleted(t, s, "tie", base.Add(3*time.Hour), ActionRefresh)
	insertCompleted(t, s, "tie", base.Add(3*time.Hour), ActionMatch)

	rules := DueRules{Now: base.Add(1000 * time.Hour)}
	got := listAll(t, s, ListQuery{Tab: TabFixed, Rules: rules})
	if keys := listKeys(got); !reflect.DeepEqual(keys, []string{"tie", "late"}) {
		t.Fatalf("Fixed = %v, want [tie late]", keys)
	}
	if got[0].FixedAction != ActionMatch {
		t.Errorf("tie: FixedAction = %q, want the higher id with %q", got[0].FixedAction, ActionMatch)
	}
	if got[1].FixedAction != ActionRefresh || !got[1].CompletedAt.Equal(base.Add(2*time.Hour)) {
		t.Errorf("late = %+v, want the newest time with %q", got[1], ActionRefresh)
	}

	tests := []struct {
		sort SortKey
		desc bool
		want []string
	}{
		{"", false, []string{"tie", "late"}},
		{SortCompleted, false, []string{"late", "tie"}},
		{SortTitle, false, []string{"late", "tie"}},
		{SortTitle, true, []string{"tie", "late"}},
		{SortYear, false, []string{"tie", "late"}},
	}
	for _, tt := range tests {
		q := ListQuery{Tab: TabFixed, Sort: tt.sort, Desc: tt.desc, Rules: rules}
		if paged := listPages(t, s, q, 1); !reflect.DeepEqual(paged, tt.want) {
			t.Errorf("%q desc=%v: pages = %v, want %v", tt.sort, tt.desc, paged, tt.want)
		}
	}
}

func TestListFixedFiltersAfterChoosingNewest(t *testing.T) {
	s := openTest(t)
	a := item("a", NoArt)
	apply(t, s, Read{Items: []Item{a}})
	accept(t, s, "a", base.Add(time.Minute), ActionRefresh)
	complete(t, s, a, base.Add(time.Hour))
	apply(t, s, Read{At: base.Add(2 * time.Hour), Items: []Item{a}})
	complete(t, s, a, base.Add(3*time.Hour))

	rules := DueRules{Now: base.Add(1000 * time.Hour)}
	if got := listAll(t, s, ListQuery{Tab: TabFixed, Rules: rules}); len(got) != 0 {
		t.Fatalf("Fixed = %+v, want none", got)
	}
	got := listAll(t, s, ListQuery{Tab: TabFixed, WithUnrequested: true, Rules: rules})
	if len(got) != 1 || got[0].FixedAction != "" || !got[0].CompletedAt.Equal(base.Add(3*time.Hour)) {
		t.Fatalf("Fixed with unrequested = %+v, want the newer completion without FixedAction", got)
	}
}

func TestListFixedFilters(t *testing.T) {
	s := openTest(t)
	mk := func(key, typ, title, path string) Item {
		it := item(key, NoArt)
		it.Type, it.Title, it.Path = typ, title, path
		return it
	}
	reads := []Read{
		{Items: []Item{mk("m", "movie", "100% Pure", "/m/1"), mk("t", "show", "Plain", "/data/needle/x")}},
		{Section: "2", Items: []Item{mk("o", "show", "Other", "/o")}},
		{Server: "srv2", Items: []Item{mk("r", "show", "Remote", "/r")}},
	}
	for _, r := range reads {
		apply(t, s, r)
	}
	for _, r := range reads {
		r.At = base.Add(time.Hour)
		for i := range r.Items {
			r.Items[i].Reasons = 0
		}
		apply(t, s, r)
	}

	tests := []struct {
		name string
		q    ListQuery
		want []string
	}{
		{"none", ListQuery{}, []string{"m", "o", "r", "t"}},
		{"servers", ListQuery{Servers: []string{"srv2"}}, []string{"r"}},
		{"section", ListQuery{Section: "2"}, []string{"o"}},
		{"type", ListQuery{Type: "movie"}, []string{"m"}},
		{"reason is not used", ListQuery{Reason: NoSummary}, []string{"m", "o", "r", "t"}},
		{"search title", ListQuery{Search: "100%"}, []string{"m"}},
		{"search path", ListQuery{Search: "needle/"}, []string{"t"}},
	}
	for _, tt := range tests {
		tt.q.Tab, tt.q.WithUnrequested, tt.q.Rules = TabFixed, true, DueRules{Now: base}
		if got := sortedKeys(listAll(t, s, tt.q)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: keys = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCountItemsMatchesLists(t *testing.T) {
	s := openTest(t)
	movie := item("movie", NoSummary)
	movie.Type = "movie"
	apply(t, s, Read{Items: []Item{
		item("a", NoSummary), item("b", Unmatched), item("ignored", NoArt), movie,
		item("s1", NoSummary), item("s2", Unmatched), item("fixed", NoSummary), item("plain", NoSummary),
	}})
	apply(t, s, Read{Server: "srv2", Items: []Item{item("remote", NoSummary)}})
	stopItem(t, s, srv, "s1", base)
	stopItem(t, s, srv, "s2", base)
	accept(t, s, "fixed", base.Add(time.Minute), ActionRefresh)
	apply(t, s, Read{At: base.Add(time.Hour), Items: []Item{item("fixed", 0), item("plain", 0)}})
	rules := DueRules{Now: base.Add(1000 * time.Hour), RefreshInterval: time.Hour, Ignored: NoArt}

	ctx := context.Background()
	unfiltered, err := s.CountItems(ctx, ListQuery{Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	if want := (ListCounts{Attention: 4, Fixed: 1, Stopped: 2}); unfiltered != want {
		t.Fatalf("counts = %+v, want %+v", unfiltered, want)
	}

	queries := []ListQuery{
		{},
		{Servers: []string{srv}},
		{Section: "2"},
		{Type: "movie"},
		{Search: "Title a"},
		{Reason: Unmatched},
		{WithIgnored: true, WithUnrequested: true},
		// CountItems ignores the tab, sort, and paging fields.
		{Tab: TabFixed, Sort: SortCompleted, Desc: true, Limit: 0, After: &Cursor{Server: "zzz"}},
	}
	for _, q := range queries {
		q.Rules = rules
		got, err := s.CountItems(ctx, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		tab := func(tab Tab) int {
			tq := q
			tq.Tab, tq.Sort, tq.Desc, tq.After = tab, "", false, nil
			return len(listAll(t, s, tq))
		}
		want := ListCounts{Attention: tab(TabAttention), Fixed: tab(TabFixed), Stopped: tab(TabStopped)}
		if got != want {
			t.Errorf("%+v: counts = %+v, want %+v", q, got, want)
		}
	}
}

func TestItemDetailFromRowOrHistory(t *testing.T) {
	s := openTest(t)
	if err := s.SaveLibraries(srv, []LibraryInfo{{Section: sec, Title: "TV"}}); err != nil {
		t.Fatal(err)
	}
	a := item("a", NoSummary)
	a.Thumb, a.Art, a.Path = "/t", "/a", "/tv/A"
	apply(t, s, Read{Items: []Item{a, item("ignored", NoArt), item("stopped", NoSummary)}})
	accept(t, s, "a", base.Add(time.Minute), ActionRefresh)
	stopItem(t, s, srv, "stopped", base.Add(time.Hour))
	rules := DueRules{Now: base.Add(time.Minute + time.Second), RefreshInterval: time.Hour, Ignored: NoArt}

	ctx := context.Background()
	listed := append(listAll(t, s, ListQuery{Tab: TabAttention, WithIgnored: true, Rules: rules}),
		listAll(t, s, ListQuery{Tab: TabStopped, Rules: rules})...)
	if len(listed) != 3 {
		t.Fatalf("listed = %+v, want 3 rows", listed)
	}
	for _, want := range listed {
		got, found, err := s.ItemDetail(ctx, srv, want.RatingKey, rules)
		if err != nil || !found {
			t.Fatalf("ItemDetail %s = %v, %v", want.RatingKey, found, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ItemDetail %s = %+v, want %+v", want.RatingKey, got, want)
		}
	}

	done := a
	done.Title, done.Year, done.Thumb, done.Art, done.Path = "New a", 2021, "/t2", "/a2", "/tv/New"
	complete(t, s, done, base.Add(2*time.Hour))
	got, found, err := s.ItemDetail(ctx, srv, "a", rules)
	if err != nil || !found {
		t.Fatalf("ItemDetail after completion = %v, %v", found, err)
	}
	want := ListItem{
		Server: srv, Section: sec, RatingKey: "a", Library: "TV", Type: "show", Title: "New a", Year: 2021,
		Thumb: "/t2", Art: "/a2", Path: "/tv/New",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ItemDetail after completion = %+v, want %+v", got, want)
	}

	if _, found, err := s.ItemDetail(ctx, srv, "unknown", rules); err != nil || found {
		t.Fatalf("ItemDetail of an unknown key = %v, %v, want not found", found, err)
	}
}

// A row whose counted reasons are all ManualReasons, or all ignored, is
// never due. The due sort places it after every due time: last when the
// soonest comes first, first when the latest comes first. Ties keep their
// order across pages, and the listed NextDue stays zero.
func TestListSortsNeverDueRowsAfterEveryDueTime(t *testing.T) {
	s := openTest(t)
	now := base.Add(1000 * time.Hour)
	rules := DueRules{Now: now, RefreshInterval: time.Hour, MaxRefreshInterval: time.Hour, Ignored: NoArt}
	apply(t, s, Read{Items: []Item{
		item("k1", NoSummary), // due
		item("k2", NoSummary), // waiting until now+30m
		item("m1", Mismatched),
		item("m2", Mismatched),
		item("i1", NoArt), // ignored
	}})
	accept(t, s, "k2", now.Add(-30*time.Minute), ActionRefresh)

	for _, tt := range []struct {
		desc bool
		want []string
	}{
		{false, []string{"k1", "k2", "i1", "m1", "m2"}},
		{true, []string{"m2", "m1", "i1", "k2", "k1"}},
	} {
		q := ListQuery{Tab: TabAttention, Sort: SortDue, Desc: tt.desc, Rules: rules, WithIgnored: true}
		items := listAll(t, s, q)
		if got := listKeys(items); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("desc=%v: one call = %v, want %v", tt.desc, got, tt.want)
		}
		if paged := listPages(t, s, q, 2); !reflect.DeepEqual(paged, tt.want) {
			t.Errorf("desc=%v: pages = %v, want %v", tt.desc, paged, tt.want)
		}
		for _, it := range items {
			if it.RatingKey[0] != 'k' && !it.NextDue.IsZero() {
				t.Errorf("%s NextDue = %v, want zero", it.RatingKey, it.NextDue)
			}
		}
	}
}
