package refresher

import (
	"testing"

	"github.com/l3uddz/autorefresh/internal/plex"
)

func TestChooseMatch(t *testing.T) {
	a := plex.Candidate{GUID: "plex://movie/a", Name: "A", Year: 2020}
	a2 := plex.Candidate{GUID: "plex://movie/a2", Name: "A2", Year: 2020}
	res := func(cands ...plex.Candidate) searchResult {
		return searchResult{id: pathID{"tmdb", "1"}, candidates: cands}
	}
	withYear := func(year int) plex.Candidate {
		c := a
		c.Year = year
		return c
	}

	tests := []struct {
		name         string
		itemYear     int
		results      []searchResult
		wantOK       bool
		wantConflict bool
	}{
		{"one id one candidate", 2020, []searchResult{res(a)}, true, false},
		{"two ids same guid", 2020, []searchResult{res(a), res(a)}, true, false},
		{"empty result beside a candidate", 2020, []searchResult{res(), res(a)}, true, false},
		{"no ids", 2020, nil, false, false},
		{"only empty results", 2020, []searchResult{res(), res()}, false, false},
		{"one id two candidates", 2020, []searchResult{res(a, a2)}, false, true},
		{"two ids different guids", 2020, []searchResult{res(a), res(a2)}, false, true},
		{"year differs by two", 2020, []searchResult{res(withYear(2022))}, false, true},
		{"year differs by one", 2020, []searchResult{res(withYear(2021))}, true, false},
		{"item year two newer", 2022, []searchResult{res(withYear(2020))}, false, true},
		{"item year one newer", 2021, []searchResult{res(withYear(2020))}, true, false},
		{"empty guids", 2020, []searchResult{res(plex.Candidate{}), res(plex.Candidate{})}, false, true},
		{"item year zero", 0, []searchResult{res(withYear(2022))}, true, false},
		{"candidate year zero", 2020, []searchResult{res(withYear(0))}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cand, ok, conflict := chooseMatch(tt.itemYear, tt.results)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if (conflict != "") != tt.wantConflict {
				t.Errorf("conflict = %q, want conflict %v", conflict, tt.wantConflict)
			}
			if ok && cand.GUID == "" {
				t.Error("ok without a candidate")
			}
			if !ok && cand != (plex.Candidate{}) {
				t.Errorf("candidate %v returned without ok", cand)
			}
		})
	}
}
