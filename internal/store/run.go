package store

import (
	"database/sql"
	"fmt"
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

const RunKindRun RunKind = "run"

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
	Due, Selected                         int
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
	Due, Selected                         int
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

// FinishRun records the end and the counts of the run id.
func (s *Store) FinishRun(id int64, r RunResult) error {
	_, err := s.db.Exec(`UPDATE run SET status = ?, finished_at = ?, error = ?, read_items = ?, refreshed = ?,
		matched = ?, failed = ?, completed = ?, fixed = ?, incomplete = ?, due = ?, selected = ? WHERE id = ?`,
		r.Status, r.FinishedAt.Unix(), r.Error, r.ReadItems, r.Refreshed,
		r.Matched, r.Failed, r.Completed, r.Fixed, r.Incomplete, r.Due, r.Selected, id)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
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
		WHERE last_match_event_id IN (SELECT id FROM event WHERE at < ?)`, cutoff); err != nil {
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

// Runs returns the runs, newest first. A limit of 0 or less returns all of them.
func (s *Store) Runs(limit int) ([]Run, error) {
	rows, err := s.db.Query(`SELECT id, server, kind, source, status, started_at, finished_at, error, read_items,
		refreshed, matched, failed, completed, fixed, incomplete, due, selected
		FROM run ORDER BY started_at DESC, id DESC LIMIT ?`, sqlLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("read runs: %w", err)
	}
	defer rows.Close()
	var runs []Run
	for rows.Next() {
		var (
			r        Run
			started  int64
			finished sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.Server, &r.Kind, &r.Source, &r.Status, &started, &finished, &r.Error,
			&r.ReadItems, &r.Refreshed, &r.Matched, &r.Failed, &r.Completed, &r.Fixed, &r.Incomplete,
			&r.Due, &r.Selected); err != nil {
			return nil, fmt.Errorf("read runs: %w", err)
		}
		r.StartedAt, r.FinishedAt = time.Unix(started, 0), timeFrom(finished)
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read runs: %w", err)
	}
	return runs, nil
}
