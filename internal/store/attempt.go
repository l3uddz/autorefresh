package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrHeld is the refusal of the save of an automatic write: the item has a
// hold, another live write attempt, or no row in a library that the run read.
var ErrHeld = errors.New("item is held by a write attempt or a hold")

// attemptAllowance is how long after the request timeout an attempt still
// protects its item: the assumption of how late Plex applies a write.
const attemptAllowance int64 = 600

// attemptRetention is how long after its end an attempt stays, so a long
// read can still see it.
const attemptRetention = 24 * time.Hour

// AttemptOutcome is what the answer of a write says about the write.
type AttemptOutcome string

const (
	// AttemptAccepted is a 2xx answer.
	AttemptAccepted AttemptOutcome = "accepted"
	// AttemptRejected is an answer that says that Plex refused the write. Its
	// attempt is deleted.
	AttemptRejected AttemptOutcome = "rejected"
	// AttemptUnknown is every other result. Plex can have taken the write.
	AttemptUnknown AttemptOutcome = "unknown"
)

// AttemptResult is the attempt of one sent write and its outcome. The zero
// AttemptResult is no attempt, and recording it changes no attempt.
type AttemptResult struct {
	ID      int64
	Outcome AttemptOutcome
}

// AttemptSpec describes the write that an attempt protects.
type AttemptSpec struct {
	Server, RatingKey string
	Action            Action
	Timeout           time.Duration // the request timeout of the server
	// Automatic marks a write of a run. Its save first checks that the item
	// has no hold, no other live attempt, and a row in one of Sections.
	Automatic bool
	Sections  []string // the libraries whose read the run applied
	// FollowsUp is the attempt of the accepted match that this refresh
	// follows up, or 0. The admission ignores it, and a refusal of the
	// refresh saves a hold for the item.
	FollowsUp int64
}

// SetClock sets the clock that BeginAttempt and the saves of checks read
// inside their transactions. Tests set the fake clock of their refresher.
func (s *Store) SetClock(now func() time.Time) { s.clock = now }

// now returns the time of the clock of the store.
func (s *Store) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

// BeginAttempt saves the write attempt of spec in one immediate
// transaction and returns its id. The start is the time inside the
// transaction, after any lock wait, and the end is the start plus the
// timeout plus the allowance, rounded up to whole seconds and saturated.
// An automatic write that the admission refuses saves nothing and returns
// ErrHeld; a refused follow-up refresh saves a hold for its item instead.
func (s *Store) BeginAttempt(ctx context.Context, spec AttemptSpec) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin attempt: %w", err)
	}
	defer tx.Rollback()

	started := s.now().Unix()
	if spec.Automatic {
		if err := admit(ctx, tx, spec, started); err != nil {
			if errors.Is(err, ErrHeld) && spec.FollowsUp != 0 {
				return 0, holdRefused(ctx, tx, spec, err)
			}
			return 0, err
		}
	}
	ends := satAdd(satAdd(started, ceilSeconds(spec.Timeout)), attemptAllowance)
	res, err := tx.ExecContext(ctx, `INSERT INTO write_attempt (server, rating_key, action, started_at, ends_at)
		VALUES (?, ?, ?, ?, ?)`, spec.Server, spec.RatingKey, string(spec.Action), started, ends)
	if err != nil {
		return 0, fmt.Errorf("save attempt: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("save attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit attempt: %w", err)
	}
	return id, nil
}

// admit returns ErrHeld when the item of the automatic write spec has a
// hold, a live attempt other than the one it follows up, or no row in one
// of the read sections of the run.
func admit(ctx context.Context, tx *sql.Tx, spec AttemptSpec, now int64) error {
	args := []any{spec.Server, spec.RatingKey, spec.Server, spec.RatingKey, now, spec.FollowsUp, spec.Server, spec.RatingKey}
	args = append(args, stringArgs(spec.Sections)...)
	inSections := `0`
	if len(spec.Sections) > 0 {
		inSections = `section IN (` + placeholders(len(spec.Sections)) + `)`
	}
	var refused bool
	err := tx.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM read_hold WHERE server = ? AND rating_key = ?)
		OR EXISTS (SELECT 1 FROM write_attempt WHERE server = ? AND rating_key = ? AND ends_at >= ? AND id != ?)
		OR NOT EXISTS (SELECT 1 FROM item WHERE server = ? AND rating_key = ? AND `+inSections+`)`, args...).Scan(&refused)
	if err != nil {
		return fmt.Errorf("check attempt admission: %w", err)
	}
	if refused {
		return ErrHeld
	}
	return nil
}

// holdRefused saves a hold for the item of the refused follow-up refresh
// spec, in the library of its row, and commits it. It returns refusal, or
// the error of the save.
func holdRefused(ctx context.Context, tx *sql.Tx, spec AttemptSpec, refusal error) error {
	if err := insertHoldOfRow(ctx, tx, spec.Server, spec.RatingKey); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hold: %w", err)
	}
	return refusal
}

// insertHoldOfRow saves a hold for the key in the library of its cached
// row. Without a row, it saves nothing.
func insertHoldOfRow(ctx context.Context, tx *sql.Tx, server, key string) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO read_hold (server, section, rating_key)
		SELECT server, section, rating_key FROM item WHERE server = ? AND rating_key = ?`, server, key); err != nil {
		return fmt.Errorf("save hold: %w", err)
	}
	return nil
}

// finishAttempt saves the outcome of the attempt of a, in the transaction
// that records its request: it deletes a rejected attempt and keeps an
// accepted or unknown one until its end, with the newest attempt id as its
// answered_seq. The zero AttemptResult changes nothing.
func finishAttempt(tx *sql.Tx, a AttemptResult) error {
	if a.ID == 0 {
		return nil
	}
	var err error
	if a.Outcome == AttemptRejected {
		_, err = tx.Exec(`DELETE FROM write_attempt WHERE id = ?`, a.ID)
	} else {
		_, err = tx.Exec(`UPDATE write_attempt SET outcome = ?,
			answered_seq = (SELECT seq FROM sqlite_sequence WHERE name = 'write_attempt') WHERE id = ?`, string(a.Outcome), a.ID)
	}
	if err != nil {
		return fmt.Errorf("record attempt outcome: %w", err)
	}
	return nil
}

// DropAttempt deletes the attempt id of a write that was not sent.
func (s *Store) DropAttempt(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM write_attempt WHERE id = ?`, id); err != nil {
		return fmt.Errorf("drop attempt: %w", err)
	}
	return nil
}

// PruneAttempts deletes the attempts that ended more than a day before
// now, and increments the generation of every server when it deletes any.
// It returns the number of deleted attempts.
func (s *Store) PruneAttempts(now time.Time) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin prune attempts: %w", err)
	}
	defer tx.Rollback()

	n, err := deleteBefore(tx, `DELETE FROM write_attempt WHERE ends_at < ?`, now.Add(-attemptRetention).Unix())
	if err != nil {
		return 0, fmt.Errorf("prune attempts: %w", err)
	}
	if n > 0 {
		if err := bumpGeneration(tx, ""); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit prune attempts: %w", err)
	}
	return n, nil
}

// bumpGeneration increments the generation of the fence row of server, or
// of every server for "".
func bumpGeneration(tx *sql.Tx, server string) error {
	if _, err := tx.Exec(`INSERT INTO prune_fence (server, generation) VALUES (?, 1)
		ON CONFLICT (server) DO UPDATE SET generation = generation + 1`, server); err != nil {
		return fmt.Errorf("bump prune generation: %w", err)
	}
	return nil
}

// ReadState is what a read of one library needs from one snapshot: its
// read times, whether it has a hold, and the mark with the generations.
type ReadState struct {
	Library
	Held bool
	Mark EventMark
}

// ReadStart returns the read state of a library in one read transaction,
// so the read times, the mark, and the generations agree. A read-only
// transaction does not take the write lock.
func (s *Store) ReadStart(server, section string) (ReadState, error) {
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ReadState{}, fmt.Errorf("begin read start: %w", err)
	}
	defer tx.Rollback()

	var st ReadState
	var full, read sql.NullInt64
	err = tx.QueryRow(`SELECT last_full_read, last_read FROM library WHERE server = ? AND section = ?`,
		server, section).Scan(&full, &read)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ReadState{}, fmt.Errorf("read library: %w", err)
	}
	st.Library = Library{LastFullRead: timeFrom(full), LastRead: timeFrom(read)}
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM read_hold WHERE server = ? AND section = ?)`,
		server, section).Scan(&st.Held); err != nil {
		return ReadState{}, fmt.Errorf("read holds: %w", err)
	}
	if st.Mark, err = readMark(tx, server); err != nil {
		return ReadState{}, err
	}
	return st, nil
}

// readMark returns the newest event id and the generations of server.
func readMark(tx *sql.Tx, server string) (EventMark, error) {
	m := EventMark{set: true, fenced: true}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM event`).Scan(&m.id); err != nil {
		return EventMark{}, fmt.Errorf("read event mark: %w", err)
	}
	var err error
	if m.generations, err = readGenerations(tx, server); err != nil {
		return EventMark{}, err
	}
	return m, nil
}

// generations are the prune generations of every server and of one server.
type generations struct{ all, server int64 }

// readGenerations returns the shared generation and that of server.
func readGenerations(tx *sql.Tx, server string) (generations, error) {
	var g generations
	err := tx.QueryRow(`SELECT
		COALESCE((SELECT generation FROM prune_fence WHERE server = ''), 0),
		COALESCE((SELECT generation FROM prune_fence WHERE server = ?), 0)`, server).Scan(&g.all, &g.server)
	if err != nil {
		return generations{}, fmt.Errorf("read prune generations: %w", err)
	}
	return g, nil
}

// ceilSeconds returns d in whole seconds, rounded up, and at most
// math.MaxInt64 seconds.
func ceilSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	s := int64(d / time.Second)
	if d%time.Second != 0 {
		s++
	}
	return s
}

// satAdd returns a + b, or math.MaxInt64 when the sum would overflow. b is
// not negative.
func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
