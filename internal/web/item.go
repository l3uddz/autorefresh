package web

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// defaultLiveTimeout bounds the Plex request of GET /api/item/live.
const defaultLiveTimeout = 5 * time.Second

type itemDetailJSON struct {
	Item     itemJSON    `json:"item"`
	Timeline []eventJSON `json:"timeline"`
}

// eventJSON is one event of the timeline of an item.
type eventJSON struct {
	ID                int64        `json:"id"`
	At                time.Time    `json:"at,omitzero"`
	Kind              string       `json:"kind"`
	Source            string       `json:"source,omitempty"`
	Action            store.Action `json:"action,omitempty"`
	Accepted          bool         `json:"accepted"`
	Outcome           string       `json:"outcome,omitempty"`
	Error             string       `json:"error,omitempty"`
	Match             *matchJSON   `json:"match,omitempty"`
	Conflict          string       `json:"conflict,omitempty"`
	PathYear          int          `json:"pathYear,omitempty"`
	TMDBYear          int          `json:"tmdbYear,omitempty"`
	EpisodeCount      int          `json:"episodeCount,omitempty"`
	Links             []string     `json:"links,omitempty"`
	FixedAction       store.Action `json:"fixedAction,omitempty"`
	FixedMatch        store.Action `json:"fixedMatch,omitempty"`
	FixedMatchEventID int64        `json:"fixedMatchEventId,omitempty"`
	Searches          []searchJSON `json:"searches"`
	// The evidence of a match decision beyond its searches.
	ItemYear    int              `json:"itemYear,omitempty"`
	TMDBID      string           `json:"tmdbId,omitempty"`
	TMDBSkip    string           `json:"tmdbSkip,omitempty"`
	TMDBMissing []string         `json:"tmdbMissing,omitempty"`
	EpisodeDiff *episodeDiffJSON `json:"episodeDiff,omitempty"`
}

// episodeDiffJSON is the first season whose regular episode numbers differ
// from TMDB, with the episode count of Plex and of TMDB, and the episode
// numbers of the season that only TMDB, or only Plex, has.
type episodeDiffJSON struct {
	Season  int   `json:"season"`
	Plex    int   `json:"plex"`
	TMDB    int   `json:"tmdb"`
	Missing []int `json:"missing,omitempty"`
	Extra   []int `json:"extra,omitempty"`
}

// toEpisodeDiffJSON returns d, or nil when no season differs.
func toEpisodeDiffJSON(d store.EpisodeDiff) *episodeDiffJSON {
	if d.Season == 0 {
		return nil
	}
	return &episodeDiffJSON{Season: d.Season, Plex: d.Plex, TMDB: d.TMDB}
}

// matchJSON is the candidate of a match request.
type matchJSON struct {
	GUID  string `json:"guid"`
	Title string `json:"title,omitempty"`
	Year  int    `json:"year,omitempty"`
}

type searchJSON struct {
	Query      string          `json:"query"`
	Source     string          `json:"source,omitempty"`
	FromID     string          `json:"fromId,omitempty"`
	Failed     bool            `json:"failed"`
	Candidates []candidateJSON `json:"candidates"`
}

type candidateJSON struct {
	GUID    string `json:"guid"`
	Name    string `json:"name,omitempty"`
	Year    int    `json:"year,omitempty"`
	Type    string `json:"type,omitempty"`
	Summary string `json:"summary,omitempty"`
	Thumb   string `json:"thumb,omitempty"`
	Score   int    `json:"score"`
	Verdict string `json:"verdict,omitempty"`
}

// liveJSON is what Plex tells about an item now, with its cached path.
type liveJSON struct {
	Summary   string   `json:"summary,omitempty"`
	GUID      string   `json:"guid,omitempty"`
	Seasons   *int     `json:"seasons,omitempty"`
	Episodes  *int     `json:"episodes,omitempty"`
	Locations []string `json:"locations"`
	Files     []string `json:"files"`
	Path      string   `json:"path,omitempty"`
}

// itemParams returns the srv and key parameters of the query v. Both are
// required.
func (s *Server) itemParams(v url.Values) (int, string, error) {
	srv, err := s.srvParam(v)
	if err != nil {
		return 0, "", err
	}
	if srv == noSrv {
		return 0, "", errInvalidSrv
	}
	key, err := keyParam(v)
	if err != nil {
		return 0, "", err
	}
	return srv, key, nil
}

// knownItem reads the item that the query v of the request r names: its
// row, or the snapshot of its newest event. It answers r and returns false
// when the parameters are invalid, the item is unknown, or the store fails.
func (s *Server) knownItem(w http.ResponseWriter, r *http.Request, v url.Values) (int, store.ListItem, bool) {
	srv, key, err := s.itemParams(v)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return 0, store.ListItem{}, false
	}
	rules := s.rules
	rules.Now = s.now()
	it, found, err := s.store.ItemDetail(r.Context(), s.servers[srv].Name, key, rules)
	if err != nil {
		writeStoreError(w, r, err)
		return 0, store.ListItem{}, false
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return 0, store.ListItem{}, false
	}
	return srv, it, true
}

// serveItem answers GET /api/item with an item and its timeline, newest
// event first.
func (s *Server) serveItem(w http.ResponseWriter, r *http.Request) {
	v, err := queryParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv, it, ok := s.knownItem(w, r, v)
	if !ok {
		return
	}
	s.writeItemDetail(w, r, srv, it)
}

func (s *Server) writeItemDetail(w http.ResponseWriter, r *http.Request, srv int, it store.ListItem) {
	events, err := s.store.TimelineContext(r.Context(), it.Server, it.RatingKey)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	out := itemDetailJSON{Item: toItemJSON(srv, it), Timeline: make([]eventJSON, 0, len(events))}
	for _, e := range events {
		out.Timeline = append(out.Timeline, s.toEventJSON(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// toEventJSON returns e with its error text redacted.
func (s *Server) toEventJSON(e store.Event) eventJSON {
	out := eventJSON{
		ID: e.ID, At: e.At, Kind: string(e.Kind), Source: string(e.Source), Action: e.Action,
		Accepted: e.Accepted, Outcome: e.Outcome, Error: s.redact(e.Error), Conflict: e.Conflict,
		PathYear: e.PathYear, TMDBYear: e.TMDBYear, EpisodeCount: e.EpisodeCount, Links: e.Links,
		FixedAction: e.FixedAction, FixedMatch: e.FixedMatch, FixedMatchEventID: e.FixedMatchEventID,
		Searches: make([]searchJSON, 0, len(e.Searches)),
	}
	if e.Kind == store.EventMatchDecision {
		out.ItemYear, out.TMDBID, out.TMDBSkip, out.TMDBMissing = e.Year, e.TMDBID, e.TMDBSkip, e.TMDBMissing
		out.EpisodeDiff = toEpisodeDiffJSON(e.EpisodeDiff)
	}
	if e.MatchGUID != "" {
		out.Match = &matchJSON{GUID: e.MatchGUID, Title: e.MatchTitle, Year: e.MatchYear}
	}
	for _, m := range e.Searches {
		out.Searches = append(out.Searches, s.toSearchJSON(m))
	}
	return out
}

func (s *Server) toSearchJSON(m store.MatchSearch) searchJSON {
	out := searchJSON{
		Query: m.Query, Source: m.Source, FromID: m.FromID, Failed: m.Failed,
		Candidates: make([]candidateJSON, 0, len(m.Candidates)),
	}
	for _, c := range m.Candidates {
		out.Candidates = append(out.Candidates, s.toCandidateJSON(c))
	}
	return out
}

// toCandidateJSON returns c. It checks the stored thumb again, so only a
// safe remote image URL without a configured credential reaches the
// browser, which loads it from a third-party host.
func (s *Server) toCandidateJSON(c store.MatchCandidate) candidateJSON {
	thumb := plex.SafeRemoteImage(c.Thumb)
	if s.containsCredential(thumb) {
		thumb = ""
	}
	return candidateJSON{
		GUID: c.GUID, Name: c.Name, Year: c.Year, Type: c.Type, Summary: c.Summary,
		Thumb: thumb, Score: c.Score, Verdict: c.Verdict,
	}
}

// serveItemLive answers GET /api/item/live with what Plex tells about a
// known item now.
func (s *Server) serveItemLive(w http.ResponseWriter, r *http.Request) {
	v, err := queryParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv, it, ok := s.knownItem(w, r, v)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.liveTimeout)
	d, err := s.servers[srv].Plex.ItemDetails(ctx, it.RatingKey)
	cancel()
	if err != nil {
		s.writePlexError(w, r, err)
		return
	}
	path, err := s.livePath(r.Context(), it, d.Paths)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	out := liveJSON{
		Summary: d.Summary, GUID: d.GUID,
		Locations: nonNil(d.Paths.Locations), Files: nonNil(d.Paths.Files), Path: path,
	}
	if it.Type == plex.ShowLibrary {
		out.Seasons, out.Episodes = &d.ChildCount, &d.LeafCount
	}
	writeJSON(w, http.StatusOK, out)
}

// livePath returns the cached path of it. A show without one gets its first
// location, which FillPath saves when the item is cached. FillPath keeps a
// path that a run saved during the Plex request, and the answer then shows
// that path. FillPath adds no row: when the row is gone, the answer shows
// the first location.
func (s *Server) livePath(ctx context.Context, it store.ListItem, paths plex.Paths) (string, error) {
	if it.Path != "" || it.Type != plex.ShowLibrary || len(paths.Locations) == 0 {
		return it.Path, nil
	}
	path := paths.Locations[0]
	if !it.Cached {
		return path, nil
	}
	kept, err := s.store.FillPath(ctx, it.Server, it.RatingKey, path)
	if err != nil {
		return "", err
	}
	return cmp.Or(kept, path), nil
}

// seriesJSON is what the metadata provider tells about a series candidate.
type seriesJSON struct {
	TMDBID   string `json:"tmdbId,omitempty"`
	Seasons  int    `json:"seasons,omitempty"`
	Episodes int    `json:"episodes,omitempty"`
}

// serveCandidate answers GET /api/candidate with the TMDB ID and the season
// and episode counts of the series candidate guid, from the Plex metadata
// provider, with the token of the server srv. The answer depends on the
// server and the GUID only, so the dashboard shares it between items. A
// GUID that the provider does not know answers no field.
func (s *Server) serveCandidate(w http.ResponseWriter, r *http.Request) {
	v, err := queryParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv, err := s.srvParam(v)
	if err == nil && srv == noSrv {
		err = errInvalidSrv
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.liveTimeout)
	series, err := s.servers[srv].Plex.ProviderSeries(ctx, v.Get("guid"))
	cancel()
	switch {
	case errors.Is(err, plex.ErrInvalidGUID):
		writeError(w, http.StatusBadRequest, "invalid guid")
	case errors.Is(err, plex.ErrNotFound):
		writeJSON(w, http.StatusOK, seriesJSON{})
	case err != nil:
		s.writeProviderError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, seriesJSON{TMDBID: series.TMDBID, Seasons: series.Seasons, Episodes: series.Episodes})
	}
}

// writeProviderError answers a failed request to the metadata provider with
// a fixed message.
func (s *Server) writeProviderError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		log.Debug().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("Metadata provider request failed")
		return
	}
	log.Warn().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("Metadata provider request failed")
	if errors.Is(err, plex.ErrUnauthorized) {
		writeError(w, http.StatusBadGateway, "the Plex metadata provider rejected the token")
		return
	}
	writeError(w, http.StatusBadGateway, "Plex metadata provider unavailable")
}

// writePlexError answers a failed Plex request with a fixed message, so no
// Plex text reaches the browser.
func (s *Server) writePlexError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		log.Debug().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("Plex request failed")
		return
	}
	log.Warn().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("Plex request failed")
	switch {
	case errors.Is(err, plex.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found in Plex")
	case errors.Is(err, plex.ErrUnauthorized):
		writeError(w, http.StatusBadGateway, "Plex rejected the token")
	default:
		writeError(w, http.StatusBadGateway, "Plex unavailable")
	}
}

// nonNil returns v, or an empty slice for nil, so that it encodes as [].
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
