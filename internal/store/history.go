package store

import (
	"cmp"
	"context"
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
	EventStopped       EventKind = "stopped"
	EventResumed       EventKind = "resumed"
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
	RunID             int64        // the run that sent it; 0: none
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
	RunID                            int64 // the run that wrote it; 0: none
	Searches                         []MatchSearch
}

const eventColumns = `id, server, section, rating_key, at, kind, source, title, year, type, thumb, art, path,
	action, accepted, outcome, error, match_guid, match_title, match_year, conflict, path_year, tmdb_year,
	episode_count, links, fixed_action, fixed_match, fixed_match_event_id, run_id`

// Outcome is the outcome of the request: the action, with "-failed" when
// Plex did not accept it, or "search-failed" without an action.
func (r Request) Outcome() string {
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
	if err := recordRequest(tx, r, snap); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request: %w", err)
	}
	return nil
}

func recordRequest(tx *sql.Tx, r Request, snap Item) error {
	var accepted Action
	if r.Accepted {
		accepted = r.Action
	}
	if err := updateAttempt(tx, r.Server, r.RatingKey, r.At, accepted); err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	return writeRequestOutcome(tx, r, snap)
}

// writeRequestOutcome sets the last outcome of the row and writes the event
// of the request. An accepted match event becomes the last match event.
func writeRequestOutcome(tx *sql.Tx, r Request, snap Item) error {
	outcome := r.Outcome()
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
	e.RunID = r.RunID
	if m := r.Match; m != nil {
		e.MatchGUID, e.MatchTitle, e.MatchYear = m.GUID, m.Title, m.Year
		e.Links, e.PathYear, e.TMDBYear, e.EpisodeCount = m.Links, m.PathYear, m.TMDBYear, m.EpisodeCount
	}
	return e
}

// DecisionOutcome is how a fix match decision ended.
type DecisionOutcome string

const (
	DecisionMatched DecisionOutcome = "matched"
	DecisionNoMatch DecisionOutcome = "no-match"
	DecisionNoIDs   DecisionOutcome = "no-ids"
	DecisionFailed  DecisionOutcome = "failed"
)

// MatchDecision is the result of the search for a fix match of an item.
// The verdicts of the candidates are set.
type MatchDecision struct {
	Server, RatingKey                string
	At                               time.Time
	Source                           Source
	Outcome                          DecisionOutcome
	Conflict, Error                  string
	PathYear, TMDBYear, EpisodeCount int
	Searches                         []MatchSearch
	RunID                            int64 // the run that searched; 0: none
}

// RecordMatchDecision writes a match decision event with its searches and
// candidates, and sets the last match outcome of the row, in one
// transaction. It writes nothing for a missing row.
func (s *Store) RecordMatchDecision(d MatchDecision) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin match decision: %w", err)
	}
	defer tx.Rollback()

	snap, found, err := readItem(tx, d.Server, d.RatingKey)
	if err != nil || !found {
		return err
	}
	e := snapshotEvent(snap, d.At, EventMatchDecision, d.Source)
	e.Outcome, e.Conflict, e.Error = string(d.Outcome), d.Conflict, d.Error
	e.PathYear, e.TMDBYear, e.EpisodeCount = d.PathYear, d.TMDBYear, d.EpisodeCount
	e.RunID = d.RunID
	eventID, err := insertEvent(tx, e)
	if err != nil {
		return err
	}
	for i, m := range d.Searches {
		if err := insertSearch(tx, eventID, i, m); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE item SET last_match_outcome = ? WHERE server = ? AND rating_key = ?`,
		d.Outcome, d.Server, d.RatingKey); err != nil {
		return fmt.Errorf("write match outcome: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit match decision: %w", err)
	}
	return nil
}

// insertSearch writes the search m at position and its candidates.
func insertSearch(tx *sql.Tx, eventID int64, position int, m MatchSearch) error {
	res, err := tx.Exec(`INSERT INTO match_search (event_id, position, query, source, from_id, failed)
		VALUES (?, ?, ?, ?, ?, ?)`, eventID, position, m.Query, m.Source, m.FromID, m.Failed)
	if err != nil {
		return fmt.Errorf("write search: %w", err)
	}
	searchID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("write search: %w", err)
	}
	for i, c := range m.Candidates {
		if _, err := tx.Exec(`INSERT INTO match_candidate (search_id, position, guid, name, year, type, summary, thumb, score, verdict)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			searchID, i, c.GUID, c.Name, c.Year, c.Type, c.Summary, c.Thumb, c.Score, c.Verdict); err != nil {
			return fmt.Errorf("write candidate: %w", err)
		}
	}
	return nil
}

// snapshotEvent returns an event of kind with the snapshot columns of the row.
func snapshotEvent(row Item, at time.Time, kind EventKind, source Source) Event {
	return Event{
		Server: row.Server, Section: row.Section, RatingKey: row.RatingKey,
		At: at, Kind: kind, Source: source,
		Title: row.Title, Year: row.Year, Type: row.Type, Thumb: row.Thumb, Art: row.Art, Path: row.Path,
	}
}

// RecordGone writes a gone event of the run runID (0: none) with a snapshot
// of the row and deletes the row, in one transaction. It writes nothing for
// a missing row.
func (s *Store) RecordGone(server, ratingKey string, at time.Time, source Source, runID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin gone: %w", err)
	}
	defer tx.Rollback()

	snap, found, err := readItem(tx, server, ratingKey)
	if err != nil || !found {
		return err
	}
	e := snapshotEvent(snap, at, EventGone, source)
	e.RunID = runID
	if _, err := insertEvent(tx, e); err != nil {
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

// writeGone writes a gone event of the read r for the row of key, if it
// exists. The row keeps its snapshot.
func writeGone(tx *sql.Tx, r Read, key string) error {
	snap, found, err := readItem(tx, r.Server, key)
	if err != nil || !found {
		return err
	}
	e := snapshotEvent(snap, r.At, EventGone, SourceAuto)
	e.RunID = r.RunID
	_, err = insertEvent(tx, e)
	return err
}

// writeCompleted writes the completed event of the row prev, which the read
// r completed. The snapshot takes the newest title, year, images, and path
// that the read has, and the event credits the requests that fixed the row.
func writeCompleted(tx *sql.Tx, r Read, prev Item, in readKey) error {
	// in.meta is empty for a row that the read does not list.
	snap := prev
	snap.Section = r.Section
	snap.Title = cmp.Or(in.meta.Title, prev.Title)
	snap.Year = cmp.Or(in.meta.Year, prev.Year)
	snap.Thumb = cmp.Or(in.meta.Thumb, prev.Thumb)
	snap.Art = cmp.Or(in.meta.Art, prev.Art)
	snap.Path = cmp.Or(in.meta.Path, r.Folders[prev.RatingKey], prev.Path)
	e := snapshotEvent(snap, r.At, EventCompleted, SourceAuto)
	e.FixedAction, e.FixedMatch, e.FixedMatchEventID = prev.LastAction, prev.LastMatch, prev.LastMatchEventID
	e.RunID = r.RunID
	_, err := insertEvent(tx, e)
	return err
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
		episode_count, links, fixed_action, fixed_match, fixed_match_event_id, run_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (SELECT id FROM event WHERE id = ?),
		(SELECT id FROM run WHERE id = ?))`,
		e.Server, e.Section, e.RatingKey, e.At.Unix(), e.Kind, e.Source, e.Title, e.Year, e.Type, e.Thumb, e.Art, e.Path,
		e.Action, e.Accepted, e.Outcome, e.Error, e.MatchGUID, e.MatchTitle, e.MatchYear, e.Conflict,
		e.PathYear, e.TMDBYear, e.EpisodeCount, strings.Join(e.Links, "\n"), e.FixedAction, e.FixedMatch, e.FixedMatchEventID,
		e.RunID)
	if err != nil {
		return 0, fmt.Errorf("write event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("write event: %w", err)
	}
	return id, nil
}

// Timeline returns the events of one item, ordered at DESC, id DESC. Match
// decisions carry their searches and candidates.
func (s *Store) Timeline(server, ratingKey string) ([]Event, error) {
	return s.TimelineContext(context.Background(), server, ratingKey)
}

// TimelineContext is Timeline with a context.
func (s *Store) TimelineContext(ctx context.Context, server, ratingKey string) ([]Event, error) {
	// One read transaction gives the events, searches, and candidates the
	// same snapshot. The store has one connection, so each query ends
	// before the next one starts.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin timeline: %w", err)
	}
	defer tx.Rollback()

	events, err := queryEvents(ctx, tx, server, ratingKey)
	if err != nil {
		return nil, err
	}
	searchAt, err := attachSearches(ctx, tx, events, server, ratingKey)
	if err != nil {
		return nil, err
	}
	if err := attachCandidates(ctx, tx, events, searchAt, server, ratingKey); err != nil {
		return nil, err
	}
	return events, nil
}

func queryEvents(ctx context.Context, tx *sql.Tx, server, ratingKey string) ([]Event, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+eventColumns+` FROM event WHERE server = ? AND rating_key = ?
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

// searchRef is the place of a search in the timeline: the index of its
// event and of the search in that event.
type searchRef struct{ event, search int }

// attachSearches reads the searches of the item and adds them to their
// events. It returns the place of each search by ID.
func attachSearches(ctx context.Context, tx *sql.Tx, events []Event, server, ratingKey string) (map[int64]searchRef, error) {
	eventAt := make(map[int64]int, len(events))
	for i, e := range events {
		eventAt[e.ID] = i
	}
	rows, err := tx.QueryContext(ctx, `SELECT match_search.id, match_search.event_id, match_search.query, match_search.source,
		match_search.from_id, match_search.failed
		FROM match_search JOIN event ON event.id = match_search.event_id
		WHERE event.server = ? AND event.rating_key = ?
		ORDER BY match_search.event_id, match_search.position`, server, ratingKey)
	if err != nil {
		return nil, fmt.Errorf("read searches: %w", err)
	}
	defer rows.Close()

	searchAt := map[int64]searchRef{}
	for rows.Next() {
		var (
			id, eventID int64
			m           MatchSearch
		)
		if err := rows.Scan(&id, &eventID, &m.Query, &m.Source, &m.FromID, &m.Failed); err != nil {
			return nil, fmt.Errorf("read searches: %w", err)
		}
		i := eventAt[eventID]
		searchAt[id] = searchRef{event: i, search: len(events[i].Searches)}
		events[i].Searches = append(events[i].Searches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read searches: %w", err)
	}
	return searchAt, nil
}

// attachCandidates reads the candidates of the item and adds them to their
// searches.
func attachCandidates(ctx context.Context, tx *sql.Tx, events []Event, searchAt map[int64]searchRef, server, ratingKey string) error {
	rows, err := tx.QueryContext(ctx, `SELECT match_candidate.search_id, match_candidate.guid, match_candidate.name,
		match_candidate.year, match_candidate.type, match_candidate.summary, match_candidate.thumb,
		match_candidate.score, match_candidate.verdict
		FROM match_candidate
		JOIN match_search ON match_search.id = match_candidate.search_id
		JOIN event ON event.id = match_search.event_id
		WHERE event.server = ? AND event.rating_key = ?
		ORDER BY match_search.event_id, match_search.position, match_candidate.position`, server, ratingKey)
	if err != nil {
		return fmt.Errorf("read candidates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			searchID int64
			c        MatchCandidate
		)
		if err := rows.Scan(&searchID, &c.GUID, &c.Name, &c.Year, &c.Type, &c.Summary, &c.Thumb, &c.Score, &c.Verdict); err != nil {
			return fmt.Errorf("read candidates: %w", err)
		}
		ref := searchAt[searchID]
		m := &events[ref.event].Searches[ref.search]
		m.Candidates = append(m.Candidates, c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read candidates: %w", err)
	}
	return nil
}

func scanEvent(row scanner) (Event, error) {
	var (
		e       Event
		at      int64
		links   string
		fixedID sql.NullInt64
		runID   sql.NullInt64
	)
	err := row.Scan(&e.ID, &e.Server, &e.Section, &e.RatingKey, &at, &e.Kind, &e.Source, &e.Title, &e.Year, &e.Type,
		&e.Thumb, &e.Art, &e.Path, &e.Action, &e.Accepted, &e.Outcome, &e.Error, &e.MatchGUID, &e.MatchTitle,
		&e.MatchYear, &e.Conflict, &e.PathYear, &e.TMDBYear, &e.EpisodeCount, &links, &e.FixedAction, &e.FixedMatch,
		&fixedID, &runID)
	if err != nil {
		return Event{}, err
	}
	e.At = time.Unix(at, 0)
	e.FixedMatchEventID, e.RunID = fixedID.Int64, runID.Int64
	if links != "" {
		e.Links = strings.Split(links, "\n")
	}
	return e, nil
}
