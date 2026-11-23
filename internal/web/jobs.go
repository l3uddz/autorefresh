package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/refresher"
	"github.com/l3uddz/autorefresh/internal/store"
)

const maxBulkItems = 5000

// Jobs is the set of background dashboard actions.
type Jobs interface {
	RunNow(names []string, full bool) ([]int64, error)
	BulkRefresh(string, []string) (int64, error)
	Cancel(int64) error
}

func (s *Server) jobsAvailable(w http.ResponseWriter) bool {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "actions unavailable")
		return false
	}
	return true
}

func (s *Server) serveRunNow(w http.ResponseWriter, r *http.Request) {
	if !s.jobsAvailable(w) {
		return
	}
	var body struct {
		Srv  *int `json:"srv"`
		Full bool `json:"full"` // read every library fully
	}
	if !decodeBody(w, r, &body) {
		return
	}
	var names []string
	if body.Srv != nil {
		srv := *body.Srv
		if srv < 0 || srv >= len(s.servers) {
			writeError(w, http.StatusBadRequest, "invalid srv")
			return
		}
		names = []string{s.servers[srv].Name}
	}
	ids, err := s.jobs.RunNow(names, body.Full)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string][]int64{"ids": ids})
}

func runID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	v, err := queryParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return 0, false
	}
	raw := v.Get("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || !digits(raw) || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func (s *Server) serveCancelRun(w http.ResponseWriter, r *http.Request) {
	if !s.jobsAvailable(w) || !noBody(w, r) {
		return
	}
	id, ok := runID(w, r)
	if !ok {
		return
	}
	if err := s.jobs.Cancel(id); err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"canceled": true})
}

type runItemJSON struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) serveRunItems(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	items, err := s.store.RunItems(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	out := make([]runItemJSON, 0, len(items))
	for _, item := range items {
		out = append(out, runItemJSON{Key: item.RatingKey, Title: item.Title, Outcome: item.Outcome, Error: s.redact(item.Error)})
	}
	writeJSON(w, http.StatusOK, map[string][]runItemJSON{"items": out})
}

func (s *Server) writeJobError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, refresher.ErrBusy):
		writeError(w, http.StatusConflict, "a run is busy")
	case errors.Is(err, refresher.ErrNoJob):
		writeError(w, http.StatusNotFound, "no such job")
	default:
		s.writeActionError(w, r, err)
	}
}

func (s *Server) serveBulkRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.jobsAvailable(w) {
		return
	}
	srv, keys, ok := s.bulkItems(w, r, r.Context(), store.TabAttention)
	if !ok {
		return
	}
	id, err := s.jobs.BulkRefresh(s.servers[srv].Name, keys)
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		ID    int64 `json:"id"`
		Count int   `json:"count"`
	}{id, len(keys)})
}

var (
	errBulkLimit = errors.New("too many items")
	errBulkKey   = errors.New("invalid key")
)

type bulkSelection struct {
	Keys     []string `json:"keys"`
	Matching bool     `json:"matching"`
}

func (s *Server) bulkItems(w http.ResponseWriter, r *http.Request, ctx context.Context, tab store.Tab) (int, []string, bool) {
	srv, q, err := s.bulkQuery(r, tab)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return 0, nil, false
	}
	var selection bulkSelection
	if !decodeBody(w, r, &selection) {
		return 0, nil, false
	}
	keys, err := s.bulkKeys(ctx, q, selection)
	switch {
	case errors.Is(err, refresher.ErrStore):
		s.writeActionError(w, r, err)
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	case len(keys) == 0:
		writeError(w, http.StatusBadRequest, "no items")
	default:
		return srv, keys, true
	}
	return 0, nil, false
}

func (s *Server) bulkQuery(r *http.Request, tab store.Tab) (int, store.ListQuery, error) {
	v, err := queryParams(r)
	if err != nil {
		return 0, store.ListQuery{}, err
	}
	srv, err := s.srvParam(v)
	if err != nil || srv == noSrv {
		return 0, store.ListQuery{}, errInvalidSrv
	}
	if v.Has("tab") {
		return 0, store.ListQuery{}, errInvalidTab
	}
	if tab != store.TabAttention && v.Get("all") == "1" {
		return 0, store.ListQuery{}, errInvalidAll
	}
	q, err := s.filterQuery(v, tab, srv)
	return srv, q, err
}

func (s *Server) bulkKeys(ctx context.Context, q store.ListQuery, selection bulkSelection) ([]string, error) {
	if selection.Matching {
		if selection.Keys != nil {
			return nil, errInvalidBulkBody
		}
		return s.matchingKeys(ctx, q)
	}
	if len(selection.Keys) == 0 {
		return nil, errInvalidBulkBody
	}
	seen := map[string]bool{}
	keys := make([]string, 0, min(len(selection.Keys), maxBulkItems))
	for _, key := range selection.Keys {
		if !plex.ValidRatingKey(key) {
			return nil, errBulkKey
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	if len(keys) > maxBulkItems {
		return nil, errBulkLimit
	}
	return keys, nil
}

var errInvalidBulkBody = errors.New("invalid body")

func (s *Server) matchingKeys(ctx context.Context, q store.ListQuery) ([]string, error) {
	q.Rules = s.rules
	q.Rules.Now = s.now()
	q.Sort, q.Desc = store.DefaultSort(q.Tab)
	q.Limit = maxPageLimit
	seen := map[string]bool{}
	var keys []string
	for {
		items, next, err := s.store.ListItems(ctx, q)
		if err != nil {
			return nil, errors.Join(refresher.ErrStore, err)
		}
		for _, item := range items {
			if !seen[item.RatingKey] {
				seen[item.RatingKey] = true
				keys = append(keys, item.RatingKey)
			}
		}
		if len(keys) > maxBulkItems {
			return nil, errBulkLimit
		}
		if next == nil {
			return keys, nil
		}
		q.After = next
	}
}

type bulkSkip struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

func (s *Server) serveBulkStop(w http.ResponseWriter, r *http.Request) {
	s.serveBulkStopped(w, r, store.TabAttention, true)
}
func (s *Server) serveBulkResume(w http.ResponseWriter, r *http.Request) {
	s.serveBulkStopped(w, r, store.TabStopped, false)
}
func (s *Server) serveBulkStopped(w http.ResponseWriter, r *http.Request, tab store.Tab, stop bool) {
	if s.actions == nil {
		writeError(w, http.StatusServiceUnavailable, "actions unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.actionTimeout)
	defer cancel()
	srv, keys, ok := s.bulkItems(w, r, ctx, tab)
	if !ok {
		return
	}
	out := struct {
		Changed int        `json:"changed"`
		Skipped []bulkSkip `json:"skipped"`
	}{Skipped: []bulkSkip{}}
	for i, key := range keys {
		reason, err := s.stopBulkItem(ctx, r.Context(), s.servers[srv].Name, key, stop)
		if err != nil {
			s.writeActionError(w, r, err)
			return
		}
		if reason == "timeout" {
			for _, remaining := range keys[i:] {
				out.Skipped = append(out.Skipped, bulkSkip{Key: remaining, Reason: reason})
			}
			break
		}
		if reason == "" {
			out.Changed++
		} else {
			out.Skipped = append(out.Skipped, bulkSkip{Key: key, Reason: reason})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) stopBulkItem(ctx, requestCtx context.Context, server, key string, stop bool) (string, error) {
	if err := requestCtx.Err(); err != nil {
		return "", err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout", nil
	}
	changed, err := s.actions.SetStopped(ctx, server, key, stop)
	switch {
	case requestCtx.Err() != nil:
		return "", requestCtx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", nil
	case errors.Is(err, refresher.ErrItemBusy):
		return "busy", nil
	case errors.Is(err, refresher.ErrNotCached), errors.Is(err, refresher.ErrUnknownItem):
		return "not-cached", nil
	case err != nil:
		return "", err
	case !changed:
		return "unchanged", nil
	default:
		return "", nil
	}
}
