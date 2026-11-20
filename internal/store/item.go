package store

import (
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
	LastRefresh, LastAttempt   time.Time // zero: never
	Refreshes                  int
}

// Read is the staged result of all requests for one library.
type Read struct {
	Server, Section  string
	At               time.Time // the start of the read
	Full             bool
	Items            []Item   // the full or incremental read, with item reasons only
	Gone             []string // full read only: rating keys that Plex answered with 404
	Shows            bool     // a show library: replace both episode reasons
	EpisodeNoSummary []Item   // the series of the first episode request
	EpisodeUnmatched []Item   // the series of the second episode request
}

const itemColumns = `server, section, rating_key, type, title, year, added_at, reasons, last_refresh, last_attempt, refreshes`

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
	)
	err := row.Scan(&it.Server, &it.Section, &it.RatingKey, &it.Type, &it.Title, &it.Year,
		&addedAt, &reasons, &lastRefresh, &lastAttempt, &it.Refreshes)
	if err != nil {
		return Item{}, err
	}
	it.AddedAt = time.Unix(addedAt, 0)
	it.Reasons = Reasons(reasons)
	it.LastRefresh = timeFrom(lastRefresh)
	it.LastAttempt = timeFrom(lastAttempt)
	return it, nil
}

// queryItems runs query and reads all rows before it returns.
func queryItems(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, query string, args ...any) ([]Item, error) {
	rows, err := q.Query(query, args...)
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
// lists (or else the old episode bits). It inserts rows for new items with
// final reasons that are not 0, deletes the rows whose final reasons are 0
// and returns them as fixed, deletes the r.Gone rows (not in fixed), and
// sets the read times. LastFullRead changes only if r.Full. The caller must
// set AddedAt on every item, because a zero time is stored as a negative number.
// A row of the same rating key in another section moves to r.Section and
// keeps its history and its reasons that the read does not replace.
func (s *Store) ApplyRead(r Read) (fixed []Item, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin read: %w", err)
	}
	defer tx.Rollback()

	// The newest metadata for each rating key: r.Items first, then the episode lists.
	latest := map[string]Item{}
	itemBits := map[string]Reasons{}
	inItems := map[string]bool{}
	for _, it := range r.Items {
		latest[it.RatingKey] = it
		itemBits[it.RatingKey] = it.Reasons & ItemReasons
		inItems[it.RatingKey] = true
	}
	episodeBits := map[string]Reasons{}
	for _, list := range []struct {
		items  []Item
		reason Reasons
	}{{r.EpisodeNoSummary, EpisodeNoSummary}, {r.EpisodeUnmatched, EpisodeUnmatched}} {
		for _, it := range list.items {
			if _, ok := latest[it.RatingKey]; !ok {
				latest[it.RatingKey] = it
			}
			episodeBits[it.RatingKey] |= list.reason
		}
	}

	// The old rows are the rows of the section, and the rows of any section
	// whose rating key is in the read (a moved item). The server's rows are
	// filtered in Go because a read can hold more keys than SQLite accepts
	// as query parameters.
	stored, err := queryItems(tx, `SELECT `+itemColumns+` FROM item WHERE server = ?`, r.Server)
	if err != nil {
		return nil, fmt.Errorf("read stored items: %w", err)
	}
	oldByKey := map[string]Item{}
	for _, it := range stored {
		if _, incoming := latest[it.RatingKey]; incoming || it.Section == r.Section {
			oldByKey[it.RatingKey] = it
		}
	}
	gone := make(map[string]bool, len(r.Gone))
	for _, k := range r.Gone {
		gone[k] = true
	}

	keys := map[string]bool{}
	for k := range oldByKey {
		keys[k] = true
	}
	for k := range latest {
		keys[k] = true
	}

	deleted := map[string]bool{}
	for key := range keys {
		prev, exists := oldByKey[key]

		final := itemBits[key]
		if !inItems[key] {
			final = prev.Reasons & ItemReasons
		}
		if r.Shows {
			final |= episodeBits[key]
		} else {
			final |= prev.Reasons &^ ItemReasons
		}

		meta, hasMeta := latest[key]
		switch {
		case exists && final == 0:
			if err := deleteItem(tx, r.Server, key); err != nil {
				return nil, err
			}
			deleted[key] = true
			if !gone[key] {
				fixed = append(fixed, prev)
			}
		case exists && hasMeta:
			_, err = tx.Exec(`UPDATE item SET section = ?, type = ?, title = ?, year = ?, added_at = ?, reasons = ?
				WHERE server = ? AND rating_key = ?`,
				r.Section, meta.Type, meta.Title, meta.Year, meta.AddedAt.Unix(), int(final), r.Server, key)
		case exists:
			_, err = tx.Exec(`UPDATE item SET reasons = ? WHERE server = ? AND rating_key = ?`, int(final), r.Server, key)
		case final != 0:
			// A row of the same key in another section moves here and keeps its history.
			_, err = tx.Exec(`INSERT INTO item (server, section, rating_key, type, title, year, added_at, reasons)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (server, rating_key) DO UPDATE SET section = excluded.section, type = excluded.type,
					title = excluded.title, year = excluded.year, added_at = excluded.added_at, reasons = excluded.reasons`,
				r.Server, r.Section, key, meta.Type, meta.Title, meta.Year, meta.AddedAt.Unix(), int(final))
		}
		if err != nil {
			return nil, fmt.Errorf("write item %s: %w", key, err)
		}
	}

	// The caller never puts a key in both Items and Gone, so no row is updated and then deleted.
	for _, key := range r.Gone {
		if deleted[key] {
			continue
		}
		if err := deleteItem(tx, r.Server, key); err != nil {
			return nil, err
		}
	}

	sort.Slice(fixed, func(i, j int) bool { return fixed[i].RatingKey < fixed[j].RatingKey })

	if err := writeReadTimes(tx, r); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit read: %w", err)
	}
	return fixed, nil
}

func deleteItem(tx *sql.Tx, server, key string) error {
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

// Due returns the rows of the sections that are incomplete, added at or
// before now-minimumAge, and with no attempt in their wait before now.
//
// The wait backs off: it is refreshInterval for a row with at most 1
// refresh, and it doubles with each later refresh, up to
// maxRefreshInterval. A maxRefreshInterval below refreshInterval means no
// backoff. A row whose reasons are all in ignored is never due.
//
// Order: the fewest Refreshes first, then no attempt, then the oldest
// LastAttempt, then the oldest AddedAt.
func (s *Store) Due(server string, sections []string, now time.Time, minimumAge, refreshInterval, maxRefreshInterval time.Duration, ignored Reasons) ([]Item, error) {
	if len(sections) == 0 {
		return nil, nil
	}
	// The wait is in milliseconds, so a wait such as 1.5s keeps its
	// fraction. The cutoff is then rounded down to whole seconds, like the
	// stored times.
	interval := refreshInterval.Milliseconds()
	maxWait := max(maxRefreshInterval.Milliseconds(), interval)
	// doublings is the number of doublings that reach maxWait. The shift in
	// the query stops there, so it cannot overflow.
	doublings := 0
	for wait := interval; wait > 0 && wait < maxWait; wait *= 2 {
		doublings++
	}

	args := append([]any{server}, stringArgs(sections)...)
	args = append(args, int(ignored), now.Add(-minimumAge).Unix(), now.UnixMilli(), maxWait, interval, doublings)

	items, err := queryItems(s.db, `SELECT `+itemColumns+` FROM item
		WHERE server = ? AND section IN (`+placeholders(len(sections))+`)
		AND reasons & ~? != 0 AND added_at <= ?
		AND (last_attempt IS NULL OR last_attempt <= (? - MIN(?, ? << MIN(MAX(refreshes - 1, 0), ?))) / 1000)
		ORDER BY refreshes, last_attempt IS NOT NULL, last_attempt, added_at, rating_key`, args...)
	if err != nil {
		return nil, fmt.Errorf("read due items: %w", err)
	}
	return items, nil
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
		AND reasons & ~? != 0`, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count items: %w", err)
	}
	return n, nil
}

// RecordAttempt sets LastAttempt. If refreshed, it also sets LastRefresh
// and adds 1 to Refreshes.
func (s *Store) RecordAttempt(server, ratingKey string, at time.Time, refreshed bool) error {
	var err error
	if refreshed {
		_, err = s.db.Exec(`UPDATE item SET last_attempt = ?, last_refresh = ?, refreshes = refreshes + 1
			WHERE server = ? AND rating_key = ?`, at.Unix(), at.Unix(), server, ratingKey)
	} else {
		_, err = s.db.Exec(`UPDATE item SET last_attempt = ? WHERE server = ? AND rating_key = ?`,
			at.Unix(), server, ratingKey)
	}
	if err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	return nil
}

// Delete removes a row. A missing row is not an error.
func (s *Store) Delete(server, ratingKey string) error {
	if _, err := s.db.Exec(`DELETE FROM item WHERE server = ? AND rating_key = ?`, server, ratingKey); err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	return nil
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
