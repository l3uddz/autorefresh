# Dashboard Stage 3: Write API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the dashboard's write actions as authenticated JSON endpoints: refresh one item, Check now, fix match search and apply, the episode-number check, stop and resume, Run now, bulk refresh, and cancel. Add the item locks that keep these actions and the runs from acting on the same item at once. No frontend yet.

**Architecture:**
- **Refresher:** gets per-item locks, a run start split into "record the rows" and "execute", the manual item actions, and a `Jobs` type. `Jobs` owns the run lock that the CLI's `runner` holds today. It runs scheduled runs, manual runs, and bulk refresh jobs, one at a time, and cancels manual jobs.
- **Store:** gets stop and resume, a read of a known item (its row, or else its newest history snapshot), a one-item check write, and manual requests for items that have no row.
- **Web:** gets the POST plumbing and the write handlers. They call the refresher through two small interfaces.
- **CLI:** wires `Jobs` into the schedule and the UI. It stops the UI first, then the jobs and the schedule, then the store.

**Tech Stack:** Go (version from `go.mod`), `net/http`, `database/sql` with the existing SQLite driver (one connection), zerolog.

**Spec:** `docs/superpowers/specs/2026-11-22-maintenance-dashboard-design.md`. Read these sections before each task:
- "Manual run" (with "Background jobs")
- "Concurrency of manual actions and runs"
- "Manual actions"
- "Confirmations" (the server side only: counts and results)
- "Items without a cache row (Fixed tab)"
- "HTTP API"
- "Delivery order", stage 3

The stage 2 plan (`docs/superpowers/plans/2026-11-22-dashboard-stage-2-http.md`) lists the conventions of the read API that this stage keeps.

**Branch:** `feat/94-dashboard-write-api` (parent: `main`). Issue: #94. The branch was created from `origin/main` and tracks it, so push with `git push -u origin feat/94-dashboard-write-api`.

## Global Constraints

### Tests and checks

- Tests need no network, no Plex, and no TMDB token.
  - Web tests use a real store (`store.Open` in `t.TempDir()`) and fakes of `web.Plex`, `web.Actions`, and `web.Jobs`.
  - Refresher tests use the existing fake Plex and a real store.
  - Plex and TMDB client tests use `httptest`.
- Each production function has a cognitive complexity of 15 or less (`mise run lint`). Split a function into named steps when needed.
- Each task that adds a public store method adds it to `TestMethodsReportDatabaseFailure` (`internal/store/store_test.go`).

### Log lines

- Do not change any existing log line or field. This includes `Run summary`, `Sent request`, `Request failed`, `Skipped scheduled start, a run is busy`, and `Run failed`.
- New log lines are listed in their task.

### Plex safety

- **No code path asks Plex to refresh or scan a library.** Bulk refresh sends one item refresh at a time, paced by `refresh-delay`.
- The Plex token never reaches the browser.
- Build URLs with `net/url`.
- Every URL the server emits is relative and has no leading slash.

### The one SQLite connection

The connection is shared by the handlers, the runs, and the jobs.
- Never hold a transaction or an open `*sql.Rows` across a Plex or TMDB request or a response write. Read, close, then call Plex.
- Inside a store transaction, use the transaction handle, never `s.db`.

### Contexts of store calls

- **Handler reads use the request context.** Each store method that a web handler calls directly takes the request context first, as in stage 2.
- **Exception: manual actions.** They go through the refresher, whose `Store` methods take no context. A store write after a Plex request was sent must not be lost when the client leaves or `Stop` cancels the request, so it uses no request context. The refresher's reads before a Plex request also take no context. They are single-row reads.

### Locks

- **Lock order:** the run lock (`Jobs`) comes before an item lock. Never take the run lock while you hold an item lock.
- **Runs and jobs wait for an item lock.** A run and a bulk job take it with `lock(ctx, …)` and wait. A manual action is short (one Plex request), so the wait is short.
- **Manual actions never wait.** They take it with `tryLock` and answer `409 {"error":"item busy"}` when it is held. A run holds an item lock for the fix match search and the request of that one item.

### Timeouts

- Each synchronous write handler bounds its whole action with a context deadline below the server's `WriteTimeout` of one minute:
  - `actionTimeout = 30 * time.Second` for refresh, match, check, stop, and resume
  - `searchTimeout = 45 * time.Second` for the match search and the episode check
- The deadline wraps the request context.

### POST requests

- Every POST under `/api/` needs `Content-Type: application/json`. `mime.ParseMediaType` must give `application/json`, and parameters such as `charset` are allowed. Otherwise the answer is `415 {"error":"content type must be application/json"}`. This also holds for POST routes that read no body.
- Bodies are limited to 1 MiB with `http.MaxBytesReader`. A body that is too large gets `413 {"error":"body too large"}`.
- A body that a route reads must be one JSON value with no unknown fields. Otherwise the answer is `400 {"error":"invalid body"}`.
- The cross-origin protection from stage 2 already covers every POST.

### JSON and errors

The JSON and error conventions of stage 2 stay:
- Keys are camelCase.
- `[]` is never `null`.
- Errors are `{"error":"<fixed message>"}`.
- A store error answers 500 `internal error` and is logged. When the request context is done, it is logged at debug instead, and nothing is written.
- No Plex or TMDB text reaches a response, except the stored diagnostic texts. These pass `redact`.

### Commits

- Before each commit, `mise run vendor && mise run test && mise run lint` pass.
- Commit subjects use Conventional Commits with a scope (`plex`, `tmdb`, `store`, `refresher`, `web`, `cli`, `readme`, `agents`) and have at most 72 characters.
- The body ends with `Refs #94` and the `Co-Authored-By` line.

### Rulings

The spec leaves the following open. This plan decides them:

1. **Bulk actions take one server.** Bulk refresh, stop, and resume require `srv`. A `run` row has one server, and most users have one. Cost if wrong: the stage 4 bulk bar sends one request per server.
2. **Cancel reaches only manual jobs.** It cancels a manual run or a bulk job, as the spec names them. A scheduled run cannot be canceled (`404 no such job`).
3. **Skip reasons are counted per run, not listed per item.** A bulk job or a run that skips an item counts it in `skipped` (live status and the new `run.skipped` column). It also logs the reason (`gone`, `stopped`, or `changed`) at debug. A failed item keeps its error in its own timeline. Signals on the event stream do not queue, so a list of item results cannot be delivered reliably. The stream instead carries the final counts in `ServerStatus.Last`, and the item timelines hold each error.
4. **Check now replaces only the item reasons.** For a show, one metadata request cannot see the episode reasons, which come from library list filters. Check now keeps them, and the next library read updates them. Check now never changes the library read times. An item without a row gets `409 item not cached`: there is nothing to update, and the next read adds the item back if it is still incomplete. A Plex 404 removes the row with a `gone` event (`source = manual`).
5. **Re-check by equality.** Attempts are stored in Unix seconds. A run or bulk job decides "changed since the batch" by comparing the row's `LastAttempt` with the batch copy. If they are not equal, the item changed. It does not use `After(batchTime)`.
6. **The open title search sends no year.** It searches by the item title only, so that a wrong item year cannot hide the right candidate. The year chips show the difference. A typed search sends the typed year, if any.
7. **Manual requests inside a manual run are `source = auto`.** The run chose them. The run row gets `source = manual`. Bulk refresh, single refresh, and match requests are `source = manual`.
8. **Completed events keep `source = auto`.** This also holds when Check now observes the completion, because Plex completed the item. The check only saw it.

### Carried over from stage 2

Done here: TMDB transport error texts, the `/api/servers` identity retry, the TMDB health from runs, a log line for Plex failures that the API answers, `ErrorLog`, and a test of the stop order.

Left for stage 4: the `/api` redirect and the non-API 404 and 405 answers that are not JSON. Stage 4 serves the frontend paths.

## Review Focus

1. **A person stops, refreshes, or checks an item while a run has it in its batch.**
   - The run takes the item lock, re-reads the row, and skips the item without an attempt when the row is gone, stopped, or changed. It never sends a fix match from a stale copy.
   - A manual action on an item that a run holds gets 409 at once.
   - Tests in Tasks 4 and 7.
2. **The client leaves, or `Stop` cancels the request, after Plex was asked.**
   - The attempt and its event are still recorded.
   - A double-clicked "Run now" or bulk refresh gets 409 and writes no second `run` row.
   - A failed `StartRun` releases the run lock.
   - Tests in Tasks 6 and 7.
3. **An item that exists only in history (Fixed tab).**
   - Refresh and match write only a `request` event (`source = manual`) and create no row.
   - Stop, resume, and Check now answer 409 `item not cached`.
   - A match search does not cache TMDB IDs, because the `tmdb_id` foreign key needs a row.
   - Tests in Tasks 2, 7, and 8.
4. **Bulk input edges.** These all answer 400:
   - duplicate keys (they are removed)
   - an invalid key
   - more than 5000 items
   - an empty list
   - `keys` together with `matching`
   - a `tab` parameter
   - a `library` without `srv`

   A filter that matches nothing answers 400 `no items`. Tests in Task 10.
5. **A cross-site POST, a POST without `Content-Type: application/json`, and an oversized body.**
   - These answer 403, 415, and 413.
   - No Plex request is sent and no row changes.
   - Tests in Task 9.

---

### Task 1: Plex search year and one-item read

**Files:**
- Modify: `internal/plex/action.go` (`SearchMatches`), `internal/plex/library.go` (`Item`)
- Modify every caller and fake of `SearchMatches`: `internal/refresher/refresh.go`, `internal/refresher/tmdb.go`, and the refresher tests' fake Plex
- Test: `internal/plex/action_test.go`, `internal/plex/library_test.go`

**Interfaces:**
- Produces:
  - `func (c *Client) SearchMatches(ctx context.Context, ratingKey, title, agent, language string, year int) ([]Candidate, error)`. With `year > 0`, the query gets `year=<year>`. With `year == 0`, the query has no `year` key.
  - `func (c *Client) Item(ctx context.Context, ratingKey string) (Item, error)`. It reads `/library/metadata/<key>` with `itemMetadata` and returns `metadata.item()`. A 404 wraps `ErrNotFound`.
- The refresher's `Plex` interface gets both methods. The automatic fix match passes `0` as the year.

- [ ] **Step 1: Write the failing tests**

```go
func TestSearchMatchesSendsYear(t *testing.T)   // year 2019 -> request query has year=2019, plus manual=1, title, agent, language
func TestSearchMatchesOmitsZeroYear(t *testing.T) // year 0 -> query has no "year" key (r.URL.Query().Has("year") == false)
func TestItemReadsMetadata(t *testing.T)
// recorded testdata/item_movie.json (reuse an existing movie metadata recording if one fits) ->
//   Item{RatingKey, GUID, Title, Year, Summary, Thumb, Art, AddedAt, Files} as in the file
func TestItemNotFound(t *testing.T) // 404 -> errors.Is(err, plex.ErrNotFound)
```

- [ ] **Step 2: Run** `go test ./internal/plex -run 'SearchMatches|TestItem' -v`. Expected: build failure.
- [ ] **Step 3: Implement** both methods. Update the callers to pass `0`, and update the refresher interface and fakes.
- [ ] **Step 4: Run** `go test ./internal/plex ./internal/refresher`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(plex): search matches by year and read one item`

---

### Task 2: Store support for manual item actions

**Files:**
- Modify: `internal/store/history.go`, `internal/store/item.go`, `internal/store/library.go`
- Test: `internal/store/history_test.go`, `internal/store/item_test.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces:

```go
const (
	EventStopped EventKind = "stopped"
	EventResumed EventKind = "resumed"
)

// ErrNoItem is the error of a write that needs an item row that does not exist.
var ErrNoItem = errors.New("no item row")

// KnownItem returns the row of the item, or else the snapshot of its newest
// event (Server, Section, RatingKey, Type, Title, Year, Thumb, Art, Path).
// cached is true for a row. found is false when neither exists.
func (s *Store) KnownItem(server, ratingKey string) (it Item, cached, found bool, err error)

// LibraryInfo returns the cached metadata of a library. found is false
// for an unknown library.
func (s *Store) LibraryInfo(server, section string) (info LibraryInfo, found bool, err error)

// SetStopped sets (stop) or clears the stopped time of a row and writes a
// stopped or resumed event (source manual), in one transaction. It returns
// changed=false and writes nothing when the row is already in that state.
// A missing row returns ErrNoItem.
func (s *Store) SetStopped(server, ratingKey string, at time.Time, stop bool) (changed bool, err error)

// ApplyCheck writes the result of a one-item check: it gives the row the
// item reasons, title, year, added time, thumb, art, and (when not empty)
// path of it, keeps the row's other reasons, and deletes the row with a
// completed event when no reason remains. It never changes the library read
// times. A missing row returns ErrNoItem.
func (s *Store) ApplyCheck(server string, it Item, at time.Time) (completed bool, err error)
```

- Changes `RecordRequest`. For a missing row with `Source == SourceManual`, it writes the `request` event from the newest event snapshot of the item, with no attempt and no row update. With no event either, it writes nothing. For `SourceAuto`, a missing row still writes nothing.
- `ApplyCheck` builds a `Read{Server, Section: prev.Section, At: at}` and a `readKey{meta: it, present: true, inItems: true}`. It reuses `writeExisting` and `writeCompleted` in its own transaction. It does not call `ApplyRead`, because `ApplyRead` would bump `last_read`.

- [ ] **Step 1: Write the failing tests**

```go
func TestKnownItemPrefersRow(t *testing.T)          // row + older event -> row values, cached=true
func TestKnownItemFallsBackToNewestEvent(t *testing.T) // completed item -> newest event snapshot, cached=false, found=true
func TestKnownItemUnknown(t *testing.T)             // found=false, err=nil
func TestLibraryInfo(t *testing.T)                  // SaveLibraries then LibraryInfo -> Title/Type/Agent/Language; unknown -> found=false
func TestSetStoppedWritesEvents(t *testing.T)
// stop -> changed, item.stopped_at = at, Timeline newest kind "stopped" source "manual";
// stop again -> changed=false, no new event; resume -> changed, stopped_at NULL, event "resumed"
func TestSetStoppedMissingRow(t *testing.T)         // errors.Is(err, store.ErrNoItem), no event
func TestStoppedSurvivesApplyRead(t *testing.T)     // stop, then ApplyRead lists the item -> stopped_at kept (pins the spec rule)
func TestRecordManualRequestWithoutRow(t *testing.T)
// item completed (row gone, completed event exists); RecordRequest{Source: manual, Action: refresh, Accepted: true}
// -> one new request event with the snapshot of the completed event; no item row exists afterwards
func TestRecordAutoRequestWithoutRowWritesNothing(t *testing.T) // unchanged behaviour
func TestApplyCheckKeepsEpisodeReasons(t *testing.T)
// show row with NoSummary|EpisodeNoSummary; ApplyCheck with item Reasons 0 -> row reasons EpisodeNoSummary, completed=false
func TestApplyCheckCompletes(t *testing.T)
// movie row NoPoster, request accepted before; ApplyCheck with Reasons 0 -> completed=true, row gone,
// completed event with fixed_action "refresh" and the new thumb in its snapshot
func TestApplyCheckKeepsReadTimes(t *testing.T)     // library last_read and last_full_read unchanged
func TestApplyCheckMissingRow(t *testing.T)         // ErrNoItem
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'KnownItem|LibraryInfo|SetStopped|Stopped|ManualRequest|AutoRequest|ApplyCheck' -v`. Expected: build failure.
- [ ] **Step 3: Implement.** Write a helper `newestSnapshot(tx *sql.Tx, server, ratingKey string) (Item, bool, error)` and use it in both `KnownItem` and `RecordRequest`. `KnownItem` runs in one read-only transaction.
- [ ] **Step 4: Run** `go test ./internal/store`. Expected: PASS. Add the new public methods to `TestMethodsReportDatabaseFailure`.
- [ ] **Step 5: Commit**: `feat(store): add stop, resume, item checks, and manual history requests`

---

### Task 3: Skipped counts and the bulk run kind

**Files:**
- Create: `internal/store/migrations/10_run_skipped.sql`
- Modify: `internal/store/run.go`, `internal/web/runs.go`
- Test: `internal/store/run_test.go`, `internal/web/runs_test.go`

**Interfaces:**
- Produces:
  - `const RunKindBulkRefresh RunKind = "bulk-refresh"`
  - `RunResult.Skipped int` and `Run.Skipped int`. `FinishRun`, `restoreRun`, `runsQuery`, and `scanRun` carry them.
  - `runJSON.Skipped int` with the key `skipped`
  - The migration: `ALTER TABLE run ADD COLUMN skipped INTEGER NOT NULL DEFAULT 0;` with a one-line comment

- [ ] **Step 1: Write the failing tests**

```go
func TestFinishRunStoresSkipped(t *testing.T)       // Skipped 3 -> Runs() row Skipped 3; also through restoreRun
func TestServeRunsShowsSkippedAndKind(t *testing.T) // bulk-refresh run with skipped 2 -> JSON kind "bulk-refresh", skipped 2
```

- [ ] **Step 2: Run** the two tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/store ./internal/web`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(store): record skipped items of a run`

---

### Task 4: Item locks and the re-check before each request

**Files:**
- Create: `internal/refresher/locks.go`, `internal/refresher/locks_test.go`
- Modify: `internal/refresher/refresher.go`, `internal/refresher/refresh.go`, `internal/refresher/progress.go`, `internal/web/status.go`
- Test: `internal/refresher/refresh_test.go`, `internal/web/status_test.go`

**Interfaces:**
- Consumes: `store.KnownItem` (Task 2) and `RunResult.Skipped` (Task 3). Both are added to the refresher's `Store` interface.
- Produces:

```go
// itemLocks holds one lock for each item (server + rating key).
type itemLocks struct { /* mu sync.Mutex; held map[itemKey]chan struct{} */ }
func (l *itemLocks) lock(ctx context.Context, server, ratingKey string) (unlock func(), err error) // waits; ctx.Err() when ctx ends
func (l *itemLocks) tryLock(server, ratingKey string) (unlock func(), ok bool)
```

- The lock works like this. A held key maps to a channel that `unlock` closes and deletes. `lock` loops: it takes the channel under `mu`; if there is none, it stores a new one and returns. Otherwise it waits on the channel or on `ctx.Done()`. The map holds only held keys, so it does not grow.
- `New` creates the locks. They live on `Refresher` as the field `locks`.
- `Counts` gets `Skipped int`, and `serverRun` gets `skipped int`. `result()` sets `RunResult.Skipped`.
- `RunProgress` gets `Skipped int` (`skipped`), and `Status.ItemDone` copies it.
- New outcome constants: `OutcomeSkipped = "skipped"`.
- New log line, at debug: `log.Debug().Str("server").Str("title").Str("reason", reason).Msg("Skipped item")`. The reason is `gone`, `stopped`, or `changed`.

`refreshDue` keeps its order, its sleep, and its counts. For each item, after the sleep and `ItemStarted`, it goes through these steps:

1. `unlock, err := r.locks.lock(ctx, server, it.RatingKey)`. An error is the end of the run (`return err`).
2. `row, cached, _, err := r.store.KnownItem(server, it.RatingKey)`. A store error is returned.
3. Skip the item, with no attempt, when:
   - the item has no row (`!cached`): reason `gone`
   - `!row.StoppedAt.IsZero()`: reason `stopped`
   - `!row.LastAttempt.Equal(it.LastAttempt)` or `row.Reasons&^r.opts.IgnoreReasons == 0`: reason `changed`

   A skipped item increments `run.skipped`, calls `r.itemDone(run, key, OutcomeSkipped)`, and logs the line above.
4. Otherwise call `refreshItem(ctx, run, row)` with the **re-read** row, so that `findMatch` sees the current reasons and refreshes.
5. `unlock()` after `refreshItem` returns, also on error.

A dry run takes the locks too, which is harmless.

- [ ] **Step 1: Write the failing tests**

```go
func TestItemLocksExclusive(t *testing.T)   // tryLock same key twice -> second false; other key -> true; after unlock -> true
func TestItemLockWaitsAndHonoursContext(t *testing.T) // held key: lock with ctx canceled after 20ms -> ctx error; after unlock -> lock succeeds
func TestItemLocksForgetReleasedKeys(t *testing.T)    // lock/unlock 100 keys -> len(held) == 0
func TestRunSkipsItemStoppedAfterBatch(t *testing.T)
// fake Plex blocks the first item's Refresh until the test stops item 2 with SetStopped; item 2 -> no request,
// no new attempt, run.Skipped == 1, Progress ItemDone outcome "skipped"
func TestRunSkipsItemChangedAfterBatch(t *testing.T)
// a manual RecordRequest for item 2 during item 1 (same Unix second as the batch) -> item 2 skipped (equality check)
func TestRunUsesReReadReasons(t *testing.T)
// unmatched item (Refreshes 1) in the batch; ApplyCheck clears Unmatched before the item is reached ->
// a plain Refresh is sent, no SearchMatches call (the stale batch copy would have searched)
func TestRunWaitsForManualItemLock(t *testing.T)
// test holds tryLock on item 1; run blocks before item 1's request; release -> request sent
func TestStatusCountsSkipped(t *testing.T)  // ItemDone with Counts{Skipped: 2} -> RunProgress.Skipped == 2
```

- [ ] **Step 2: Run** `go test ./internal/refresher -run 'Lock|Skips|ReRead|WaitsForManual' -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the locks and the re-check in a helper `func (r *Refresher) recheck(run *serverRun, batch store.Item) (row store.Item, skip string, err error)`, so that `refreshDue` stays below the complexity limit.
- [ ] **Step 4: Run** `go test -race ./internal/refresher ./internal/web`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): lock each item and re-check it before a request`

---

### Task 5: Recorded run starts, subset runs, and run identity in the status

**Files:**
- Modify: `internal/refresher/refresher.go`, `internal/refresher/progress.go`, `internal/web/status.go`, `internal/web/events.go` and `internal/web/server.go` (redaction of `Last.Error`)
- Test: `internal/refresher/refresher_test.go`, `internal/web/status_test.go`, `internal/web/events_test.go`

**Interfaces:**
- Produces:

```go
// startedRun is a server run whose row StartRun recorded (id 0 in a dry run).
type startedRun struct {
	srv   Server
	id    int64
	start store.RunStart
}

// begin removes the cache of unconfigured servers and old history (pruneBeforeRun,
// always with the full configured server list), then records a running row for
// each of servers with source and kind. If a StartRun fails, it finishes the rows
// it already started as canceled and returns the error. A dry run records nothing.
func (r *Refresher) begin(servers []Server, source store.RunSource) ([]startedRun, error)

// execute runs each started run in its own goroutine, as Run did (same error
// rules: a store error cancels the others, an unavailable server stops only itself).
func (r *Refresher) execute(ctx context.Context, runs []startedRun) error

// serversNamed returns the configured servers with the given names, in the configured
// order, and ErrUnknownServer for a name that is not configured.
func (r *Refresher) serversNamed(names []string) ([]Server, error)

var ErrUnknownServer = errors.New("unknown server")
```

- `Run(ctx)` keeps its contract. If `ctx` already ended, it returns nil. Otherwise it calls `begin(r.servers, store.RunSourceSchedule)`, then `execute`.
- `runServer` gets a `startedRun`. When `ctx` already ended, it logs `Run canceled` at debug, as today, and finishes the row as canceled.
- `serverRun` gets `id int64`, `start store.RunStart`, and `source store.Source`. A run's requests use `run.source` in `request()`. Scheduled and manual runs use `store.SourceAuto` (ruling 7). `run.start()` returns the stored `start`.
- `Progress.RunStarted` changes to `RunStarted(id int64, start store.RunStart)`. The server is `start.Server`.
- `RunProgress` gets these fields:
  - `ID int64` (`id`)
  - `Kind store.RunKind` (`kind`)
  - `Source store.RunSource` (`source`)
  - `Status store.RunStatus` (`status`, `running` while the run is live)
  - `FinishedAt time.Time` (`finishedAt,omitzero`)
  - `Error string` (`error,omitempty`)
- `ServerStatus` gets `Last *RunProgress` (`last`). `RunFinished` copies the run into `Last` with the final counts of `res`: `Refreshed`, `Matched`, `Failed`, `Skipped`, `Selected`, `Due`, `Status`, `FinishedAt`, and `Error`. Then it sets `Run` to nil. `Snapshot` deep-copies `Last` too.
- `serveStatus` and the event stream send `Last.Error` through `s.redact`. Do this in one helper, `func (s *Server) redactStatus(v StatusView) StatusView`.

- [ ] **Step 1: Write the failing tests**

```go
func TestBeginRecordsRowsBeforeExecute(t *testing.T) // begin(2 servers, manual) -> 2 running rows, source "manual", kind "run"
func TestBeginFailureFinishesStartedRows(t *testing.T)
// store fake fails the 2nd StartRun -> error returned; the 1st row status "canceled"; no running row left
func TestSubsetRunKeepsOtherServer(t *testing.T)
// servers a and b, b has items (one stopped), events, and runs; begin([a]) + execute ->
// b keeps its items, stopped_at, events, and runs (PruneServers got [a b])
func TestCanceledBeforeExecuteFinishesRow(t *testing.T) // ctx canceled, execute -> row status "canceled"
func TestStatusKeepsLastRun(t *testing.T)
// RunStarted(7, start{kind bulk-refresh, source manual}) -> Run.ID 7, Kind, Source, Status running;
// RunFinished(res{Status finished, Skipped 1, Refreshed 2}) -> Run nil, Last.ID 7, Last.Status finished, Last.Skipped 1
func TestStatusRedactsLastError(t *testing.T) // Last.Error holding a configured secret -> "<redacted>" in /api/status and on the stream
```

- [ ] **Step 2: Run** the tests. Expected: FAIL.
- [ ] **Step 3: Implement.** Update every `RunStarted` caller and fake.
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): record runs before they start and run server subsets`

---

### Task 6: Jobs: one run lock for scheduled runs, manual runs, and bulk refresh

**Files:**
- Create: `internal/refresher/jobs.go`, `internal/refresher/jobs_test.go`
- Modify: `internal/refresher/refresh.go` (`serverRun.plain` skips `findMatch`), `cmd/autorefresh/schedule.go`, `cmd/autorefresh/main.go`, `cmd/autorefresh/schedule_test.go`
- Move: `TestRunnerSkipsOverlappingStart` and `TestRunnerLogsRunError` become `TestJobsSkipOverlappingScheduledStart` and `TestJobsLogScheduledRunError` in `jobs_test.go`, with the same assertions.

**Interfaces:**
- Consumes: `begin`, `execute`, and `serversNamed` (Task 5); `itemLocks` and `recheck` (Task 4); `RunKindBulkRefresh` (Task 3).
- Produces:

```go
var (
	ErrBusy  = errors.New("a run is busy")
	ErrNoJob = errors.New("no such job")
	// ErrStore marks a store failure of a manual action or a job admission.
	ErrStore = errors.New("store")
)

// Jobs runs one job at a time: a scheduled run, a manual run, or a bulk refresh.
type Jobs struct {
	r     *Refresher
	run   func(context.Context) error // the scheduled run: r.Run; tests replace it
	ctx   context.Context             // owned by the process; manual jobs run with a child of it
	stop  context.CancelFunc
	mu    sync.Mutex // the run lock, held for the whole job
	curMu sync.Mutex // guards cur
	cur   *job       // the busy manual job; nil otherwise
	wg    sync.WaitGroup
}
type job struct {
	ids    []int64
	cancel context.CancelFunc
}

func NewJobs(r *Refresher) *Jobs
// Scheduled runs j.run(ctx) unless a job is busy; then it logs the warning
// "Skipped scheduled start, a run is busy" and returns. A run error logs
// "Run failed". Both lines are moved unchanged from cmd's runner.
func (j *Jobs) Scheduled(ctx context.Context)
// RunNow starts a manual run of the named servers (nil: all) in the background
// and returns the IDs of its rows. ErrBusy when a job is busy; ErrUnknownServer;
// a begin failure wraps ErrStore. The lock is taken with TryLock before any write.
func (j *Jobs) RunNow(servers []string) ([]int64, error)
// BulkRefresh starts a refresh of the rating keys of server in the background
// and returns the ID of its run row (kind bulk-refresh, source manual).
func (j *Jobs) BulkRefresh(server string, keys []string) (int64, error)
// Cancel cancels the busy manual job if id is one of its run IDs, else ErrNoJob.
func (j *Jobs) Cancel(id int64) error
// Close cancels the manual jobs and waits for them.
func (j *Jobs) Close()
```

**BulkRefresh admission.** Every step runs before the answer:
1. `TryLock` the run lock, or else return `ErrBusy`.
2. Check the server name, or else unlock and return `ErrUnknownServer`.
3. Read the batch copy of each key with `KnownItem`. A key with no row becomes `store.Item{Server: server, RatingKey: key}`, which the loop skips as `gone`.
4. Record the row with `StartRun(RunStart{Server, Kind: RunKindBulkRefresh, Source: RunSourceManual, StartedAt: now})`.
5. Start the goroutine and return the ID.

A store failure in steps 3 or 4 unlocks and returns an error that wraps `ErrStore`.

**The bulk job** runs a `serverRun` with these fields:
- `plain: true`: `refreshItem` skips `findMatch` and sends a plain refresh
- `source: store.SourceManual`
- `selected = len(keys)`

It shares the loop of `refreshDue`: the sleep of `RefreshDelay` between items, the item lock, the re-check, and `refreshItem`. Extract that loop as `func (r *Refresher) refreshBatch(ctx context.Context, run *serverRun, items []store.Item) error` and use it from both. Progress gets these calls:
- `RunStarted`
- `Batch(server, len(keys), len(keys))`
- `ItemStarted` and `ItemDone` for each item
- `RunFinished`

`finishRun` records the end. It logs no `Run summary`. New log line, at info: `log.Info().Str("server").Int("selected").Int("refresh_requests").Int("failed_requests").Int("skipped_items").Dur("duration").Msg("Bulk refresh summary")`.

**CLI.**
- `runner` is removed.
- `serve(ctx, j scheduledStarter, sched)` takes `type scheduledStarter interface{ Scheduled(context.Context) }`. The remaining `serve` tests use a fake starter.
- `main.go` builds `jobs := refresher.NewJobs(r)` and passes it to `serveUntilStopped`. Task 12 does the full wiring and the stop order. Here, keep the current order and add `jobs.Close()` after `served.Wait()`.

- [ ] **Step 1: Write the failing tests**

```go
func TestJobsSkipOverlappingScheduledStart(t *testing.T) // moved; same assertions
func TestJobsLogScheduledRunError(t *testing.T)          // moved; same assertions
func TestRunNowRefusesWhileBusy(t *testing.T)
// scheduled run blocked in j.run -> RunNow returns ErrBusy and writes no run row; BulkRefresh too
func TestScheduledStartSkipsWhileManualJobBusy(t *testing.T) // manual job blocked -> Scheduled logs the skip warning
func TestRunNowReturnsRowIDs(t *testing.T)   // 2 servers -> 2 IDs matching the rows, source manual
func TestRunNowUnknownServer(t *testing.T)   // ErrUnknownServer, lock released (a later RunNow succeeds)
func TestRunNowStoreFailureReleasesLock(t *testing.T) // begin fails -> errors.Is(err, ErrStore); next RunNow works
func TestBulkRefreshSendsPlainRefreshes(t *testing.T)
// unmatched item with Refreshes 3 -> Refresh sent, no SearchMatches; events source "manual";
// run row kind "bulk-refresh", status finished, refreshed 2, skipped 1 (a stopped key), selected 3
func TestBulkRefreshSkipsChangedItem(t *testing.T) // manual RecordRequest on key 2 after admission -> skipped
func TestBulkRefreshPaces(t *testing.T)      // fake sleep records RefreshDelay between items
func TestCancelStopsManualJobAtNextItem(t *testing.T)
// bulk of 3, cancel after item 1 -> row status "canceled", item 3 not requested
func TestCancelUnknownOrScheduled(t *testing.T) // wrong id -> ErrNoJob; during a scheduled run -> ErrNoJob
func TestCloseCancelsAndWaits(t *testing.T)   // Close during a bulk job returns after the job finished its row as canceled
```

- [ ] **Step 2: Run** `go test ./internal/refresher -run 'Jobs|RunNow|Bulk|Cancel|Close|ScheduledStart' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** A manual job sets `cur` under `curMu` before its goroutine starts. The goroutine clears `cur` and unlocks the run lock in a `defer`, so a panic cannot leave the lock held.
- [ ] **Step 4: Run** `go test -race ./internal/refresher ./cmd/autorefresh`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): run manual runs and bulk refreshes as jobs`

---

### Task 7: Manual item actions

**Files:**
- Create: `internal/refresher/manual.go`, `internal/refresher/manual_test.go`

**Interfaces:**
- Consumes: `KnownItem`, `SetStopped`, `ApplyCheck`, the manual `RecordRequest`, and `ErrNoItem` (Task 2); `Plex.Item` (Task 1); the locks (Task 4); `ErrStore` (Task 6). Add `SetStopped`, `ApplyCheck`, and `LibraryInfo` to the refresher's `Store` interface.
- Produces:

```go
var (
	ErrItemBusy    = errors.New("item busy")
	ErrUnknownItem = errors.New("unknown item")
	ErrNotCached   = errors.New("item not cached")
)

// RefreshItem sends a refresh of one item now and records it (source manual).
// It returns nil when Plex accepted it, or the Plex error after the failed
// attempt was recorded. A 404 records a gone event for a cached item.
func (r *Refresher) RefreshItem(ctx context.Context, server, ratingKey string) error
// ApplyMatch sends a match of one item to cand and records it (source manual).
func (r *Refresher) ApplyMatch(ctx context.Context, server, ratingKey string, cand plex.Candidate) error
// CheckItem reads one cached item from Plex and writes its item reasons
// (store.ApplyCheck). It records no attempt.
func (r *Refresher) CheckItem(ctx context.Context, server, ratingKey string) error
// SetStopped stops or resumes one cached item. changed is false when it was
// already in that state.
func (r *Refresher) SetStopped(server, ratingKey string, stop bool) (changed bool, err error)
```

**Common steps of each action:**
1. Resolve the server with `serversNamed`. An unknown name returns `ErrUnknownServer`.
2. `tryLock` the item, or else return `ErrItemBusy`.
3. `KnownItem`:
   - not found: `ErrUnknownItem`
   - not cached, for check and stop: `ErrNotCached`
4. Call Plex with `ctx`.
5. Record the result with the store methods, which take no context. A store error is returned wrapped with `ErrStore` (`fmt.Errorf("%w: %w", ErrStore, err)`). A Plex error is returned as it is.

**Plex errors:**
- A refresh or match whose Plex call fails, also with a context error, still records the failed attempt with `RecordRequest{Accepted: false, Error: err.Error()}`. The request counts as sent.
- A 404 of a refresh, match, or check on a cached item calls `RecordGone(server, key, now, SourceManual)`.

**CheckItem** builds the row with `storeItem(server, plex.Library{ID: row.Section, Type: row.Type}, item, itemReasons(item))`, then calls `ApplyCheck`.

**Requests** use `store.Request{Source: SourceManual, Action: ActionRefresh | ActionMatch, Match: &MatchTarget{GUID, Title: cand.Name, Year: cand.Year}}`. A stopped item stays stopped.

**New log lines:**
- `itemEvent(log.Info(), …).Str("action", action).Msg("Sent manual request")`
- `itemEvent(log.Warn(), …).Str("action", action).Err(err).Msg("Manual request failed")`
- `itemEvent(log.Info(), …).Bool("completed", done).Msg("Checked item")`
- `log.Info().Str("server").Str("title").Bool("stopped", stop).Msg("Changed stopped state")`

`itemEvent` takes a `plex.Library`. Pass the library title from `LibraryInfo`, or an empty `plex.Library` when the library is unknown. Manual actions report nothing to `Progress`. A busy run on the same server must not count them.

- [ ] **Step 1: Write the failing tests**

```go
func TestRefreshItemRecordsManualAttempt(t *testing.T) // Refresh sent; attempts+1, refreshes+1, event source manual, accepted
func TestRefreshItemFailureRecordsAttempt(t *testing.T) // Plex 500 -> error returned, failed event with error text, attempts+1
func TestRefreshItemRecordsAfterContextCancel(t *testing.T)
// fake Refresh cancels ctx and returns ctx.Err() -> attempt + failed event recorded (writes use no ctx)
func TestRefreshItemNotFoundRecordsGone(t *testing.T) // Plex 404 -> row gone, gone event source manual, errors.Is(err, plex.ErrNotFound)
func TestRefreshHistoryItemWritesOnlyEvent(t *testing.T) // completed item -> request event, no row created
func TestRefreshStoppedItemStaysStopped(t *testing.T)
func TestApplyMatchRecordsTarget(t *testing.T) // Match called with guid/name/year; event match_guid/title/year; last_match set
func TestManualActionBusyItem(t *testing.T) // locks.tryLock held -> ErrItemBusy, no Plex call
func TestManualActionUnknown(t *testing.T)  // unknown server -> ErrUnknownServer; unknown key -> ErrUnknownItem
func TestCheckItemUpdatesReasons(t *testing.T) // fake Item with a summary -> NoSummary cleared, no attempt recorded
func TestCheckItemCompletes(t *testing.T)   // all reasons cleared -> row gone, completed event
func TestCheckItemNotCached(t *testing.T)   // history-only item -> ErrNotCached, no Plex call
func TestCheckItemNotFound(t *testing.T)    // 404 -> gone event source manual
func TestSetStoppedItem(t *testing.T)       // stop -> changed true; again -> false; history-only -> ErrNotCached
func TestManualActionStoreFailure(t *testing.T) // store closed after the Plex call -> errors.Is(err, ErrStore)
```

- [ ] **Step 2: Run** `go test ./internal/refresher -run 'RefreshItem|RefreshHistory|RefreshStopped|ApplyMatch|ManualAction|CheckItem|SetStoppedItem' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** Write one helper for the send-and-record path of refresh and match, `func (r *Refresher) sendManual(ctx context.Context, srv Server, it store.Item, cached bool, action store.Action, cand *plex.Candidate) error`. Keep it separate from `refreshItem`, which reports progress and counts the run.
- [ ] **Step 4: Run** `go test -race ./internal/refresher`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): add manual refresh, match, check, and stop actions`

---

### Task 8: Manual match search and the episode check

**Files:**
- Create: `internal/refresher/search.go`, `internal/refresher/search_test.go`
- Modify:
  - `internal/refresher/refresh.go`: extract `fetchPaths`
  - `internal/refresher/tmdb.go`: `tmdbID` gets a `save bool` parameter, and `errTMDB` is renamed to `ErrTMDB`
  - `internal/refresher/episodes.go`: extract `compareEpisodes`
  - `internal/refresher/ids.go`: `parseID` for a typed query

**Interfaces:**
- Produces:

```go
var (
	ErrNotShow = errors.New("not a show")
	ErrTMDBOff = errors.New("tmdb is off")
	ErrTMDB    = errors.New("tmdb lookup") // was errTMDB
)

// MatchQuery is a typed search of the fix match dialog. Text "" runs the
// searches of the dialog's opening.
type MatchQuery struct {
	Text string
	Year int // 0: none; only for a title search
}
type FolderID struct{ ID, Link string } // ID like "tmdb-48213"
type MatchResult struct {
	IDs              []FolderID
	Locations, Files []string
	Searches         []store.MatchSearch // Source: "title", "id", "path", or "tmdb"
	TMDBFailed       bool                // a TMDB lookup failed, so the TMDB step stopped
}
type EpisodeResult struct {
	Match    bool
	Episodes int // the regular episodes that matched; 0 when Match is false
}

func (r *Refresher) SearchMatches(ctx context.Context, server, ratingKey string, q MatchQuery) (MatchResult, error)
func (r *Refresher) EpisodeCheck(ctx context.Context, server, ratingKey, tmdbID string) (EpisodeResult, error)

// fetchPaths gets the paths of the item from Plex and saves a series folder
// with SetPath (a missing row is fine). itemPaths uses it.
func (r *Refresher) fetchPaths(ctx context.Context, srv Server, it store.Item) (plex.Paths, error)
// compareEpisodes compares the local regular episode numbers with TMDB. A TMDB
// failure wraps ErrTMDB; a Plex failure is returned as it is. confirmEpisodeNumbers
// wraps it and keeps its logging and run handling.
func compareEpisodes(ctx context.Context, p Plex, t TMDB, ratingKey, tmdbID string) (count int, ok bool, err error)
// parseID returns the ID of a typed query such as "tvdb-31207", with the rules of parseGroup.
func parseID(text string) (pathID, bool)
```

`SearchMatches` needs no item lock and records no decision. Its steps:

1. Resolve the server and the item (`KnownItem`). Get the agent and the language from `LibraryInfo` of the item's section. An unknown library sends empty values.
2. **Typed query** (`Text != ""`):
   - If `parseID(Text)` matches, search `id.title()` with no year (source `id`).
   - Otherwise search `Text` with `q.Year` (source `title`).
   - `IDs`, `Locations`, and `Files` are empty.
3. **Opening** (`Text == ""`):
   - First, a title search of `it.Title` with no year (source `title`, ruling 6).
   - Then `fetchPaths` gives `Locations` and `Files`, and `parseIDs(paths.All())` gives `IDs`, each with `id.link(show)`.
   - Then one search for each path ID (source `path`).
   - Then, when `r.opts.TMDB != nil`, the TMDB step. It takes the same IDs as `tmdbResults`: `imdb`, and `tvdb` for a show, skipped when the paths hold a `tmdb` ID. It translates each with `tmdbID(…, save: cached)`, so an item with no row caches nothing, and searches each new TMDB ID (source `tmdb`, `FromID` = the path ID).
4. **Failures:**
   - A failed search with `stopError(ctx, err) == nil` that is not a 404 marks that search `Failed` and goes on.
   - A 404, a stop error, or an `ItemPaths` failure returns the error.
   - An `ErrTMDB` failure sets `TMDBFailed` and ends the TMDB step.
   - A store error wraps `ErrStore`.

`EpisodeCheck`:
- An item whose type is not `show` returns `ErrNotShow`.
- With no TMDB client, it returns `ErrTMDBOff`.
- A `tmdbID` that is not all digits returns `ErrUnknownItem`. The web layer validates it first, so this is only a guard.
- Then it calls `compareEpisodes`.

- [ ] **Step 1: Write the failing tests**

```go
func TestSearchMatchesOpening(t *testing.T)
// movie with file ".../Film (2020) {imdb-tt123}/f.mkv", TMDB fake maps tt123->77 ->
// searches in order: title "Film" (no year), path "imdb-tt123", tmdb "tmdb-77" FromID "imdb-tt123";
// IDs [{imdb-tt123, https://www.imdb.com/title/tt123}]; Files from the fake
func TestSearchMatchesTypedID(t *testing.T)    // Text "tvdb-31207" -> one search "tvdb-31207", source "id", year 0
func TestSearchMatchesTypedTitle(t *testing.T) // Text "Other", Year 1999 -> SearchMatches(title "Other", year 1999)
func TestSearchMatchesFailedSearchContinues(t *testing.T) // path search 500 -> Failed true, TMDB step still runs
func TestSearchMatchesTMDBFailure(t *testing.T) // Find fails -> TMDBFailed true, earlier searches kept, err nil
func TestSearchMatchesHistoryItemCachesNothing(t *testing.T) // completed item -> tmdb_id table empty afterwards
func TestSearchMatchesNotFound(t *testing.T)   // ItemPaths 404 -> errors.Is(err, plex.ErrNotFound)
func TestEpisodeCheck(t *testing.T)            // local {1:[1,2]}, TMDB {1:[1,2]} -> Match true, Episodes 2; differing -> Match false
func TestEpisodeCheckGuards(t *testing.T)      // movie -> ErrNotShow; no TMDB -> ErrTMDBOff
func TestParseID(t *testing.T)                 // "tmdb-48213", "TVDB-31207", "imdb-tt7712040" ok; "tmdb-x", "foo-1", "" not
```

- [ ] **Step 2: Run** `go test ./internal/refresher -run 'SearchMatches|EpisodeCheck|ParseID' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.** The existing fix match tests must pass unchanged after the extractions.
- [ ] **Step 4: Run** `go test -race ./internal/refresher`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): search fix match candidates and check episodes on request`

---

### Task 9: POST plumbing and the item write endpoints

**Files:**
- Create: `internal/web/body.go`, `internal/web/actions.go`, `internal/web/actions_test.go`
- Modify: `internal/web/server.go` (routes, `Options.Actions`, the JSON middleware)

**Interfaces:**
- Consumes: the refresher methods and errors of Tasks 6 to 8.
- Produces:

```go
// Actions is the part of the refresher that the item write API uses.
type Actions interface {
	RefreshItem(ctx context.Context, server, ratingKey string) error
	ApplyMatch(ctx context.Context, server, ratingKey string, cand plex.Candidate) error
	CheckItem(ctx context.Context, server, ratingKey string) error
	SetStopped(server, ratingKey string, stop bool) (bool, error)
	SearchMatches(ctx context.Context, server, ratingKey string, q refresher.MatchQuery) (refresher.MatchResult, error)
	EpisodeCheck(ctx context.Context, server, ratingKey, tmdbID string) (refresher.EpisodeResult, error)
}
// Options.Actions Actions; nil: every write route answers 503 {"error":"actions unavailable"}.

func requireJSON(next http.Handler) http.Handler            // POST only: 415 as in Global Constraints; also wraps r.Body in MaxBytesReader(1<<20)
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool // 400 invalid body / 413 body too large; answers and returns false on error
func (s *Server) writeActionError(w http.ResponseWriter, r *http.Request, err error)
```

- **Middleware order:** `securityHeaders → admit → crossOriginProtection → basicAuth → requireJSON → mux`. A cross-site POST is rejected before the content-type check.

**Routes.** Each takes `srv` and `key` with `itemParams`. An invalid value answers 400.

| Route | Body | Success |
| --- | --- | --- |
| `POST /api/item/refresh` | none | `200 {"accepted":true}` |
| `POST /api/item/match` | `{"guid":"…","name":"…","year":2020}` | `200 {"accepted":true}` |
| `POST /api/item/check` | none | `200`, the same JSON as `GET /api/item` for the item after the check: its detail and timeline, read again from the store. A completed item shows from its completed snapshot. |
| `GET /api/item/matches?q=&year=` | none | `200`, see `matchesJSON` below |
| `POST /api/item/episode-check?tmdb=` | none | `200 {"match":bool,"episodes":n}` |

**Validation:**
- **Match body:**
  - `guid` is required, has at most 1024 bytes, and has no control character. Check that with a new `containsControl(s string) bool` in `web`: any rune below 0x20, or 0x7f.
  - `name` has at most 1024 bytes and no control character.
  - `year` is 0 to 9999.
  - Otherwise: `400 {"error":"invalid match"}`.
- **`q`:** at most `maxSearchBytes`, trimmed.
- **`year`:** empty or 1 to 9999. Otherwise `400 invalid year`.
- **`tmdb`:** 1 to 12 digits. Otherwise `400 invalid tmdb`.

`matchesJSON`:

```go
type matchesJSON struct {
	IDs        []folderIDJSON `json:"ids"`        // [] when none
	Locations  []string       `json:"locations"`
	Files      []string       `json:"files"`
	Searches   []searchJSON   `json:"searches"`   // via toSearchJSON: candidate thumbs pass SafeRemoteImage and containsCredential
	TMDBFailed bool           `json:"tmdbFailed"`
}
type folderIDJSON struct{ ID, Link string } // keys "id", "link"
```

**`writeActionError` mapping:**

| Error | Answer |
| --- | --- |
| `ErrItemBusy` | 409 `item busy` |
| `ErrUnknownItem` | 404 `not found` |
| `ErrNotCached` | 409 `item not cached` |
| `ErrNotShow` | 400 `not a show` |
| `ErrTMDBOff` | 400 `tmdb is off` |
| `ErrUnknownServer` | 400 `invalid srv` |
| `ErrStore` | `writeStoreError` |
| `ErrTMDB` wrapping `tmdb.ErrUnauthorized` | 502 `TMDB rejected the token` |
| other `ErrTMDB` | 502 `TMDB unavailable` |
| anything else | `writePlexError` |

Each 502 is logged as in Task 11.

The timeouts of Global Constraints wrap `r.Context()`.

- [ ] **Step 1: Write the failing tests** (fake `Actions` records its calls)

```go
func TestPostNeedsJSONContentType(t *testing.T)   // no header / text/plain -> 415, fake not called; "application/json; charset=utf-8" accepted
func TestPostBodyTooLarge(t *testing.T)           // 2 MiB match body -> 413
func TestCrossSitePostRejected(t *testing.T)      // Sec-Fetch-Site: cross-site -> 403, fake not called
func TestRefreshItemEndpoint(t *testing.T)        // 200 {"accepted":true}; fake got server name of srv and key
func TestActionErrorMapping(t *testing.T)         // table over every row of writeActionError -> status and message
func TestMatchEndpointValidates(t *testing.T)     // empty guid, control char, 1025-byte name, year 10000, unknown field -> 400
func TestCheckEndpointAnswersDetail(t *testing.T) // real store row; fake CheckItem deletes it with a completed event -> 200, cached false
func TestMatchesEndpoint(t *testing.T)            // fake result with a thumb holding a configured token -> thumb dropped; ids/locations/files [] not null
func TestEpisodeCheckEndpointValidates(t *testing.T) // tmdb "x", "" -> 400; "77" -> fake called with "77"
func TestWriteRoutesWithoutActions(t *testing.T)  // Options without Actions -> 503 actions unavailable
func TestActionTimeoutIsSet(t *testing.T)         // fake reads ctx.Deadline() -> present and <= actionTimeout from now
```

- [ ] **Step 2: Run** `go test ./internal/web -run 'Post|CrossSite|Endpoint|ActionError|WriteRoutes|ActionTimeout' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/web`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(web): add the item write endpoints`

---

### Task 10: Runs, cancel, and bulk endpoints

**Files:**
- Create: `internal/web/bulk.go`, `internal/web/bulk_test.go`
- Modify: `internal/web/runs.go`, `internal/web/server.go` (`Options.Jobs`), `internal/web/items.go` (extract the filter parsing for reuse)

**Interfaces:**
- Produces:

```go
// Jobs is the part of refresher.Jobs that the API uses.
type Jobs interface {
	RunNow(servers []string) ([]int64, error)
	BulkRefresh(server string, keys []string) (int64, error)
	Cancel(id int64) error
}
// Options.Jobs Jobs; nil: these routes answer 503 actions unavailable.

const maxBulkItems = 5000

// bulkItems returns the rating keys of a bulk request: the deduplicated
// "keys" of the body, or every item of tab that matches the filter of the
// query when the body is {"matching": true}.
func (s *Server) bulkItems(w http.ResponseWriter, r *http.Request, tab store.Tab) (srv int, keys []string, ok bool)
```

**Routes:**

| Route | Body | Success | Other answers |
| --- | --- | --- | --- |
| `POST /api/runs` | `{"srv": null}` for all servers, or `{"srv": 0}` | `202 {"ids":[…]}` | `ErrBusy` → 409 `a run is busy`; an out-of-range `srv` → 400 `invalid srv` |
| `POST /api/runs/cancel?id=` | none | `200 {"canceled":true}` | `ErrNoJob` → 404 `no such job`; an `id` that is not a positive integer → 400 `invalid id` |
| `POST /api/items/refresh?srv=…&library=&reason=&type=&q=&all=` | `{"keys":["1","2"]}` or `{"matching":true}` | `202 {"id":n,"count":n}` | `ErrBusy` → 409 |
| `POST /api/items/stop` | as above | `200 {"changed":n,"skipped":[{"key":"…","reason":"busy\|not-cached\|unchanged"}]}` | |
| `POST /api/items/resume` | as above, with the Stopped tab | as `stop` | |

- Refresh and stop work on the Attention tab, and resume on the Stopped tab.
- `skipped` is `[]` when empty.
- Stop and resume call `Actions.SetStopped` for each key. `ErrItemBusy` gives `busy`, `ErrNotCached` gives `not-cached`, and `changed == false` gives `unchanged`. Any other error stops the loop and answers with `writeActionError`. The changes made so far stay.
- The bulk stop and resume bound the whole loop with `actionTimeout`. When it ends, the remaining keys are reported as `busy`.

**`bulkItems` rules:**
- **`srv` is required.** Otherwise: 400 `invalid srv`.
- **A `tab` parameter answers 400 `invalid tab`.** The route sets the tab.
- **The filter parameters** are those of `GET /api/items`, parsed by the same function (extract `filterParams` and `allParam` use). `all=1` is valid only on the Attention tab.
- **`keys`:**
  - must not be empty
  - each key must pass `plex.ValidRatingKey`, or else 400 `invalid key`
  - duplicates are removed, keeping the first order
- **`keys` together with `matching`, or neither**, answers 400 `invalid body`.
- **`matching` pages through `ListItems`** (`Limit: maxPageLimit`, following `next`) with `Rules` set as in `serveItems`. It reads only rating keys. No page is held open across a page boundary: `ListItems` returns a closed slice.
- **No items** (a filter that matches nothing) answers 400 `no items`.
- **More than `maxBulkItems`** answers 400 `too many items`.

- [ ] **Step 1: Write the failing tests** (fake `Jobs` and fake `Actions` record calls; real store for `matching`)

```go
func TestRunNowEndpoint(t *testing.T)        // {"srv":null} -> fake got all names, 202 ids; {"srv":1} -> [name1]; {"srv":9} -> 400
func TestRunNowBusy(t *testing.T)            // ErrBusy -> 409 {"error":"a run is busy"}
func TestCancelEndpoint(t *testing.T)        // id=7 -> 200; ErrNoJob -> 404; id=0, id=x -> 400
func TestBulkRefreshKeys(t *testing.T)       // keys ["2","1","2"] -> fake got ["2","1"], 202 {"id":..,"count":2}
func TestBulkRefreshMatching(t *testing.T)   // store with 3 attention rows (1 reason no-poster), reason=no-poster -> fake got that key only
func TestBulkInputEdges(t *testing.T)
// table -> 400 message: no srv; tab=attention; library without srv; keys []; key "../1"; keys+matching; {} ;
// 5001 keys; matching that finds nothing ("no items"); unknown body field
func TestBulkStopAndResume(t *testing.T)
// fake SetStopped: key1 changed, key2 ErrItemBusy, key3 ErrNotCached, key4 unchanged ->
// {"changed":1,"skipped":[{"key":"2","reason":"busy"},{"key":"3","reason":"not-cached"},{"key":"4","reason":"unchanged"}]}
func TestBulkResumeUsesStoppedTab(t *testing.T) // matching on /resume lists only stopped rows
```

- [ ] **Step 2: Run** `go test ./internal/web -run 'RunNow|CancelEndpoint|Bulk' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./internal/web`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(web): add run, cancel, and bulk endpoints`

---

### Task 11: Carried-over fixes: transport texts, identity retry, Plex failure logs, ErrorLog

**Files:**
- Create: `internal/neterr/neterr.go`, `internal/neterr/neterr_test.go`
- Modify: `internal/plex/client.go` (use `neterr.Reason`), `internal/tmdb/client.go`, `internal/web/servers.go`, `internal/web/item.go` (`writePlexError`), `internal/web/server.go` (`ErrorLog`)
- Test: `internal/tmdb/client_test.go`, `internal/web/servers_test.go`, `internal/web/item_test.go`, `internal/web/server_test.go`

**Interfaces:**
- Produces:
  - `package neterr`, with `func Reason(err error) string`. It holds the body of `plex.transportReason`, moved unchanged with its tests.
- Changes:
  - **TMDB transport errors.** A transport failure in `tmdb.Client.get` becomes `fmt.Errorf("GET %s: %s", path, neterr.Reason(err))`. When `ctx` ended, it also wraps the context error: `fmt.Errorf("GET %s: %s: %w", path, reason, ctx.Err())`. No text of the underlying error stays, because it can name a resolver or an address. Status, decode, and validation errors stay as they are.
  - **Identity retry.** `serveServers` asks Plex for an unread identity at most once per `identityRetry = time.Minute` for each server. It keeps `identityTried []time.Time` under `idMu`, and between tries it answers at once without an identity. A successful read is cached as today.
  - **Plex failure logs.** `writePlexError(w, r, err)` gains the request and logs `log.Warn().Err(err).Str("path", r.URL.Path).Msg("Plex request failed")` before it answers. Plex error texts hold no host. When `r.Context()` is done, it logs at debug and writes nothing, as `writeStoreError` does. The image proxy keeps its own answers and does not use this function.
  - **`ErrorLog`.** `http.Server` gets `ErrorLog: stdlog.New(serverErrorWriter{}, "", 0)`. `serverErrorWriter.Write` logs `log.Warn().Str("error", strings.TrimSpace(string(p))).Msg("UI server error")` and returns `len(p), nil`.

- [ ] **Step 1: Write the failing tests**

```go
func TestReasonFixedTexts(t *testing.T)     // moved from plex: timeout, DNS, refused, certificate -> fixed texts
func TestTMDBTransportErrorHidesAddress(t *testing.T)
// client against a closed httptest listener -> err text contains "connection refused", not the listener's host:port
func TestServersRetryIdentityOncePerMinute(t *testing.T)
// fake Identity fails: two GET /api/servers -> 1 Identity call; s.now advanced 61 s -> 2 calls
func TestPlexFailureIsLogged(t *testing.T)  // live view with fake 500 -> warn line "Plex request failed" with path, 502 answer
func TestServerErrorLogUsesZerolog(t *testing.T) // s.srv.ErrorLog.Print("boom") -> warn line "UI server error" with error "boom"
```

- [ ] **Step 2: Run** these tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit** these as separate commits:
  - `fix(tmdb): keep addresses out of transport error texts`
  - `fix(web): retry an unreachable server identity once a minute`
  - `fix(web): log the Plex failures that the API answers`
  - `fix(web): send server errors to the log`

---

### Task 12: CLI wiring, TMDB health from use, stop order, and docs

**Files:**
- Modify: `cmd/autorefresh/main.go`, `cmd/autorefresh/ui.go`, `cmd/autorefresh/schedule.go`, `README.md`, `AGENTS.md`
- Create: `cmd/autorefresh/tmdbhealth.go`
- Test: `cmd/autorefresh/ui_test.go`, `cmd/autorefresh/main_test.go`, `cmd/autorefresh/tmdbhealth_test.go`

**Interfaces:**
- Consumes: `refresher.Jobs` (Task 6), the `Refresher` actions (Tasks 7 and 8), and `web.Options.Actions` and `web.Options.Jobs` (Tasks 9 and 10).
- Produces:

```go
// tmdbHealth reports the result of each TMDB request to rec, so the TMDB
// health follows real use and not only the start check.
type tmdbHealth struct {
	tmdb refresher.TMDB
	rec  interface{ TMDBChecked(error) }
}
// Find, Year, and SeasonEpisodes call the wrapped client and then rec.TMDBChecked(err).

// serveUntilStopped(ctx, ui uiStopper, jobs scheduledJobs, sched cron.Schedule)
type uiStopper interface{ Stop(context.Context) error } // *web.Server; nil interface when the UI is off
type scheduledJobs interface {
	Scheduled(context.Context)
	Close()
}
```

**Wiring:**
- `newUIServer` passes `Actions: r` and `Jobs: jobs`.
- With the UI on and a TMDB token, the refresher's `Options.TMDB` is `tmdbHealth{client, status}`. Otherwise it is the client, or nil.
- `stopUI` takes a `uiStopper`. Keep the nil-interface check. Pass a typed nil `*web.Server` as no `uiStopper` at all, so that no typed nil reaches the interface.

**Stop order.** After the signal, `serveUntilStopped` does these steps in order:
1. `stopUI(ui)`
2. `cancelSched()`
3. `served.Wait()`
4. `jobs.Close()`

Then `run` closes the store.

**Docs:**
- **README, Dashboard section:**
  - The dashboard can now send item refreshes, matches, stop and resume, bulk refreshes, and manual runs.
  - Bulk refresh is paced by `refresh-delay` and never refreshes a whole library.
  - A manual run or a bulk refresh waits for no scheduled run, and is refused while one is busy.
  - Keep the auth warning.
  - No API reference: the API serves the dashboard.
- **AGENTS.md:**
  - The `internal/refresher` row gains the item locks, `Jobs`, the manual actions, and the manual match search.
  - The `internal/web` row says the API now writes, through the refresher, and lists the POST rules (JSON content type, body limit).
  - The `cmd/autorefresh` row describes the stop order with `Jobs`.
  - The `internal/store` row gains stop and resume, the known-item read, and `ApplyCheck`.
  - Add a row for `internal/neterr`.

- [ ] **Step 1: Write the failing tests**

```go
func TestServeUntilStoppedOrder(t *testing.T)
// fakes record "ui-stop", "scheduled-return" (the fake Scheduled returns when its ctx ends), "jobs-close";
// cancel ctx -> order exactly ui-stop, scheduled-return, jobs-close; and ui-stop happens while the
// scheduled ctx is still live (fake checks ctx.Err() == nil inside Stop)
func TestServeUntilStoppedWithoutUI(t *testing.T) // nil uiStopper -> no panic, jobs closed
func TestTMDBHealthReports(t *testing.T)   // Find error tmdb.ErrUnauthorized -> rec got it; Year ok -> rec got nil
func TestRunWiresWriteAPI(t *testing.T)
// run() with ui.listen on a free port (listenFunc replaced), fake Plex server via httptest:
// POST /api/runs {"srv":null} with Content-Type application/json and Sec-Fetch-Site same-origin -> 202;
// a second POST while the run is busy -> 409; signal -> run returns, store closed
```

- [ ] **Step 2: Run** `go test ./cmd/autorefresh -v`. Expected: FAIL.
- [ ] **Step 3: Implement** the wiring and the docs.
- [ ] **Step 4: Run** `mise run vendor && mise run test && mise run lint`. Expected: PASS.
- [ ] **Step 5: Commit** these as separate commits:
  - `feat(cli): wire the dashboard write API`
  - `docs(readme): describe the dashboard actions`
  - `docs(agents): describe the write API and jobs`

---

## Live check (after the final review, before the PR)

Run the branch build against the maintainer's Plex server with `ui.listen` on `127.0.0.1` and a scratch copy of the database:
- `GET /api/item/matches` on one unmatched item. It only reads.
- `POST /api/item/refresh` on one item.
- `POST /api/item/check` on that item.
- Stop and resume on one item.
- `POST /api/runs` for one server, then a second call while that run is busy, which must answer 409.

**Do not apply a match, and do not start a bulk refresh from a filter, on the real server without asking the maintainer first.** A bulk refresh of two explicit keys is fine. Never refresh a whole library. Record what ran and its results in the PR, apart from the local checks.
