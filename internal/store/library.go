package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LibraryInfo is the Plex metadata of one library.
type LibraryInfo struct{ Section, Title, Type, Agent, Language string }

// SaveLibraries stores the metadata of libs for server in one transaction. It
// inserts a missing library row with no read times and never changes the
// read times of a row.
func (s *Store) SaveLibraries(server string, libs []LibraryInfo) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin save libraries: %w", err)
	}
	defer tx.Rollback()

	for _, l := range libs {
		_, err := tx.Exec(`INSERT INTO library (server, section, title, type, agent, language) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (server, section) DO UPDATE SET
			title = excluded.title, type = excluded.type, agent = excluded.agent, language = excluded.language`,
			server, l.Section, l.Title, l.Type, l.Agent, l.Language)
		if err != nil {
			return fmt.Errorf("save library %s: %w", l.Section, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save libraries: %w", err)
	}
	return nil
}

// SetPath sets the path of an item row. A missing row is not an error.
func (s *Store) SetPath(server, ratingKey, path string) error {
	if _, err := s.db.Exec(`UPDATE item SET path = ? WHERE server = ? AND rating_key = ?`, path, server, ratingKey); err != nil {
		return fmt.Errorf("set item path: %w", err)
	}
	return nil
}

// FillPath sets the path of an item row only while its path is empty, so it
// never replaces a path that a run saved. It returns the path that the row
// holds afterwards. A missing row is not an error: it returns "".
func (s *Store) FillPath(ctx context.Context, server, ratingKey, path string) (string, error) {
	var kept string
	err := s.db.QueryRowContext(ctx, `UPDATE item SET path = CASE WHEN path = '' THEN ? ELSE path END
		WHERE server = ? AND rating_key = ? RETURNING path`, path, server, ratingKey).Scan(&kept)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("fill item path: %w", err)
	}
	return kept, nil
}

// LibraryEntry is the metadata and the read times of one library.
type LibraryEntry struct {
	LibraryInfo
	Library
}

// Libraries returns the libraries of server, ordered by title, then section.
func (s *Store) Libraries(ctx context.Context, server string) ([]LibraryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT section, title, type, agent, language, last_full_read, last_read
		FROM library WHERE server = ? ORDER BY title, section`, server)
	if err != nil {
		return nil, fmt.Errorf("read libraries: %w", err)
	}
	defer rows.Close()

	libs := []LibraryEntry{}
	for rows.Next() {
		var (
			l          LibraryEntry
			full, read sql.NullInt64
		)
		if err := rows.Scan(&l.Section, &l.Title, &l.Type, &l.Agent, &l.Language, &full, &read); err != nil {
			return nil, fmt.Errorf("read libraries: %w", err)
		}
		l.LastFullRead, l.LastRead = timeFrom(full), timeFrom(read)
		libs = append(libs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read libraries: %w", err)
	}
	return libs, nil
}

// LibraryInfo reads the Plex metadata of one cached library.
func (s *Store) LibraryInfo(server, section string) (LibraryInfo, bool, error) {
	info := LibraryInfo{Section: section}
	err := s.db.QueryRow(`SELECT title, type, agent, language FROM library WHERE server = ? AND section = ?`,
		server, section).Scan(&info.Title, &info.Type, &info.Agent, &info.Language)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryInfo{}, false, nil
	}
	if err != nil {
		return LibraryInfo{}, false, fmt.Errorf("read library info: %w", err)
	}
	return info, true, nil
}
