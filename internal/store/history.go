package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Source tells who started a request: the schedule or a person.
type Source string

const (
	SourceAuto   Source = "auto"
	SourceManual Source = "manual"
)

// EventKind is the kind of an event.
type EventKind string

const (
	EventRequest       EventKind = "request"
	EventMatchDecision EventKind = "match-decision"
	EventCompleted     EventKind = "completed"
	EventGone          EventKind = "gone"
)

// MatchTarget is the candidate of a match request and how it was confirmed.
type MatchTarget struct {
	GUID, Title                      string
	Year                             int
	Links                            []string
	PathYear, TMDBYear, EpisodeCount int
}

// Request is one attempt of an item. Action is "" for a fix match search
// that failed before any request was sent.
type Request struct {
	Server, RatingKey string
	At                time.Time
	Source            Source
	Action            Action
	Accepted          bool
	Error             string
	Match             *MatchTarget // match requests, accepted or failed
}

// MatchCandidate is one candidate that a Plex search returned.
type MatchCandidate struct {
	GUID, Name           string
	Year                 int
	Type, Summary, Thumb string
	Score                int
	Verdict              string
}

// MatchSearch is one Plex search of a match decision.
type MatchSearch struct {
	Query      string // for example "tmdb-48213"
	Source     string // "path", "tmdb", or "tie"
	FromID     string // the path ID a tmdb search came from
	Failed     bool
	Candidates []MatchCandidate
}

// Event is one thing that happened to an item, with a snapshot of the item.
type Event struct {
	ID                               int64
	Server, Section, RatingKey       string
	At                               time.Time
	Kind                             EventKind
	Source                           Source
	Title                            string
	Year                             int
	Type, Thumb, Art, Path           string
	Action                           Action
	Accepted                         bool
	Outcome, Error                   string
	MatchGUID, MatchTitle            string
	MatchYear                        int
	Conflict                         string
	PathYear, TMDBYear, EpisodeCount int
	Links                            []string // nil when none
	FixedAction, FixedMatch          Action
	FixedMatchEventID                int64 // 0: none
	Searches                         []MatchSearch
}

const eventColumns = `id, server, section, rating_key, at, kind, source, title, year, type, thumb, art, path,
	action, accepted, outcome, error, match_guid, match_title, match_year, conflict, path_year, tmdb_year,
	episode_count, links, fixed_action, fixed_match, fixed_match_event_id`

// requestOutcome is the outcome of a request: the action, with "-failed"
// when Plex did not accept it, or "search-failed" without an action.
func requestOutcome(r Request) string {
	switch {
	case r.Action == "":
		return "search-failed"
	case r.Accepted:
		return string(r.Action)
	default:
		return string(r.Action) + "-failed"
	}
}

// RecordRequest counts one attempt of an item and, if the request has an
// action, writes a request event with a snapshot of the row. It writes
// nothing for a missing row.
func (s *Store) RecordRequest(r Request) error {
	if r.Source == "" {
		return fmt.Errorf("record request: empty source")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin request: %w", err)
	}
	defer tx.Rollback()

	snap, found, err := readItem(tx, r.Server, r.RatingKey)
	if err != nil || !found {
		return err
	}
	var accepted Action
	if r.Accepted {
		accepted = r.Action
	}
	if err := updateAttempt(tx, r.Server, r.RatingKey, r.At, accepted); err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	if err := writeRequestOutcome(tx, r, snap); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request: %w", err)
	}
	return nil
}

// writeRequestOutcome sets the last outcome of the row and writes the event
// of the request. An accepted match event becomes the last match event.
func writeRequestOutcome(tx *sql.Tx, r Request, snap Item) error {
	outcome := requestOutcome(r)
	var matchEventID any
	if r.Action != "" {
		id, err := insertEvent(tx, requestEvent(r, snap, outcome))
		if err != nil {
			return err
		}
		if r.Accepted && r.Action.isMatch() {
			matchEventID = id
		}
	}
	if _, err := tx.Exec(`UPDATE item SET last_outcome = ?, last_match_event_id = COALESCE(?, last_match_event_id)
		WHERE server = ? AND rating_key = ?`, outcome, matchEventID, r.Server, r.RatingKey); err != nil {
		return fmt.Errorf("write request outcome: %w", err)
	}
	return nil
}

// requestEvent returns the event of r. snap is the item row.
func requestEvent(r Request, snap Item, outcome string) Event {
	e := snapshotEvent(snap, r.At, EventRequest, r.Source)
	e.Action, e.Accepted, e.Outcome, e.Error = r.Action, r.Accepted, outcome, r.Error
	if m := r.Match; m != nil {
		e.MatchGUID, e.MatchTitle, e.MatchYear = m.GUID, m.Title, m.Year
		e.Links, e.PathYear, e.TMDBYear, e.EpisodeCount = m.Links, m.PathYear, m.TMDBYear, m.EpisodeCount
	}
	return e
}

// snapshotEvent returns an event of kind with the snapshot columns of the row.
func snapshotEvent(row Item, at time.Time, kind EventKind, source Source) Event {
	return Event{
		Server: row.Server, Section: row.Section, RatingKey: row.RatingKey,
		At: at, Kind: kind, Source: source,
		Title: row.Title, Year: row.Year, Type: row.Type, Thumb: row.Thumb, Art: row.Art, Path: row.Path,
	}
}

// RecordGone writes a gone event with a snapshot of the row and deletes the
// row, in one transaction. It writes nothing for a missing row.
func (s *Store) RecordGone(server, ratingKey string, at time.Time, source Source) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin gone: %w", err)
	}
	defer tx.Rollback()

	snap, found, err := readItem(tx, server, ratingKey)
	if err != nil || !found {
		return err
	}
	if _, err := insertEvent(tx, snapshotEvent(snap, at, EventGone, source)); err != nil {
		return err
	}
	if err := deleteItem(tx, server, ratingKey); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit gone: %w", err)
	}
	return nil
}

// readItem reads one row and closes the query before it returns.
func readItem(tx *sql.Tx, server, ratingKey string) (it Item, found bool, err error) {
	items, err := queryItems(tx, `SELECT `+itemColumns+` FROM item WHERE server = ? AND rating_key = ?`, server, ratingKey)
	if err != nil {
		return Item{}, false, fmt.Errorf("read item: %w", err)
	}
	if len(items) == 0 {
		return Item{}, false, nil
	}
	return items[0], true, nil
}

// insertEvent writes e and returns its ID.
func insertEvent(tx *sql.Tx, e Event) (int64, error) {
	res, err := tx.Exec(`INSERT INTO event (server, section, rating_key, at, kind, source, title, year, type, thumb, art, path,
		action, accepted, outcome, error, match_guid, match_title, match_year, conflict, path_year, tmdb_year,
		episode_count, links, fixed_action, fixed_match)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Server, e.Section, e.RatingKey, e.At.Unix(), e.Kind, e.Source, e.Title, e.Year, e.Type, e.Thumb, e.Art, e.Path,
		e.Action, e.Accepted, e.Outcome, e.Error, e.MatchGUID, e.MatchTitle, e.MatchYear, e.Conflict,
		e.PathYear, e.TMDBYear, e.EpisodeCount, strings.Join(e.Links, "\n"), e.FixedAction, e.FixedMatch)
	if err != nil {
		return 0, fmt.Errorf("write event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("write event: %w", err)
	}
	return id, nil
}

// Timeline returns the events of one item, ordered at DESC, id DESC.
func (s *Store) Timeline(server, ratingKey string) ([]Event, error) {
	rows, err := s.db.Query(`SELECT `+eventColumns+` FROM event WHERE server = ? AND rating_key = ?
		ORDER BY at DESC, id DESC`, server, ratingKey)
	if err != nil {
		return nil, fmt.Errorf("read timeline: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("read timeline: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read timeline: %w", err)
	}
	return events, nil
}

func scanEvent(row scanner) (Event, error) {
	var (
		e       Event
		at      int64
		links   string
		fixedID sql.NullInt64
	)
	err := row.Scan(&e.ID, &e.Server, &e.Section, &e.RatingKey, &at, &e.Kind, &e.Source, &e.Title, &e.Year, &e.Type,
		&e.Thumb, &e.Art, &e.Path, &e.Action, &e.Accepted, &e.Outcome, &e.Error, &e.MatchGUID, &e.MatchTitle,
		&e.MatchYear, &e.Conflict, &e.PathYear, &e.TMDBYear, &e.EpisodeCount, &links, &e.FixedAction, &e.FixedMatch,
		&fixedID)
	if err != nil {
		return Event{}, err
	}
	e.At = time.Unix(at, 0)
	e.FixedMatchEventID = fixedID.Int64
	if links != "" {
		e.Links = strings.Split(links, "\n")
	}
	return e, nil
}
