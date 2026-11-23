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
	e, err := scanEvent(tx.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM event
 WHERE server = ? AND rating_key = ? ORDER BY at DESC, id DESC LIMIT 1`, server, ratingKey))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, false, nil
	}
	if err != nil {
		return Item{}, false, false, fmt.Errorf("read item snapshot: %w", err)
	}
	return Item{Server: e.Server, Section: e.Section, RatingKey: e.RatingKey, Type: e.Type, Title: e.Title,
		Year: e.Year, Thumb: e.Thumb, Art: e.Art, Path: e.Path}, false, true, nil
}

// SetStopped changes the stop state and records the change in the same transaction.
func (s *Store) SetStopped(ctx context.Context, server, ratingKey string, at time.Time, stop bool) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, txError(ctx, fmt.Errorf("begin stop change: %w", err))
	}
	defer tx.Rollback()
	row, found, err := readItem(tx, server, ratingKey)
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
	if _, err := insertEvent(tx, snapshotEvent(row, at, kind, SourceManual)); err != nil {
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
	if err := writeManual(tx, m); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit manual request: %w", err)
	}
	return nil
}

func writeManual(tx *sql.Tx, m ManualRecord) error {
	if !m.Cached {
		_, err := insertEvent(tx, requestEvent(m.Request, m.Snapshot, m.Outcome()))
		return err
	}
	row, found, err := readItem(tx, m.Server, m.RatingKey)
	if err != nil {
		return err
	}
	if !found {
		row = m.Snapshot
	}
	if m.Gone {
		if _, err := insertEvent(tx, snapshotEvent(row, m.At, EventGone, SourceManual)); err != nil {
			return err
		}
		if found {
			return deleteItem(tx, m.Server, m.RatingKey)
		}
		return nil
	}
	if found {
		return recordRequest(tx, m.Request, row)
	}
	_, err = insertEvent(tx, requestEvent(m.Request, row, m.Outcome()))
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
