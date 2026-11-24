package refresher

import (
	"context"
	"errors"
	"fmt"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

var (
	ErrNotShow = errors.New("not a show")
	ErrTMDBOff = errors.New("tmdb is off")
)

// MatchQuery supplies the optional typed search and release year.
type MatchQuery struct {
	Text string
	Year int
}

// FolderID identifies an external ID found in an item's paths.
type FolderID struct{ ID, Link string }

// MatchResult is the read-only view of the search candidates.
type MatchResult struct {
	IDs              []FolderID
	Locations, Files []string
	Searches         []store.MatchSearch
	TMDBFailed       bool
}

// EpisodeResult reports whether the locally represented regular seasons
// confirm the TMDB series. Without a match, Diff is the first season with
// extra or invalid numbers, or the first season missing episodes when
// coverage is too low. Diff is zero when Plex lists no complete regular
// episodes.
type EpisodeResult struct {
	Match    bool
	Episodes int
	Diff     store.EpisodeDiff
}

func (r *Refresher) lookupItem(server, key string) (manualItem, error) {
	srv, err := r.serverNamed(server)
	if err != nil {
		return manualItem{}, err
	}
	row, cached, found, err := r.store.KnownItem(server, key)
	if err != nil {
		return manualItem{}, fmt.Errorf("%w: %w", ErrStore, err)
	}
	if !found {
		return manualItem{}, ErrUnknownItem
	}
	return manualItem{server: srv, row: row, cached: cached}, nil
}

type matchSearch struct {
	manualItem
	lib    store.LibraryInfo
	result MatchResult
}

// SearchMatches lists candidates without recording any match decision.
func (r *Refresher) SearchMatches(ctx context.Context, server, key string, q MatchQuery) (MatchResult, error) {
	if !r.enter() {
		return MatchResult{}, ErrClosed
	}
	defer r.actions.Done()
	it, err := r.lookupItem(server, key)
	if err != nil {
		return MatchResult{}, err
	}
	lib, _, err := r.store.LibraryInfo(server, it.row.Section)
	if err != nil {
		return MatchResult{}, fmt.Errorf("%w: %w", ErrStore, err)
	}
	search := matchSearch{manualItem: it, lib: lib}
	if q.Text != "" {
		if id, ok := parseID(q.Text); ok {
			err = search.add(ctx, id.title(), "id", "", 0)
		} else {
			err = search.add(ctx, q.Text, "title", "", q.Year)
		}
	} else {
		err = r.openSearch(ctx, &search)
	}
	return search.result, err
}

func (s *matchSearch) add(ctx context.Context, text, source, from string, year int) error {
	cs, err := s.server.Plex.SearchMatches(ctx, s.row.RatingKey, text, s.lib.Agent, s.lib.Language, year)
	if stop := stopError(ctx, err); stop != nil {
		return stop
	}
	if errors.Is(err, plex.ErrNotFound) {
		return err
	}
	search := store.MatchSearch{Query: text, Source: source, FromID: from, Failed: err != nil}
	for _, c := range cs {
		search.Candidates = append(search.Candidates, store.MatchCandidate{GUID: c.GUID, Name: c.Name, Year: c.Year, Type: c.Type,
			Summary: c.Summary, Thumb: c.Thumb, Score: c.Score})
	}
	s.result.Searches = append(s.result.Searches, search)
	return nil
}

func (r *Refresher) openSearch(ctx context.Context, s *matchSearch) error {
	if err := s.add(ctx, s.row.Title, "title", "", 0); err != nil {
		return err
	}
	paths, err := s.server.Plex.ItemPaths(ctx, s.row.RatingKey)
	if err != nil {
		return err
	}
	s.result.Locations, s.result.Files = paths.Locations, paths.Files
	ids := parseIDs(paths.All())
	useTMDB := r.opts.TMDB != nil
	for _, id := range ids {
		s.result.IDs = append(s.result.IDs, FolderID{ID: id.title(), Link: id.link(s.row.Type == plex.ShowLibrary)})
		if err := s.add(ctx, id.title(), "path", "", 0); err != nil {
			return err
		}
		if id.Source == "tmdb" {
			useTMDB = false
		}
	}
	if useTMDB {
		return r.searchTMDB(ctx, s, ids)
	}
	return nil
}

func (r *Refresher) searchTMDB(ctx context.Context, s *matchSearch, ids []pathID) error {
	seen := map[string]bool{}
	for _, from := range ids {
		if !tmdbSource(from, s.row.Type) {
			continue
		}
		id, ok, err := r.tmdbID(ctx, s.server.Name, s.row, from, s.cached)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, ErrTMDB):
			s.result.TMDBFailed = true
			return nil
		case err != nil:
			return fmt.Errorf("%w: %w", ErrStore, err)
		case !ok || seen[id]:
			continue
		}
		seen[id] = true
		if err := s.add(ctx, (pathID{"tmdb", id}).title(), "tmdb", from.title(), 0); err != nil {
			return err
		}
	}
	return nil
}

// EpisodeCheck compares the cached show's local episode numbers with TMDB.
func (r *Refresher) EpisodeCheck(ctx context.Context, server, key, id string) (EpisodeResult, error) {
	if !r.enter() {
		return EpisodeResult{}, ErrClosed
	}
	defer r.actions.Done()
	it, err := r.lookupItem(server, key)
	switch {
	case err != nil:
		return EpisodeResult{}, err
	case it.row.Type != plex.ShowLibrary:
		return EpisodeResult{}, ErrNotShow
	case r.opts.TMDB == nil:
		return EpisodeResult{}, ErrTMDBOff
	case !numberValue.MatchString(id):
		return EpisodeResult{}, ErrUnknownItem
	}
	result, err := compareEpisodes(ctx, it.server.Plex, r.opts.TMDB, key, id)
	return EpisodeResult{Match: result.count > 0, Episodes: result.count, Diff: result.diff}, err
}
