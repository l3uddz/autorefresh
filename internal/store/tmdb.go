package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// TMDBID returns the cached TMDB ID of an external ID of an item. source is
// "tvdb" or "imdb". ok is false if the cache has no such ID.
func (s *Store) TMDBID(server, ratingKey, source, externalID string) (tmdbID string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT tmdb_id FROM tmdb_id
		WHERE server = ? AND rating_key = ? AND source = ? AND external_id = ?`,
		server, ratingKey, source, externalID).Scan(&tmdbID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("read tmdb id: %w", err)
	}
	return tmdbID, true, nil
}

// SaveTMDBID caches the TMDB ID of an external ID of an item. The item row
// must exist to cache the ID. A missing item is a no-op.
func (s *Store) SaveTMDBID(server, ratingKey, source, externalID, tmdbID string) error {
	_, err := s.db.Exec(`INSERT INTO tmdb_id (server, rating_key, source, external_id, tmdb_id)
		SELECT ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM item WHERE server = ? AND rating_key = ?)
		ON CONFLICT (server, rating_key, source, external_id) DO UPDATE SET tmdb_id = excluded.tmdb_id`,
		server, ratingKey, source, externalID, tmdbID, server, ratingKey)
	if err != nil {
		return fmt.Errorf("save tmdb id: %w", err)
	}
	return nil
}
