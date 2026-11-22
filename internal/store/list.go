package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DueRules are the rules that decide the queue state of a row. Due and
// ListItems both build their SQL from them, so the scheduler and the
// dashboard cannot disagree.
type DueRules struct {
	Now                                             time.Time
	MinimumAge, RefreshInterval, MaxRefreshInterval time.Duration
	Ignored                                         Reasons
}

// sqlPart is a piece of SQL with the arguments of its placeholders.
type sqlPart struct {
	sql  string
	args []any
}

// tooNew is true for a row added after Now-MinimumAge. The cutoff is rounded
// down to whole seconds, like the stored times.
func (r DueRules) tooNew() sqlPart {
	return sqlPart{`added_at > ?`, []any{r.Now.Add(-r.MinimumAge).Unix()}}
}

// waitSQL is the backoff wait of a row in milliseconds. Its placeholders
// take backoffArgs.
const waitSQL = `MIN(?, ? << MIN(MAX(refreshes - 1, 0), ?))`

func (r DueRules) backoffArgs() []any {
	interval, maxWait, doublings := backoff(r.RefreshInterval, r.MaxRefreshInterval)
	return []any{maxWait, interval, doublings}
}

// waiting is true for a row with an attempt in its wait before Now.
func (r DueRules) waiting() sqlPart {
	return sqlPart{`last_attempt IS NOT NULL AND last_attempt > (? - ` + waitSQL + `) / 1000`,
		append([]any{r.Now.UnixMilli()}, r.backoffArgs()...)}
}

// nextDue is the time in Unix milliseconds when a row stops being too new
// and waiting.
func (r DueRules) nextDue() sqlPart {
	return sqlPart{`MAX(added_at * 1000 + ?, COALESCE(last_attempt * 1000 + ` + waitSQL + `, 0))`,
		append([]any{r.MinimumAge.Milliseconds()}, r.backoffArgs()...)}
}

// state is the QueueState of a row.
func (r DueRules) state() sqlPart {
	tooNew, waiting := r.tooNew(), r.waiting()
	args := append([]any{int(r.Ignored)}, tooNew.args...)
	return sqlPart{`CASE WHEN stopped_at IS NOT NULL THEN 'stopped' WHEN reasons & ~? = 0 THEN 'ignored'
		WHEN ` + tooNew.sql + ` THEN 'too-new' WHEN ` + waiting.sql + ` THEN 'waiting' ELSE 'due' END`,
		append(args, waiting.args...)}
}

// QueueState is where a cached row stands in the refresh queue.
type QueueState string

const (
	StateDue     QueueState = "due"
	StateWaiting QueueState = "waiting" // until the end of its backoff wait
	StateTooNew  QueueState = "too-new" // until MinimumAge passes
	StateIgnored QueueState = "ignored" // all its reasons are ignored
	StateStopped QueueState = "stopped"
)

// Tab is a list of the dashboard.
type Tab string

const (
	TabAttention Tab = "attention" // cached rows that are not stopped
	TabFixed     Tab = "fixed"     // completed items
	TabStopped   Tab = "stopped"   // stopped rows
)

// SortKey is the sort column of a list.
type SortKey string

const (
	SortTitle       SortKey = "title"
	SortYear        SortKey = "year"
	SortAdded       SortKey = "added"
	SortDue         SortKey = "due"
	SortTries       SortKey = "tries"
	SortLastAttempt SortKey = "last-attempt"
	SortStopped     SortKey = "stopped"
	SortCompleted   SortKey = "completed"
)

// Cursor is the position of the last row of a page: the value of the sort
// column and the key of the row.
type Cursor struct {
	Num               int64  // the sort value of a numeric key
	Text              string // the sort value of SortTitle
	Server, RatingKey string
}

// ListQuery selects one page of a tab.
type ListQuery struct {
	Tab                   Tab
	Servers               []string // the servers to include; empty: every server
	Section, Type, Search string   // "": any
	Reason                Reasons  // 0: any; not used by TabFixed
	WithIgnored           bool     // TabAttention: also rows whose reasons are all ignored
	WithUnrequested       bool     // TabFixed: also completions without an accepted request
	Sort                  SortKey  // "": the tab default
	Desc                  bool     // used only with a Sort
	After                 *Cursor
	Limit                 int // must be > 0
	Rules                 DueRules
}

// ListItem is one row of a list.
type ListItem struct {
	Server, Section, RatingKey, Library string // Library: the cached title, "" if unknown
	Type, Title                         string
	Year                                int
	Thumb, Art, Path                    string
	Cached                              bool // an item row exists
	Reasons                             Reasons
	State                               QueueState
	NextDue                             time.Time // zero unless State is waiting or too-new
	AddedAt, LastAttempt, StoppedAt     time.Time
	Attempts, Refreshes                 int
	LastAction                          Action
	LastOutcome, LastMatchOutcome       string
	CompletedAt                         time.Time // TabFixed
	FixedAction, FixedMatch             Action    // TabFixed
}

// ErrInvalidQuery is the error of a ListQuery that ListItems cannot run.
var ErrInvalidQuery = errors.New("invalid list query")

// queueSort is a sort column of the queue tabs. text is true for a column
// whose cursor value is Cursor.Text.
type queueSort struct {
	expr sqlPart
	text bool
}

// queueSorts returns the sort columns of the queue tabs. Each is not null,
// so the keyset comparison sees every row.
func queueSorts(r DueRules) map[SortKey]queueSort {
	return map[SortKey]queueSort{
		SortTitle:       {sqlPart{sql: `item.title COLLATE NOCASE`}, true},
		SortYear:        {sqlPart{sql: `item.year`}, false},
		SortAdded:       {sqlPart{sql: `item.added_at`}, false},
		SortDue:         {r.nextDue(), false},
		SortTries:       {sqlPart{sql: `item.attempts`}, false},
		SortLastAttempt: {sqlPart{sql: `COALESCE(item.last_attempt, 0)`}, false},
		SortStopped:     {sqlPart{sql: `COALESCE(item.stopped_at, 0)`}, false},
	}
}

// queueOrder returns the sort column and the direction of a query on a
// queue tab.
func queueOrder(q ListQuery) (queueSort, bool, error) {
	key, desc := q.Sort, q.Desc
	if key == "" {
		key, desc = SortDue, false
		if q.Tab == TabStopped {
			key, desc = SortStopped, true
		}
	}
	s, ok := queueSorts(q.Rules)[key]
	if !ok {
		return queueSort{}, false, fmt.Errorf("%w: sort %q on tab %q", ErrInvalidQuery, key, q.Tab)
	}
	return s, desc, nil
}

// queryBuilder joins SQL text and keeps the arguments in the order of their
// placeholders.
type queryBuilder struct {
	sql  strings.Builder
	args []any
}

func (b *queryBuilder) add(sql string, args ...any) {
	b.sql.WriteString(sql)
	b.args = append(b.args, args...)
}

func (b *queryBuilder) part(p sqlPart) { b.add(p.sql, p.args...) }

// likeEscaper escapes the LIKE wildcards and the escape character itself.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// queueFilters adds the conditions of the tab and the filters of q.
func queueFilters(b *queryBuilder, q ListQuery) {
	if q.Tab == TabStopped {
		b.add(` WHERE item.stopped_at IS NOT NULL`)
	} else {
		b.add(` WHERE item.stopped_at IS NULL`)
		if !q.WithIgnored {
			b.add(` AND item.reasons & ~? != 0`, int(q.Rules.Ignored))
		}
	}
	if len(q.Servers) > 0 {
		b.add(` AND item.server IN (`+placeholders(len(q.Servers))+`)`, stringArgs(q.Servers)...)
	}
	if q.Section != "" {
		b.add(` AND item.section = ?`, q.Section)
	}
	if q.Type != "" {
		b.add(` AND item.type = ?`, q.Type)
	}
	if q.Reason != 0 {
		b.add(` AND item.reasons & ? != 0`, int(q.Reason))
	}
	if q.Search != "" {
		pattern := "%" + likeEscaper.Replace(q.Search) + "%"
		b.add(` AND (item.title LIKE ? ESCAPE '\' OR item.path LIKE ? ESCAPE '\')`, pattern, pattern)
	}
}

// queueQuery builds the query of one page of a queue tab. It reads one row
// more than q.Limit, to find out whether another page follows.
func queueQuery(q ListQuery, order queueSort, desc bool) (string, []any) {
	dir, cmp := "ASC", ">"
	if desc {
		dir, cmp = "DESC", "<"
	}
	var b queryBuilder
	b.add(`SELECT item.server, item.section, item.rating_key, COALESCE(library.title, ''), item.type, item.title,
		item.year, item.thumb, item.art, item.path, item.reasons, `)
	b.part(q.Rules.state())
	b.add(`, `)
	b.part(q.Rules.nextDue())
	b.add(`, item.added_at, item.last_attempt, item.stopped_at, item.attempts, item.refreshes,
		item.last_action, item.last_outcome, item.last_match_outcome, `)
	b.part(order.expr)
	b.add(` FROM item LEFT JOIN library ON library.server = item.server AND library.section = item.section`)
	queueFilters(&b, q)
	if q.After != nil {
		var value any = q.After.Num
		if order.text {
			value = q.After.Text
		}
		b.add(` AND (`)
		b.part(order.expr)
		b.add(`, item.server, item.rating_key) `+cmp+` (?, ?, ?)`, value, q.After.Server, q.After.RatingKey)
	}
	b.add(` ORDER BY `)
	b.part(order.expr)
	b.add(` `+dir+`, item.server `+dir+`, item.rating_key `+dir+` LIMIT ?`, q.Limit+1)
	return b.sql.String(), b.args
}

// scanQueueRow reads one row of queueQuery and the cursor that points at it.
func scanQueueRow(row scanner, text bool) (ListItem, Cursor, error) {
	var (
		it                     ListItem
		reasons                int
		nextDue, addedAt       int64
		lastAttempt, stoppedAt sql.NullInt64
		sortNum                int64
		sortText               string
		sortValue              any = &sortNum
	)
	if text {
		sortValue = &sortText
	}
	err := row.Scan(&it.Server, &it.Section, &it.RatingKey, &it.Library, &it.Type, &it.Title,
		&it.Year, &it.Thumb, &it.Art, &it.Path, &reasons, &it.State, &nextDue,
		&addedAt, &lastAttempt, &stoppedAt, &it.Attempts, &it.Refreshes,
		&it.LastAction, &it.LastOutcome, &it.LastMatchOutcome, sortValue)
	if err != nil {
		return ListItem{}, Cursor{}, err
	}
	it.Cached = true
	it.Reasons = Reasons(reasons)
	if it.State == StateWaiting || it.State == StateTooNew {
		it.NextDue = time.UnixMilli(nextDue)
	}
	it.AddedAt = time.Unix(addedAt, 0)
	it.LastAttempt = timeFrom(lastAttempt)
	it.StoppedAt = timeFrom(stoppedAt)
	return it, Cursor{Num: sortNum, Text: sortText, Server: it.Server, RatingKey: it.RatingKey}, nil
}

// ListItems returns one page of a tab, in the order of the sort column, then
// the server, then the rating key. next points at the last returned row, and
// it is nil when no row follows. A query that it cannot run gives an error
// that wraps ErrInvalidQuery.
func (s *Store) ListItems(ctx context.Context, q ListQuery) (items []ListItem, next *Cursor, err error) {
	if q.Limit <= 0 {
		return nil, nil, fmt.Errorf("%w: limit %d", ErrInvalidQuery, q.Limit)
	}
	if q.Tab != TabAttention && q.Tab != TabStopped {
		return nil, nil, fmt.Errorf("%w: tab %q", ErrInvalidQuery, q.Tab)
	}
	order, desc, err := queueOrder(q)
	if err != nil {
		return nil, nil, err
	}
	query, args := queueQuery(q, order, desc)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()

	items = []ListItem{}
	var last Cursor
	for rows.Next() {
		if len(items) == q.Limit {
			// The extra row: another page follows the last returned row.
			next = &last
			break
		}
		it, cursor, err := scanQueueRow(rows, order.text)
		if err != nil {
			return nil, nil, fmt.Errorf("list items: %w", err)
		}
		items = append(items, it)
		last = cursor
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list items: %w", err)
	}
	return items, next, nil
}
