package refresher

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

func TestEpisodeNumbersConfirmSeriesYearConflict(t *testing.T) {
	tests := []struct {
		name     string
		local    map[int][]int
		catalog  map[string][]int
		want     []string
		directID bool
	}{
		{
			name:    "complete local season matches despite a bad year and absent later season",
			local:   map[int][]int{1: {3, 1, 2}},
			catalog: map[string][]int{"77 1": {1, 2, 3}, "77 2": {1, 2}},
			want:    []string{"Match 201 plex://show/right", "Refresh 201"},
		},
		{
			name:     "direct and translated IDs agree",
			local:    map[int][]int{1: {1, 2, 3}},
			catalog:  map[string][]int{"77 1": {1, 2, 3}},
			want:     []string{"Match 201 plex://show/right", "Refresh 201"},
			directID: true,
		},
		{
			name:    "same count with different numbering",
			local:   map[int][]int{1: {1, 2, 4}},
			catalog: map[string][]int{"77 1": {1, 2, 3}},
			want:    []string{"Refresh 201"},
		},
		{
			name:    "a second local season is incomplete",
			local:   map[int][]int{1: {1, 2, 3}, 2: {1}},
			catalog: map[string][]int{"77 1": {1, 2, 3}, "77 2": {1, 2}},
			want:    []string{"Refresh 201"},
		},
		{
			name:    "duplicate numbers cannot confirm a season",
			local:   map[int][]int{1: {1, 1, 3}},
			catalog: map[string][]int{"77 1": {1, 1, 3}},
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
				wantField(t, sent[0], "episode_count", float64(3))
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
