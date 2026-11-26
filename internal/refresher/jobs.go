package refresher

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autorefresh/internal/plex"
	"github.com/l3uddz/autorefresh/internal/store"
)

var (
	ErrBusy  = errors.New("a run is busy")
	ErrNoJob = errors.New("no such job")
)

// Jobs coordinates scheduled runs and dashboard jobs.
type Jobs struct {
	r      *Refresher
	run    func(context.Context) error
	ctx    context.Context
	stop   context.CancelFunc
	mu     sync.Mutex
	curMu  sync.Mutex
	cur    *job
	closed bool
	wg     sync.WaitGroup
}

type job struct {
	ids    []int64
	cancel context.CancelFunc
}

// NewJobs creates the process-wide run coordinator.
func NewJobs(r *Refresher) *Jobs {
	ctx, stop := context.WithCancel(context.Background())
	return &Jobs{r: r, run: r.Run, ctx: ctx, stop: stop}
}

// Scheduled starts a scheduled run if the coordinator is idle.
func (j *Jobs) Scheduled(ctx context.Context) {
	if !j.mu.TryLock() {
		log.Warn().Msg("Skipped scheduled start, a run is busy")
		return
	}
	defer j.mu.Unlock()
	if err := j.run(ctx); err != nil {
		log.Error().Err(err).Msg("Run failed")
	}
}

func (j *Jobs) admit() error {
	j.curMu.Lock()
	defer j.curMu.Unlock()
	if j.closed {
		return ErrClosed
	}
	if !j.mu.TryLock() {
		return ErrBusy
	}
	j.wg.Add(1)
	return nil
}

func (j *Jobs) release() {
	j.mu.Unlock()
	j.wg.Done()
}

func (j *Jobs) launch(ids []int64, work func(context.Context)) {
	ctx, cancel := context.WithCancel(j.ctx)
	j.curMu.Lock()
	j.cur = &job{ids: slices.Clone(ids), cancel: cancel}
	j.curMu.Unlock()
	go func() {
		defer j.release()
		defer cancel()
		defer func() {
			j.curMu.Lock()
			j.cur = nil
			j.curMu.Unlock()
		}()
		work(ctx)
	}()
}

// RunNow admits a manual scan of the selected servers. Nil selects all
// servers. With full, the run reads every library fully, also when its full
// read is not due.
func (j *Jobs) RunNow(names []string, full bool) ([]int64, error) {
	if err := j.admit(); err != nil {
		return nil, err
	}
	servers, err := j.selectServers(names)
	if err != nil {
		j.release()
		return nil, err
	}
	runs, err := j.r.begin(servers, store.RunSourceManual)
	if err != nil {
		j.release()
		return nil, fmt.Errorf("%w: %w", ErrStore, err)
	}
	ids := make([]int64, len(runs))
	for i := range runs {
		runs[i].full = full
		ids[i] = runs[i].id
	}
	j.launch(ids, func(ctx context.Context) {
		if err := j.r.execute(ctx, runs); err != nil {
			log.Error().Err(err).Msg("Run failed")
		}
	})
	return ids, nil
}

func (j *Jobs) selectServers(names []string) ([]Server, error) {
	if names == nil {
		return j.r.servers, nil
	}
	seen := make(map[string]bool, len(names))
	servers := make([]Server, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		srv, err := j.r.serverNamed(name)
		if err != nil {
			return nil, err
		}
		seen[name] = true
		servers = append(servers, srv)
	}
	return servers, nil
}

// Cancel requests cancellation of the manual job owning id.
func (j *Jobs) Cancel(id int64) error {
	j.curMu.Lock()
	defer j.curMu.Unlock()
	if j.closed {
		return ErrClosed
	}
	if j.cur == nil || !slices.Contains(j.cur.ids, id) {
		return ErrNoJob
	}
	j.cur.cancel()
	return nil
}

// Close drains manual jobs and item actions after closing admission.
func (j *Jobs) Close() {
	j.curMu.Lock()
	j.closed = true
	j.stop()
	j.curMu.Unlock()
	j.wg.Wait()
	j.r.Close()
}

type bulkAdmission struct {
	item     store.RunItem
	shown    ProgressItem // the item as the live status shows it
	attempts int
	cached   bool
}

// BulkRefresh admits the selected keys to a background refresh job.
func (j *Jobs) BulkRefresh(server string, keys []string) (int64, error) {
	if err := j.admit(); err != nil {
		return 0, err
	}
	run, items, err := j.prepareBulk(server, keys)
	if err != nil {
		j.release()
		return 0, err
	}
	j.launch([]int64{run.id}, func(ctx context.Context) { j.r.executeBulk(ctx, run, items) })
	return run.id, nil
}

func (j *Jobs) prepareBulk(server string, keys []string) (*serverRun, []bulkAdmission, error) {
	srv, err := j.r.serverNamed(server)
	if err != nil {
		return nil, nil, err
	}
	if j.ctx.Err() != nil {
		return nil, nil, ErrClosed
	}
	known, err := j.r.store.KnownItems(j.ctx, server, keys)
	if err != nil {
		if j.ctx.Err() != nil {
			return nil, nil, ErrClosed
		}
		return nil, nil, fmt.Errorf("%w: %w", ErrStore, err)
	}
	items := make([]bulkAdmission, len(keys))
	titles := libraryTitles{store: j.r.store, server: server}
	for i, key := range keys {
		k := known[key]
		row := k.Item
		library, err := titles.title(row.Section)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrStore, err)
		}
		// An item without a cache row or history has no key in row. A bulk
		// refresh selects its items by hand, so it shows no reasons.
		shown := progressItem(row, library)
		shown.RatingKey, shown.Reasons = key, 0
		items[i] = bulkAdmission{item: store.RunItem{Position: i, RatingKey: key, Title: row.Title, Outcome: store.ItemNotReached},
			shown: shown, attempts: row.Attempts, cached: k.Cached}
	}
	if j.ctx.Err() != nil {
		return nil, nil, ErrClosed
	}
	start := store.RunStart{Server: server, Kind: store.RunKindBulkRefresh, Source: store.RunSourceManual, StartedAt: j.r.now()}
	id, err := j.r.store.StartRun(start)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrStore, err)
	}
	return &serverRun{server: srv, id: id, startInfo: start, began: start.StartedAt, selected: len(items), due: len(items), source: store.SourceManual}, items, nil
}

// libraryTitles reads the library titles of a server once for each section.
type libraryTitles struct {
	store  Store
	server string
	seen   map[string]string
}

// title returns the title of the library section, or "" if it is unknown.
func (l *libraryTitles) title(section string) (string, error) {
	if section == "" {
		return "", nil
	}
	if title, ok := l.seen[section]; ok {
		return title, nil
	}
	info, _, err := l.store.LibraryInfo(l.server, section)
	if err != nil {
		return "", err
	}
	if l.seen == nil {
		l.seen = map[string]string{}
	}
	l.seen[section] = info.Title
	return info.Title, nil
}

func (r *Refresher) executeBulk(ctx context.Context, run *serverRun, admitted []bulkAdmission) {
	p := r.progress()
	p.RunStarted(run.id, run.startInfo)
	p.Batch(run.server.Name, len(admitted), len(admitted))
	items := make([]store.RunItem, len(admitted))
	for i, a := range admitted {
		items[i] = a.item
	}
	err := r.bulkBatch(ctx, run, admitted, items)
	r.finishBulk(ctx, run, items, err)
}

func (r *Refresher) bulkBatch(ctx context.Context, run *serverRun, admitted []bulkAdmission, items []store.RunItem) error {
	needPace := false
	for i, a := range admitted {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.cached && needPace {
			if err := r.pace(ctx, run, a.shown, false); err != nil {
				return err
			}
			needPace = false
		}
		item := &items[i]
		r.progress().ItemStarted(run.server.Name, a.shown)
		sent, err := r.bulkItem(ctx, run.server, a, item)
		if sent {
			run.refreshes++
			needPace = true
		}
		switch item.Outcome {
		case store.ItemNotReached:
			return err
		case store.ItemFailed:
			run.failures++
		case store.ItemStopped, store.ItemChanged, store.ItemNotCached:
			run.skipped++
		}
		r.itemDone(run, item.RatingKey, item.Outcome)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Refresher) bulkItem(ctx context.Context, srv Server, a bulkAdmission, item *store.RunItem) (bool, error) {
	if !a.cached {
		item.Outcome = store.ItemNotCached
		return false, nil
	}
	unlock, err := r.locks.lock(ctx, srv.Name, item.RatingKey)
	if err != nil {
		return false, err
	}
	defer unlock()
	row, cached, _, err := r.store.KnownItem(srv.Name, item.RatingKey)
	switch {
	case err != nil:
		return false, fmt.Errorf("%w: %w", ErrStore, err)
	case !cached:
		item.Outcome = store.ItemNotCached
	case !row.StoppedAt.IsZero():
		item.Outcome = store.ItemStopped
	case row.Attempts != a.attempts:
		item.Outcome = store.ItemChanged
	default:
		sent, err := r.sendManual(ctx, srv, row, true, nil)
		return sent, bulkPlexOutcome(ctx, item, sent, err)
	}
	return false, nil
}

func bulkPlexOutcome(ctx context.Context, item *store.RunItem, sent bool, err error) error {
	switch {
	case !sent:
		return err
	case errors.Is(err, ErrStore):
		item.Outcome, item.Error = store.ItemFailed, "recording failed"
		return err
	case err == nil:
		item.Outcome = store.ItemAccepted
	case errors.Is(err, plex.ErrNotFound):
		item.Outcome = store.ItemGone
	default:
		item.Outcome, item.Error = store.ItemFailed, err.Error()
		if ctx.Err() != nil || errors.Is(err, plex.ErrUnavailable) {
			return err
		}
	}
	return nil
}

func (r *Refresher) finishBulk(ctx context.Context, run *serverRun, items []store.RunItem, err error) {
	var reason string
	canceled := ctx.Err() != nil && !errors.Is(err, ErrStore)
	switch {
	case canceled:
		err = nil
	case errors.Is(err, ErrStore):
		reason = "store"
	case errors.Is(err, plex.ErrUnavailable):
		reason = "plex unavailable"
	}
	res := run.result(r.now(), canceled, err)
	recorded, saveErr := r.store.FinishJob(run.id, run.startInfo, res, items)
	r.progress().RunFinished(run.server.Name, res, err, recorded && saveErr == nil)
	if reason != "" {
		log.Error().Str("server", run.server.Name).Int64("run", run.id).Str("reason", reason).Err(err).Msg("Bulk refresh failed")
	}
	if saveErr != nil || !recorded {
		log.Error().Str("server", run.server.Name).Int64("run", run.id).Err(saveErr).Msg("Recording bulk refresh failed")
	}
	log.Info().Str("server", run.server.Name).Int("selected", run.selected).Int("refresh_requests", run.refreshes).
		Int("failed_requests", run.failures).Int("skipped_items", run.skipped).Dur("duration", r.now().Sub(run.began)).Msg("Bulk refresh summary")
}
