package plex

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestRefreshSendsPutToItem(t *testing.T) {
	rec, c := newServer(t, func(http.ResponseWriter, *http.Request) {})

	if err := c.Refresh(context.Background(), "1001"); err != nil {
		t.Fatal(err)
	}

	r := rec.all()[0]
	if r.Method != http.MethodPut || r.URL.Path != "/library/metadata/1001/refresh" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
}

func TestSearchMatchesDecodesCandidates(t *testing.T) {
	tests := []struct {
		fixture string
		want    []Candidate
	}{
		{"matches_one.json", []Candidate{{GUID: "plex://movie/000000000000000000000001", Name: "Fixture Movie One", Year: 2020, Type: "movie", Summary: "A neutral summary.", Thumb: "https://metadata.example.invalid/thumb.jpg", Score: 98},
			{GUID: "plex://movie/000000000000000000000002", Name: "Fixture Movie Two", Year: 2021, Type: "movie", Summary: "Another neutral summary.", Thumb: "", Score: 80}}},
		{"matches_none.json", nil},
		// The fields shown only on the dashboard are optional. A value of an
		// unexpected type leaves that field empty and keeps the candidate.
		{"matches_odd_fields.json", []Candidate{{GUID: "plex://movie/000000000000000000000003", Name: "Fixture Movie Three", Year: 2022, Type: "movie", Thumb: "https://metadata.example.invalid/three.jpg"}}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			rec, c := newServer(t, serveFixture(t, tt.fixture))

			got, err := c.SearchMatches(context.Background(), "1001", "Fixture Movie One", "tv.plex.agents.movie", "en-GB")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) || (len(got) > 0 && !reflect.DeepEqual(got, tt.want)) {
				t.Errorf("candidates = %+v, want %+v", got, tt.want)
			}

			r := rec.all()[0]
			if r.Method != http.MethodGet || r.URL.Path != "/library/metadata/1001/matches" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			want := map[string]string{
				"manual": "1", "title": "Fixture Movie One", "agent": "tv.plex.agents.movie", "language": "en-GB",
			}
			for k, v := range want {
				if got := r.URL.Query().Get(k); got != v {
					t.Errorf("query %s = %q, want %q", k, got, v)
				}
			}
		})
	}
}

func TestMatchSendsGuidNameYear(t *testing.T) {
	rec, c := newServer(t, func(http.ResponseWriter, *http.Request) {})
	ctx := context.Background()

	if err := c.Match(ctx, "1001", Candidate{GUID: "plex://movie/1", Name: "Name", Year: 2020}); err != nil {
		t.Fatal(err)
	}
	if err := c.Match(ctx, "1001", Candidate{GUID: "plex://movie/1", Name: "Name"}); err != nil {
		t.Fatal(err)
	}

	reqs := rec.all()
	for _, r := range reqs {
		if r.Method != http.MethodPut || r.URL.Path != "/library/metadata/1001/match" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
	}
	if got, want := reqs[0].URL.RawQuery, "guid=plex%3A%2F%2Fmovie%2F1&name=Name&year=2020"; got != want {
		t.Errorf("query = %s, want %s", got, want)
	}
	if reqs[1].URL.Query().Has("year") {
		t.Errorf("query %s has year", reqs[1].URL.RawQuery)
	}
}
