package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNoItem reports that an action needs an item that is no longer cached.
var ErrNoItem = errors.New("no item row")

const eventSnapshotColumns = `server, section, rating_key, type, title, year, thumb, art, path`

func scanEventSnapshot(row scanner) (Item, error) {
	var it Item
	err := row.Scan(&it.Server, &it.Section, &it.RatingKey, &it.Type, &it.Title, &it.Year, &it.Thumb, &it.Art, &it.Path)
	return it, err
}

// KnownItem returns the row, or the latest event snapshot when there is no row.
func (s *Store) KnownItem(server, ratingKey string) (Item, bool, bool, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Item{}, false, false, fmt.Errorf("begin known item: %w", err)
	}
	defer tx.Rollback()
	it, found, err := readItem(tx, server, ratingKey)
	if err != nil || found {
		return it, found, found, err
	}
	snap, err := scanEventSnapshot(tx.QueryRowContext(ctx, `SELECT `+eventSnapshotColumns+` FROM event
 WHERE server = ? AND rating_key = ? ORDER BY at DESC, id DESC LIMIT 1`, server, ratingKey))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, false, nil
	}
	if err != nil {
		return Item{}, false, false, fmt.Errorf("read item snapshot: %w", err)
	}
	return snap, false, true, nil
}

// KnownResult holds a resolved item and whether it came from the item cache.
type KnownResult struct {
	Item   Item
	Cached bool
}

// KnownItems resolves each rating key of server in one read-only transaction,
// returning cached rows or latest event snapshots for known keys.
func (s *Store) KnownItems(ctx context.Context, server string, keys []string) (map[string]KnownResult, error) {
	out := make(map[string]KnownResult, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin known items: %w", err)
	}
	defer tx.Rollback()
	return readKnownItems(ctx, tx, server, keys, out)
}

func readKnownItems(ctx context.Context, tx *sql.Tx, server string, keys []string, out map[string]KnownResult) (map[string]KnownResult, error) {
	itemStmt, err := tx.PrepareContext(ctx, `SELECT `+itemColumns+` FROM item WHERE server = ? AND rating_key = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare known item: %w", err)
	}
	defer itemStmt.Close()
	snapStmt, err := tx.PrepareContext(ctx, `SELECT `+eventSnapshotColumns+` FROM event
 WHERE server = ? AND rating_key = ? ORDER BY at DESC, id DESC LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("prepare known snapshot: %w", err)
	}
	defer snapStmt.Close()

	for _, key := range keys {
		if _, seen := out[key]; seen {
			continue
		}
		if res, ok, err := readOneKnown(ctx, itemStmt, snapStmt, server, key); err != nil {
			return nil, err
		} else if ok {
			out[key] = res
		}
	}
	return out, nil
}

func readOneKnown(ctx context.Context, itemStmt, snapStmt *sql.Stmt, server, key string) (KnownResult, bool, error) {
	it, err := scanItem(itemStmt.QueryRowContext(ctx, server, key))
	if err == nil {
		return KnownResult{Item: it, Cached: true}, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return KnownResult{}, false, fmt.Errorf("read item %s: %w", key, err)
	}
	snap, err := scanEventSnapshot(snapStmt.QueryRowContext(ctx, server, key))
	if errors.Is(err, sql.ErrNoRows) {
		return KnownResult{}, false, nil
	}
	if err != nil {
		return KnownResult{}, false, fmt.Errorf("read item snapshot %s: %w", key, err)
	}
	return KnownResult{Item: snap, Cached: false}, true, nil
}

// SetStopped changes the stop state and records the change in the same transaction.
func (s *Store) SetStopped(ctx context.Context, server, ratingKey string, at time.Time, stop bool) (bool, error) {
	if at.IsZero() {
		return false, errors.New("set stopped: zero time")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, txError(ctx, fmt.Errorf("begin stop change: %w", err))
	}
	defer tx.Rollback()
	row, found, err := readItemContext(ctx, tx, server, ratingKey)
	if err != nil {
		return false, txError(ctx, err)
	}
	if !found {
		return false, ErrNoItem
	}
	if stop == !row.StoppedAt.IsZero() {
		return false, nil
	}
	kind, stoppedAt := EventResumed, time.Time{}
	if stop {
		kind, stoppedAt = EventStopped, at
	}
	if _, err := tx.ExecContext(ctx, `UPDATE item SET stopped_at = ? WHERE server = ? AND rating_key = ?`,
		nullTime(stoppedAt), server, ratingKey); err != nil {
		return false, txError(ctx, fmt.Errorf("update stop state: %w", err))
	}
	if _, err := insertEventContext(ctx, tx, snapshotEvent(row, at, kind, SourceManual)); err != nil {
		return false, txError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return false, txError(ctx, fmt.Errorf("commit stop change: %w", err))
	}
	return true, nil
}

func txError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// ManualRecord contains the Plex outcome and the item snapshot at admission.
type ManualRecord struct {
	Request
	Snapshot Item
	Cached   bool
	Gone     bool
}

// RecordManual records a manual Plex outcome atomically.
func (s *Store) RecordManual(m ManualRecord) error {
	if m.Source != SourceManual {
		return fmt.Errorf("record manual request: invalid source")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin manual request: %w", err)
	}
	defer tx.Rollback()
	if err := finishAttempt(tx, m.Attempt); err != nil {
		return err
	}
	if err := writeManual(tx, m); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit manual request: %w", err)
	}
	return nil
}

// writeManual writes the event and the row changes of m. Without a cached
// row, it writes the event from the snapshot of the admission.
func writeManual(tx *sql.Tx, m ManualRecord) error {
	if !m.Cached {
		return writeManualSnapshot(tx, m, false)
	}
	row, found, err := readItem(tx, m.Server, m.RatingKey)
	if err != nil {
		return err
	}
	if !found {
		return writeManualSnapshot(tx, m, m.Gone)
	}
	if m.Gone {
		e := snapshotEvent(row, m.At, EventGone, SourceManual)
		e.Action = m.Action
		if _, err := insertEvent(tx, e); err != nil {
			return err
		}
		return deleteItem(tx, m.Server, m.RatingKey)
	}
	return recordRequest(tx, m.Request, row)
}

// writeManualSnapshot writes the gone or request event of m from its
// snapshot. When the library of the snapshot is gone, for example because
// another process removed it after the send, it writes nothing, because the
// event needs its library.
func writeManualSnapshot(tx *sql.Tx, m ManualRecord, gone bool) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM library WHERE server = ? AND section = ?)`,
		m.Server, m.Snapshot.Section).Scan(&exists); err != nil {
		return fmt.Errorf("read library: %w", err)
	}
	if !exists {
		return nil
	}
	e := requestEvent(m.Request, m.Snapshot, m.Outcome())
	if gone {
		e = snapshotEvent(m.Snapshot, m.At, EventGone, SourceManual)
		e.Action = m.Action
	}
	_, err := insertEvent(tx, e)
	return err
}

// ApplyCheck updates an existing item from an individual metadata check.
// runID is the run that checked the item, which a completed event names;
// 0: none.
func (s *Store) ApplyCheck(server string, it Item, at time.Time, runID int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin item check: %w", err)
	}
	defer tx.Rollback()
	prev, found, err := readItem(tx, server, it.RatingKey)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrNoItem
	}
	if it.Type == "" {
		it.Type = prev.Type
	}
	if it.AddedAt.IsZero() {
		it.AddedAt = prev.AddedAt
	}
	r := Read{Server: server, Section: prev.Section, At: at, RunID: runID}
	in := readKey{meta: it, present: true, inItems: true}
	fixed, err := writeExisting(tx, r, prev, in)
	if err != nil {
		return false, err
	}
	if fixed {
		if err := writeCompleted(tx, r, prev, in); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit item check: %w", err)
	}
	return fixed, nil
}
