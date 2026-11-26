package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunSource is what started a run.
type RunSource string

const (
	RunSourceSchedule RunSource = "schedule"
	RunSourceManual   RunSource = "manual"
)

// RunKind is the kind of work of a run.
type RunKind string

const (
	RunKindRun         RunKind = "run"
	RunKindBulkRefresh RunKind = "bulk-refresh"
)

// RunStatus is the state of a run.
type RunStatus string

const (
	RunRunning     RunStatus = "running"
	RunFinished    RunStatus = "finished"
	RunFailed      RunStatus = "failed"
	RunCanceled    RunStatus = "canceled"
	RunInterrupted RunStatus = "interrupted"
)

// RunStart is what StartRun records.
type RunStart struct {
	Server    string
	Kind      RunKind
	Source    RunSource
	StartedAt time.Time
}

// RunResult is what FinishRun records.
type RunResult struct {
	FinishedAt                            time.Time
	Status                                RunStatus
	Error                                 string
	ReadItems, Refreshed, Matched, Failed int
	Completed, Fixed, Incomplete          int
	Due, Selected, Skipped                int
	FullReads, IncrementalReads           int // the libraries read fully and incrementally
}

// Run is one run of a server, with its counts.
type Run struct {
	ID                                    int64
	Server                                string
	Kind                                  RunKind
	Source                                RunSource
	Status                                RunStatus
	StartedAt                             time.Time
	FinishedAt                            time.Time // zero while running
	Error                                 string
	ReadItems, Refreshed, Matched, Failed int
	Completed, Fixed, Incomplete          int
	Due, Selected, Skipped                int
	FullReads, IncrementalReads           int  // the libraries read fully and incrementally
	EventsLinked                          bool // false for a run from before runs linked their events
}

// StartRun records a run with the status running and returns its ID.
func (s *Store) StartRun(r RunStart) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO run (server, kind, source, status, started_at) VALUES (?, ?, ?, ?, ?)`,
		r.Server, r.Kind, r.Source, RunRunning, r.StartedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	return id, nil
}

// FinishRun records the end and the counts of the run id, which StartRun
// stored from start. If retention removed the row meanwhile (for example
// after the scheduled service marked a still-running --once check
// interrupted), FinishRun stores it again under the same ID, so a finished
// run is not lost. A run of a server that PruneServers removed stays
// removed: the row comes back only while the server still has a library
// row. It runs in one transaction.
func (s *Store) FinishRun(id int64, start RunStart, r RunResult) (bool, error) {
	return s.FinishJob(id, start, r, nil)
}

// FinishJob stores the final run counts and item results in one transaction.
func (s *Store) FinishJob(id int64, start RunStart, r RunResult, items []RunItem) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin finish run: %w", err)
	}
	defer tx.Rollback()
	written, err := finishRunRow(tx, id, start, r)
	if err != nil || !written {
		return false, err
	}
	if len(items) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO run_item (run_id, position, rating_key, title, outcome, error)
   VALUES (?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return false, fmt.Errorf("prepare run item: %w", err)
		}
		defer stmt.Close()
		for _, item := range items {
			if _, err := stmt.Exec(id, item.Position, item.RatingKey, item.Title, item.Outcome, item.Error); err != nil {
				return false, fmt.Errorf("record run item: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit finish run: %w", err)
	}
	return true, nil
}

func finishRunRow(tx *sql.Tx, id int64, start RunStart, r RunResult) (bool, error) {
	res, err := tx.Exec(`UPDATE run SET status = ?, finished_at = ?, error = ?, read_items = ?, refreshed = ?,
  matched = ?, failed = ?, completed = ?, fixed = ?, incomplete = ?, due = ?, selected = ?, skipped = ?,
  full_reads = ?, incremental_reads = ? WHERE id = ?`,
		r.Status, r.FinishedAt.Unix(), r.Error, r.ReadItems, r.Refreshed,
		r.Matched, r.Failed, r.Completed, r.Fixed, r.Incomplete, r.Due, r.Selected, r.Skipped,
		r.FullReads, r.IncrementalReads, id)
	if err != nil {
		return false, fmt.Errorf("finish run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("finish run: %w", err)
	}
	if n != 0 {
		return true, nil
	}
	return restoreRun(tx, id, start, r)
}

// restoreRun stores the finished run id again, unless its server no longer
// has a library row. Its events were unlinked when retention removed the row,
// so events_linked is recorded as 0.
func restoreRun(tx *sql.Tx, id int64, start RunStart, r RunResult) (bool, error) {
	res, err := tx.Exec(`INSERT INTO run (id, server, kind, source, started_at, status, finished_at, error,
  read_items, refreshed, matched, failed, completed, fixed, incomplete, due, selected, skipped,
  full_reads, incremental_reads, events_linked)
  SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0
  WHERE EXISTS (SELECT 1 FROM library WHERE server = ?)`,
		id, start.Server, start.Kind, start.Source, start.StartedAt.Unix(), r.Status, r.FinishedAt.Unix(), r.Error,
		r.ReadItems, r.Refreshed, r.Matched, r.Failed, r.Completed, r.Fixed, r.Incomplete, r.Due, r.Selected, r.Skipped,
		r.FullReads, r.IncrementalReads, start.Server)
	if err != nil {
		return false, fmt.Errorf("restore run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("restore run: %w", err)
	}
	return n != 0, nil
}

// InterruptRuns marks every running run as interrupted, with the end time at.
// It returns the number of runs that it changed.
func (s *Store) InterruptRuns(at time.Time) (int, error) {
	res, err := s.db.Exec(`UPDATE run SET status = ?, finished_at = ? WHERE status = ?`,
		RunInterrupted, at.Unix(), RunRunning)
	if err != nil {
		return 0, fmt.Errorf("interrupt runs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("interrupt runs: %w", err)
	}
	return int(n), nil
}

// PruneHistory removes the events that are older than before, with their
// searches and candidates, and the runs that ended before it. A running
// row stays, however long it runs. An interrupted row counts from the time
// it was marked, so a process that still runs it (for example --once
// beside the service) can finish it later. It first
// clears the last match event of each item row that points to a removed
// event. It runs in one transaction and returns the numbers of removed
// events and runs.
func (s *Store) PruneHistory(before time.Time) (events, runs int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin prune history: %w", err)
	}
	defer tx.Rollback()

	cutoff := before.Unix()
	if _, err := tx.Exec(`UPDATE item SET last_match_event_id = NULL
		WHERE last_match_event_id IS NOT NULL
			AND EXISTS (SELECT 1 FROM event WHERE id = item.last_match_event_id AND at < ?)`, cutoff); err != nil {
		return 0, 0, fmt.Errorf("clear match events: %w", err)
	}
	if events, err = deleteBefore(tx, `DELETE FROM event WHERE at < ?`, cutoff); err != nil {
		return 0, 0, fmt.Errorf("prune events: %w", err)
	}
	if runs, err = deleteBefore(tx, `DELETE FROM run WHERE status != ? AND COALESCE(finished_at, started_at) < ?`, RunRunning, cutoff); err != nil {
		return 0, 0, fmt.Errorf("prune runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit prune history: %w", err)
	}
	return events, runs, nil
}

// deleteBefore runs the DELETE statement with args and returns the number of rows.
func deleteBefore(tx *sql.Tx, query string, args ...any) (int, error) {
	res, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// deleteRunsExcept removes the runs of every server that is not in servers.
// An empty servers removes every run.
func deleteRunsExcept(tx *sql.Tx, servers []string) error {
	query := `DELETE FROM run`
	if len(servers) > 0 {
		query += ` WHERE server NOT IN (` + placeholders(len(servers)) + `)`
	}
	if _, err := tx.Exec(query, stringArgs(servers)...); err != nil {
		return fmt.Errorf("prune runs: %w", err)
	}
	return nil
}

// sqlLimit maps a limit of 0 or less to -1, which SQLite reads as no limit.
func sqlLimit(limit int) int {
	if limit <= 0 {
		return -1
	}
	return limit
}

// RunCursor is the position of a run in the order of Runs.
type RunCursor struct {
	StartedAt time.Time
	ID        int64
}

// RunQuery selects runs.
type RunQuery struct {
	Kind    RunKind    // empty: any
	Servers []string   // empty: every server
	Status  RunStatus  // "": any
	Before  *RunCursor // only the runs that follow this one in the order of Runs
	Limit   int        // <= 0: all
}

const runColumns = `id, server, kind, source, status, started_at, finished_at, error, read_items,
	refreshed, matched, failed, completed, fixed, incomplete, due, selected, skipped, full_reads, incremental_reads,
	events_linked`

// runsQuery builds the query of q.
func runsQuery(q RunQuery) (string, []any) {
	var (
		conds []string
		args  []any
	)
	if len(q.Servers) > 0 {
		conds = append(conds, `server IN (`+placeholders(len(q.Servers))+`)`)
		args = append(args, stringArgs(q.Servers)...)
	}
	if q.Kind != "" {
		conds = append(conds, `kind = ?`)
		args = append(args, q.Kind)
	}
	if q.Status != "" {
		conds = append(conds, `status = ?`)
		args = append(args, q.Status)
	}
	if q.Before != nil {
		conds = append(conds, `(started_at, id) < (?, ?)`)
		args = append(args, q.Before.StartedAt.Unix(), q.Before.ID)
	}
	query := `SELECT ` + runColumns + ` FROM run`
	if len(conds) > 0 {
		query += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	query += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	return query, append(args, sqlLimit(q.Limit))
}

func scanRun(row scanner) (Run, error) {
	var (
		r        Run
		started  int64
		finished sql.NullInt64
	)
	if err := row.Scan(&r.ID, &r.Server, &r.Kind, &r.Source, &r.Status, &started, &finished, &r.Error,
		&r.ReadItems, &r.Refreshed, &r.Matched, &r.Failed, &r.Completed, &r.Fixed, &r.Incomplete,
		&r.Due, &r.Selected, &r.Skipped, &r.FullReads, &r.IncrementalReads, &r.EventsLinked); err != nil {
		return Run{}, err
	}
	r.StartedAt, r.FinishedAt = time.Unix(started, 0), timeFrom(finished)
	return r, nil
}

// Runs returns the runs that q selects, ordered started_at DESC, id DESC.
func (s *Store) Runs(ctx context.Context, q RunQuery) ([]Run, error) {
	query, args := runsQuery(q)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read runs: %w", err)
	}
	defer rows.Close()
	var runs []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("read runs: %w", err)
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read runs: %w", err)
	}
	return runs, nil
}

// RunDetail is one run with the events that it wrote and the items of a
// bulk job.
type RunDetail struct {
	Run    Run
	Events []Event
	Items  []RunItem
}

// querier is a database or a transaction to read from.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// RunDetail returns the run id with its events, ordered at ASC, id ASC and
// without the searches of match decisions, and its bulk items, or false if
// there is no such run. One read transaction gives them the same snapshot,
// so a run that ends or is removed meanwhile gives no mixed answer.
func (s *Store) RunDetail(ctx context.Context, id int64) (RunDetail, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RunDetail{}, false, fmt.Errorf("begin run detail: %w", err)
	}
	defer tx.Rollback()

	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM run WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RunDetail{}, false, nil
	}
	if err != nil {
		return RunDetail{}, false, fmt.Errorf("read run: %w", err)
	}
	events, err := runEvents(ctx, tx, id)
	if err != nil {
		return RunDetail{}, false, err
	}
	items, err := runItems(ctx, tx, id)
	if err != nil {
		return RunDetail{}, false, err
	}
	return RunDetail{Run: run, Events: events, Items: items}, true, nil
}

func runEvents(ctx context.Context, q querier, runID int64) ([]Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+eventColumns+` FROM event WHERE run_id = ? ORDER BY at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("read run events: %w", err)
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("read run event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read run events: %w", err)
	}
	return events, nil
}

// Outcomes of bulk refresh items.
const (
	ItemAccepted   = "accepted"
	ItemFailed     = "failed"
	ItemGone       = "gone"
	ItemStopped    = "stopped"
	ItemChanged    = "changed"
	ItemNotCached  = "not-cached"
	ItemNotReached = "not-reached"
)

// RunItem records the outcome of one selected item of a bulk job.
type RunItem struct {
	Position         int
	RatingKey, Title string
	Outcome, Error   string
	// Section, Type, Year, and Thumb come from the cached row of the item,
	// or else from its newest event, when the items are read. FinishJob
	// does not store them.
	Section, Type, Thumb string
	Year                 int
}

// runItemsQuery reads the bulk outcomes of a run in selection order, each
// with the newest snapshot of its item: the cached row, or else the newest
// event, which it looks up only for an item without a cached row.
const runItemsQuery = `SELECT ri.position, ri.rating_key, ri.title, ri.outcome, ri.error,
	COALESCE(i.section, e.section, ''), COALESCE(i.type, e.type, ''), COALESCE(i.thumb, e.thumb, ''), COALESCE(i.year, e.year, 0)
  FROM run_item ri
  JOIN run r ON r.id = ri.run_id
  LEFT JOIN item i ON i.server = r.server AND i.rating_key = ri.rating_key
  LEFT JOIN event e ON i.rating_key IS NULL AND e.id = (SELECT id FROM event
    WHERE server = r.server AND rating_key = ri.rating_key ORDER BY at DESC, id DESC LIMIT 1)
  WHERE ri.run_id = ? ORDER BY ri.position`

// RunItems returns the bulk outcomes in selection order.
func (s *Store) RunItems(ctx context.Context, runID int64) ([]RunItem, error) {
	return runItems(ctx, s.db, runID)
}

// RunItemsWithServer returns the server name and bulk outcomes of runID in one
// read-only transaction, or found=false when no such run exists.
func (s *Store) RunItemsWithServer(ctx context.Context, runID int64) (string, []RunItem, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", nil, false, fmt.Errorf("begin run items: %w", err)
	}
	defer tx.Rollback()

	var server string
	err = tx.QueryRowContext(ctx, `SELECT server FROM run WHERE id = ?`, runID).Scan(&server)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, fmt.Errorf("read run server: %w", err)
	}
	items, err := runItems(ctx, tx, runID)
	if err != nil {
		return "", nil, false, err
	}
	return server, items, true, nil
}

func runItems(ctx context.Context, q querier, runID int64) ([]RunItem, error) {
	rows, err := q.QueryContext(ctx, runItemsQuery, runID)
	if err != nil {
		return nil, fmt.Errorf("read run items: %w", err)
	}
	defer rows.Close()
	items := []RunItem{}
	for rows.Next() {
		var item RunItem
		if err := rows.Scan(&item.Position, &item.RatingKey, &item.Title, &item.Outcome, &item.Error,
			&item.Section, &item.Type, &item.Thumb, &item.Year); err != nil {
			return nil, fmt.Errorf("read run item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read run items: %w", err)
	}
	return items, nil
}
