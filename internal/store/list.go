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
	args := append([]any{int(r.Ignored), int(r.Ignored | ManualReasons)}, tooNew.args...)
	return sqlPart{`CASE WHEN stopped_at IS NOT NULL THEN 'stopped' WHEN reasons & ~? = 0 THEN 'ignored'
		WHEN reasons & ~? = 0 THEN 'manual'
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
	StateManual  QueueState = "manual"  // only reasons that need a manual fix, such as a wrong match
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

// sortColumn is a sort column of a list. text is true for a column whose
// cursor value is Cursor.Text.
type sortColumn struct {
	expr sqlPart
	text bool
}

// queueSorts returns the sort columns of the queue tabs. Each is not null,
// so the keyset comparison sees every row.
func queueSorts(r DueRules) map[SortKey]sortColumn {
	return map[SortKey]sortColumn{
		SortTitle:       {sqlPart{sql: `item.title COLLATE NOCASE`}, true},
		SortYear:        {sqlPart{sql: `item.year`}, false},
		SortAdded:       {sqlPart{sql: `item.added_at`}, false},
		SortDue:         {r.nextDue(), false},
		SortTries:       {sqlPart{sql: `item.attempts`}, false},
		SortLastAttempt: {sqlPart{sql: `COALESCE(item.last_attempt, 0)`}, false},
		SortStopped:     {sqlPart{sql: `COALESCE(item.stopped_at, 0)`}, false},
	}
}

// fixedSorts are the sort columns of TabFixed, on the completed event e.
var fixedSorts = map[SortKey]sortColumn{
	SortTitle:     {sqlPart{sql: `e.title COLLATE NOCASE`}, true},
	SortYear:      {sqlPart{sql: `e.year`}, false},
	SortCompleted: {sqlPart{sql: `e.at`}, false},
}

// tabSorts returns the sort columns of tab, or nil for an unknown tab.
func tabSorts(tab Tab, r DueRules) map[SortKey]sortColumn {
	switch tab {
	case TabAttention, TabStopped:
		return queueSorts(r)
	case TabFixed:
		return fixedSorts
	default:
		return nil
	}
}

// tabDefaults are the default sort key and direction of each tab.
// Attention lists the items with the most tries first, because they need
// a person most.
var tabDefaults = map[Tab]struct {
	key  SortKey
	desc bool
}{
	TabAttention: {SortTries, true},
	TabStopped:   {SortStopped, true},
	TabFixed:     {SortCompleted, true},
}

// DefaultSort returns the sort key and the direction that a ListQuery of
// tab with an empty Sort uses. It returns "" for an unknown tab.
func DefaultSort(tab Tab) (key SortKey, desc bool) {
	d := tabDefaults[tab]
	return d.key, d.desc
}

// ValidSort reports whether ListItems can sort tab by key.
func ValidSort(tab Tab, key SortKey) bool {
	_, ok := tabSorts(tab, DueRules{})[key]
	return ok
}

// listOrder returns the sort column and the direction of q.
func listOrder(q ListQuery) (sortColumn, bool, error) {
	sorts := tabSorts(q.Tab, q.Rules)
	if sorts == nil {
		return sortColumn{}, false, fmt.Errorf("%w: tab %q", ErrInvalidQuery, q.Tab)
	}
	key, desc := q.Sort, q.Desc
	if key == "" {
		key, desc = DefaultSort(q.Tab)
	}
	s, ok := sorts[key]
	if !ok {
		return sortColumn{}, false, fmt.Errorf("%w: sort %q on tab %q", ErrInvalidQuery, key, q.Tab)
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

// commonFilters adds the filters of q that every tab has. table is the name
// of the listed table in the query.
func commonFilters(b *queryBuilder, q ListQuery, table string) {
	if len(q.Servers) > 0 {
		b.add(` AND `+table+`.server IN (`+placeholders(len(q.Servers))+`)`, stringArgs(q.Servers)...)
	}
	if q.Section != "" {
		b.add(` AND `+table+`.section = ?`, q.Section)
	}
	if q.Type != "" {
		b.add(` AND `+table+`.type = ?`, q.Type)
	}
	if q.Search != "" {
		pattern := "%" + likeEscaper.Replace(q.Search) + "%"
		b.add(` AND (`+table+`.title LIKE ? ESCAPE '\' OR `+table+`.path LIKE ? ESCAPE '\')`, pattern, pattern)
	}
}

// queueFilters adds the conditions of a queue tab and the filters of q.
func queueFilters(b *queryBuilder, q ListQuery) {
	if q.Tab == TabStopped {
		b.add(` WHERE item.stopped_at IS NOT NULL`)
	} else {
		b.add(` WHERE item.stopped_at IS NULL`)
		if !q.WithIgnored {
			b.add(` AND item.reasons & ~? != 0`, int(q.Rules.Ignored))
		}
	}
	commonFilters(b, q, "item")
	if q.Reason != 0 {
		b.add(` AND item.reasons & ? != 0`, int(q.Reason))
	}
}

// fixedFilters adds the conditions of TabFixed and the filters of q. It
// first keeps only the newest completed event of each item, in the order of
// Timeline, and filters that event, so an older completion never shows in
// place of a newer one.
func fixedFilters(b *queryBuilder, q ListQuery) {
	b.add(` WHERE e.kind = ? AND e.id = (SELECT id FROM event WHERE kind = ?
		AND server = e.server AND rating_key = e.rating_key ORDER BY at DESC, id DESC LIMIT 1)`,
		EventCompleted, EventCompleted)
	if !q.WithUnrequested {
		b.add(` AND e.fixed_action != ''`)
	}
	commonFilters(b, q, "e")
}

// pageClause adds the keyset condition of q.After, the order, and a limit of
// one row more than q.Limit, to find out whether another page follows. The
// server and the rating key of table break ties.
func pageClause(b *queryBuilder, q ListQuery, order sortColumn, desc bool, table string) {
	dir, cmp := "ASC", ">"
	if desc {
		dir, cmp = "DESC", "<"
	}
	if q.After != nil {
		var value any = q.After.Num
		if order.text {
			value = q.After.Text
		}
		b.add(` AND (`)
		b.part(order.expr)
		b.add(`, `+table+`.server, `+table+`.rating_key) `+cmp+` (?, ?, ?)`, value, q.After.Server, q.After.RatingKey)
	}
	b.add(` ORDER BY `)
	b.part(order.expr)
	b.add(` `+dir+`, `+table+`.server `+dir+`, `+table+`.rating_key `+dir+` LIMIT ?`, q.Limit+1)
}

// queueColumns adds the select list of a cached row, which scanQueueRow reads.
func queueColumns(b *queryBuilder, r DueRules) {
	b.add(`SELECT item.server, item.section, item.rating_key, COALESCE(library.title, ''), item.type, item.title,
		item.year, item.thumb, item.art, item.path, item.reasons, `)
	b.part(r.state())
	b.add(`, `)
	b.part(r.nextDue())
	b.add(`, item.added_at, item.last_attempt, item.stopped_at, item.attempts, item.refreshes,
		item.last_action, item.last_outcome, item.last_match_outcome`)
}

const queueFrom = ` FROM item LEFT JOIN library ON library.server = item.server AND library.section = item.section`

// queueQuery builds the query of one page of a queue tab.
func queueQuery(q ListQuery, order sortColumn, desc bool) (string, []any) {
	var b queryBuilder
	queueColumns(&b, q.Rules)
	b.add(`, `)
	b.part(order.expr)
	b.add(queueFrom)
	queueFilters(&b, q)
	pageClause(&b, q, order, desc, "item")
	return b.sql.String(), b.args
}

// snapshotColumns are the item snapshot of the event e and the title of its
// library, which snapshotDest reads.
const snapshotColumns = `e.server, e.section, e.rating_key, COALESCE(library.title, ''), e.type, e.title, e.year,
	e.thumb, e.art, e.path`

const eventFrom = ` FROM event e LEFT JOIN library ON library.server = e.server AND library.section = e.section`

// fixedQuery builds the query of one page of TabFixed.
func fixedQuery(q ListQuery, order sortColumn, desc bool) (string, []any) {
	var b queryBuilder
	b.add(`SELECT ` + snapshotColumns + `,
		EXISTS (SELECT 1 FROM item WHERE item.server = e.server AND item.rating_key = e.rating_key),
		e.at, e.fixed_action, e.fixed_match, `)
	b.part(order.expr)
	b.add(eventFrom)
	fixedFilters(&b, q)
	pageClause(&b, q, order, desc, "e")
	return b.sql.String(), b.args
}

// rowScanner reads one row of a list query into a ListItem, and the columns
// after it into extra.
type rowScanner func(row scanner, extra ...any) (ListItem, error)

// listQuery returns the query of one page of q and the scanner of its rows.
func listQuery(q ListQuery, order sortColumn, desc bool) (string, []any, rowScanner) {
	if q.Tab == TabFixed {
		query, args := fixedQuery(q, order, desc)
		return query, args, scanFixedRow
	}
	query, args := queueQuery(q, order, desc)
	return query, args, scanQueueRow
}

// scanQueueRow reads the queueColumns of one row, and the columns after them
// into extra.
func scanQueueRow(row scanner, extra ...any) (ListItem, error) {
	var (
		it                     ListItem
		reasons                int
		nextDue, addedAt       int64
		lastAttempt, stoppedAt sql.NullInt64
	)
	dest := append([]any{&it.Server, &it.Section, &it.RatingKey, &it.Library, &it.Type, &it.Title,
		&it.Year, &it.Thumb, &it.Art, &it.Path, &reasons, &it.State, &nextDue,
		&addedAt, &lastAttempt, &stoppedAt, &it.Attempts, &it.Refreshes,
		&it.LastAction, &it.LastOutcome, &it.LastMatchOutcome}, extra...)
	if err := row.Scan(dest...); err != nil {
		return ListItem{}, err
	}
	it.Cached = true
	it.Reasons = Reasons(reasons)
	if it.State == StateWaiting || it.State == StateTooNew {
		it.NextDue = time.UnixMilli(nextDue)
	}
	it.AddedAt = time.Unix(addedAt, 0)
	it.LastAttempt = timeFrom(lastAttempt)
	it.StoppedAt = timeFrom(stoppedAt)
	return it, nil
}

// snapshotDest returns the scan targets of snapshotColumns in it.
func snapshotDest(it *ListItem) []any {
	return []any{&it.Server, &it.Section, &it.RatingKey, &it.Library, &it.Type, &it.Title, &it.Year,
		&it.Thumb, &it.Art, &it.Path}
}

// scanFixedRow reads one row of fixedQuery before its sort value, and the
// columns after it into extra.
func scanFixedRow(row scanner, extra ...any) (ListItem, error) {
	var (
		it ListItem
		at int64
	)
	dest := append(snapshotDest(&it), &it.Cached, &at, &it.FixedAction, &it.FixedMatch)
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return ListItem{}, err
	}
	it.CompletedAt = time.Unix(at, 0)
	return it, nil
}

// ListItems returns one page of a tab, in the order of the sort column, then
// the server, then the rating key. next points at the last returned row, and
// it is nil when no row follows. A query that it cannot run gives an error
// that wraps ErrInvalidQuery.
func (s *Store) ListItems(ctx context.Context, q ListQuery) (items []ListItem, next *Cursor, err error) {
	if q.Limit <= 0 {
		return nil, nil, fmt.Errorf("%w: limit %d", ErrInvalidQuery, q.Limit)
	}
	order, desc, err := listOrder(q)
	if err != nil {
		return nil, nil, err
	}
	query, args, scan := listQuery(q, order, desc)

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
		var sortValue any = &last.Num
		if order.text {
			sortValue = &last.Text
		}
		it, err := scan(rows, sortValue)
		if err != nil {
			return nil, nil, fmt.Errorf("list items: %w", err)
		}
		items = append(items, it)
		last.Server, last.RatingKey = it.Server, it.RatingKey
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list items: %w", err)
	}
	return items, next, nil
}

// ListCounts are the numbers of rows of each tab.
type ListCounts struct{ Attention, Fixed, Stopped int }

// countQuery builds the query that counts the rows of the tab of q.
func countQuery(q ListQuery) (string, []any) {
	var b queryBuilder
	if q.Tab == TabFixed {
		b.add(`SELECT COUNT(*) FROM event e`)
		fixedFilters(&b, q)
	} else {
		b.add(`SELECT COUNT(*) FROM item`)
		queueFilters(&b, q)
	}
	return b.sql.String(), b.args
}

// CountItems counts each tab under the filters of q (Servers, Section, Type,
// Search; Reason for the queue tabs; WithIgnored and WithUnrequested for
// their tab). It ignores Tab, Sort, Desc, After, and Limit, and reads in one
// read-only transaction.
func (s *Store) CountItems(ctx context.Context, q ListQuery) (ListCounts, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ListCounts{}, fmt.Errorf("begin count items: %w", err)
	}
	defer tx.Rollback()

	var c ListCounts
	for _, tab := range []struct {
		tab Tab
		n   *int
	}{{TabAttention, &c.Attention}, {TabFixed, &c.Fixed}, {TabStopped, &c.Stopped}} {
		q.Tab = tab.tab
		query, args := countQuery(q)
		if err := tx.QueryRowContext(ctx, query, args...).Scan(tab.n); err != nil {
			return ListCounts{}, fmt.Errorf("count %s items: %w", tab.tab, err)
		}
	}
	return c, nil
}

// ItemDetail returns the item row as ListItems shows it (Cached true), or
// else the snapshot of the newest event of the item (Cached false; no
// State). found is false when neither exists. It reads in one read-only
// transaction.
func (s *Store) ItemDetail(ctx context.Context, server, ratingKey string, rules DueRules) (item ListItem, found bool, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ListItem{}, false, fmt.Errorf("begin item detail: %w", err)
	}
	defer tx.Rollback()

	var b queryBuilder
	queueColumns(&b, rules)
	b.add(queueFrom+` WHERE item.server = ? AND item.rating_key = ?`, server, ratingKey)
	switch item, err = scanQueueRow(tx.QueryRowContext(ctx, b.sql.String(), b.args...)); {
	case err == nil:
		return item, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return ListItem{}, false, fmt.Errorf("read item detail: %w", err)
	}

	// The row is gone: show the snapshot of the newest event.
	item = ListItem{}
	err = tx.QueryRowContext(ctx, `SELECT `+snapshotColumns+eventFrom+`
		WHERE e.server = ? AND e.rating_key = ? ORDER BY e.at DESC, e.id DESC LIMIT 1`,
		server, ratingKey).Scan(snapshotDest(&item)...)
	if errors.Is(err, sql.ErrNoRows) {
		return ListItem{}, false, nil
	}
	if err != nil {
		return ListItem{}, false, fmt.Errorf("read item snapshot: %w", err)
	}
	return item, true, nil
}
