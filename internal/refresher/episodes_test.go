package refresher

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

func seq(n int, skip ...int) []int {
	out := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		if !slices.Contains(skip, i) {
			out = append(out, i)
		}
	}
	return out
}

func TestEpisodeNumbersConfirmSeriesYearConflict(t *testing.T) {
	tests := []struct {
		name      string
		local     map[int][]int
		catalog   map[string][]int
		want      []string
		wantCount float64
		wantDiff  store.EpisodeDiff
		directID  bool
	}{
		{
			name:      "complete local season matches despite a bad year and absent later season",
			local:     map[int][]int{1: {3, 1, 2}},
			catalog:   map[string][]int{"77 1": {1, 2, 3}, "77 2": {1, 2}},
			want:      []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount: 3,
		},
		{
			name:      "direct and translated IDs agree",
			local:     map[int][]int{1: {1, 2, 3}},
			catalog:   map[string][]int{"77 1": {1, 2, 3}},
			want:      []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount: 3,
			directID:  true,
		},
		{
			name:      "missing episodes across seasons at or above 90% confirm",
			local:     map[int][]int{1: seq(20), 2: seq(21, 8, 9)},
			catalog:   map[string][]int{"77 1": seq(20), "77 2": seq(21)},
			want:      []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount: 39,
		},
		{
			name:      "missing one episode at exact 90% boundary confirms",
			local:     map[int][]int{1: seq(10, 5)},
			catalog:   map[string][]int{"77 1": seq(10)},
			want:      []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount: 9,
		},
		{
			name:      "ongoing later season confirmed by high overall coverage",
			local:     map[int][]int{1: seq(20), 2: {1}},
			catalog:   map[string][]int{"77 1": seq(20), "77 2": {1, 2, 3}},
			want:      []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount: 21,
		},
		{
			name:     "missing one episode below 90% boundary does not confirm",
			local:    map[int][]int{1: seq(9, 5)},
			catalog:  map[string][]int{"77 1": seq(9)},
			want:     []string{"Refresh 201"},
			wantDiff: store.EpisodeDiff{Season: 1, Plex: 8, TMDB: 9, Missing: []int{5}},
		},
		{
			name:     "remake with longer seasons does not confirm",
			local:    map[int][]int{1: seq(13)},
			catalog:  map[string][]int{"77 1": seq(22)},
			want:     []string{"Refresh 201"},
			wantDiff: store.EpisodeDiff{Season: 1, Plex: 13, TMDB: 22, Missing: []int{14, 15, 16, 17, 18, 19, 20, 21, 22}},
		},
		{
			name:     "extra episode in later season rejects and reports that season",
			local:    map[int][]int{1: seq(20, 20), 2: seq(21)},
			catalog:  map[string][]int{"77 1": seq(20), "77 2": seq(20)},
			want:     []string{"Refresh 201"},
			wantDiff: store.EpisodeDiff{Season: 2, Plex: 21, TMDB: 20, Extra: []int{21}},
		},
		{
			name:    "same count with different numbering",
			local:   map[int][]int{1: {1, 2, 4}},
			catalog: map[string][]int{"77 1": {1, 2, 3}},
			want:    []string{"Refresh 201"},
		},
		{
			name:    "a second local season is incomplete below threshold",
			local:   map[int][]int{1: {1, 2, 3}, 2: {1}},
			catalog: map[string][]int{"77 1": {1, 2, 3}, "77 2": {1, 2}},
			want:    []string{"Refresh 201"},
		},
		{
			name:    "duplicate catalog numbers cannot confirm a season",
			local:   map[int][]int{1: {1, 1, 3}},
			catalog: map[string][]int{"77 1": {1, 1, 3}},
			want:    []string{"Refresh 201"},
		},
		{
			name:    "duplicate local numbers cannot confirm even with high count",
			local:   map[int][]int{1: {1, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
			catalog: map[string][]int{"77 1": seq(10)},
			want:    []string{"Refresh 201"},
		},
		{
			name:    "non-positive episode number cannot confirm",
			local:   map[int][]int{1: {0, 1, 2}},
			catalog: map[string][]int{"77 1": {0, 1, 2}},
			want:    []string{"Refresh 201"},
		},
		{
			name:  "missing catalog season",
			local: map[int][]int{1: {1, 2, 3}},
			want:  []string{"Refresh 201"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := newFakePlex(showsLib)
			fp.paths["201"] = []string{"/tv/Show (2003) {tvdb-5}"}
			if tt.directID {
				fp.paths["201"][0] += " {tmdb-77}"
			}
			fp.episodeNumbers = map[string]map[int][]int{"201": tt.local}
			candidate := []plex.Candidate{{GUID: "plex://show/right", Name: "Show", Year: 2011}}
			fp.candidates["201 tvdb-5"] = candidate
			fp.candidates["201 tmdb-77"] = candidate
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			ft := &fakeTMDB{
				ids:     map[string]string{"tvdb 5 true": "77"},
				years:   map[string]int{"77 true": 2011},
				seasons: tt.catalog,
			}
			f.r.opts.TMDB = ft
			item := unmatchedItem("201")
			item.Year = 2003
			f.seed(testServer, showsLib, item, store.Unmatched, 1)

			f.run(t0)

			wantCalls(t, sends(fp.calls), tt.want...)
			if tt.directID {
				wantCalls(t, ft.calls, "Find tvdb 5 true", "Year 77 true", "SeasonEpisodes 77 1")
				wantCalls(t, matchReads(fp.calls), "ItemPaths 201",
					"SearchMatches 201 tvdb-5 tv.plex.agents.series en-US",
					"SearchMatches 201 tmdb-77 tv.plex.agents.series en-US")
			}
			if tt.want[0] == "Match 201 plex://show/right" {
				// The match, then the refresh that follows it.
				sent := f.logLines("info", "Sent request")
				if len(sent) != 2 {
					t.Fatalf("sent %d requests, want 2", len(sent))
				}
				wantField(t, sent[0], "episode_count", tt.wantCount)
			}
			if tt.wantDiff.Season > 0 {
				e := f.decision("201")
				if !reflect.DeepEqual(e.EpisodeDiff, tt.wantDiff) {
					t.Errorf("episode diff = %+v, want %+v", e.EpisodeDiff, tt.wantDiff)
				}
			}
		})
	}
}

type cancelSeasonLookup struct {
	*fakeTMDB
	cancel context.CancelFunc
}

func (f cancelSeasonLookup) SeasonEpisodes(ctx context.Context, _ string, _ int) ([]int, error) {
	f.cancel()
	return nil, ctx.Err()
}

func TestEpisodeEvidenceLookupFailures(t *testing.T) {
	tests := []struct {
		name         string
		plexErr      error
		tmdbErr      error
		cancel       bool
		wantSend     []string
		wantWarning  string
		wantAttempt  bool
		wantDecision store.DecisionOutcome // "": no decision
	}{
		{name: "Plex read error skips request", plexErr: errServer, wantWarning: "Match search failed", wantAttempt: true, wantDecision: store.DecisionFailed},
		{name: "TMDB read error after a matching season falls back to refresh", tmdbErr: errors.New("season request failed"), wantSend: []string{"Refresh 201"}, wantWarning: "TMDB lookup failed", wantAttempt: true, wantDecision: store.DecisionFailed},
		{name: "cancel during season lookup skips request and attempt", cancel: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePlex(showsLib)
			fp.paths["201"] = []string{"/tv/Show (2003) {tmdb-77}"}
			fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/right", Year: 2011}}
			fp.episodeNumbers = map[string]map[int][]int{"201": {1: {1, 2, 3}, 2: {1}}}
			fp.episodeReadErr = tc.plexErr
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			ft := &fakeTMDB{
				years:      map[string]int{"77 true": 2011},
				seasons:    map[string][]int{"77 1": {1, 2, 3}, "77 2": {1}},
				seasonErrs: map[string]error{"77 2": tc.tmdbErr},
			}
			f.r.opts.TMDB = ft
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				f.r.opts.TMDB = cancelSeasonLookup{ft, cancel}
			}
			item := unmatchedItem("201")
			item.Year = 2003
			f.seed(testServer, showsLib, item, store.Unmatched, 1)

			f.now = t0
			if err := f.r.Run(ctx); err != nil {
				t.Fatal(err)
			}

			wantCalls(t, sends(fp.calls), tc.wantSend...)
			if tc.wantWarning != "" && len(f.logLines("warn", tc.wantWarning)) != 1 {
				t.Errorf("expected one %q warning", tc.wantWarning)
			}
			if tc.cancel && len(f.logLines("warn", "TMDB lookup failed")) != 0 {
				t.Error("cancellation logged as a TMDB failure")
			}
			if row, ok := f.row(testServer, showsLib.ID, "201"); !ok || row.LastAttempt.Equal(t0) != tc.wantAttempt {
				t.Errorf("last attempt = %v, want attempted now: %v", row.LastAttempt, tc.wantAttempt)
			}
			if tc.plexErr != nil && slices.Contains(ft.calls, "SeasonEpisodes 77 1") {
				t.Error("TMDB season queried without Plex episode evidence")
			}
			decisions := f.decisions("201")
			switch {
			case tc.wantDecision == "" && len(decisions) != 0:
				t.Errorf("decisions = %+v, want none", decisions)
			case tc.wantDecision != "" && (len(decisions) != 1 || store.DecisionOutcome(decisions[0].Outcome) != tc.wantDecision):
				t.Errorf("decisions = %+v, want one %q decision", decisions, tc.wantDecision)
			}
		})
	}
}

func TestMovieYearConflictSkipsEpisodeLookup(t *testing.T) {
	fp := newFakePlex(moviesLib)
	movie := unmatchedItem("101")
	movie.Year = 2003
	movie.Files = []string{"/movies/Film (2003) {tmdb-77}/Film (2003).mkv"}
	fp.lists["1?type=1&unmatched=1"] = []plex.Item{movie}
	fp.candidates["101 tmdb-77"] = []plex.Candidate{{GUID: "plex://movie/a", Year: 2011}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	ft := &fakeTMDB{years: map[string]int{"77 false": 2011}}
	f.r.opts.TMDB = ft
	f.seed(testServer, moviesLib, movie, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 101")
	wantCalls(t, ft.calls, "Year 77 false")
	if slices.Contains(fp.calls, "EpisodeNumbers 101") {
		t.Error("read Plex episodes for a movie")
	}
}

func TestEpisodeGroupsFallback(t *testing.T) {
	tests := []struct {
		name           string
		local          map[int][]int
		defaultSeasons map[string][]int
		groups         []tmdb.EpisodeGroup
		groupSeasons   map[string]map[int][]int
		groupListErr   error
		groupDetailErr map[string]error
		wantSend       []string
		wantCount      float64
		wantDiff       store.EpisodeDiff
		wantGroupCalls []string
		wantDecision   store.DecisionOutcome
	}{
		{
			name:           "confirms via original air date group when default seasons differ",
			local:          map[int][]int{1: seq(49)},
			defaultSeasons: map[string][]int{"77 1": seq(25)},
			groups: []tmdb.EpisodeGroup{
				{ID: "aaaa", Name: "Aired Order", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 49},
			},
			groupSeasons:   map[string]map[int][]int{"aaaa": {1: seq(49)}},
			wantSend:       []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount:      49,
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons aaaa"},
			wantDecision:   store.DecisionMatched,
		},
		{
			name:           "tries TVDB-aligned types in priority order and stops at first match",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "tv01", Name: "TV Order", Type: tmdb.EpisodeGroupTV, EpisodeCount: 14},
				{ID: "abs1", Name: "Absolute", Type: tmdb.EpisodeGroupAbsolute, EpisodeCount: 14},
				{ID: "dvd1", Name: "DVD Order", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 14},
				{ID: "air1", Name: "Aired Order", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 14},
			},
			groupSeasons: map[string]map[int][]int{
				"air1": {1: seq(12)},
				"dvd1": {1: seq(14)},
				"abs1": {1: seq(14)},
				"tv01": {1: seq(14)},
			},
			wantSend:       []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount:      14,
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons air1", "EpisodeGroupSeasons dvd1"},
			wantDecision:   store.DecisionMatched,
		},
		{
			name:           "ignores non-TVDB group types and undersized groups",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "dig1", Name: "Netflix", Type: tmdb.EpisodeGroupDigital, EpisodeCount: 14},
				{ID: "arc1", Name: "Story Arc", Type: tmdb.EpisodeGroupStoryArc, EpisodeCount: 14},
				{ID: "prd1", Name: "Production", Type: tmdb.EpisodeGroupProduction, EpisodeCount: 14},
				{ID: "sml1", Name: "Short Aired", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 10},
				{ID: "emp1", Name: "Empty DVD", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 0},
			},
			groupSeasons: map[string]map[int][]int{
				"dig1": {1: seq(14)},
				"arc1": {1: seq(14)},
				"prd1": {1: seq(14)},
			},
			wantSend:       []string{"Refresh 201"},
			wantDiff:       store.EpisodeDiff{Season: 1, Plex: 14, TMDB: 11, Extra: []int{12, 13, 14}},
			wantGroupCalls: []string{"EpisodeGroups 77"},
			wantDecision:   store.DecisionNoMatch,
		},
		{
			name:           "keeps default season diff when no group confirms",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "dvd1", Name: "DVD Order", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 20},
			},
			groupSeasons:   map[string]map[int][]int{"dvd1": {1: seq(20)}},
			wantSend:       []string{"Refresh 201"},
			wantDiff:       store.EpisodeDiff{Season: 1, Plex: 14, TMDB: 11, Extra: []int{12, 13, 14}},
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons dvd1"},
			wantDecision:   store.DecisionNoMatch,
		},
		{
			name:           "skips episode groups when local numbers have duplicates",
			local:          map[int][]int{1: {1, 1, 2}},
			defaultSeasons: map[string][]int{"77 1": {1, 2, 3}},
			groups: []tmdb.EpisodeGroup{
				{ID: "air1", Name: "Aired Order", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 3},
			},
			wantSend:     []string{"Refresh 201"},
			wantDecision: store.DecisionNoMatch,
		},
		{
			name:           "TMDB error listing episode groups falls back to refresh",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groupListErr:   errors.New("500 internal error"),
			wantSend:       []string{"Refresh 201"},
			wantGroupCalls: []string{"EpisodeGroups 77"},
			wantDecision:   store.DecisionFailed,
		},
		{
			name:           "TMDB error reading group details falls back to refresh",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "air1", Name: "Aired Order", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 14},
			},
			groupDetailErr: map[string]error{"air1": errors.New("500 internal error")},
			wantSend:       []string{"Refresh 201"},
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons air1"},
			wantDecision:   store.DecisionFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := newFakePlex(showsLib)
			fp.paths["201"] = []string{"/tv/Show (2003) {tmdb-77}"}
			fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/right", Name: "Show", Year: 2011}}
			fp.episodeNumbers = map[string]map[int][]int{"201": tt.local}
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			ft := &fakeTMDB{
				years:           map[string]int{"77 true": 2011},
				seasons:         tt.defaultSeasons,
				episodeGroups:   map[string][]tmdb.EpisodeGroup{"77": tt.groups},
				groupListErrs:   map[string]error{"77": tt.groupListErr},
				groupSeasons:    tt.groupSeasons,
				groupSeasonErrs: tt.groupDetailErr,
			}
			f.r.opts.TMDB = ft
			item := unmatchedItem("201")
			item.Year = 2003
			f.seed(testServer, showsLib, item, store.Unmatched, 1)

			f.run(t0)

			wantCalls(t, sends(fp.calls), tt.wantSend...)
			var gotGroupCalls []string
			for _, c := range ft.calls {
				if len(c) >= 12 && c[:12] == "EpisodeGroup" {
					gotGroupCalls = append(gotGroupCalls, c)
				}
			}
			wantCalls(t, gotGroupCalls, tt.wantGroupCalls...)
			e := f.decision("201")
			if store.DecisionOutcome(e.Outcome) != tt.wantDecision {
				t.Errorf("decision outcome = %q, want %q", e.Outcome, tt.wantDecision)
			}
			if tt.wantCount > 0 {
				sent := f.logLines("info", "Sent request")
				if len(sent) != 2 {
					t.Fatalf("sent %d requests, want 2", len(sent))
				}
				wantField(t, sent[0], "episode_count", tt.wantCount)
			}
			if tt.wantDiff.Season > 0 && !reflect.DeepEqual(e.EpisodeDiff, tt.wantDiff) {
				t.Errorf("episode diff = %+v, want %+v", e.EpisodeDiff, tt.wantDiff)
			}
		})
	}
}

func TestEpisodeGroupsCappedAtMax(t *testing.T) {
	groups := make([]tmdb.EpisodeGroup, 0, maxEpisodeGroups+3)
	groupSeasons := make(map[string]map[int][]int, maxEpisodeGroups+3)
	for i := range maxEpisodeGroups + 3 {
		id := fmt.Sprintf("aa%02d", i)
		groups = append(groups, tmdb.EpisodeGroup{
			ID: id, Name: "Aired", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 20,
		})
		groupSeasons[id] = map[int][]int{1: seq(20)}
	}
	fp := newFakePlex(showsLib)
	fp.paths["201"] = []string{"/tv/Show (2003) {tmdb-77}"}
	fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/right", Name: "Show", Year: 2011}}
	fp.episodeNumbers = map[string]map[int][]int{"201": {1: seq(14)}}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	ft := &fakeTMDB{
		years:         map[string]int{"77 true": 2011},
		seasons:       map[string][]int{"77 1": seq(11)},
		episodeGroups: map[string][]tmdb.EpisodeGroup{"77": groups},
		groupSeasons:  groupSeasons,
	}
	f.r.opts.TMDB = ft
	item := unmatchedItem("201")
	item.Year = 2003
	f.seed(testServer, showsLib, item, store.Unmatched, 1)

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201")
	detailCalls := 0
	for _, c := range ft.calls {
		if len(c) >= 19 && c[:19] == "EpisodeGroupSeasons" {
			detailCalls++
		}
	}
	if detailCalls != maxEpisodeGroups {
		t.Fatalf("detail calls = %d, want cap %d", detailCalls, maxEpisodeGroups)
	}
}
