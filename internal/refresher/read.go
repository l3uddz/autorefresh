package refresher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// supportedAgents gives the agent that each supported library type must use.
var supportedAgents = map[string]string{
	plex.MovieLibrary: "tv.plex.agents.movie",
	plex.ShowLibrary:  "tv.plex.agents.series",
}

func supported(lib plex.Library) bool {
	agent, ok := supportedAgents[lib.Type]
	return ok && lib.Agent == agent
}

// readLibraries selects the libraries of the server and reads each one into
// the cache. It returns only the error of stopError, the error of a failed
// library listing, or a store error. It logs and skips the other failures.
func (r *Refresher) readLibraries(ctx context.Context, run *serverRun) error {
	libraries, err := run.server.Plex.Libraries(ctx)
	if err != nil {
		if stop := stopError(ctx, err); stop != nil {
			return stop
		}
		// Without its libraries, the run can check nothing on the server.
		return fmt.Errorf("%w: %w", err, plex.ErrUnavailable)
	}

	run.libraries = r.selectLibraries(run.server, libraries)
	if err := r.saveLibraries(run); err != nil {
		return err
	}
	if err := r.pruneSections(run, libraries); err != nil {
		return err
	}
	for _, lib := range run.libraries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.readLibrary(ctx, run, lib); err != nil {
			return err
		}
	}
	return nil
}

// saveLibraries stores the metadata of the selected libraries, also in a dry
// run, because it is cache metadata.
func (r *Refresher) saveLibraries(run *serverRun) error {
	infos := make([]store.LibraryInfo, 0, len(run.libraries))
	for _, lib := range run.libraries {
		infos = append(infos, store.LibraryInfo{
			Section: lib.ID, Title: lib.Title, Type: lib.Type, Agent: lib.Agent, Language: lib.Language,
		})
	}
	return r.store.SaveLibraries(run.server.Name, infos)
}

// pruneSections removes the cache of each library of the server that the
// run did not select. A dry run removes nothing and logs that once. A
// server that lists no library at all can still be starting, so it keeps
// its cache.
func (r *Refresher) pruneSections(run *serverRun, libraries []plex.Library) error {
	if r.opts.DryRun {
		log.Info().Str("server", run.server.Name).Msg("Dry run keeps cache of removed libraries")
		return nil
	}
	if len(libraries) == 0 {
		return nil
	}
	pruned, err := r.store.PruneSections(run.server.Name, run.sections())
	if err != nil {
		return err
	}
	titles := make(map[string]string, len(libraries))
	for _, lib := range libraries {
		titles[lib.ID] = lib.Title
	}
	logPruned(pruned, titles)
	return nil
}

// logPruned logs each removed library cache. titles gives the library
// title by section, if Plex still lists the library.
func logPruned(pruned []store.Pruned, titles map[string]string) {
	for _, p := range pruned {
		ev := log.Info().Str("server", p.Server).Str("section", p.Section)
		if title, ok := titles[p.Section]; ok {
			ev = ev.Str("library", title)
		}
		ev.Int("items", p.Items).Msg("Removed cache of library")
	}
}

// selectLibraries returns the supported libraries, or only the configured
// ones if the server has a list. Each warning logs once per process.
func (r *Refresher) selectLibraries(srv Server, libraries []plex.Library) []plex.Library {
	wanted := make(map[string]bool, len(srv.Libraries))
	for _, title := range srv.Libraries {
		wanted[title] = true
	}

	found := map[string]bool{}
	var selected []plex.Library
	for _, lib := range libraries {
		if len(wanted) > 0 && !wanted[lib.Title] {
			continue
		}
		found[lib.Title] = true
		if !supported(lib) {
			if r.warnOnce("agent\x00" + srv.Name + "\x00" + lib.ID) {
				log.Warn().Str("server", srv.Name).Str("library", lib.Title).
					Str("type", lib.Type).Str("agent", lib.Agent).Msg("Skipping library with unsupported agent")
			}
			continue
		}
		selected = append(selected, lib)
	}

	for _, title := range srv.Libraries {
		if !found[title] && r.warnOnce("missing\x00"+srv.Name+"\x00"+title) {
			log.Warn().Str("server", srv.Name).Str("library", title).Msg("Configured library not found")
		}
	}
	return selected
}

// readLibrary stages all requests of one library and applies them to the
// cache only if every request succeeds.
func (r *Refresher) readLibrary(ctx context.Context, run *serverRun, lib plex.Library) error {
	at := r.now()
	name, client := run.server.Name, run.server.Plex

	state, err := r.store.Library(name, lib.ID)
	if err != nil {
		return err
	}
	rd := store.Read{
		Server:  name,
		Section: lib.ID,
		At:      at,
		Full:    state.LastFullRead.IsZero() || !r.opts.CacheRefresh.Next(state.LastFullRead).After(at),
		Shows:   lib.Type == plex.ShowLibrary,
		DryRun:  r.opts.DryRun,
	}
	itemType := plex.TypeMovie
	if rd.Shows {
		itemType = plex.TypeShow
		// episodeSeries sends no request for an ignored episode reason.
		rd.EpisodeSkipped = r.opts.IgnoreReasons &^ store.ItemReasons
	}

	items, err := r.listItems(ctx, client, lib.ID, itemType, rd.Full, state.LastRead)
	if err != nil {
		return readFailed(ctx, name, lib, err)
	}
	rd.Items = make([]store.Item, 0, len(items))
	seen := make([]string, 0, len(items))
	for _, it := range items {
		rd.Items = append(rd.Items, storeItem(name, lib, it, itemReasons(it)))
		seen = append(seen, it.RatingKey)
	}

	if rd.Full {
		unseen, err := r.store.Unseen(name, lib.ID, seen)
		if err != nil {
			return err
		}
		if rd.Gone, rd.Folders, err = goneKeys(ctx, client, name, lib, unseen); err != nil {
			return readFailed(ctx, name, lib, err)
		}
	}

	if rd.Shows {
		if rd.EpisodeNoSummary, rd.EpisodeUnmatched, err = r.episodeSeries(ctx, client, name, lib, at); err != nil {
			return readFailed(ctx, name, lib, err)
		}
	}

	fixed, err := r.store.ApplyRead(rd)
	if err != nil {
		return err
	}
	run.recordRead(rd, lib, items, fixed)
	r.logRead(rd, lib, r.now().Sub(at))
	return nil
}

// recordRead adds an applied read of lib to the run: the counts of the
// summary, the rows that became complete, and the movie files that a fix
// match can use instead of a request.
func (run *serverRun) recordRead(rd store.Read, lib plex.Library, items []plex.Item, fixed []store.Item) {
	if rd.Full {
		run.fullReadItems += len(items)
	} else {
		run.incrementalReadItems += len(items)
	}
	for _, it := range fixed {
		if it.Refreshes > 0 {
			run.fixed++
		}
		if it.LastMatch != "" {
			run.fixedAfterMatch++
		}
		run.completed = append(run.completed, completedItem{library: lib.Title, item: it})
	}
	if !rd.Shows {
		for _, it := range items {
			if len(it.Files) > 0 && itemReasons(it)&store.Unmatched != 0 {
				run.files[it.RatingKey] = it.Files
			}
		}
	}
}

// stopError returns the error that stops the server after a failed Plex
// request: the context error once ctx is done, or err if it wraps
// plex.ErrUnavailable. It returns nil for any other failure.
func stopError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, plex.ErrUnavailable) {
		return err
	}
	return nil
}

// readFailed returns the error of stopError. Otherwise it logs err and
// returns nil, so the library keeps its cache and the run continues.
func readFailed(ctx context.Context, server string, lib plex.Library, err error) error {
	if stop := stopError(ctx, err); stop != nil {
		return stop
	}
	log.Warn().Str("server", server).Str("library", lib.Title).Err(err).Msg("Reading library failed")
	return nil
}

// listItems does a full read, or an incremental read that joins three
// listings by rating key.
func (r *Refresher) listItems(ctx context.Context, client Plex, section string, itemType int, full bool, lastRead time.Time) ([]plex.Item, error) {
	if full {
		return client.List(ctx, section, itemType)
	}

	since := unixString(lastRead.Add(-r.opts.Oversample))
	var items []plex.Item
	seen := map[string]bool{}
	for _, f := range []plex.Filter{
		filter("updatedAt>>", since),
		filter("addedAt>>", since),
		filter("unmatched", "1"),
	} {
		list, err := client.List(ctx, section, itemType, f)
		if err != nil {
			return nil, err
		}
		for _, it := range list {
			if !seen[it.RatingKey] {
				seen[it.RatingKey] = true
				items = append(items, it)
			}
		}
	}
	return items, nil
}

// goneKeys returns the rating keys of the unseen rows that Plex answers with
// 404. A row with another answer stays. folders holds the folder of each
// unseen series that Plex answers with a location, by rating key.
func goneKeys(ctx context.Context, client Plex, server string, lib plex.Library, unseen []store.Item) (gone []string, folders map[string]string, err error) {
	folders = map[string]string{}
	for _, it := range unseen {
		paths, err := client.ItemPaths(ctx, it.RatingKey)
		if err == nil {
			if folder := seriesFolder(lib.Type, paths); folder != "" {
				folders[it.RatingKey] = folder
			}
			continue
		}
		if errors.Is(err, plex.ErrNotFound) {
			gone = append(gone, it.RatingKey)
			continue
		}
		if stop := stopError(ctx, err); stop != nil {
			return nil, nil, stop
		}
		log.Warn().Str("server", server).Str("library", lib.Title).Str("title", it.Title).
			Err(err).Msg("Checking unseen item failed")
	}
	return gone, folders, nil
}

// episodeSeries sends the episode requests of a show library. It skips the
// request of an ignored reason. Only episodes added at or before
// at-MinimumAge count.
func (r *Refresher) episodeSeries(ctx context.Context, client Plex, server string, lib plex.Library, at time.Time) (noSummary, unmatched []store.Item, err error) {
	addedBefore := unixString(at.Add(-r.opts.MinimumAge))
	if r.opts.IgnoreReasons&store.EpisodeNoSummary == 0 {
		noSummary, err = listSeries(ctx, client, server, lib, store.EpisodeNoSummary,
			filter("unmatched", "0"),
			filter("episode.unmatched", "0"),
			filter("episode.summary", ""),
			filter("episode.addedAt<<", addedBefore),
			filter("season.index>>", "1"),
		)
		if err != nil {
			return nil, nil, err
		}
	}
	if r.opts.IgnoreReasons&store.EpisodeUnmatched == 0 {
		unmatched, err = listSeries(ctx, client, server, lib, store.EpisodeUnmatched,
			filter("unmatched", "0"),
			filter("episode.unmatched", "1"),
			filter("episode.addedAt<<", addedBefore),
			filter("season.index>>", "1"),
		)
		if err != nil {
			return nil, nil, err
		}
	}
	return noSummary, unmatched, nil
}

func listSeries(ctx context.Context, client Plex, server string, lib plex.Library, reason store.Reasons, filters ...plex.Filter) ([]store.Item, error) {
	series, err := client.List(ctx, lib.ID, plex.TypeShow, filters...)
	if err != nil {
		return nil, err
	}
	items := make([]store.Item, 0, len(series))
	for _, it := range series {
		items = append(items, storeItem(server, lib, it, reason))
	}
	return items, nil
}

// logRead logs the young incomplete items and the read.
func (r *Refresher) logRead(rd store.Read, lib plex.Library, took time.Duration) {
	youngAfter := rd.At.Add(-r.opts.MinimumAge)
	incomplete := map[string]bool{}
	for _, it := range rd.Items {
		if it.Reasons == 0 {
			continue
		}
		incomplete[it.RatingKey] = true
		if it.AddedAt.After(youngAfter) {
			log.Debug().Str("server", rd.Server).Str("library", lib.Title).Str("title", it.Title).
				Msg("Item too young to refresh")
		}
	}
	for _, list := range [][]store.Item{rd.EpisodeNoSummary, rd.EpisodeUnmatched} {
		for _, it := range list {
			incomplete[it.RatingKey] = true
		}
	}

	kind := "incremental"
	if rd.Full {
		kind = "full"
	}
	log.Debug().Str("server", rd.Server).Str("library", lib.Title).Str("kind", kind).
		Int("items", len(rd.Items)).Int("incomplete", len(incomplete)).Dur("duration", took.Round(time.Millisecond)).
		Msg("Read library")
}

// storeItem returns the row of a listed item. It keeps only the Plex image
// paths of the item itself. Only a movie gets the folder of its first file
// as its path: a series folder comes only from a Plex Location.
func storeItem(server string, lib plex.Library, it plex.Item, reasons store.Reasons) store.Item {
	row := store.Item{
		Server: server, Section: lib.ID, RatingKey: it.RatingKey, Type: lib.Type,
		Title: it.Title, Year: it.Year, AddedAt: it.AddedAt, Reasons: reasons,
		Thumb: plex.ItemImagePath(it.RatingKey, it.Thumb), Art: plex.ItemImagePath(it.RatingKey, it.Art),
	}
	if lib.Type == plex.MovieLibrary {
		row.Path = folderOf(it.Files)
	}
	return row
}

// folderOf returns the folder of the first file: the file up to its last
// slash or backslash, because Plex can run on Windows. It returns "" without
// a file or a separator.
func folderOf(files []string) string {
	if len(files) == 0 {
		return ""
	}
	i := strings.LastIndexAny(files[0], `/\`)
	if i < 0 {
		return ""
	}
	return files[0][:i]
}

func filter(key, value string) plex.Filter {
	return plex.Filter{Key: key, Value: value}
}

func unixString(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
