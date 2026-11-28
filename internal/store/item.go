package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Library holds the read times of one library. A zero time means never.
type Library struct{ LastFullRead, LastRead time.Time }

// Item is a Plex item that is incomplete, with its refresh history.
type Item struct {
	Server, Section, RatingKey string
	Type                       string // "movie" or "show"
	Title                      string
	Year                       int
	AddedAt                    time.Time
	Reasons                    Reasons
	// Keep holds the item reasons that the reader could not decide, so a
	// read or a check keeps their stored value. It is not stored.
	Keep                     Reasons
	LastRefresh, LastAttempt time.Time // zero: never
	Refreshes, Attempts      int
	LastAction               Action // the last request that Plex accepted
	LastMatch                Action // the last match request that Plex accepted

	Thumb, Art, Path string    // Plex image paths and the folder or file path; "": unknown
	StoppedAt        time.Time // zero: not stopped
	// LastOutcome and LastMatchOutcome are the outcomes of the last request
	// and of the last match request. "": none, or from before they were recorded.
	LastOutcome, LastMatchOutcome string
	LastMatchEventID              int64 // the request event of the last accepted match; 0: none
}

// Action is a request that Plex accepted for an item. The empty Action
// means no request, or a request from before autorefresh recorded actions.
type Action string

const (
	ActionRefresh Action = "refresh"
	ActionMatch   Action = "match"
	// ActionTMDBMatch is a match whose candidate a search with a TMDB ID
	// from TMDB found.
	ActionTMDBMatch Action = "tmdb-match"
	// ActionTitleMatch is a match whose candidate a search with the title and
	// the year of a series found and TMDB episode numbers confirmed.
	ActionTitleMatch Action = "title-match"
)

// isMatch reports whether a is a match request.
func (a Action) isMatch() bool {
	return a == ActionMatch || a == ActionTMDBMatch || a == ActionTitleMatch
}

// Read is the staged result of all requests for one library.
type Read struct {
	Server, Section  string
	At               time.Time // the start of the read
	Full             bool
	Items            []Item   // the full or incremental read, with item reasons only
	Gone             []string // full read only: rating keys that Plex answered with 404
	Shows            bool     // a show library: replace both episode reasons
	EpisodeSkipped   Reasons  // show reads only: the episode reasons without a request, which the rows keep
	EpisodeNoSummary []Item   // the series of the first episode request
	EpisodeUnmatched []Item   // the series of the second episode request
	// Folders maps a rating key to its series folder. ApplyRead saves each
	// non-empty value as the path of the row, if the read keeps the row.
	Folders map[string]string
	// DryRun keeps ApplyRead from writing events. The rows and the read
	// times change as in any other read.
	DryRun bool
	// RunID is the run of the read, which its events name; 0: none.
	RunID int64
	// Mark is the newest event before the read listed its items. ApplyRead
	// neither adds nor changes the row of an item with a completed or gone
	// event, or a request that Plex accepted, after Mark: a check or a manual
	// action settled or changed the item during the read, so the listing can
	// be older. The zero EventMark checks no event.
	Mark EventMark
}

const itemColumns = `server, section, rating_key, type, title, year, added_at, reasons, last_refresh, last_attempt, refreshes,
	last_action, last_match, thumb, art, path, stopped_at, last_outcome, last_match_outcome, last_match_event_id, attempts`

// nullTime stores a zero time as NULL and any other time as Unix seconds.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func timeFrom(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0)
}

type scanner interface{ Scan(dest ...any) error }

func scanItem(row scanner) (Item, error) {
	var (
		it                       Item
		addedAt                  int64
		reasons                  int
		lastRefresh, lastAttempt sql.NullInt64
		stoppedAt, matchEventID  sql.NullInt64
	)
	err := row.Scan(&it.Server, &it.Section, &it.RatingKey, &it.Type, &it.Title, &it.Year,
		&addedAt, &reasons, &lastRefresh, &lastAttempt, &it.Refreshes, &it.LastAction, &it.LastMatch,
		&it.Thumb, &it.Art, &it.Path, &stoppedAt, &it.LastOutcome, &it.LastMatchOutcome, &matchEventID, &it.Attempts)
	if err != nil {
		return Item{}, err
	}
	it.AddedAt = time.Unix(addedAt, 0)
	it.Reasons = Reasons(reasons)
	it.LastRefresh = timeFrom(lastRefresh)
	it.LastAttempt = timeFrom(lastAttempt)
	it.StoppedAt = timeFrom(stoppedAt)
	it.LastMatchEventID = matchEventID.Int64
	return it, nil
}

// queryItems runs query and reads all rows before it returns.
func queryItems(q querier, query string, args ...any) ([]Item, error) {
	return queryItemsContext(context.Background(), q, query, args...)
}

// queryItemsContext runs query with ctx and reads all rows before it returns.
func queryItemsContext(ctx context.Context, q querier, query string, args ...any) ([]Item, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// Library returns the read times of a library. A library that was never read
// gives the zero Library.
func (s *Store) Library(server, section string) (Library, error) {
	var full, read sql.NullInt64
	err := s.db.QueryRow(`SELECT last_full_read, last_read FROM library WHERE server = ? AND section = ?`,
		server, section).Scan(&full, &read)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, nil
	}
	if err != nil {
		return Library{}, fmt.Errorf("read library: %w", err)
	}
	return Library{LastFullRead: timeFrom(full), LastRead: timeFrom(read)}, nil
}

// ApplyRead writes r in one transaction. For each row of the section, the
// final reasons are the item bits from r.Items (or the old item bits if the
// item is not in r.Items) and, if r.Shows, the episode bits from the two
// lists and the old bits of r.EpisodeSkipped (or else the old episode bits).
// It deletes the r.Gone rows, inserts rows for new items with final reasons
// that are not 0, deletes the other rows whose final reasons are 0 and
// returns them as fixed, sorted by rating key, writes the thumb and art of
// each listed row and its path unless the read has none, saves the r.Folders
// paths, and sets the read times. Unless r.DryRun, it also writes a completed
// event for each fixed row and a gone event for each r.Gone row that exists;
// a dry run writes no event. LastFullRead changes only if r.Full. The caller
// must set AddedAt on every item, because a zero time is stored as a negative
// number, and must not put a key in both r.Items and r.Gone. A row of the
// same rating key in another section moves to r.Section and keeps its history
// and its reasons that the read does not replace. It neither inserts nor
// changes the row of an item that an event after r.Mark settled or changed;
// a later read applies its listing again, at the latest the next full read.
func (s *Store) ApplyRead(r Read) (fixed []Item, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin read: %w", err)
	}
	defer tx.Rollback()

	// The events of the read itself are not newer than the read.
	newer, err := newerKeys(tx, r)
	if err != nil {
		return nil, err
	}
	// The item rows of the library need its library row first.
	if err := writeReadTimes(tx, r); err != nil {
		return nil, err
	}
	if err := deleteGone(tx, r, newer); err != nil {
		return nil, err
	}
	incoming := readKeys(r)
	old, err := oldRows(tx, r, incoming)
	if err != nil {
		return nil, err
	}
	fixed, err = applyOldRows(tx, r, old, incoming, newer)
	if err != nil {
		return nil, err
	}
	if err := insertNewRows(tx, r, old, incoming, newer); err != nil {
		return nil, err
	}
	if err := writeFolders(tx, r, newer); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit read: %w", err)
	}
	return fixed, nil
}

// newerKeys returns the rating keys of r.Server with a completed or gone
// event, or a request that Plex accepted, after r.Mark, or none for the zero
// mark. Plex can have changed such an item after the listing of the read.
func newerKeys(tx *sql.Tx, r Read) (map[string]bool, error) {
	if !r.Mark.set {
		return nil, nil
	}
	// NOT INDEXED keeps SQLite on the ID range, the few events after the
	// mark, instead of the server's whole history in an index.
	rows, err := tx.Query(`SELECT rating_key FROM event NOT INDEXED
		WHERE id > ? AND server = ? AND (kind IN (?, ?) OR kind = ? AND accepted = 1)`,
		r.Mark.id, r.Server, EventCompleted, EventGone, EventRequest)
	if err != nil {
		return nil, fmt.Errorf("read newer items: %w", err)
	}
	defer rows.Close()
	newer := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("read newer items: %w", err)
		}
		newer[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read newer items: %w", err)
	}
	return newer, nil
}

// deleteGone deletes the rows of r.Gone except the rows of newer. Unless
// r.DryRun, it writes a gone event for each row that it deletes.
func deleteGone(tx *sql.Tx, r Read, newer map[string]bool) error {
	for _, key := range r.Gone {
		if newer[key] {
			continue
		}
		if !r.DryRun {
			if err := writeGone(tx, r, key); err != nil {
				return err
			}
		}
		if err := deleteItem(tx, r.Server, key); err != nil {
			return err
		}
	}
	return nil
}

// oldRows returns the stored rows that the read r can change, by rating key:
// the rows of the section, and the rows of any section whose rating key is
// in the read (a moved item). The server's rows are filtered in Go because a
// read can hold more keys than SQLite accepts as query parameters.
func oldRows(tx *sql.Tx, r Read, incoming map[string]readKey) (map[string]Item, error) {
	stored, err := queryItems(tx, `SELECT `+itemColumns+` FROM item WHERE server = ?`, r.Server)
	if err != nil {
		return nil, fmt.Errorf("read stored items: %w", err)
	}
	old := map[string]Item{}
	for _, it := range stored {
		if _, ok := incoming[it.RatingKey]; ok || it.Section == r.Section {
			old[it.RatingKey] = it
		}
	}
	return old, nil
}

// completion is a row that a read completed, with what the read says about it.
type completion struct {
	prev Item
	in   readKey
}

// applyOldRows updates or deletes each stored row in old after the read r,
// except the rows of newer, returning the rows that became complete. Unless
// r.DryRun, it writes a completed event for each of them, ordered by rating
// key.
func applyOldRows(tx *sql.Tx, r Read, old map[string]Item, incoming map[string]readKey, newer map[string]bool) ([]Item, error) {
	var done []completion
	for key, prev := range old {
		if newer[key] {
			continue
		}
		fixed, err := writeExisting(tx, r, prev, incoming[key])
		if err != nil {
			return nil, err
		}
		if fixed {
			done = append(done, completion{prev, incoming[key]})
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].prev.RatingKey < done[j].prev.RatingKey })

	fixed := make([]Item, 0, len(done))
	for _, c := range done {
		if !r.DryRun {
			if err := writeCompleted(tx, r, c.prev, c.in); err != nil {
				return nil, err
			}
		}
		fixed = append(fixed, c.prev)
	}
	return fixed, nil
}

// writeExisting writes the row of one existing item after the read r. It
// deletes the row and returns fixed=true when no reason remains, and skips
// the write when an unmentioned row's reasons did not change.
func writeExisting(tx *sql.Tx, r Read, prev Item, in readKey) (fixed bool, err error) {
	final := in.reasons(r, prev.Reasons)
	switch {
	case final == 0:
		if err := deleteItem(tx, r.Server, prev.RatingKey); err != nil {
			return false, err
		}
		return true, nil
	case in.present:
		_, err = tx.Exec(`UPDATE item SET section = ?, type = ?, title = ?, year = ?, added_at = ?, reasons = ?,
			thumb = ?, art = ?, path = CASE WHEN ? = '' THEN path ELSE ? END
			WHERE server = ? AND rating_key = ?`,
			r.Section, in.meta.Type, in.meta.Title, in.meta.Year, in.meta.AddedAt.Unix(), int(final),
			in.meta.Thumb, in.meta.Art, in.meta.Path, in.meta.Path, r.Server, prev.RatingKey)
	case final != prev.Reasons:
		_, err = tx.Exec(`UPDATE item SET reasons = ? WHERE server = ? AND rating_key = ?`, int(final), r.Server, prev.RatingKey)
	}
	if err != nil {
		return false, fmt.Errorf("write item %s: %w", prev.RatingKey, err)
	}
	return false, nil
}

// insertNewRows inserts rows for incoming items not in old or newer that
// have at least one incomplete reason.
func insertNewRows(tx *sql.Tx, r Read, old map[string]Item, incoming map[string]readKey, newer map[string]bool) error {
	if len(incoming) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons, thumb, art, path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare item insert: %w", err)
	}
	defer stmt.Close()
	for key, in := range incoming {
		if _, exists := old[key]; exists || newer[key] {
			continue
		}
		final := in.reasons(r, 0)
		if final == 0 {
			continue
		}
		_, err := stmt.Exec(r.Server, r.Section, key, in.meta.Type, in.meta.Title, in.meta.Year, in.meta.AddedAt.Unix(), int(final),
			in.meta.Thumb, in.meta.Art, in.meta.Path)
		if err != nil {
			return fmt.Errorf("write item %s: %w", key, err)
		}
	}
	return nil
}

// writeFolders saves the staged folder of each row that the read kept,
// except the rows of newer. A key without a row, such as a deleted one,
// matches nothing.
func writeFolders(tx *sql.Tx, r Read, newer map[string]bool) error {
	if len(r.Folders) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`UPDATE item SET path = ? WHERE server = ? AND rating_key = ?`)
	if err != nil {
		return fmt.Errorf("prepare item folder: %w", err)
	}
	defer stmt.Close()
	for key, folder := range r.Folders {
		if folder == "" || newer[key] {
			continue
		}
		if _, err := stmt.Exec(folder, r.Server, key); err != nil {
			return fmt.Errorf("write item folder %s: %w", key, err)
		}
	}
	return nil
}

// readKey is what a read says about one rating key.
type readKey struct {
	meta     Item    // the newest metadata: from r.Items, else from an episode list
	present  bool    // the read listed the key (in r.Items or an episode list)
	inItems  bool    // r.Items holds the key, so its item reasons replace the old ones
	episodes Reasons // the episode reasons of the episode lists
}

// readKeys returns what r says about each rating key that it lists, excluding
// keys marked gone.
func readKeys(r Read) map[string]readKey {
	keys := make(map[string]readKey, len(r.Items))
	for _, it := range r.Items {
		keys[it.RatingKey] = readKey{meta: it, present: true, inItems: true}
	}
	for _, list := range []struct {
		items  []Item
		reason Reasons
	}{{r.EpisodeNoSummary, EpisodeNoSummary}, {r.EpisodeUnmatched, EpisodeUnmatched}} {
		for _, it := range list.items {
			k, ok := keys[it.RatingKey]
			if !ok {
				k.meta = it
			}
			k.present = true
			k.episodes |= list.reason
			keys[it.RatingKey] = k
		}
	}
	for _, key := range r.Gone {
		delete(keys, key)
	}
	return keys
}

// reasons returns the final reasons of a row after the read r. prev is the
// old reasons of the row, or 0 for a new row. The zero readKey stands for a
// row that the read does not list. A listed row keeps the old value of each
// reason in its Keep.
func (k readKey) reasons(r Read, prev Reasons) Reasons {
	final := prev & ItemReasons
	if k.inItems {
		keep := k.meta.Keep & ItemReasons
		final = k.meta.Reasons&^keep&ItemReasons | prev&keep
	}
	if r.Shows {
		return final | k.episodes | prev&r.EpisodeSkipped
	}
	return final | prev&^ItemReasons
}

func deleteItem(tx execer, server, key string) error {
	if _, err := tx.Exec(`DELETE FROM item WHERE server = ? AND rating_key = ?`, server, key); err != nil {
		return fmt.Errorf("delete item %s: %w", key, err)
	}
	return nil
}

func writeReadTimes(tx *sql.Tx, r Read) error {
	var err error
	if r.Full {
		_, err = tx.Exec(`INSERT INTO library (server, section, last_full_read, last_read) VALUES (?, ?, ?, ?)
			ON CONFLICT (server, section) DO UPDATE SET last_full_read = excluded.last_full_read, last_read = excluded.last_read`,
			r.Server, r.Section, nullTime(r.At), nullTime(r.At))
	} else {
		_, err = tx.Exec(`INSERT INTO library (server, section, last_read) VALUES (?, ?, ?)
			ON CONFLICT (server, section) DO UPDATE SET last_read = excluded.last_read`,
			r.Server, r.Section, nullTime(r.At))
	}
	if err != nil {
		return fmt.Errorf("write library times: %w", err)
	}
	return nil
}

// Unseen returns the rows of the section whose rating key is not in seen.
// It compares in Go because a full read can hold more keys than SQLite
// accepts as query parameters.
func (s *Store) Unseen(server, section string, seen []string) ([]Item, error) {
	stored, err := queryItems(s.db, `SELECT `+itemColumns+` FROM item WHERE server = ? AND section = ? ORDER BY rating_key`,
		server, section)
	if err != nil {
		return nil, fmt.Errorf("read stored items: %w", err)
	}

	seenSet := make(map[string]struct{}, len(seen))
	for _, k := range seen {
		seenSet[k] = struct{}{}
	}
	var unseen []Item
	for _, it := range stored {
		if _, ok := seenSet[it.RatingKey]; !ok {
			unseen = append(unseen, it)
		}
	}
	return unseen, nil
}

// backoff returns the first wait and the longest wait in milliseconds, and
// the number of doublings from the first wait that reach the longest wait.
// Milliseconds keep the fraction of a wait such as 1.5s. Due stops the
// doubling at doublings, so its shift cannot overflow.
func backoff(refreshInterval, maxRefreshInterval time.Duration) (interval, maxWait int64, doublings int) {
	interval = refreshInterval.Milliseconds()
	maxWait = max(maxRefreshInterval.Milliseconds(), interval)
	for wait := interval; wait > 0 && wait < maxWait; wait *= 2 {
		doublings++
	}
	return interval, maxWait, doublings
}

// LongestWaitRefreshes returns the number of refreshes after which the wait
// of a row is maxRefreshInterval. It returns 0 if the wait never grows.
func LongestWaitRefreshes(refreshInterval, maxRefreshInterval time.Duration) int {
	_, _, doublings := backoff(refreshInterval, maxRefreshInterval)
	if doublings == 0 {
		return 0
	}
	return doublings + 1
}

// DueQuery selects the due rows of some sections of a server.
type DueQuery struct {
	Server          string
	Sections        []string
	Now             time.Time
	MinimumAge      time.Duration // a row added after Now-MinimumAge is not due
	RefreshInterval time.Duration
	// MaxRefreshInterval is the longest wait. Below RefreshInterval, the
	// wait does not back off.
	MaxRefreshInterval time.Duration
	Ignored            Reasons // a row whose reasons are all in Ignored or ManualReasons is never due
	Limit              int     // the most rows that Due returns; 0: no limit
}

// rules returns the queue rules of q.
func (q DueQuery) rules() DueRules {
	return DueRules{Now: q.Now, MinimumAge: q.MinimumAge, RefreshInterval: q.RefreshInterval,
		MaxRefreshInterval: q.MaxRefreshInterval, Ignored: q.Ignored}
}

// Due returns the first q.Limit rows of the sections that have a reason
// outside Ignored and ManualReasons, are added at or before Now-MinimumAge, and with no attempt in their wait
// before Now. total is the number of all such rows.
//
// The wait backs off: it is RefreshInterval for a row with at most 1
// refresh, and it doubles with each later refresh, up to
// MaxRefreshInterval.
//
// Order: the fewest attempts first, then no attempt, then the oldest
// LastAttempt, then the oldest AddedAt. Each RecordRequest counts as an
// attempt, also a failed one, so an item that keeps failing moves back
// instead of taking the first place in every run.
func (s *Store) Due(q DueQuery) (items []Item, total int, err error) {
	if len(q.Sections) == 0 {
		return nil, 0, nil
	}
	rules := q.rules()
	tooNew, waiting := rules.tooNew(), rules.waiting()
	where := `server = ? AND section IN (` + placeholders(len(q.Sections)) + `)
		AND stopped_at IS NULL AND reasons & ~? != 0 AND NOT (` + tooNew.sql + `) AND NOT (` + waiting.sql + `)`
	args := append([]any{q.Server}, stringArgs(q.Sections)...)
	args = append(args, int(q.Ignored|ManualReasons))
	args = append(append(args, tooNew.args...), waiting.args...)

	// One read transaction gives the count and the rows the same snapshot,
	// also while another process writes. A read-only transaction does not
	// take the write lock.
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("begin due items: %w", err)
	}
	defer tx.Rollback()

	if err := tx.QueryRow(`SELECT COUNT(*) FROM item WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count due items: %w", err)
	}

	// SQLite reads a negative LIMIT as no limit.
	limit := q.Limit
	if limit <= 0 {
		limit = -1
	}
	items, err = queryItems(tx, `SELECT `+itemColumns+` FROM item WHERE `+where+`
		ORDER BY attempts, last_attempt IS NOT NULL, last_attempt, added_at, rating_key LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("read due items: %w", err)
	}
	return items, total, nil
}

// CountIncomplete counts the stored rows of the sections that have a reason
// outside ignored.
func (s *Store) CountIncomplete(server string, sections []string, ignored Reasons) (int, error) {
	if len(sections) == 0 {
		return 0, nil
	}
	args := append([]any{server}, stringArgs(sections)...)
	args = append(args, int(ignored))

	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM item WHERE server = ? AND section IN (`+placeholders(len(sections))+`)
		AND stopped_at IS NULL AND reasons & ~? != 0`, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count items: %w", err)
	}
	return n, nil
}

// execer runs a statement on the database or in a transaction.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// updateAttempt sets LastAttempt and counts one attempt of a row. accepted
// is the request that Plex accepted, or "" for a failed attempt. An accepted
// request also sets LastRefresh and LastAction and adds 1 to Refreshes. An
// accepted match request also sets LastMatch. A missing row is not an error.
func updateAttempt(db execer, server, ratingKey string, at time.Time, accepted Action) error {
	var err error
	switch {
	case accepted.isMatch():
		_, err = db.Exec(`UPDATE item SET last_attempt = ?, last_refresh = ?, refreshes = refreshes + 1,
			attempts = attempts + 1, last_action = ?, last_match = ? WHERE server = ? AND rating_key = ?`,
			at.Unix(), at.Unix(), accepted, accepted, server, ratingKey)
	case accepted != "":
		_, err = db.Exec(`UPDATE item SET last_attempt = ?, last_refresh = ?, refreshes = refreshes + 1,
			attempts = attempts + 1, last_action = ? WHERE server = ? AND rating_key = ?`,
			at.Unix(), at.Unix(), accepted, server, ratingKey)
	default:
		_, err = db.Exec(`UPDATE item SET last_attempt = ?, attempts = attempts + 1 WHERE server = ? AND rating_key = ?`,
			at.Unix(), server, ratingKey)
	}
	return err
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return args
}

// Pruned is one library whose cache a prune removed.
type Pruned struct {
	Server, Section string
	Items           int // the item rows that the prune removed
}

// PruneServers removes the libraries of each server that is not in servers.
// The removal of a library also removes its item rows. It also removes the
// run rows of those servers, because a run has no library.
func (s *Store) PruneServers(servers []string) ([]Pruned, error) {
	keep := make(map[string]bool, len(servers))
	for _, name := range servers {
		keep[name] = true
	}
	return s.prune("", func(server, _ string) bool { return !keep[server] }, func(tx *sql.Tx) error {
		return deleteRunsExcept(tx, servers)
	})
}

// PruneSections removes the library of each section of server that is not in
// sections, with its item rows. An empty sections removes all of them.
func (s *Store) PruneSections(server string, sections []string) ([]Pruned, error) {
	keep := make(map[string]bool, len(sections))
	for _, section := range sections {
		keep[section] = true
	}
	return s.prune(server, func(_, section string) bool { return !keep[section] }, nil)
}

// prune removes each library for which remove is true, in one transaction.
// The foreign key of item removes the item rows of the library. It reads only
// the libraries of server, or of all servers if server is "". It returns the
// removed libraries, ordered by server and section. If also is not nil, it
// runs in the same transaction after the libraries are removed.
func (s *Store) prune(server string, remove func(server, section string) bool, also func(tx *sql.Tx) error) ([]Pruned, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin prune: %w", err)
	}
	defer tx.Rollback()

	// Each item row has a library row, so the join finds every library.
	rows, err := tx.Query(`SELECT library.server, library.section, COUNT(item.rating_key)
		FROM library LEFT JOIN item ON item.server = library.server AND item.section = library.section
		WHERE ?1 = '' OR library.server = ?1
		GROUP BY library.server, library.section ORDER BY library.server, library.section`, server)
	if err != nil {
		return nil, fmt.Errorf("read libraries: %w", err)
	}
	var pruned []Pruned
	for rows.Next() {
		var p Pruned
		if err := rows.Scan(&p.Server, &p.Section, &p.Items); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read libraries: %w", err)
		}
		if remove(p.Server, p.Section) {
			pruned = append(pruned, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read libraries: %w", err)
	}

	if err := deletePrunedLibraries(tx, pruned); err != nil {
		return nil, err
	}
	if also != nil {
		if err := also(tx); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit prune: %w", err)
	}
	return pruned, nil
}

func deletePrunedLibraries(tx *sql.Tx, pruned []Pruned) error {
	bumped := map[string]bool{}
	for _, p := range pruned {
		if !bumped[p.Server] {
			if err := bumpGeneration(tx, p.Server); err != nil {
				return err
			}
			bumped[p.Server] = true
		}
		if _, err := tx.Exec(`UPDATE item SET last_match_event_id = NULL
			WHERE last_match_event_id IS NOT NULL
				AND EXISTS (SELECT 1 FROM event WHERE id = item.last_match_event_id AND server = ? AND section = ?)`,
			p.Server, p.Section); err != nil {
			return fmt.Errorf("clear pruned match events: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM library WHERE server = ? AND section = ?`, p.Server, p.Section); err != nil {
			return fmt.Errorf("prune library: %w", err)
		}
	}
	return nil
}
