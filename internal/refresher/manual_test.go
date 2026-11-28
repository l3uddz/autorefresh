package refresher

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestManualRefreshRecordsAttempt(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	item, found := f.row("home", "1", "1")
	if !found || item.Attempts != 1 || item.Refreshes != 1 {
		t.Fatalf("item = %+v, found %v", item, found)
	}
	events, err := f.store.Timeline("home", "1")
	if err != nil || len(events) != 1 || events[0].Source != store.SourceManual || !events[0].Accepted {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestRunRechecksSameSecondManualAttempt(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	batch, found := f.row("home", "1", "1")
	if !found {
		t.Fatal("missing batch row")
	}
	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	run := &serverRun{server: f.r.servers[0], source: store.SourceAuto}
	if sent, err := f.r.runItem(t.Context(), run, batch); err != nil || sent {
		t.Fatalf("runItem = sent %v, err %v", sent, err)
	}
	current, _ := f.row("home", "1", "1")
	if run.skipped != 1 || current.Attempts != 2 || !current.LastAttempt.Equal(batch.LastAttempt) {
		t.Fatalf("skipped %d, attempts %d, timestamps %v and %v", run.skipped, current.Attempts, batch.LastAttempt, current.LastAttempt)
	}
}

func TestManualRefreshOfHistoryOnlyItem(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	row, _ := f.row("home", "1", "1")
	row.Reasons = 0
	if _, err := f.store.ApplyCheck("home", row, t0, 0, store.Observation{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	if _, found := f.row("home", "1", "1"); found {
		t.Fatal("manual refresh inserted a cache row")
	}
	latest := f.newestEvent("1")
	if latest.Kind != store.EventRequest || latest.Source != store.SourceManual || !latest.Accepted {
		t.Fatalf("latest event = %+v", latest)
	}
}

type recordingBarrier struct {
	Store
	entered chan struct{}
	release chan struct{}
}

func (s recordingBarrier) RecordManual(m store.ManualRecord) error {
	close(s.entered)
	<-s.release
	return s.Store.RecordManual(m)
}

func TestCloseDrainsManualRecording(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: newFakePlex(lib)})
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	f.r.now = func() time.Time { return t0 }
	barrier := recordingBarrier{Store: f.store, entered: make(chan struct{}), release: make(chan struct{})}
	f.r.store = barrier
	done := make(chan error, 1)
	go func() {
		_, err := f.r.RefreshItem(t.Context(), "home", "1")
		done <- err
	}()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("manual request did not reach recording")
	}
	released := false
	defer func() {
		if !released {
			close(barrier.release)
		}
	}()
	closed := make(chan struct{})
	go func() {
		f.r.Close()
		close(closed)
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
waitForGate:
	for {
		_, err := f.r.RefreshItem(t.Context(), "home", "1")
		if errors.Is(err, ErrClosed) {
			break waitForGate
		}
		if !errors.Is(err, ErrItemBusy) {
			t.Fatalf("action while recording = %v", err)
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("action gate did not close")
		}
	}
	select {
	case <-closed:
		t.Fatal("close returned before recording")
	default:
	}
	close(barrier.release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not drain")
	}
	if event := f.newestEvent("1"); !event.Accepted || event.Source != store.SourceManual {
		t.Fatalf("event = %+v", event)
	}
}

type manualMetadata struct {
	*fakePlex
	live plex.Item
	err  error
}

func (p manualMetadata) Item(context.Context, string) (plex.Item, error) { return p.live, p.err }

// The check after a manual refresh keeps only images of the requested key.
func TestManualRefreshCheckValidatesImagesAgainstRequestedKey(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	const thumb = "/library/metadata/1/thumb/1700000050"
	p := manualMetadata{fakePlex: newFakePlex(lib), live: plex.Item{RatingKey: "2", Title: "Updated", Thumb: thumb, Art: "/library/metadata/2/art/1700000050"}}
	f := newRefreshFixture(t, Server{Name: "home", Plex: p})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	row, found := f.row("home", "1", "1")
	if !found || row.Thumb != thumb || row.Art != "" || row.Title != "Updated" || row.Attempts != 1 {
		t.Fatalf("checked item = %+v, found %v", row, found)
	}
}

// A check after a manual refresh that Plex answers with 404 removes the
// row; the refresh of the snapshot that remains sends no check.
func TestManualRefreshCheckGoneRemovesRowAndRecordsManualEvent(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	p := manualMetadata{fakePlex: newFakePlex(lib), err: plex.ErrNotFound}
	f := newRefreshFixture(t, Server{Name: "home", Plex: p})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatal(err)
	}
	if _, found := f.row("home", "1", "1"); found {
		t.Fatal("gone item still cached")
	}
	if event := f.newestEvent("1"); event.Kind != store.EventGone || event.Source != store.SourceManual || event.Action != "" {
		t.Fatalf("gone event = %+v, want a manual gone event of the check", event)
	}
	if len(f.sleeps) != 1 {
		t.Fatalf("sleeps = %v, want one wait before the check", f.sleeps)
	}
	if _, err := f.r.RefreshItem(t.Context(), "home", "1"); err != nil {
		t.Fatalf("history refresh = %v", err)
	}
	if len(f.sleeps) != 1 {
		t.Fatalf("sleeps = %v, want no check of the snapshot", f.sleeps)
	}
	if _, err := f.r.SetStopped(t.Context(), "home", "1", true); !errors.Is(err, ErrNotCached) {
		t.Fatalf("history stop = %v", err)
	}
}

func TestManualMatchRecordsTargetAndStoppedRunSkipsItem(t *testing.T) {
	lib := plex.Library{ID: "1", Type: "movie"}
	f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: newFakePlex(lib), answers: []checkAnswer{{item: incompleteItem("1")}}, checks: new(int)}})
	f.now = t0
	f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
	batch, _ := f.row("home", "1", "1")
	if changed, err := f.r.SetStopped(t.Context(), "home", "1", true); err != nil || !changed {
		t.Fatalf("stop = %v, %v", changed, err)
	}
	run := &serverRun{server: f.r.servers[0], source: store.SourceAuto}
	if sent, err := f.r.runItem(t.Context(), run, batch); err != nil || sent || run.skipped != 1 {
		t.Fatalf("scheduled skip = sent %v, skipped %d, %v", sent, run.skipped, err)
	}
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	row, _ := f.row("home", "1", "1")
	// The match and the refresh after it are two attempts.
	if row.StoppedAt.IsZero() || row.Attempts != 2 {
		t.Fatalf("item after match = %+v", row)
	}
	e := f.matchEvent("1")
	if e.MatchGUID != cand.GUID || e.MatchTitle != cand.Name || e.MatchYear != cand.Year || e.Source != store.SourceManual {
		t.Fatalf("match event = %+v", e)
	}
}

// matchEvent returns the newest match request event of key.
func (f *fixture) matchEvent(key string) store.Event {
	f.t.Helper()
	for _, e := range f.timeline(key) {
		if e.Kind == store.EventRequest && e.Action == store.ActionMatch {
			return e
		}
	}
	f.t.Fatalf("item %s has no match event", key)
	return store.Event{}
}

// checkedMetadata answers each Item call with the next result of answers,
// and repeats the last one.
type checkedMetadata struct {
	*fakePlex
	answers []checkAnswer
	checks  *int
}

type checkAnswer struct {
	item plex.Item
	err  error
}

func (p checkedMetadata) Item(context.Context, string) (plex.Item, error) {
	a := p.answers[min(*p.checks, len(p.answers)-1)]
	*p.checks++
	return a.item, a.err
}

func (p checkedMetadata) ItemWithPaths(ctx context.Context, ratingKey string) (plex.Item, plex.Paths, error) {
	if err := canceled(ctx); err != nil {
		return plex.Item{}, plex.Paths{}, err
	}
	p.record("ItemWithPaths " + ratingKey)
	if err := p.pathErrs[ratingKey]; err != nil {
		return plex.Item{}, plex.Paths{}, err
	}
	a := p.answers[min(*p.checks, len(p.answers)-1)]
	*p.checks++
	if a.err != nil {
		return plex.Item{}, plex.Paths{}, a.err
	}
	return a.item, plex.Paths{Locations: p.paths[ratingKey]}, nil
}

func matchCheckFixture(t *testing.T, answers ...checkAnswer) (*fixture, *fakePlex, *int) {
	t.Helper()
	lib := plex.Library{ID: "1", Type: "movie"}
	fake, checks := newFakePlex(lib), new(int)
	f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: fake, answers: answers, checks: checks}})
	f.now = t0
	unmatched := incompleteItem("1")
	unmatched.GUID = "local://1"
	f.seed("home", lib, unmatched, store.Unmatched|store.NoSummary, 0)
	return f, fake, checks
}

func TestApplyMatchRefreshesAndChecksUntilComplete(t *testing.T) {
	still := incompleteItem("1")
	f, fake, checks := matchCheckFixture(t, checkAnswer{item: still}, checkAnswer{item: completeItem("1")})
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Match 1 plex://movie/1", "Refresh 1"}; !reflect.DeepEqual(fake.calls, want) {
		t.Errorf("calls = %v, want %v", fake.calls, want)
	}
	if want := []time.Duration{time.Second, 2 * time.Second}; *checks != 2 || !reflect.DeepEqual(f.sleeps, want) {
		t.Errorf("checks %d, sleeps %v, want 2 and %v", *checks, f.sleeps, want)
	}
	if _, found := f.row("home", "1", "1"); found {
		t.Error("complete item still in the cache")
	}
	match := f.matchEvent("1")
	if e := f.newestEvent("1"); e.Kind != store.EventCompleted || e.FixedAction != store.ActionRefresh ||
		e.FixedMatch != store.ActionMatch || e.FixedMatchEventID != match.ID {
		t.Errorf("newest event = %+v, want a completion credited to the refresh after match %d", e, match.ID)
	}
}

// The first check that leaves reasons saves them: the matched item has a
// GUID now, so only its missing summary remains.
func TestApplyMatchCheckSavesRemainingReasons(t *testing.T) {
	f, _, checks := matchCheckFixture(t, checkAnswer{item: incompleteItem("1")})
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	row, found := f.row("home", "1", "1")
	if *checks != 3 || !found || row.Reasons != store.NoSummary {
		t.Errorf("checks %d, row %+v, found %v, want 3 checks and only no-summary", *checks, row, found)
	}
}

func TestApplyMatchCheckFailureKeepsMatch(t *testing.T) {
	f, _, checks := matchCheckFixture(t, checkAnswer{err: errors.New("plex unavailable")})
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatalf("match = %v, want nil after an accepted match", err)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}; *checks != 3 || !reflect.DeepEqual(f.sleeps, want) {
		t.Errorf("checks %d, sleeps %v, want 3 and %v", *checks, f.sleeps, want)
	}
	row, found := f.row("home", "1", "1")
	if !found || row.Reasons != store.Unmatched|store.NoSummary || row.LastMatch != store.ActionMatch {
		t.Errorf("row = %+v, found %v", row, found)
	}
	if e := f.matchEvent("1"); !e.Accepted {
		t.Errorf("match event = %+v", e)
	}
}

func TestApplyMatchFailedSkipsRefreshAndCheck(t *testing.T) {
	f, fake, checks := matchCheckFixture(t, checkAnswer{item: completeItem("1")})
	fake.matchErrs["1"] = errors.New("plex refused")
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err == nil {
		t.Fatal("failed match returned nil")
	}
	if want := []string{"Match 1 plex://movie/1"}; !reflect.DeepEqual(fake.calls, want) || *checks != 0 || len(f.sleeps) != 0 {
		t.Errorf("calls %v, checks %d, sleeps %v", fake.calls, *checks, f.sleeps)
	}
}

// A run that removes the row during a wait settles the match: the checks
// stop without a warning and ask Plex nothing, also when Plex would fail.
func TestApplyMatchStopsWhenRowLeavesCache(t *testing.T) {
	f, _, checks := matchCheckFixture(t, checkAnswer{err: errors.New("item 1: Plex answered 500")})
	buf := captureLogs(t)
	f.r.sleep = func(context.Context, time.Duration) error {
		f.sleeps = append(f.sleeps, time.Second)
		return f.store.RecordGone("home", "1", t0, store.SourceAuto, "", 0, store.AttemptResult{})
	}
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	if *checks != 0 || len(f.sleeps) != 1 {
		t.Errorf("checks %d, sleeps %v; want no check after 1 wait", *checks, f.sleeps)
	}
	if strings.Contains(buf.String(), "Following up a request failed") {
		t.Errorf("log warns about a row that a run removed: %s", buf)
	}
}

// An unavailable server ends the follow-up: no check asks it again. The
// accepted match is no error.
func TestApplyMatchRefreshUnavailableSkipsChecks(t *testing.T) {
	f, fake, checks := matchCheckFixture(t, checkAnswer{item: completeItem("1")})
	fake.refreshErrs["1"] = fmt.Errorf("refresh: %w", plex.ErrUnavailable)
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	if *checks != 0 || len(f.sleeps) != 0 {
		t.Errorf("checks %d, sleeps %v; want none", *checks, f.sleeps)
	}
}

func TestApplyMatchStopsCheckingWhenPlexIsUnavailable(t *testing.T) {
	f, _, checks := matchCheckFixture(t, checkAnswer{err: fmt.Errorf("item 1: %w", plex.ErrUnavailable)}, checkAnswer{item: completeItem("1")})
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	if *checks != 1 {
		t.Errorf("checks = %d, want 1", *checks)
	}
}

// A refresh that Plex answers with 404 records the item gone and skips the
// checks; the accepted match is no error.
func TestApplyMatchRefreshGoneSkipsChecks(t *testing.T) {
	f, fake, checks := matchCheckFixture(t, checkAnswer{item: completeItem("1")})
	fake.refreshErrs["1"] = plex.ErrNotFound
	cand := plex.Candidate{GUID: "plex://movie/1", Name: "Match", Year: 2020}
	if err := f.r.ApplyMatch(t.Context(), "home", "1", cand); err != nil {
		t.Fatal(err)
	}
	if *checks != 0 || len(f.sleeps) != 0 {
		t.Errorf("checks %d, sleeps %v; want none", *checks, f.sleeps)
	}
	if _, found := f.row("home", "1", "1"); found {
		t.Error("gone item still cached")
	}
	if e := f.newestEvent("1"); e.Kind != store.EventGone || e.Action != store.ActionRefresh {
		t.Errorf("newest event = %+v, want gone by the refresh", e)
	}
}

// refreshNowFixture seeds the unmatched movie 101, which was never
// refreshed, whose folder tmdb-1 finds one candidate. Plex first reads the
// item unmatched, before the match, and then complete in each check.
func refreshNowFixture(t *testing.T) (*fixture, *fakePlex) {
	t.Helper()
	fp := newFakePlex(moviesLib)
	fp.paths["101"] = []string{"/m/Film (2020) {tmdb-1}/Film.mkv"}
	fp.candidates["101 tmdb-1"] = []plex.Candidate{{GUID: "plex://movie/m", Name: "Film", Year: 2020}}
	answers := []checkAnswer{{item: unmatchedItem("101")}, {item: unmatchedItem("101")}, {item: completeItem("101")}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: checkedMetadata{fakePlex: fp, answers: answers, checks: new(int)}})
	f.now = t0
	f.seed(testServer, moviesLib, unmatchedItem("101"), store.Unmatched, 0)
	f.saveLibrary(moviesLib)
	return f, fp
}

// Refresh now runs the automatic fix match of an unmatched item, also
// before its first refresh, applies the pick, and follows it up until the
// check completes the item.
func TestRefreshItemAppliesTheAutomaticMatch(t *testing.T) {
	f, fp := refreshNowFixture(t)

	res, err := f.r.RefreshItem(t.Context(), testServer, "101")
	if err != nil {
		t.Fatal(err)
	}
	if res.Match == nil || res.Match.GUID != "plex://movie/m" || res.Match.Title != "Film" || !res.Checked {
		t.Errorf("result match = %+v, want plex://movie/m", res.Match)
	}
	wantCalls(t, sends(fp.calls), "Match 101 plex://movie/m", "Refresh 101")
	if _, found := f.row(testServer, "1", "101"); found {
		t.Error("complete item still in the cache")
	}
	var decision, match store.Event
	for _, e := range f.timeline("101") {
		switch {
		case e.Kind == store.EventMatchDecision:
			decision = e
		case e.Kind == store.EventRequest && e.Action == store.ActionMatch:
			match = e
		}
	}
	if decision.Outcome != string(store.DecisionMatched) || decision.Source != store.SourceAuto || decision.RunID != 0 {
		t.Errorf("decision = %+v, want a matched automatic decision of no run", decision)
	}
	if match.Source != store.SourceManual || !match.Accepted || match.MatchGUID != "plex://movie/m" ||
		len(match.Links) != 1 {
		t.Errorf("match request = %+v, want an accepted manual match with the link of tmdb-1", match)
	}
}

// With the unmatched reason ignored, Refresh now sends a plain refresh and
// records no decision.
func TestRefreshItemIgnoredUnmatchedOnlyRefreshes(t *testing.T) {
	f, fp := refreshNowFixture(t)
	f.r.opts.IgnoreReasons = store.Unmatched

	res, err := f.r.RefreshItem(t.Context(), testServer, "101")
	if err != nil || res.Match != nil {
		t.Fatalf("refresh = %+v, %v", res, err)
	}
	wantCalls(t, matchReads(fp.calls))
	wantCalls(t, sends(fp.calls), "Refresh 101")
	for _, e := range f.timeline("101") {
		if e.Kind == store.EventMatchDecision {
			t.Errorf("decision recorded: %+v", e)
		}
	}
}

// A failed search records a failed decision, and the plain refresh still
// goes to Plex.
func TestRefreshItemFailedSearchStillRefreshes(t *testing.T) {
	f, fp := refreshNowFixture(t)
	fp.searchErrs["101"] = plex.ErrUnavailable

	res, err := f.r.RefreshItem(t.Context(), testServer, "101")
	if err != nil || res.Match != nil {
		t.Fatalf("refresh = %+v, %v", res, err)
	}
	wantCalls(t, sends(fp.calls), "Refresh 101")
	var outcome string
	for _, e := range f.timeline("101") {
		if e.Kind == store.EventMatchDecision {
			outcome = e.Outcome
		}
	}
	if outcome != string(store.DecisionFailed) {
		t.Errorf("decision outcome = %q, want failed", outcome)
	}
}

// When Plex no longer has the item, the search records no decision, and
// the refresh records the item gone once.
func TestRefreshItemGoneDuringSearchRecordsGoneOnce(t *testing.T) {
	f, fp := refreshNowFixture(t)
	fp.pathErrs["101"] = plex.ErrNotFound
	fp.refreshErrs["101"] = plex.ErrNotFound

	if _, err := f.r.RefreshItem(t.Context(), testServer, "101"); !errors.Is(err, plex.ErrNotFound) {
		t.Fatalf("refresh = %v, want not found", err)
	}
	var kinds []store.EventKind
	for _, e := range f.timeline("101") {
		kinds = append(kinds, e.Kind)
	}
	if !reflect.DeepEqual(kinds, []store.EventKind{store.EventGone}) {
		t.Errorf("events = %v, want one gone event", kinds)
	}
}

// The end of ctx during the search sends nothing and records nothing.
func TestRefreshItemCanceledDuringSearch(t *testing.T) {
	f, fp := refreshNowFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	fp.onCall = func(call string) {
		if call == "ItemWithPaths 101" {
			cancel()
		}
	}

	if _, err := f.r.RefreshItem(ctx, testServer, "101"); !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh = %v, want canceled", err)
	}
	wantCalls(t, sends(fp.calls))
	if events := f.timeline("101"); len(events) != 0 {
		t.Errorf("events = %+v, want none", events)
	}
}

// Refresh now reports whether a check saved the reasons of the item: not
// when Plex became unavailable after it accepted the refresh.
func TestRefreshItemReportsWhetherTheCheckRan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answer  checkAnswer
		checked bool
	}{
		{"check saved", checkAnswer{item: incompleteItem("1")}, true},
		{"plex unavailable", checkAnswer{err: fmt.Errorf("item 1: %w", plex.ErrUnavailable)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := plex.Library{ID: "1", Type: "movie"}
			f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: newFakePlex(lib), answers: []checkAnswer{tc.answer}, checks: new(int)}})
			f.now = t0
			f.seed("home", lib, incompleteItem("1"), store.NoSummary, 0)
			res, err := f.r.RefreshItem(t.Context(), "home", "1")
			if err != nil || res.Checked != tc.checked {
				t.Fatalf("refresh = %+v, %v; want checked %v", res, err, tc.checked)
			}
		})
	}
}

// Plex tells the match state of the item just before the match: a match
// that the item got during the search stays, and a state that Plex cannot
// tell keeps the match too. Refresh now then sends only the refresh.
func TestRefreshItemSendsNoMatchUnlessPlexStillHasTheItemUnmatched(t *testing.T) {
	for _, tc := range []struct {
		name         string
		answers      []checkAnswer
		wantDecision bool
		wantSearch   bool
	}{
		{"already matched before search", []checkAnswer{{item: completeItem("101")}}, false, false},
		{"matched during the search", []checkAnswer{{item: unmatchedItem("101")}, {item: completeItem("101")}}, true, true},
		{"plex unavailable during keepMatch", []checkAnswer{{item: unmatchedItem("101")}, {err: fmt.Errorf("item 101: %w", plex.ErrUnavailable)}}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, fp := refreshNowFixture(t)
			f.r.servers[0].Plex = checkedMetadata{fakePlex: fp, answers: tc.answers, checks: new(int)}

			res, err := f.r.RefreshItem(t.Context(), testServer, "101")
			if err != nil || res.Match != nil {
				t.Fatalf("refresh = %+v, %v; want a plain refresh", res, err)
			}
			wantCalls(t, sends(fp.calls), "Refresh 101")
			hasDecision := false
			for _, e := range f.timeline("101") {
				if e.Kind == store.EventMatchDecision {
					hasDecision = true
				}
			}
			if hasDecision != tc.wantDecision {
				t.Errorf("hasDecision = %v, want %v", hasDecision, tc.wantDecision)
			}
			hasSearch := false
			for _, c := range fp.calls {
				if strings.HasPrefix(c, "SearchMatches ") {
					hasSearch = true
				}
			}
			if hasSearch != tc.wantSearch {
				t.Errorf("hasSearch = %v, want %v (calls %v)", hasSearch, tc.wantSearch, fp.calls)
			}
		})
	}
}

// A search that runs out of its time records a failed decision, and the
// plain refresh still goes to Plex.
func TestRefreshItemSlowSearchStillRefreshes(t *testing.T) {
	f, fp := refreshNowFixture(t)
	saved := manualSearchTimeout
	manualSearchTimeout = 10 * time.Millisecond
	t.Cleanup(func() { manualSearchTimeout = saved })
	fp.onCall = func(call string) {
		if call == "ItemWithPaths 101" {
			time.Sleep(50 * time.Millisecond)
		}
	}

	res, err := f.r.RefreshItem(t.Context(), testServer, "101")
	if err != nil || res.Match != nil {
		t.Fatalf("refresh = %+v, %v; want a plain refresh", res, err)
	}
	wantCalls(t, sends(fp.calls), "Refresh 101")
	if e := f.decision("101"); e.Outcome != string(store.DecisionFailed) {
		t.Errorf("decision = %+v, want failed", e)
	}
}

// Refresh now can complete an item, or find it gone, while a run reads its
// library, after the read listed it, also in the same second. The read does
// not add the row back, so the run sends no request for the item. A later
// read that lists the item as incomplete adds the row again.
func TestReadKeepsRefreshNowSettlement(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refreshErr error
		kind       store.EventKind
	}{
		{"check completes", nil, store.EventCompleted},
		{"refresh finds it gone", errNotFound, store.EventGone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakePlex(moviesLib)
			if tc.refreshErr != nil {
				fake.refreshErrs["1"] = tc.refreshErr
			}
			f := newRefreshFixture(t, Server{Name: "home", Plex: checkedMetadata{fakePlex: fake,
				answers: []checkAnswer{{item: completeItem("1")}}, checks: new(int)}})
			f.seed("home", moviesLib, incompleteItem("1"), store.NoSummary, 0)
			listing := "1?type=1&updatedAt>>=" + unixString(seedAt.Add(-oversample))
			fake.lists[listing] = []plex.Item{incompleteItem("1")}
			refreshed := false
			fake.onCall = func(call string) {
				if call != "List "+listing || refreshed {
					return
				}
				refreshed = true
				if _, err := f.r.RefreshItem(t.Context(), "home", "1"); !errors.Is(err, tc.refreshErr) {
					t.Errorf("refresh now = %v, want %v", err, tc.refreshErr)
				}
			}
			f.run(t0)

			if !refreshed {
				t.Fatal("the read did not list the item")
			}
			if row, found := f.row("home", "1", "1"); found {
				t.Fatalf("row = %+v, want none after Refresh now", row)
			}
			wantCalls(t, sends(fake.calls), "Refresh 1")
			if last := f.newestEvent("1"); last.Kind != tc.kind || last.RunID != 0 {
				t.Errorf("newest event = %+v, want the %s event of Refresh now", last, tc.kind)
			}

			// The dry run keeps the row that its read adds.
			f.r.opts.DryRun = true
			fake.lists["1?type=1&updatedAt>>="+unixString(t0.Add(-oversample))] = []plex.Item{incompleteItem("1")}
			f.run(t0.Add(time.Hour))
			if row, found := f.row("home", "1", "1"); !found || row.Reasons != store.NoSummary || row.Attempts != 0 {
				t.Fatalf("row = %+v, found %v; want the row again", row, found)
			}
		})
	}
}
