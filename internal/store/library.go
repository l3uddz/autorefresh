package store

import "fmt"

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
