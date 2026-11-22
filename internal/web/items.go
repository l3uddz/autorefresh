package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// The limits of GET /api/items.
const (
	defaultItemLimit = 100
	maxItemLimit     = 500
	maxSearchBytes   = 200
)

// The errors of the parameters of GET /api/items. Their texts are the
// messages of the 400 answers.
var (
	errInvalidTab      = errors.New("invalid tab")
	errLibraryNeedsSrv = errors.New("library needs srv")
	errInvalidReason   = errors.New("invalid reason")
	errInvalidType     = errors.New("invalid type")
	errInvalidQ        = errors.New("invalid q")
	errInvalidSort     = errors.New("invalid sort")
	errInvalidAll      = errors.New("invalid all")
	errInvalidLimit    = errors.New("invalid limit")
	errInvalidCursor   = errors.New("invalid cursor")
)

// queueSortKeys are the sort keys of the queue tabs.
var queueSortKeys = []store.SortKey{
	store.SortTitle, store.SortYear, store.SortAdded, store.SortDue, store.SortTries,
	store.SortLastAttempt, store.SortStopped,
}

// tabSorts are the sort keys of each tab, and its default sort.
var tabSorts = map[store.Tab]struct {
	keys []store.SortKey
	def  string
}{
	store.TabAttention: {queueSortKeys, string(store.SortDue)},
	store.TabStopped:   {queueSortKeys, "-" + string(store.SortStopped)},
	store.TabFixed:     {[]store.SortKey{store.SortTitle, store.SortYear, store.SortCompleted}, "-" + string(store.SortCompleted)},
}

// cursorVersion is the version of the item cursor.
const cursorVersion = 1

// itemCursor is the JSON of an item cursor. Only the value field of the
// sort type is set: T for SortTitle, N otherwise.
type itemCursor struct {
	V     int     `json:"v"`
	Scope string  `json:"scope"`
	N     *int64  `json:"n,omitempty"`
	T     *string `json:"t,omitempty"`
	S     string  `json:"s"`
	K     string  `json:"k"`
}

type itemsJSON struct {
	Items  []itemJSON `json:"items"`
	Next   string     `json:"next,omitempty"`
	Counts countsJSON `json:"counts"`
}

type countsJSON struct {
	Attention int `json:"attention"`
	Fixed     int `json:"fixed"`
	Stopped   int `json:"stopped"`
}

// itemJSON is one item of the API.
type itemJSON struct {
	Srv              int              `json:"srv"`
	Server           string           `json:"server,omitempty"`
	Section          string           `json:"section,omitempty"`
	Library          string           `json:"library,omitempty"`
	Key              string           `json:"key,omitempty"`
	Type             string           `json:"type,omitempty"`
	Title            string           `json:"title,omitempty"`
	Year             int              `json:"year,omitempty"`
	Path             string           `json:"path,omitempty"`
	Thumb            string           `json:"thumb,omitempty"`
	Art              string           `json:"art,omitempty"`
	Cached           bool             `json:"cached"`
	Reasons          []string         `json:"reasons,omitempty"`
	State            store.QueueState `json:"state,omitempty"`
	NextDue          time.Time        `json:"nextDue,omitzero"`
	AddedAt          time.Time        `json:"addedAt,omitzero"`
	LastAttempt      time.Time        `json:"lastAttempt,omitzero"`
	StoppedAt        time.Time        `json:"stoppedAt,omitzero"`
	Attempts         int              `json:"attempts"`
	Refreshes        int              `json:"refreshes"`
	LastAction       store.Action     `json:"lastAction,omitempty"`
	LastOutcome      string           `json:"lastOutcome,omitempty"`
	LastMatchOutcome string           `json:"lastMatchOutcome,omitempty"`
	CompletedAt      time.Time        `json:"completedAt,omitzero"`
	FixedAction      store.Action     `json:"fixedAction,omitempty"`
	FixedMatch       store.Action     `json:"fixedMatch,omitempty"`
}

// serveItems answers GET /api/items with one page of a tab and the counts
// of every tab under the same filters.
func (s *Server) serveItems(w http.ResponseWriter, r *http.Request) {
	q, scope, err := s.itemsQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q.Rules = s.rules
	q.Rules.Now = s.now()

	items, next, err := s.store.ListItems(r.Context(), q)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	counts, err := s.store.CountItems(r.Context(), q)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	out := itemsJSON{
		Items:  make([]itemJSON, 0, len(items)),
		Next:   encodeItemCursor(scope, q.Sort, next),
		Counts: countsJSON{Attention: counts.Attention, Fixed: counts.Fixed, Stopped: counts.Stopped},
	}
	for _, it := range items {
		out.Items = append(out.Items, toItemJSON(s.srvIndex[it.Server], it))
	}
	writeJSON(w, http.StatusOK, out)
}

// itemsQuery parses the parameters of r into the query of the store, with
// no Rules, and returns the scope of its cursors.
func (s *Server) itemsQuery(r *http.Request) (store.ListQuery, string, error) {
	v := r.URL.Query()
	var q store.ListQuery
	var err error
	if q.Tab, err = tabParam(v.Get("tab")); err != nil {
		return store.ListQuery{}, "", err
	}
	srv, err := s.srvParam(r)
	if err != nil {
		return store.ListQuery{}, "", err
	}
	q.Servers = s.serverNames(srv)
	if err := filterParams(&q, v, srv); err != nil {
		return store.ListQuery{}, "", err
	}
	sort, err := sortParam(&q, v.Get("sort"))
	if err != nil {
		return store.ListQuery{}, "", err
	}
	all, err := allParam(v.Get("all"))
	if err != nil {
		return store.ListQuery{}, "", err
	}
	q.WithIgnored = all && q.Tab == store.TabAttention
	q.WithUnrequested = all && q.Tab == store.TabFixed
	if q.Limit, err = limitParam(v.Get("limit")); err != nil {
		return store.ListQuery{}, "", err
	}
	scope := itemsScope(q, srv, sort, all)
	if q.After, err = decodeItemCursor(v.Get("cursor"), scope, q.Sort); err != nil {
		return store.ListQuery{}, "", err
	}
	return q, scope, nil
}

// tabParam returns the tab of raw, TabAttention when raw is empty.
func tabParam(raw string) (store.Tab, error) {
	switch tab := store.Tab(raw); tab {
	case "":
		return store.TabAttention, nil
	case store.TabAttention, store.TabFixed, store.TabStopped:
		return tab, nil
	default:
		return "", errInvalidTab
	}
}

// filterParams sets the library, reason, type, and search filters of q from
// v. A library needs a server, because section IDs differ per server.
func filterParams(q *store.ListQuery, v url.Values, srv int) error {
	q.Section = v.Get("library")
	if q.Section != "" && srv == noSrv {
		return errLibraryNeedsSrv
	}
	if raw := v.Get("reason"); raw != "" {
		reason, err := store.ParseReasons([]string{raw})
		if err != nil {
			return errInvalidReason
		}
		q.Reason = reason
	}
	switch q.Type = v.Get("type"); q.Type {
	case "", "movie", "show":
	default:
		return errInvalidType
	}
	q.Search = strings.TrimSpace(v.Get("q"))
	if len(q.Search) > maxSearchBytes {
		return errInvalidQ
	}
	return nil
}

// sortParam sets the sort of q from raw, a sort key of the tab with an
// optional "-" for descending, or the tab default when raw is empty. It
// returns the sort with the default filled in.
func sortParam(q *store.ListQuery, raw string) (string, error) {
	sorts := tabSorts[q.Tab]
	if raw == "" {
		raw = sorts.def
	}
	name, desc := strings.CutPrefix(raw, "-")
	key := store.SortKey(name)
	if !slices.Contains(sorts.keys, key) {
		return "", errInvalidSort
	}
	q.Sort, q.Desc = key, desc
	return raw, nil
}

// allParam reports whether raw is "1". Only "" and "1" are valid.
func allParam(raw string) (bool, error) {
	switch raw {
	case "":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, errInvalidAll
	}
}

// limitParam returns the page size of raw, defaultItemLimit when raw is
// empty.
func limitParam(raw string) (int, error) {
	if raw == "" {
		return defaultItemLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxItemLimit {
		return 0, errInvalidLimit
	}
	return limit, nil
}

// itemsScope returns the normalized request of q without its cursor and
// limit. A cursor fits only the request of the same scope.
func itemsScope(q store.ListQuery, srv int, sort string, all bool) string {
	scope := url.Values{
		"tab":     {string(q.Tab)},
		"srv":     {""},
		"library": {q.Section},
		"reason":  {q.Reason.String()},
		"type":    {q.Type},
		"q":       {q.Search},
		"sort":    {sort},
		"all":     {""},
	}
	if srv != noSrv {
		scope.Set("srv", strconv.Itoa(srv))
	}
	if all {
		scope.Set("all", "1")
	}
	return scope.Encode()
}

// encodeItemCursor returns the cursor of next in scope, or "" when next is
// nil.
func encodeItemCursor(scope string, sort store.SortKey, next *store.Cursor) string {
	if next == nil {
		return ""
	}
	c := itemCursor{V: cursorVersion, Scope: scope, S: next.Server, K: next.RatingKey}
	if sort == store.SortTitle {
		c.T = &next.Text
	} else {
		c.N = &next.Num
	}
	// Marshal cannot fail on strings, integers, and pointers to them.
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeItemCursor returns the position of raw, or nil when raw is empty. A
// cursor that does not decode, has another version or scope, or lacks a
// required field is an error.
func decodeItemCursor(raw, scope string, sort store.SortKey) (*store.Cursor, error) {
	if raw == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errInvalidCursor
	}
	var c itemCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, errInvalidCursor
	}
	if c.V != cursorVersion || c.Scope != scope || c.S == "" || c.K == "" {
		return nil, errInvalidCursor
	}
	after := &store.Cursor{Server: c.S, RatingKey: c.K}
	switch {
	case sort == store.SortTitle && c.T != nil:
		after.Text = *c.T
	case sort != store.SortTitle && c.N != nil:
		after.Num = *c.N
	default:
		return nil, errInvalidCursor
	}
	return after, nil
}

// toItemJSON returns it as the item of the server srv.
func toItemJSON(srv int, it store.ListItem) itemJSON {
	return itemJSON{
		Srv: srv, Server: it.Server, Section: it.Section, Library: it.Library, Key: it.RatingKey,
		Type: it.Type, Title: it.Title, Year: it.Year, Path: it.Path,
		Thumb:  imageURL("thumb", srv, it.RatingKey, it.Thumb),
		Art:    imageURL("art", srv, it.RatingKey, it.Art),
		Cached: it.Cached, Reasons: reasonNames(it.Reasons), State: it.State,
		NextDue: it.NextDue, AddedAt: it.AddedAt, LastAttempt: it.LastAttempt, StoppedAt: it.StoppedAt,
		Attempts: it.Attempts, Refreshes: it.Refreshes, LastAction: it.LastAction,
		LastOutcome: it.LastOutcome, LastMatchOutcome: it.LastMatchOutcome,
		CompletedAt: it.CompletedAt, FixedAction: it.FixedAction, FixedMatch: it.FixedMatch,
	}
}

// imageURL returns the relative URL of the image endpoint kind for the
// stored image path p of the item key, or "" when p is not an image path of
// the item. v, the digits after the last "/", changes with the image, so a
// browser cache never shows an old one.
func imageURL(kind string, srv int, key, p string) string {
	if plex.ItemImagePath(key, p) == "" {
		return ""
	}
	u := url.URL{
		Path: "api/items/" + kind,
		RawQuery: url.Values{
			"key": {key},
			"srv": {strconv.Itoa(srv)},
			"v":   {p[strings.LastIndex(p, "/")+1:]},
		}.Encode(),
	}
	return u.String()
}

// reasonNames returns the names of r, an empty slice for 0.
func reasonNames(r store.Reasons) []string {
	if r == 0 {
		return []string{}
	}
	return strings.Split(r.String(), ",")
}
