package refresher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

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
			name:     "two local seasons both missing episodes below threshold report first season",
			local:    map[int][]int{1: {1, 2}, 2: {1, 2}},
			catalog:  map[string][]int{"77 1": {1, 2, 3}, "77 2": {1, 2, 3}},
			want:     []string{"Refresh 201"},
			wantDiff: store.EpisodeDiff{Season: 1, Plex: 2, TMDB: 3, Missing: []int{3}},
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
		wantGroup      string
		wantDiff       store.EpisodeDiff
		wantGroupCalls []string
		wantDecision   store.DecisionOutcome
	}{
		{
			name:           "default seasons confirm without querying episode groups",
			local:          map[int][]int{1: seq(10)},
			defaultSeasons: map[string][]int{"77 1": seq(10)},
			groups: []tmdb.EpisodeGroup{
				{ID: "aaaa", Name: "Aired Order", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 10},
			},
			wantSend:     []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount:    10,
			wantDecision: store.DecisionMatched,
		},
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
			wantGroup:      "Aired Order",
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons aaaa"},
			wantDecision:   store.DecisionMatched,
		},
		{
			name:           "confirms via episode group when default layout lacks a local season",
			local:          map[int][]int{1: seq(10), 2: seq(10)},
			defaultSeasons: map[string][]int{"77 1": seq(10)},
			groups: []tmdb.EpisodeGroup{
				{ID: "air2", Name: "Aired Order", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 20},
			},
			groupSeasons:   map[string]map[int][]int{"air2": {1: seq(10), 2: seq(10)}},
			wantSend:       []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount:      20,
			wantGroup:      "Aired Order",
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons air2"},
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
			wantGroup:      "DVD Order",
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons air1", "EpisodeGroupSeasons dvd1"},
			wantDecision:   store.DecisionMatched,
		},
		{
			name:           "prioritizes plausible episode counts before unknown summaries and deduplicates failing IDs",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "air0", Name: "Stale Count Aired", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 0},
				{ID: "dvd1", Name: "DVD Order", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 14},
				{ID: "dvd1", Name: "Duplicate DVD", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 14},
			},
			groupSeasons: map[string]map[int][]int{
				"dvd1": {1: seq(12)},
				"air0": {1: seq(14)},
			},
			wantSend:       []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount:      14,
			wantGroup:      "Stale Count Aired",
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons dvd1", "EpisodeGroupSeasons air0"},
			wantDecision:   store.DecisionMatched,
		},
		{
			name:           "ignores non-TVDB group types",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "dig1", Name: "Netflix", Type: tmdb.EpisodeGroupDigital, EpisodeCount: 14},
				{ID: "arc1", Name: "Story Arc", Type: tmdb.EpisodeGroupStoryArc, EpisodeCount: 14},
				{ID: "prd1", Name: "Production", Type: tmdb.EpisodeGroupProduction, EpisodeCount: 14},
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
			name:           "isolated error on first group continues to valid second group",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "air1", Name: "Broken Aired", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 14},
				{ID: "dvd1", Name: "DVD Order", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 14},
			},
			groupDetailErr: map[string]error{"air1": errors.New("500 internal error")},
			groupSeasons:   map[string]map[int][]int{"dvd1": {1: seq(14)}},
			wantSend:       []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount:      14,
			wantGroup:      "DVD Order",
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons air1", "EpisodeGroupSeasons dvd1"},
			wantDecision:   store.DecisionMatched,
		},
		{
			name:           "isolated error listing episode groups preserves default diff",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groupListErr:   errors.New("500 internal error"),
			wantSend:       []string{"Refresh 201"},
			wantDiff:       store.EpisodeDiff{Season: 1, Plex: 14, TMDB: 11, Extra: []int{12, 13, 14}},
			wantGroupCalls: []string{"EpisodeGroups 77"},
			wantDecision:   store.DecisionNoMatch,
		},
		{
			name:           "falls back to group ID when TMDB omits episode group name",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "5e9077d2e640d600151f32bd", Name: "   ", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 14},
			},
			groupSeasons:   map[string]map[int][]int{"5e9077d2e640d600151f32bd": {1: seq(14)}},
			wantSend:       []string{"Match 201 plex://show/right", "Refresh 201"},
			wantCount:      14,
			wantGroup:      "5e9077d2e640d600151f32bd",
			wantGroupCalls: []string{"EpisodeGroups 77", "EpisodeGroupSeasons 5e9077d2e640d600151f32bd"},
			wantDecision:   store.DecisionMatched,
		},
		{
			name:           "rate limit listing episode groups fails TMDB lookup and keeps default diff",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groupListErr:   fmt.Errorf("429: %w", tmdb.ErrRateLimited),
			wantSend:       []string{"Refresh 201"},
			wantDiff:       store.EpisodeDiff{Season: 1, Plex: 14, TMDB: 11, Extra: []int{12, 13, 14}},
			wantGroupCalls: []string{"EpisodeGroups 77"},
			wantDecision:   store.DecisionFailed,
		},
		{
			name:           "unauthorized error reading group details stops immediately and keeps default diff",
			local:          map[int][]int{1: seq(14)},
			defaultSeasons: map[string][]int{"77 1": seq(11)},
			groups: []tmdb.EpisodeGroup{
				{ID: "air1", Name: "Aired Order", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 14},
				{ID: "dvd1", Name: "DVD Order", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 14},
			},
			groupDetailErr: map[string]error{"air1": fmt.Errorf("401: %w", tmdb.ErrUnauthorized)},
			groupSeasons:   map[string]map[int][]int{"dvd1": {1: seq(14)}},
			wantSend:       []string{"Refresh 201"},
			wantDiff:       store.EpisodeDiff{Season: 1, Plex: 14, TMDB: 11, Extra: []int{12, 13, 14}},
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
			if e.EpisodeGroup != tt.wantGroup {
				t.Errorf("episode group = %q, want %q", e.EpisodeGroup, tt.wantGroup)
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

func TestEpisodeGroupIsolatedFailureDoesNotDisableTMDBForLaterItems(t *testing.T) {
	fp := newFakePlex(showsLib)
	fp.paths["201"] = []string{"/tv/First (2003) {tmdb-77}"}
	fp.paths["202"] = []string{"/tv/Second (2003) {tmdb-88}"}
	fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/first", Name: "First", Year: 2011}}
	fp.candidates["202 tmdb-88"] = []plex.Candidate{{GUID: "plex://show/second", Name: "Second", Year: 2011}}
	fp.episodeNumbers = map[string]map[int][]int{
		"201": {1: seq(14)},
		"202": {1: seq(10)},
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	ft := &fakeTMDB{
		years:   map[string]int{"77 true": 2011, "88 true": 2011},
		seasons: map[string][]int{"77 1": seq(11), "88 1": seq(10)},
		episodeGroups: map[string][]tmdb.EpisodeGroup{
			"77": {{ID: "air1", Name: "Broken Aired", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 14}},
		},
		groupSeasonErrs: map[string]error{"air1": errors.New("500 internal error")},
	}
	f.r.opts.TMDB = ft
	for _, id := range []string{"201", "202"} {
		it := unmatchedItem(id)
		it.Year = 2003
		f.seed(testServer, showsLib, it, store.Unmatched, 1)
	}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201", "Match 202 plex://show/second", "Refresh 202")
	failIdx := slices.Index(ft.calls, "EpisodeGroupSeasons air1")
	nextIdx := slices.Index(ft.calls, "Year 88 true")
	if failIdx < 0 || nextIdx < 0 || failIdx > nextIdx {
		t.Fatalf("calls = %v, want EpisodeGroupSeasons air1 before Year 88 true", ft.calls)
	}
	if diff := f.decision("201").EpisodeDiff; diff.Season != 1 || diff.Plex != 14 || diff.TMDB != 11 {
		t.Errorf("first item diff = %+v, want season 1 (14 vs 11)", diff)
	}
}

func TestEpisodeGroupsCappedAndBalancedAcrossTypes(t *testing.T) {
	groups := make([]tmdb.EpisodeGroup, 0, maxEpisodeGroups+4)
	groupSeasons := make(map[string]map[int][]int, maxEpisodeGroups+4)
	for i := range maxEpisodeGroups + 3 {
		id := fmt.Sprintf("aa%02d", i)
		groups = append(groups, tmdb.EpisodeGroup{
			ID: id, Name: "Aired", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 20,
		})
		groupSeasons[id] = map[int][]int{1: seq(20)}
	}
	// A DVD group with an unknown (0) summary count listed after >10 plausible
	// Aired groups must still survive cross-type balancing under the cap.
	groups = append(groups, tmdb.EpisodeGroup{
		ID: "dd01", Name: "DVD Order", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 0,
	})
	groupSeasons["dd01"] = map[int][]int{1: seq(14)}

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

	wantCalls(t, sends(fp.calls), "Match 201 plex://show/right", "Refresh 201")
	detailCalls := 0
	for _, c := range ft.calls {
		if len(c) >= 19 && c[:19] == "EpisodeGroupSeasons" {
			detailCalls++
		}
	}
	if detailCalls != maxEpisodeGroups {
		t.Fatalf("detail calls = %d, want cap %d", detailCalls, maxEpisodeGroups)
	}
	if e := f.decision("201"); e.EpisodeGroup != "DVD Order" {
		t.Errorf("episode group = %q, want DVD Order", e.EpisodeGroup)
	}
}

type cancelGroupLookup struct {
	*fakeTMDB
	stage  string
	cancel context.CancelFunc
}

func (c cancelGroupLookup) EpisodeGroups(ctx context.Context, id string) ([]tmdb.EpisodeGroup, error) {
	if c.stage == "list" {
		c.cancel()
		return nil, ctx.Err()
	}
	return c.fakeTMDB.EpisodeGroups(ctx, id)
}

func (c cancelGroupLookup) EpisodeGroupSeasons(ctx context.Context, groupID string) (map[int][]int, error) {
	if c.stage == "detail" {
		c.cancel()
		return nil, ctx.Err()
	}
	return c.fakeTMDB.EpisodeGroupSeasons(ctx, groupID)
}

func TestEpisodeGroupCancellationStopsWithoutTMDBFailure(t *testing.T) {
	for _, stage := range []string{"list", "detail"} {
		t.Run(stage, func(t *testing.T) {
			fp := newFakePlex(showsLib)
			fp.paths["201"] = []string{"/tv/Show (2003) {tmdb-77}"}
			fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/right", Year: 2011}}
			fp.episodeNumbers = map[string]map[int][]int{"201": {1: seq(14)}}
			f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
			ft := &fakeTMDB{
				years:   map[string]int{"77 true": 2011},
				seasons: map[string][]int{"77 1": seq(11)},
				episodeGroups: map[string][]tmdb.EpisodeGroup{
					"77": {{ID: "air1", Name: "Aired Order", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 14}},
				},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.r.opts.TMDB = cancelGroupLookup{fakeTMDB: ft, stage: stage, cancel: cancel}
			item := unmatchedItem("201")
			item.Year = 2003
			f.seed(testServer, showsLib, item, store.Unmatched, 1)

			f.now = t0
			if err := f.r.Run(ctx); err != nil {
				t.Fatal(err)
			}

			wantCalls(t, sends(fp.calls))
			if len(f.logLines("warn", "TMDB lookup failed")) != 0 {
				t.Error("cancellation logged as TMDB failure")
			}
			if len(f.decisions("201")) != 0 {
				t.Errorf("decisions = %+v, want none on cancellation", f.decisions("201"))
			}
		})
	}
}

// End-to-end parser-to-comparison test using a real *tmdb.Client to verify
// omitted/null/negative orders never confirm S01E01 while explicit order 0 does.
func TestEpisodeGroupParserToComparisonOrderPresence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		groupBody string
		wantMatch bool
	}{
		{"missing episode order cannot confirm S01E01", `{"groups":[{"order":1,"episodes":[{}]}]}`, false},
		{"null episode order cannot confirm S01E01", `{"groups":[{"order":1,"episodes":[{"order":null}]}]}`, false},
		{"negative subgroup order cannot confirm S01E01", `{"groups":[{"order":-1,"episodes":[{"order":0}]},{"order":1,"episodes":[{"order":0}]}]}`, false},
		{"explicit order 0 confirms S01E01", `{"groups":[{"order":1,"episodes":[{"order":0}]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/3/tv/77/season/1":
					w.Write([]byte(`{"episodes":[{"episode_number":1},{"episode_number":2}]}`))
				case "/3/tv/77/episode_groups":
					w.Write([]byte(`{"results":[{"id":"5e9077d2e640d600151f32bd","name":"Aired Order","type":1,"episode_count":1}]}`))
				case "/3/tv/episode_group/5e9077d2e640d600151f32bd":
					w.Write([]byte(tc.groupBody))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			fp := newFakePlex(showsLib)
			fp.episodeNumbers = map[string]map[int][]int{"1": {1: {1}}}
			f := newRefreshFixture(t, Server{Name: "home", Plex: fp})
			f.seed("home", showsLib, incompleteItem("1"), store.NoSummary, 0)
			base, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			f.r.opts.TMDB = tmdb.NewWithBaseURL(base, "test-token", 5*time.Second)

			got, err := f.r.EpisodeCheck(t.Context(), "home", "1", "77")
			if err != nil {
				t.Fatal(err)
			}
			if got.Match != tc.wantMatch {
				t.Fatalf("EpisodeCheck = %+v, want Match=%v", got, tc.wantMatch)
			}
		})
	}
}

type slowGroupLookup struct {
	*fakeTMDB
	slowID   string
	onSlowID func()
}

func (s slowGroupLookup) EpisodeGroupSeasons(ctx context.Context, groupID string) (map[int][]int, error) {
	s.calls = append(s.calls, "EpisodeGroupSeasons "+groupID)
	if groupID == s.slowID {
		s.onSlowID()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, errors.New("expected fallback budget context to expire")
		}
	}
	return s.groupSeasons[groupID], nil
}

func TestEpisodeGroupFallbackBudgetExpiresCleanly(t *testing.T) {
	var expireBudget context.CancelFunc
	old := withEpisodeGroupBudget
	withEpisodeGroupBudget = func(ctx context.Context) (context.Context, context.CancelFunc) {
		gctx, cancel := context.WithCancel(ctx)
		expireBudget = cancel
		return gctx, cancel
	}
	t.Cleanup(func() { withEpisodeGroupBudget = old })

	fp := newFakePlex(showsLib)
	fp.paths["201"] = []string{"/tv/First (2003) {tmdb-77}"}
	fp.paths["202"] = []string{"/tv/Second (2003) {tmdb-88}"}
	fp.candidates["201 tmdb-77"] = []plex.Candidate{{GUID: "plex://show/first", Name: "First", Year: 2011}}
	fp.candidates["202 tmdb-88"] = []plex.Candidate{{GUID: "plex://show/second", Name: "Second", Year: 2011}}
	fp.episodeNumbers = map[string]map[int][]int{
		"201": {1: seq(14)},
		"202": {1: seq(10)},
	}
	f := newRefreshFixture(t, Server{Name: testServer, Plex: fp})
	ft := &fakeTMDB{
		years:   map[string]int{"77 true": 2011, "88 true": 2011},
		seasons: map[string][]int{"77 1": seq(11), "88 1": seq(10)},
		episodeGroups: map[string][]tmdb.EpisodeGroup{
			"77": {
				{ID: "air1", Name: "Slow Aired", Type: tmdb.EpisodeGroupOriginalAirDate, EpisodeCount: 14},
				{ID: "dvd1", Name: "Later DVD", Type: tmdb.EpisodeGroupDVD, EpisodeCount: 14},
			},
		},
		groupSeasons: map[string]map[int][]int{"dvd1": {1: seq(14)}},
	}
	f.r.opts.TMDB = slowGroupLookup{
		fakeTMDB: ft,
		slowID:   "air1",
		onSlowID: func() {
			if expireBudget == nil {
				t.Fatal("withEpisodeGroupBudget was not called before EpisodeGroupSeasons")
			}
			expireBudget()
		},
	}
	for _, id := range []string{"201", "202"} {
		it := unmatchedItem(id)
		it.Year = 2003
		f.seed(testServer, showsLib, it, store.Unmatched, 1)
	}

	f.run(t0)

	wantCalls(t, sends(fp.calls), "Refresh 201", "Match 202 plex://show/second", "Refresh 202")
	if !slices.Contains(ft.calls, "EpisodeGroupSeasons air1") {
		t.Fatalf("calls = %v, expected slow lookup air1 to start before budget expired", ft.calls)
	}
	if slices.Contains(ft.calls, "EpisodeGroupSeasons dvd1") {
		t.Errorf("calls = %v, should stop after fallback budget expired", ft.calls)
	}
	if diff := f.decision("201").EpisodeDiff; diff.Season != 1 || diff.Plex != 14 || diff.TMDB != 11 {
		t.Errorf("first item diff = %+v, want default season diff (14 vs 11)", diff)
	}
	if len(f.logLines("warn", "TMDB lookup failed")) != 0 {
		t.Error("fallback budget expiry should not log a run-wide TMDB failure")
	}
}
