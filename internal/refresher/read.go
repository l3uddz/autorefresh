package refresher

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

// supportedAgents gives the agent that each supported library type must use.
var supportedAgents = map[string]string{
	"movie": "tv.plex.agents.movie",
	"show":  "tv.plex.agents.series",
}

func supported(lib plex.Library) bool {
	agent, ok := supportedAgents[lib.Type]
	return ok && lib.Agent == agent
}

// readLibraries selects the libraries of the server and reads each one into
// the cache. It returns only an error that wraps plex.ErrUnavailable or a
// store error. It logs and skips the other failures.
func (r *Refresher) readLibraries(ctx context.Context, run *serverRun) error {
	libraries, err := run.server.Plex.Libraries(ctx)
	if errors.Is(err, plex.ErrUnavailable) {
		return err
	}
	if err != nil {
		log.Warn().Str("server", run.server.Name).Err(err).Msg("Listing libraries failed")
		return nil
	}

	run.libraries = r.selectLibraries(run.server, libraries)
	for _, lib := range run.libraries {
		if err := r.readLibrary(ctx, run, lib); err != nil {
			return err
		}
	}
	return nil
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
	began := time.Now()
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
		Shows:   lib.Type == "show",
	}
	itemType := plex.TypeMovie
	if rd.Shows {
		itemType = plex.TypeShow
	}

	items, err := r.listItems(ctx, client, lib.ID, itemType, rd.Full, state.LastRead)
	if err != nil {
		return readFailed(name, lib, err)
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
		if rd.Gone, err = goneKeys(ctx, client, name, lib, unseen); err != nil {
			return readFailed(name, lib, err)
		}
	}

	if rd.Shows {
		if rd.EpisodeNoSummary, rd.EpisodeUnmatched, err = r.episodeSeries(ctx, client, name, lib, at); err != nil {
			return readFailed(name, lib, err)
		}
	}

	fixed, err := r.store.ApplyRead(rd)
	if err != nil {
		return err
	}

	run.itemsRead += len(items)
	run.completed += len(fixed)
	if !rd.Shows {
		for _, it := range items {
			run.files[it.RatingKey] = it.Files
		}
	}
	r.logRead(rd, lib, fixed, time.Since(began))
	return nil
}

// readFailed returns err if it stops the server. Otherwise it logs err and
// returns nil, so the library keeps its cache and the run continues.
func readFailed(server string, lib plex.Library, err error) error {
	if errors.Is(err, plex.ErrUnavailable) {
		return err
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
		filter("updatedAt", ">>=", since),
		filter("addedAt", ">>=", since),
		filter("unmatched", "=", "1"),
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
// 404. A row with another answer stays.
func goneKeys(ctx context.Context, client Plex, server string, lib plex.Library, unseen []store.Item) ([]string, error) {
	var gone []string
	for _, it := range unseen {
		_, err := client.ItemPaths(ctx, it.RatingKey)
		switch {
		case err == nil:
		case errors.Is(err, plex.ErrNotFound):
			gone = append(gone, it.RatingKey)
		case errors.Is(err, plex.ErrUnavailable):
			return nil, err
		default:
			log.Warn().Str("server", server).Str("library", lib.Title).Str("title", it.Title).
				Err(err).Msg("Checking unseen item failed")
		}
	}
	return gone, nil
}

// episodeSeries sends the two episode requests of a show library. Only
// episodes added at or before at-MinimumAge count.
func (r *Refresher) episodeSeries(ctx context.Context, client Plex, server string, lib plex.Library, at time.Time) (noSummary, unmatched []store.Item, err error) {
	addedBefore := unixString(at.Add(-r.opts.MinimumAge))
	noSummary, err = listSeries(ctx, client, server, lib, store.EpisodeNoSummary,
		filter("unmatched", "=", "0"),
		filter("episode.unmatched", "=", "0"),
		filter("episode.summary", "=", ""),
		filter("episode.addedAt", "<<=", addedBefore),
		filter("season.index", ">>=", "1"),
	)
	if err != nil {
		return nil, nil, err
	}
	unmatched, err = listSeries(ctx, client, server, lib, store.EpisodeUnmatched,
		filter("unmatched", "=", "0"),
		filter("episode.unmatched", "=", "1"),
		filter("episode.addedAt", "<<=", addedBefore),
		filter("season.index", ">>=", "1"),
	)
	if err != nil {
		return nil, nil, err
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

// logRead logs the fixed rows, the young incomplete items, and the read.
func (r *Refresher) logRead(rd store.Read, lib plex.Library, fixed []store.Item, took time.Duration) {
	for _, it := range fixed {
		log.Info().Str("server", rd.Server).Str("library", lib.Title).Str("title", it.Title).
			Int("refreshes", it.Refreshes).Msg("Item complete")
	}

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
		Int("items", len(rd.Items)).Int("incomplete", len(incomplete)).Dur("duration", took).
		Msg("Read library")
}

func storeItem(server string, lib plex.Library, it plex.Item, reasons store.Reasons) store.Item {
	return store.Item{
		Server: server, Section: lib.ID, RatingKey: it.RatingKey, Type: lib.Type,
		Title: it.Title, Year: it.Year, AddedAt: it.AddedAt, Reasons: reasons,
	}
}

func filter(field, op, value string) plex.Filter {
	return plex.Filter{Field: field, Op: op, Value: value}
}

func unixString(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
