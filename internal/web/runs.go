package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/l3uddz/autorefresh/internal/store"
)

// defaultRunLimit is the page size of GET /api/runs without a limit.
const defaultRunLimit = 50

// runCursorVersion is the version of the run cursor.
const runCursorVersion = 1

// runCursor is the JSON of a run cursor: the start time and the ID of the
// last run of a page. Every field is required. Scope is the srv of the
// request, or "" for every server, so the pointers tell a missing field
// from a zero value.
type runCursor struct {
	V     int     `json:"v"`
	Scope *string `json:"scope"`
	A     *int64  `json:"a"`
	I     *int64  `json:"i"`
}

type runsJSON struct {
	Runs []runJSON `json:"runs"`
	Next string    `json:"next,omitempty"`
}

// runJSON is one run of the API.
type runJSON struct {
	ID         int64     `json:"id"`
	Srv        int       `json:"srv"`
	Server     string    `json:"server"`
	Kind       string    `json:"kind"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
	Error      string    `json:"error,omitempty"`
	ReadItems  int       `json:"readItems"`
	Refreshed  int       `json:"refreshed"`
	Matched    int       `json:"matched"`
	Failed     int       `json:"failed"`
	Completed  int       `json:"completed"`
	Fixed      int       `json:"fixed"`
	Incomplete int       `json:"incomplete"`
	Due        int       `json:"due"`
	Selected   int       `json:"selected"`
	Skipped    int       `json:"skipped"`
	// FullReads and IncrementalReads count the libraries that the run
	// read fully and incrementally.
	FullReads        int `json:"fullReads"`
	IncrementalReads int `json:"incrementalReads"`
	// EventsLinked is false for a run from before runs linked their events.
	EventsLinked bool `json:"eventsLinked"`
}

// serveRuns answers GET /api/runs with one page of the runs of the
// configured servers, newest first.
func (s *Server) serveRuns(w http.ResponseWriter, r *http.Request) {
	q, scope, err := s.runsQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(q.Servers) == 0 {
		// No server is configured. The store would read the empty list as
		// every server.
		writeJSON(w, http.StatusOK, runsJSON{Runs: []runJSON{}})
		return
	}
	limit := q.Limit
	q.Limit++ // one more run tells whether a next page exists
	runs, err := s.store.Runs(r.Context(), q)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	out := runsJSON{Runs: make([]runJSON, 0, min(len(runs), limit))}
	if len(runs) > limit {
		runs = runs[:limit]
		out.Next = encodeRunCursor(scope, runs[limit-1])
	}
	for _, run := range runs {
		out.Runs = append(out.Runs, s.toRunJSON(run))
	}
	writeJSON(w, http.StatusOK, out)
}

// runsQuery parses the parameters of r into the query of the store and
// returns the scope of its cursors. The query always names the servers, so
// the runs of a server that is no longer configured stay hidden.
func (s *Server) runsQuery(r *http.Request) (store.RunQuery, string, error) {
	v, err := queryParams(r)
	if err != nil {
		return store.RunQuery{}, "", err
	}
	srv, err := s.srvParam(v)
	if err != nil {
		return store.RunQuery{}, "", err
	}
	q := store.RunQuery{Servers: s.serverNames(srv)}
	if q.Limit, err = limitParam(v.Get("limit"), defaultRunLimit); err != nil {
		return store.RunQuery{}, "", err
	}
	scope := ""
	if srv != noSrv {
		scope = strconv.Itoa(srv)
	}
	if q.Before, err = decodeRunCursor(v.Get("cursor"), scope); err != nil {
		return store.RunQuery{}, "", err
	}
	return q, scope, nil
}

// encodeRunCursor returns the cursor after last in scope.
func encodeRunCursor(scope string, last store.Run) string {
	at := last.StartedAt.Unix()
	c := runCursor{V: runCursorVersion, Scope: &scope, A: &at, I: &last.ID}
	// Marshal cannot fail on strings, integers, and pointers to them.
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeRunCursor returns the position of raw, or nil when raw is empty. A
// cursor that does not decode, has another version or scope, or lacks a
// field is an error.
func decodeRunCursor(raw, scope string) (*store.RunCursor, error) {
	if raw == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errInvalidCursor
	}
	var c runCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, errInvalidCursor
	}
	if c.V != runCursorVersion || c.Scope == nil || *c.Scope != scope || c.A == nil || c.I == nil {
		return nil, errInvalidCursor
	}
	return &store.RunCursor{StartedAt: time.Unix(*c.A, 0), ID: *c.I}, nil
}

// toRunJSON returns run with its error text redacted.
func (s *Server) toRunJSON(run store.Run) runJSON {
	return runJSON{
		ID: run.ID, Srv: s.srvIndex[run.Server], Server: run.Server, Kind: string(run.Kind),
		Source: string(run.Source), Status: string(run.Status), StartedAt: run.StartedAt,
		FinishedAt: run.FinishedAt, Error: s.redact(run.Error),
		ReadItems: run.ReadItems, Refreshed: run.Refreshed, Matched: run.Matched, Failed: run.Failed,
		Completed: run.Completed, Fixed: run.Fixed, Incomplete: run.Incomplete, Due: run.Due,
		Selected: run.Selected, Skipped: run.Skipped,
		FullReads: run.FullReads, IncrementalReads: run.IncrementalReads, EventsLinked: run.EventsLinked,
	}
}

// runDetailJSON is a run with its events and, for a bulk job, its items.
type runDetailJSON struct {
	Run    runJSON        `json:"run"`
	Events []runEventJSON `json:"events"`
	Items  []runItemJSON  `json:"items"`
}

// runEventJSON is an event of a run with the item that it names.
type runEventJSON struct {
	eventJSON
	Key   string `json:"key"`
	Title string `json:"title,omitempty"`
	Year  int    `json:"year,omitempty"`
	Type  string `json:"type,omitempty"`
	Thumb string `json:"thumb,omitempty"`
	// Library is the title of the library of the item, which tells apart
	// the same title in several libraries.
	Library string `json:"library,omitempty"`
}

// serveRunDetail answers GET /api/runs/detail with one run of a configured
// server, its events, and its bulk items. It reads everything before it
// writes the answer.
func (s *Server) serveRunDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	run, found, err := s.store.Run(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	srv, configured := s.srvIndex[run.Server]
	if !found || !configured {
		writeError(w, http.StatusNotFound, "no such run")
		return
	}
	events, err := s.store.RunEvents(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	items, err := s.store.RunItems(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	libraries, err := s.libraryTitles(r.Context(), run.Server)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	out := runDetailJSON{Run: s.toRunJSON(run), Events: make([]runEventJSON, 0, len(events)), Items: make([]runItemJSON, 0, len(items))}
	for _, e := range events {
		out.Events = append(out.Events, runEventJSON{
			eventJSON: s.toEventJSON(e), Key: e.RatingKey, Title: e.Title, Year: e.Year, Type: e.Type,
			Thumb: imageURL("thumb", srv, e.RatingKey, e.Thumb), Library: libraries[e.Section],
		})
	}
	for _, item := range items {
		out.Items = append(out.Items, s.toRunItemJSON(item))
	}
	writeJSON(w, http.StatusOK, out)
}

// libraryTitles returns the title of each library of server by section.
func (s *Server) libraryTitles(ctx context.Context, server string) (map[string]string, error) {
	libs, err := s.store.Libraries(ctx, server)
	if err != nil {
		return nil, err
	}
	titles := make(map[string]string, len(libs))
	for _, l := range libs {
		titles[l.Section] = l.Title
	}
	return titles, nil
}
