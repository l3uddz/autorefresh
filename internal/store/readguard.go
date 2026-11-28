package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// ErrReadPruned is the refusal of a read that a pruning made older than the
// cache: a pruning of attempts, of events, or of the libraries of its server
// ran after the read took its mark.
var ErrReadPruned = errors.New("read is older than a pruning")

// ReadResult is what ApplyRead did.
type ReadResult struct {
	Fixed []Item // the rows that the read completed, by rating key
	// Omitted are the keys of Read.Present that keep a hold in the library:
	// Plex has them, but its listing omits them.
	Omitted []string
}

// checkFence returns ErrReadPruned when a generation of the mark of r
// changed. The zero mark checks nothing.
func checkFence(tx *sql.Tx, r Read) error {
	if !r.Mark.set {
		return nil
	}
	g, err := readGenerations(tx, r.Server)
	if err != nil {
		return err
	}
	if g != r.Mark.generations {
		return ErrReadPruned
	}
	return nil
}

// readGuard holds the keys that a read must leave unchanged, because a
// write can have changed them after the listing, and the holds that its
// skips collect.
type readGuard struct {
	// protected maps each key to whether a skip of it saves a hold: true for
	// a write attempt live at or after the start of the read or an accepted
	// request after the mark, false for only a completed or gone event.
	protected map[string]bool
	held      map[string]bool
}

// newReadGuard returns the guard of r: the keys of newerKeys, and the keys
// with a write attempt that was live at or after r.At.
func newReadGuard(tx *sql.Tx, r Read) (*readGuard, error) {
	g := &readGuard{protected: map[string]bool{}, held: map[string]bool{}}
	if err := newerKeys(tx, r, g.protected); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT DISTINCT rating_key FROM write_attempt WHERE server = ? AND ends_at >= ?`,
		r.Server, r.At.Unix())
	if err != nil {
		return nil, fmt.Errorf("read write attempts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("read write attempts: %w", err)
		}
		g.protected[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read write attempts: %w", err)
	}
	return g, nil
}

// skip reports whether the read must leave key unchanged. When it must, and
// the read names or would change the key (changes), it collects a hold for
// the key if its protection needs one.
func (g *readGuard) skip(key string, changes bool) bool {
	hold, ok := g.protected[key]
	if ok && hold && changes {
		g.held[key] = true
	}
	return ok
}

// saveHolds saves the collected holds in the library of r.
func (g *readGuard) saveHolds(tx *sql.Tx, r Read) error {
	for key := range g.held {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO read_hold (server, section, rating_key) VALUES (?, ?, ?)`,
			r.Server, r.Section, key); err != nil {
			return fmt.Errorf("save hold: %w", err)
		}
	}
	return nil
}

// reconcileHolds deletes, after a full read r, each hold of its library
// whose key the read did not protect and reconciled, and returns the held
// keys of r.Present, sorted. old are the rows before the read, after its
// gone rows were deleted. Another read changes no hold.
func reconcileHolds(tx *sql.Tx, r Read, guard *readGuard, old map[string]Item, incoming map[string]readKey) ([]string, error) {
	if !r.Full {
		return nil, nil
	}
	keys, err := libraryHolds(tx, r)
	if err != nil {
		return nil, err
	}
	gone := setOf(r.Gone)
	present := setOf(r.Present)
	var omitted []string
	for _, key := range keys {
		if _, protected := guard.protected[key]; !protected && reconciled(r, key, old, incoming, gone) {
			if _, err := tx.Exec(`DELETE FROM read_hold WHERE server = ? AND section = ? AND rating_key = ?`,
				r.Server, r.Section, key); err != nil {
				return nil, fmt.Errorf("delete hold: %w", err)
			}
			continue
		}
		if present[key] {
			omitted = append(omitted, key)
		}
	}
	sort.Strings(omitted)
	return omitted, nil
}

// reconciled reports whether the full read r settled key in its library:
// it applied the item listing of the key, its 404 check answered 404, or
// the key had no row in the library and the read does not list it.
func reconciled(r Read, key string, old map[string]Item, incoming map[string]readKey, gone map[string]bool) bool {
	in := incoming[key]
	prev, had := old[key]
	inLibrary := had && prev.Section == r.Section
	return in.inItems || gone[key] || !in.present && !inLibrary
}

// libraryHolds returns the held keys of the library of r.
func libraryHolds(tx *sql.Tx, r Read) ([]string, error) {
	rows, err := tx.Query(`SELECT rating_key FROM read_hold WHERE server = ? AND section = ?`, r.Server, r.Section)
	if err != nil {
		return nil, fmt.Errorf("read holds: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("read holds: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read holds: %w", err)
	}
	return keys, nil
}

// setOf returns the set of keys.
func setOf(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}
