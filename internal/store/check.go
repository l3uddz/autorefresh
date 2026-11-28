package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrCheckYielded is the refusal of the save of a check: another write, or a
// pruning, can have changed the item since the check observed it, or no
// attempt of its own action is live any more. The store saved a hold for the
// item instead, so the next full read of its library reconciles it.
var ErrCheckYielded = errors.New("check yielded to another write")

// Observation is what a check takes before it reads the metadata of an item:
// the attempts of the action that it follows up, the time, and the prune
// generations. The zero Observation makes a save that never yields.
type Observation struct {
	Chain       []int64   // the match and its follow-up refresh, or the one refresh
	At          time.Time // before the metadata read
	generations generations
	set         bool
}

// Observe returns the observation of a check of server at the time at,
// after the attempts of chain. A read-only transaction does not take the
// write lock.
func (s *Store) Observe(server string, chain []int64, at time.Time) (Observation, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Observation{}, fmt.Errorf("begin observe: %w", err)
	}
	defer tx.Rollback()
	g, err := readGenerations(tx, server)
	if err != nil {
		return Observation{}, err
	}
	return Observation{Chain: chain, At: at, generations: g, set: true}, nil
}

// checkRow returns the cached row of key for the save of a check, in the
// transaction of the save. Without a row, it returns ErrNoItem and saves
// nothing. When the check must yield, it commits a hold for the row and
// returns ErrCheckYielded.
func (s *Store) checkRow(tx *sql.Tx, server, key string, obs Observation) (Item, error) {
	prev, found, err := readItem(tx, server, key)
	if err != nil {
		return Item{}, err
	}
	if !found {
		return Item{}, ErrNoItem
	}
	ok, err := checkMaySave(tx, server, key, obs, s.now().Unix())
	if err != nil {
		return Item{}, err
	}
	if ok {
		return prev, nil
	}
	if err := insertHoldOfRow(context.Background(), tx, server, key); err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit hold: %w", err)
	}
	return Item{}, ErrCheckYielded
}

// checkMaySave reports whether a check of key with obs may save at now. It
// may not when a prune generation changed, when an attempt for the key
// outside its chain was live at or after the observation, except a plain
// refresh that Plex accepted before the chain began, or when no attempt of
// its chain is live at now. A hold does not stop it.
func checkMaySave(tx *sql.Tx, server, key string, obs Observation, now int64) (bool, error) {
	if !obs.set {
		return true, nil
	}
	if len(obs.Chain) == 0 {
		return false, nil
	}
	g, err := readGenerations(tx, server)
	if err != nil {
		return false, err
	}
	if g != obs.generations {
		return false, nil
	}
	chain := make([]any, 0, len(obs.Chain))
	first := obs.Chain[0]
	for _, id := range obs.Chain {
		chain = append(chain, id)
		first = min(first, id)
	}
	in := placeholders(len(obs.Chain))
	args := append([]any{server, key, obs.At.Unix()}, chain...)
	args = append(args, string(ActionRefresh), string(AttemptAccepted), first)
	args = append(args, chain...)
	args = append(args, now)
	var may bool
	err = tx.QueryRow(`SELECT NOT EXISTS (SELECT 1 FROM write_attempt
			WHERE server = ? AND rating_key = ? AND ends_at >= ? AND id NOT IN (`+in+`)
			AND NOT (action = ? AND outcome = ? AND answered_seq IS NOT NULL AND answered_seq < ?))
		AND EXISTS (SELECT 1 FROM write_attempt WHERE id IN (`+in+`) AND ends_at >= ?)`, args...).Scan(&may)
	if err != nil {
		return false, fmt.Errorf("check competing attempts: %w", err)
	}
	return may, nil
}

// ApplyCheckGone records that a check found the cached item gone from Plex:
// a gone event of source and runID (0: none), or with the rating key merged
// of the item that Plex merged it into after a match, the merged completion
// (removeRow), and the removal of its row, unless the check must yield
// (ErrCheckYielded). Without a row, it returns ErrNoItem and saves nothing.
// It returns the removed row and whether it wrote the merged completion.
func (s *Store) ApplyCheckGone(server, key string, at time.Time, source Source, runID int64, obs Observation,
	merged string) (removed Item, wroteMerge bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Item{}, false, fmt.Errorf("begin check gone: %w", err)
	}
	defer tx.Rollback()
	row, err := s.checkRow(tx, server, key, obs)
	if err != nil {
		return Item{}, false, err
	}
	e := snapshotEvent(row, at, EventGone, source)
	e.RunID = runID
	if wroteMerge, err = removeRow(tx, row, e, merged); err != nil {
		return Item{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, false, fmt.Errorf("commit check gone: %w", err)
	}
	return row, wroteMerge, nil
}

// AddHold saves a hold for the cached item in the library of its row. It
// saves nothing when the row, and so its library, is gone.
func (s *Store) AddHold(ctx context.Context, server, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin hold: %w", err)
	}
	defer tx.Rollback()
	if err := insertHoldOfRow(ctx, tx, server, key); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hold: %w", err)
	}
	return nil
}
