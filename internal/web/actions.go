package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
	"github.com/l3uddz/autorefresh/internal/tmdb"
)

const (
	defaultActionTimeout = 30 * time.Second
	searchTimeout        = 45 * time.Second
	// refreshTimeout bounds the Plex requests of POST /api/item/refresh:
	// the fix match search, which the refresher bounds to 45 s, and then the
	// match or refresh with its follow-up.
	refreshTimeout = 90 * time.Second
	// refreshWriteTimeout bounds its answer. Plex can accept a request at
	// the end of refreshTimeout, and recording it can then wait for a store
	// lock of another process (30 s at most). The answer must still reach
	// the browser, so the user does not repeat a request that Plex accepted.
	refreshWriteTimeout = 3 * time.Minute
	maxBodyBytes        = 1 << 20
)

// Actions is the set of synchronous dashboard actions.
type Actions interface {
	RefreshItem(context.Context, string, string) (refresher.RefreshResult, error)
	ApplyMatch(context.Context, string, string, plex.Candidate) error
	SetStopped(context.Context, string, string, bool) (bool, error)
	SearchMatches(context.Context, string, string, refresher.MatchQuery) (refresher.MatchResult, error)
	EpisodeCheck(context.Context, string, string, string) (refresher.EpisodeResult, error)
	PreviewMatch(context.Context, string, string) (store.MatchDecision, error)
}

func (s *Server) api(pattern string, next http.HandlerFunc) {
	s.mux.Handle(pattern, requireJSON(next))
}

func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func noBody(w http.ResponseWriter, r *http.Request) bool {
	var b [1]byte
	n, err := r.Body.Read(b[:])
	switch {
	case n > 0:
		writeError(w, http.StatusBadRequest, "body not allowed")
	case err != io.EOF:
		writeError(w, http.StatusBadRequest, "invalid body")
	default:
		return true
	}
	return false
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	var object json.RawMessage
	err := dec.Decode(&object)
	if err == nil {
		err = dec.Decode(new(json.RawMessage))
		if err == io.EOF && len(object) > 0 && object[0] == '{' {
			strict := json.NewDecoder(strings.NewReader(string(object)))
			strict.DisallowUnknownFields()
			err = strict.Decode(v)
			if err == nil {
				return true
			}
		}
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "body too large")
	} else {
		writeError(w, http.StatusBadRequest, "invalid body")
	}
	return false
}

type itemAction struct {
	server, key string
	params      url.Values
}

func (s *Server) actionParams(w http.ResponseWriter, r *http.Request) (itemAction, bool) {
	if s.actions == nil {
		writeError(w, http.StatusServiceUnavailable, "actions unavailable")
		return itemAction{}, false
	}
	v, err := queryParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return itemAction{}, false
	}
	srv, key, err := s.itemParams(v)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return itemAction{}, false
	}
	return itemAction{server: s.servers[srv].Name, key: key, params: v}, true
}

// refreshJSON answers POST /api/item/refresh: the item detail after the
// refresh, the fix match that the refresh applied, if any, and whether a
// check after the request saved the current reasons of the item. Plex
// accepted the refresh, so a failed read of the detail only leaves it out.
type refreshJSON struct {
	*itemDetailJSON
	Match   *matchJSON `json:"match,omitempty"`
	Checked bool       `json:"checked"`
}

// serveRefresh answers POST /api/item/refresh. The refresh can run the
// automatic fix match, so it gets refreshTimeout, and its answer gets
// refreshWriteTimeout instead of the write timeout of the server.
func (s *Server) serveRefresh(w http.ResponseWriter, r *http.Request) {
	a, ok := s.actionParams(w, r)
	if !ok || !noBody(w, r) {
		return
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(refreshWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Warn().Err(err).Msg("Extending the write deadline of a refresh failed")
	}
	ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
	defer cancel()
	res, err := s.actions.RefreshItem(ctx, a.server, a.key)
	if err != nil {
		s.writeActionError(w, r, err)
		return
	}
	out := refreshJSON{itemDetailJSON: s.refreshedDetail(r, a), Checked: res.Checked}
	if m := res.Match; m != nil {
		out.Match = &matchJSON{GUID: m.GUID, Title: m.Title, Year: m.Year}
	}
	writeJSON(w, http.StatusOK, out)
}

// refreshedDetail reads the detail of the item after its refresh, or
// returns nil and logs why it could not.
func (s *Server) refreshedDetail(r *http.Request, a itemAction) *itemDetailJSON {
	srv, key, err := s.itemParams(a.params)
	if err != nil {
		return nil
	}
	it, found, err := s.lookupItem(r.Context(), srv, key)
	if err == nil && found {
		var detail itemDetailJSON
		if detail, err = s.itemDetail(r.Context(), srv, it); err == nil {
			return &detail
		}
	}
	if err != nil {
		log.Warn().Err(err).Str("path", r.URL.Path).Msg("Reading the item after a refresh failed")
	}
	return nil
}

func (s *Server) accepted(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		s.writeActionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
}

func (s *Server) serveMatch(w http.ResponseWriter, r *http.Request) {
	a, ok := s.actionParams(w, r)
	if !ok {
		return
	}
	var body struct {
		GUID string `json:"guid"`
		Name string `json:"name"`
		Year int    `json:"year"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.GUID == "" || len(body.GUID) > 1024 || len(body.Name) > 1024 ||
		containsControl(body.GUID) || containsControl(body.Name) || body.Year < 0 || body.Year > 9999 {
		writeError(w, http.StatusBadRequest, "invalid match")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.actionTimeout)
	defer cancel()
	s.accepted(w, r, s.actions.ApplyMatch(ctx, a.server, a.key, plex.Candidate{GUID: body.GUID, Name: body.Name, Year: body.Year}))
}

func containsControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func (s *Server) serveStop(w http.ResponseWriter, r *http.Request)   { s.serveStopped(w, r, true) }
func (s *Server) serveResume(w http.ResponseWriter, r *http.Request) { s.serveStopped(w, r, false) }

func (s *Server) serveStopped(w http.ResponseWriter, r *http.Request, stop bool) {
	a, ok := s.actionParams(w, r)
	if !ok || !noBody(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.actionTimeout)
	defer cancel()
	changed, err := s.actions.SetStopped(ctx, a.server, a.key, stop)
	if err != nil {
		s.writeActionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"changed": changed})
}

type folderIDJSON struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}

type matchesJSON struct {
	IDs        []folderIDJSON `json:"ids"`
	Locations  []string       `json:"locations"`
	Files      []string       `json:"files"`
	Searches   []searchJSON   `json:"searches"`
	TMDBFailed bool           `json:"tmdbFailed"`
}

func (s *Server) serveMatches(w http.ResponseWriter, r *http.Request) {
	a, ok := s.actionParams(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(a.params.Get("q"))
	if len(q) > maxSearchBytes {
		writeError(w, http.StatusBadRequest, "invalid q")
		return
	}
	var year int
	if raw := a.params.Get("year"); raw != "" {
		y, err := strconv.Atoi(raw)
		if err != nil || !digits(raw) || y < 1 || y > 9999 {
			writeError(w, http.StatusBadRequest, "invalid year")
			return
		}
		year = y
	}
	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	result, err := s.actions.SearchMatches(ctx, a.server, a.key, refresher.MatchQuery{Text: q, Year: year})
	if err != nil {
		s.writeActionError(w, r, err)
		return
	}
	out := matchesJSON{IDs: make([]folderIDJSON, 0, len(result.IDs)), Locations: nonNil(result.Locations), Files: nonNil(result.Files),
		Searches: make([]searchJSON, 0, len(result.Searches)), TMDBFailed: result.TMDBFailed}
	for _, id := range result.IDs {
		out.IDs = append(out.IDs, folderIDJSON{ID: id.ID, Link: id.Link})
	}
	for _, search := range result.Searches {
		out.Searches = append(out.Searches, s.toSearchJSON(search))
	}
	writeJSON(w, http.StatusOK, out)
}

// serveAutoMatch answers GET /api/item/auto-match with the decision that
// the automatic fix match makes for the item now, as a match decision
// event. The refresher records and applies nothing.
func (s *Server) serveAutoMatch(w http.ResponseWriter, r *http.Request) {
	a, ok := s.actionParams(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	d, err := s.actions.PreviewMatch(ctx, a.server, a.key)
	if err != nil {
		s.writeActionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toEventJSON(d.Event()))
}

func digits(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
}

func (s *Server) serveEpisodeCheck(w http.ResponseWriter, r *http.Request) {
	a, ok := s.actionParams(w, r)
	if !ok || !noBody(w, r) {
		return
	}
	id := a.params.Get("tmdb")
	if len(id) > 12 || !digits(id) {
		writeError(w, http.StatusBadRequest, "invalid tmdb")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	result, err := s.actions.EpisodeCheck(ctx, a.server, a.key, id)
	if err != nil {
		s.writeActionError(w, r, err)
		return
	}
	out := struct {
		Match         bool                 `json:"match"`
		Episodes      int                  `json:"episodes"`
		EpisodeGroup  string               `json:"episodeGroup,omitempty"`
		Diff          *episodeDiffJSON     `json:"diff,omitempty"`
		Seasons       []seasonEpisodesJSON `json:"seasons"`
		Stopped       bool                 `json:"stopped,omitempty"`
		EpisodeGroups []string             `json:"episodeGroups,omitempty"`
	}{
		Match: result.Match, Episodes: result.Episodes, EpisodeGroup: result.EpisodeGroup, Diff: toEpisodeDiffJSON(result.Diff),
		Seasons: toSeasonEpisodesJSON(result.Seasons), Stopped: result.Stopped, EpisodeGroups: result.EpisodeGroups,
	}
	writeJSON(w, http.StatusOK, out)
}

// seasonEpisodesJSON are the regular episode numbers of one season in Plex
// and in the TMDB listing that the episode check compared them with, each
// sorted, never null.
type seasonEpisodesJSON struct {
	Season int   `json:"season"`
	Plex   []int `json:"plex"`
	TMDB   []int `json:"tmdb"`
}

func toSeasonEpisodesJSON(seasons []refresher.SeasonEpisodes) []seasonEpisodesJSON {
	out := make([]seasonEpisodesJSON, 0, len(seasons))
	for _, s := range seasons {
		out = append(out, seasonEpisodesJSON{Season: s.Season, Plex: sortedCopy(s.Plex), TMDB: sortedCopy(s.TMDB)})
	}
	return out
}

// sortedCopy returns a sorted copy of nums, never nil.
func sortedCopy(nums []int) []int {
	out := append(make([]int, 0, len(nums)), nums...)
	slices.Sort(out)
	return out
}

func (s *Server) writeActionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		if r.Context().Err() != nil {
			writeStoreError(w, r, errors.New(s.redact(err.Error())))
			return
		}
		writeError(w, http.StatusGatewayTimeout, "timed out")
	case errors.Is(err, refresher.ErrClosed):
		writeError(w, http.StatusServiceUnavailable, "shutting down")
	case errors.Is(err, refresher.ErrItemBusy):
		writeError(w, http.StatusConflict, "item busy")
	case errors.Is(err, refresher.ErrUnknownItem):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, refresher.ErrNotCached):
		writeError(w, http.StatusConflict, "item not cached")
	case errors.Is(err, refresher.ErrNotShow):
		writeError(w, http.StatusBadRequest, "not a show")
	case errors.Is(err, refresher.ErrTMDBOff):
		writeError(w, http.StatusBadRequest, "tmdb is off")
	case errors.Is(err, refresher.ErrUnknownServer):
		writeError(w, http.StatusBadRequest, "invalid srv")
	case errors.Is(err, refresher.ErrStore):
		log.Error().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("API request failed")
		if r.Context().Err() == nil {
			writeError(w, http.StatusInternalServerError, "internal error")
		}
	case errors.Is(err, refresher.ErrTMDB):
		s.writeTMDBError(w, r, err)
	default:
		s.writePlexError(w, r, err)
	}
}

func (s *Server) writeTMDBError(w http.ResponseWriter, r *http.Request, err error) {
	log.Warn().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("TMDB request failed")
	if errors.Is(err, tmdb.ErrUnauthorized) {
		writeError(w, http.StatusBadGateway, "TMDB rejected the token")
	} else {
		writeError(w, http.StatusBadGateway, "TMDB unavailable")
	}
}
