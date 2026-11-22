# Dashboard Stage 3: Write API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the dashboard's write actions as authenticated JSON endpoints, with item locks so that these actions and the runs never act on the same item at once. No frontend yet. The actions are:
- refresh one item
- Check now
- fix match search and apply
- the episode-number check
- stop and resume
- Run now
- bulk refresh, with a stored result for each item
- cancel

**Architecture:**
- **Refresher** gets:
  - per-item locks
  - a run start that records the run rows first and then executes them
  - the manual item actions, behind an admission gate that shutdown drains
  - a `Jobs` type. It owns the run lock that the CLI's `runner` holds today and runs scheduled runs, manual runs, and bulk refresh jobs, one at a time.
- **Store** gets:
  - stop and resume
  - a read of a known item: its row, or else its newest history snapshot
  - a write for a check of one item
  - manual requests that use the snapshot taken at admission
  - the per-item results of a bulk job
- **Web** gets the POST plumbing and the write handlers. They call the refresher through two small interfaces.
- **CLI** wires everything. The stop order is: UI, then the schedule and the jobs, then the manual actions, then the store.

**Tech Stack:** Go (version from `go.mod`), `net/http`, `database/sql` with the existing SQLite driver (one connection), zerolog.

**Spec:** `docs/superpowers/specs/2026-11-22-maintenance-dashboard-design.md`. Read these sections before each task:
- "Manual run" (with "Background jobs")
- "Concurrency of manual actions and runs"
- "Manual actions"
- "Confirmations" (the server side only)
- "Items without a cache row (Fixed tab)"
- "HTTP API"
- "Delivery order", stage 3

The stage 2 plan (`docs/superpowers/plans/2026-11-22-dashboard-stage-2-http.md`) lists the read API conventions, which stay.

**Branch:** `feat/94-dashboard-write-api` (parent: `main`). Issue: #94. Push with `git push -u origin feat/94-dashboard-write-api`.

## Global Constraints

### Tests

- Tests need no network, no Plex, and no TMDB token.
- Web tests use a real store (`store.Open` in `t.TempDir()`) and fakes of `web.Plex`, `web.Actions`, and `web.Jobs`.
- Refresher tests use the existing `fakePlex` (`internal/refresher/fake_test.go`) and a real store. They make a failing or blocking store with a type that embeds `*store.Store` and overrides one method, as `failingFinish` does.
- Interleaving tests use channels as barriers, never sleeps.

### Each commit

- Before each commit, `mise run vendor && mise run test && mise run lint` pass. Every task ends compiling and lint-clean.
- Only add an unexported helper together with its first production caller. Otherwise U1000 fails.
- When a signature changes, update every caller and every test in the same step.
- Each production function has a cognitive complexity of 15 or less.
- Each new public store method is added to `TestMethodsReportDatabaseFailure` (`internal/store/store_test.go`).
- Commit subjects use Conventional Commits with a scope (`plex`, `tmdb`, `store`, `refresher`, `web`, `cli`, `readme`, `agents`) and have at most 72 characters. The body ends with `Refs #94` and the `Co-Authored-By` line.

### Log lines

- Do not change any existing log line or field. This includes:
  - `Run summary`
  - `Sent request`
  - `Request failed`
  - `Skipped scheduled start, a run is busy`
  - `Run failed`
- New log lines are listed in their task.
- **Redaction:** a new log line that carries an error text from Plex, TMDB, or `net/http` is a web package line, and it passes the text through the web server's `redact`, as a `Str("error", …)` field.
- The refresher adds no new log line with a Plex or TMDB error text:
  - A manual action returns its error, and the web layer logs it redacted.
  - A bulk job keeps each error in its stored results, which `redact` filters on output.
- The existing refresher lines stay as they are.

### Plex safety

- **No code path asks Plex to refresh or scan a library.** Bulk refresh sends one item refresh at a time, paced by `refresh-delay`.
- The Plex token never reaches the browser.
- Build URLs with `net/url`.
- Every URL that the server emits is relative and has no leading slash.

### The one SQLite connection

- Never hold a transaction or an open `*sql.Rows` across a Plex or TMDB request or a response write.
- Inside a transaction, use the transaction handle.

### Contexts

- **Handler reads use the request context.** Each store method that a web handler calls directly takes the request context first.
- **Manual actions and jobs are the exception.** They go through the refresher, whose `Store` methods take no context. A write after a sent Plex request must not be lost when the client leaves or `Stop` cancels the request, so it uses no request context.
- **Exception to the exception: `SetStopped`.** It takes a context through the web, refresher, and store layers (`BeginTx(ctx, …)`), because it sends no Plex request: canceling it loses nothing. A canceled stop or resume rolls back, and the key is reported as not changed (`timeout` in a bulk request).

### Cancellation boundary

These rules apply to every manual action and every run or job item:
- `ctx` is checked after the item lock is taken and again just before the Plex call starts. If it has ended, nothing is sent and nothing is recorded. An action returns `ctx.Err()`, and a job item counts as not reached.
- Once the Plex call has started, its result is recorded, also when it fails with a context error. It counts as sent.

### Locks

- **Lock order:** the run lock (`Jobs`) before an item lock. Never take the run lock while you hold an item lock.
- **Runs and bulk jobs wait** for an item lock with `lock(ctx, …)`.
- **Manual actions never wait.** They use `tryLock` and answer `409 item busy` when the lock is held.
- **Lock span:** a lock covers the re-check, the Plex call, and the record.

### Re-check

- A run or bulk job takes the item lock and re-reads the row before it acts. The item **changed** when the row's `Attempts` differ from the batch copy. `attempts` grows on every recorded request, accepted or failed (`updateAttempt`), so a second request in the same second is still seen. A timestamp comparison misses it.
- The item also changed when the row's reasons are now all ignored.
- The run then acts on the re-read row.

### Timeouts

Each synchronous write handler bounds its whole action with a deadline below the `WriteTimeout` of one minute. The deadline wraps the request context.
- `actionTimeout = 30 * time.Second`: refresh, match, check, and stop or resume (single and bulk)
- `searchTimeout = 45 * time.Second`: the match search and the episode check

### POST requests

- Every POST under `/api/` needs `Content-Type: application/json`. `mime.ParseMediaType` must give `application/json`, and parameters are allowed. Otherwise: `415 {"error":"content type must be application/json"}`.
- **Bodyless routes accept an empty body only.** These are the single-item refresh, check, stop, resume, episode check, and cancel. Before the route acts, it reads at most one byte, and it proceeds only on a confirmed EOF (`n == 0 && err == io.EOF`).
  - Any byte gives `400 {"error":"body not allowed"}`.
  - Any other read error gives `400 {"error":"invalid body"}`.
  - In both cases the action is not called. This also covers chunked bodies.
- `requireJSON` wraps only the `/api/` routes, and it checks only POST.
- **Routes with a body** read it through `http.MaxBytesReader` (1 MiB). Before the route acts, it decodes one JSON object with `DisallowUnknownFields` and then requires EOF.
  - A literal `null`, a second value, or an unknown field gives `400 invalid body`.
  - Too many bytes give `413 {"error":"body too large"}`.
- Cross-origin protection from stage 2 covers every POST and runs first.

### JSON and errors

The stage 2 conventions stay:
- Keys are camelCase.
- `[]` is never `null`.
- Errors are `{"error":"<fixed message>"}`.
- A store error answers 500 `internal error` and is logged. When the request context is done, it is logged at debug and nothing is written.
- No Plex or TMDB text reaches a response, except the stored diagnostic texts. These pass `redact`.
- `refresher.ErrClosed` answers `503 {"error":"shutting down"}`.

### Rulings

The spec leaves the following open. This plan decides them:

1. **Bulk actions take one server.** Bulk refresh, stop, and resume require `srv`. A `run` row has one server, and most users have one. Stage 4 groups a cross-server selection by server and submits the refresh jobs one after another, because the run lock is global.
2. **Cancel reaches only manual jobs.** It cancels a manual run or a bulk job, as the spec names them. A scheduled run gives `404 no such job`.
3. **Check now replaces only the item reasons.** For a show, one metadata request cannot see the episode reasons, which come from library list filters. Check now keeps them, and the next library read updates them. It never changes the library read times. An item without a row gets `409 item not cached`. A Plex 404 removes the row with a `gone` event (`source = manual`).
4. **The open title search sends no year.** A wrong item year must not hide the right candidate. A typed search sends the typed year, if any.
5. **Requests inside a manual run are `source = auto`.** The run chose them. The run row gets `source = manual`. Bulk refresh, single refresh, and match requests are `source = manual`.
6. **Completed events keep `source = auto`.** This also holds when Check now observes the completion.
7. **A library read that started before a manual action can apply its older view.**
   - The case: a scheduled read collects its Plex data before `ApplyRead`. If Check now or a manual request lands in between, `ApplyRead` can write older reasons, or add back a row that Check now completed.
   - Nothing is lost: each manual request is recorded from the snapshot taken at admission (Task 2), also when the row is gone by then.
   - The next library read corrects the reasons. A row that is added back and completed again gets a second `completed` event.
   - Why it is allowed: the maintainer chose to keep manual actions available during runs. Locking whole library reads against them would block them for the length of a read.
   - Task 4 pins this behaviour with a test, so a later change cannot make it worse unnoticed.
8. **Duplicate bulk keys are removed, not rejected.** The 5000 limit applies after duplicates are removed.

### Carried over from stage 2

Done here:
- TMDB transport error texts
- the `/api/servers` identity retry
- the TMDB health from runs
- a redacted log line for Plex and TMDB failures that the API answers
- `ErrorLog`
- a test of the stop order

Left for stage 4: the `/api` redirect and the non-API 404 and 405 answers that are not JSON.

## Review Focus

1. **A person stops, refreshes, matches, or checks an item while a run has it in its batch.**
   - The run waits for the item lock, re-reads the row, and skips the item without an attempt when it is gone, stopped, or changed (`Attempts` differ). This holds also within the same second.
   - The run never sends a fix match from a stale copy.
   - A manual action on an item that a run holds gets 409.
   - Tests in Tasks 4 and 6.
2. **The client leaves, or `Stop` forces a close, during an action, an admission, or a job.**
   - A sent request is still recorded.
   - Admission is drained before the store closes.
   - A double-clicked Run now or bulk refresh gets 409 and writes no second row.
   - A failed `StartRun` releases the run lock.
   - Tests in Tasks 6, 7, and 12.
3. **An item that exists only in history (Fixed tab).**
   - Refresh and match write only a `request` event from the admission snapshot. They create no row, also when retention removed the event meanwhile.
   - Stop, resume, and Check now answer 409.
   - The match search caches no TMDB ID, also when the row disappears during the search.
   - Tests in Tasks 2, 6, and 8.
4. **Bulk input edges.** These all answer 400:
   - an invalid key
   - more than 5000 items after duplicates are removed
   - an empty list
   - `keys` together with `matching`
   - a `tab` parameter
   - no `srv`

   A filter that matches nothing answers 400 `no items`. Duplicates are removed. Tests in Task 10.
5. **A cross-site POST, a wrong content type, an oversized body, and any body on a bodyless route.**
   - These answer 403, 415, 413, and 400.
   - The action is never called.
   - Tests in Task 9.

---

### Task 1: Plex search year and one-item read

**Files:**
- Modify: `internal/plex/action.go`, `internal/plex/library.go`
- Update every caller of `SearchMatches`:
  - `internal/plex/client_test.go`
  - `internal/plex/action_test.go`
  - `internal/refresher/refresh.go`
  - `internal/refresher/tmdb.go`
  - `internal/refresher/fake_test.go`
  - any other test that `grep -rn "SearchMatches(" --include=*.go` finds
- Test: `internal/plex/action_test.go`, `internal/plex/library_test.go`

**Interfaces:**
- Produces:
  - `func (c *Client) SearchMatches(ctx context.Context, ratingKey, title, agent, language string, year int) ([]Candidate, error)`. With `year > 0`, the query gets `year=<year>`. With 0, it has no `year` key.
  - `func (c *Client) Item(ctx context.Context, ratingKey string) (Item, error)`. It reads `/library/metadata/<key>` with `itemMetadata`, returns `metadata.item()`, and wraps a 404 in `ErrNotFound`.
- The refresher's `Plex` interface gets `Item`, and the updated `SearchMatches`. The automatic fix match passes year 0. `fakePlex` gets `Item`.

- [ ] **Step 1: Write the failing tests**

```go
func TestSearchMatchesSendsYear(t *testing.T)     // year 2019 -> query year=2019 plus manual=1, title, agent, language
func TestSearchMatchesOmitsZeroYear(t *testing.T) // year 0 -> !r.URL.Query().Has("year")
func TestItemReadsMetadata(t *testing.T)          // recorded movie metadata (reuse an existing testdata file if it fits) -> RatingKey, GUID, Title, Year, Summary, Thumb, Art, AddedAt, Files
func TestItemNotFound(t *testing.T)               // 404 -> errors.Is(err, plex.ErrNotFound)
```

- [ ] **Step 2: Run** `go test ./internal/plex -run 'SearchMatches|TestItem' -v`. Expected: build failure.
- [ ] **Step 3: Implement**, and update every caller.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(plex): search matches by year and read one item`

---

### Task 2: Store support for manual item actions

**Files:**
- Modify: `internal/store/history.go`, `internal/store/item.go`, `internal/store/library.go`, `internal/store/tmdb.go`
- Test: `internal/store/history_test.go`, `internal/store/item_test.go`, `internal/store/tmdb_test.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces:

```go
const (
	EventStopped EventKind = "stopped"
	EventResumed EventKind = "resumed"
)
var ErrNoItem = errors.New("no item row") // a write that needs a row that does not exist

// Item gets Attempts int (column attempts); itemColumns and scanItem read it.

// KnownItem returns the row of the item, or else the snapshot of its newest event
// (Server, Section, RatingKey, Type, Title, Year, Thumb, Art, Path). cached: a row;
// found: either exists. One read-only transaction.
func (s *Store) KnownItem(server, ratingKey string) (it Item, cached, found bool, err error)
func (s *Store) LibraryInfo(server, section string) (info LibraryInfo, found bool, err error)
// SetStopped sets (stop) or clears stopped_at and writes a stopped or resumed event
// (source manual) in one transaction begun with BeginTx(ctx, nil). Already in that
// state: changed=false, nothing written. Missing row: ErrNoItem.
func (s *Store) SetStopped(ctx context.Context, server, ratingKey string, at time.Time, stop bool) (changed bool, err error)

// ManualRecord is the outcome of a manual request, with the item as admission saw it.
type ManualRecord struct {
	Request          // Source must be SourceManual
	Snapshot Item    // the item at admission (row or history snapshot)
	Cached   bool    // admission found a row
	Gone     bool    // Plex answered 404
}
// RecordManual writes a manual outcome in one transaction (rules below).
func (s *Store) RecordManual(m ManualRecord) error
// ApplyCheck gives the row the item reasons, title, year, added time, thumb, art, and
// non-empty path of it, keeps its other reasons, and deletes it with a completed event
// when no reason remains. Never touches library read times. Missing row: ErrNoItem.
func (s *Store) ApplyCheck(server string, it Item, at time.Time) (completed bool, err error)
```

**`RecordManual` rules.** `RecordRequest` and `RecordGone` stay unchanged for the automatic path. `RecordManual` reuses their helpers (`updateAttempt`, `writeRequestOutcome`, `requestEvent`, `snapshotEvent`, `insertEvent`, `deleteItem`). It takes the mode from admission, not from the row's state when it records.

| Admission | Plex | Row exists now | Writes |
| --- | --- | --- | --- |
| history-only (`!Cached`) | any | either | a `request` event from `Snapshot` (accepted, or failed with `Error`; a 404 is a failed request). No row is touched, also not a row that a read inserted meanwhile. |
| cached | 404 | yes | a `gone` event from the row, and the row is deleted |
| cached | 404 | no | a `gone` event from `Snapshot` |
| cached | other | yes | today's `RecordRequest` path: attempt, outcome, and event |
| cached | other | no | a `request` event from `Snapshot`, no row |

**`ApplyCheck` implementation.** It builds `Read{Server, Section: prev.Section, At: at}` and `readKey{meta: it, present: true, inItems: true}`. It reuses `writeExisting` and `writeCompleted` in its own transaction. It never calls `ApplyRead`, because that would bump `last_read`.

**`SaveTMDBID` change.** It inserts only while the item row exists: `INSERT … SELECT … WHERE EXISTS (SELECT 1 FROM item WHERE server = ? AND rating_key = ?)`, with the existing `ON CONFLICT` update. A missing row is a no-op and not an error. This one statement is atomic, so a row that is deleted during a TMDB request cannot cause a foreign-key error. Update its doc comment.

- [ ] **Step 1: Write the failing tests**

```go
func TestKnownItemPrefersRow(t *testing.T)             // row + older event -> row values, cached
func TestKnownItemFallsBackToNewestEvent(t *testing.T) // completed item -> newest snapshot, !cached, found
func TestKnownItemUnknown(t *testing.T)                // !found, nil error
func TestItemAttemptsCounted(t *testing.T)             // two RecordRequest calls at the same Unix second -> KnownItem Attempts 2
func TestLibraryInfo(t *testing.T)                     // SaveLibraries then LibraryInfo -> Title/Type/Agent/Language; unknown -> !found
func TestSetStoppedWritesEvents(t *testing.T)
// stop -> changed, stopped_at = at, newest event "stopped"/"manual"; again -> unchanged, no event;
// resume -> changed, stopped_at NULL, event "resumed"
func TestSetStoppedMissingRow(t *testing.T)            // ErrNoItem, no event
func TestStoppedSurvivesApplyRead(t *testing.T)        // stop, then a read lists the item -> stopped_at kept
func TestRecordManualTable(t *testing.T)
// one case per row of the RecordManual table; each asserts the events written (kind, source manual, snapshot
// fields), the row's attempts/outcome, and whether the row exists afterwards. The "history-only, row exists now"
// case inserts a row before recording and asserts its attempts and last_outcome are untouched.
func TestRecordManualAfterRowAndEventsPruned(t *testing.T) // cached, row deleted and its events pruned -> event from Snapshot
func TestSetStoppedHonoursContext(t *testing.T)            // canceled ctx -> error, stopped_at unchanged, no event
func TestApplyCheckKeepsEpisodeReasons(t *testing.T)   // show NoSummary|EpisodeNoSummary, check with 0 -> EpisodeNoSummary, !completed
func TestApplyCheckCompletes(t *testing.T)             // accepted request before; check with 0 -> completed, row gone, completed event fixed_action "refresh", new thumb
func TestApplyCheckKeepsReadTimes(t *testing.T)        // last_read and last_full_read unchanged
func TestApplyCheckMissingRow(t *testing.T)            // ErrNoItem
func TestSaveTMDBIDWithoutRowIsNoop(t *testing.T)      // nil error, tmdb_id empty
```

- [ ] **Step 2: Run** the new tests. Expected: build failure.
- [ ] **Step 3: Implement.** Add one helper, `newestSnapshot(tx *sql.Tx, server, ratingKey string) (Item, bool, error)`, for `KnownItem`.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(store): add stop, resume, item checks, and manual history requests`

---

### Task 3: Skipped counts, bulk run kind, and per-item job results

**Files:**
- Create: `internal/store/migrations/10_run_items.sql`
- Modify: `internal/store/run.go`, `internal/web/runs.go` (the `skipped` field only)
- Test: `internal/store/run_test.go`, `internal/web/runs_test.go`, `internal/store/store_test.go`

**Interfaces:**
- The migration, with a comment for each statement:

```sql
ALTER TABLE run ADD COLUMN skipped INTEGER NOT NULL DEFAULT 0;
CREATE TABLE run_item (
    run_id INTEGER NOT NULL REFERENCES run (id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    rating_key TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, position)
);
```

- Produces:

```go
const RunKindBulkRefresh RunKind = "bulk-refresh"
// RunResult.Skipped and Run.Skipped int: FinishRun, restoreRun, runsQuery, scanRun carry them.

// The outcomes of one item of a bulk job.
const (
	ItemAccepted   = "accepted"    // Plex accepted the refresh
	ItemFailed     = "failed"      // the request failed; Error holds the text
	ItemGone       = "gone"        // Plex answered 404; the row was removed
	ItemStopped    = "stopped"     // skipped: the item is stopped
	ItemChanged    = "changed"     // skipped: another action changed it since admission
	ItemNotCached  = "not-cached"  // skipped: no row at admission or at the re-check
	ItemNotReached = "not-reached" // the job ended (cancel, stop, failure) before the item
)
type RunItem struct {
	Position             int
	RatingKey, Title     string
	Outcome, Error       string
}
// FinishJob records the end of the bulk job id like FinishRun and writes its item
// results, in one transaction, so a job never shows a terminal status without its
// results. FinishRun keeps its signature; both share one unexported helper that runs
// inside the transaction.
func (s *Store) FinishJob(id int64, start RunStart, r RunResult, items []RunItem) error
// RunItems returns the results of a run in position order ([] when none).
func (s *Store) RunItems(ctx context.Context, runID int64) ([]RunItem, error)
```

- `runJSON` gets `Skipped int` (`skipped`).

- [ ] **Step 1: Write the failing tests**

```go
func TestFinishRunStoresSkipped(t *testing.T)     // Skipped 3 -> Runs() Skipped 3, also via restoreRun
func TestRunItemsRoundTrip(t *testing.T)          // FinishJob with 2 items -> run finished, RunItems same order and fields
func TestFinishJobIsAtomic(t *testing.T)          // an item that violates the primary key (duplicate position) -> error, run still running, no items
func TestRunItemsGoWithRun(t *testing.T)          // PruneHistory removes the run -> RunItems []
func TestServeRunsShowsSkipped(t *testing.T)      // kind "bulk-refresh", skipped 2 in JSON
```

- [ ] **Step 2: Run** these tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(store): record skipped items and bulk job results`

---

### Task 4: Item locks and the re-check before each run request

**Files:**
- Create: `internal/refresher/locks.go`, `internal/refresher/locks_test.go`
- Modify: `internal/refresher/refresher.go`, `internal/refresher/refresh.go`, `internal/refresher/progress.go`, `internal/web/status.go`
- Test: `internal/refresher/refresh_test.go`, `internal/web/status_test.go`

**Interfaces:**
- Consumes: `KnownItem` and `Item.Attempts` (Task 2), and `RunResult.Skipped` (Task 3). Add `KnownItem` to the refresher's `Store` interface.
- Produces:

```go
type itemLocks struct { /* mu sync.Mutex; held map[itemKey]chan struct{} */ }
// lock waits for the item lock or the end of ctx. It checks ctx again after it got the
// lock, and then releases it and returns ctx.Err() if ctx ended.
func (l *itemLocks) lock(ctx context.Context, server, ratingKey string) (unlock func(), err error)
```

- **The lock algorithm.** A held key maps to a channel that `unlock` closes and deletes from the map. `lock` loops: under `mu`, it stores a new channel if the key is free and returns. Otherwise it waits on the channel or on `ctx.Done()`. The map holds only held keys.
- `tryLock` arrives in Task 6 with its first caller.
- `Refresher` gets the field `locks *itemLocks`, which `New` creates.
- `Counts` gets `Skipped int`, and `serverRun` gets `skipped int`. `result()` sets `RunResult.Skipped`. `RunProgress` gets `Skipped` (`skipped`), and `Status.ItemDone` copies it.
- New constant: `OutcomeSkipped = "skipped"`.
- New log line, at debug: `log.Debug().Str("server").Str("title").Str("reason", reason).Msg("Skipped item")`. The reason is `gone`, `stopped`, or `changed`.

**The batch loop.** Extract it from `refreshDue` into `func (r *Refresher) refreshBatch(ctx context.Context, run *serverRun, items []store.Item) error`, with these helpers. Each stays below the complexity limit.
- `func (r *Refresher) pace(ctx context.Context, i int) error`: the sleep of `RefreshDelay` before every item after the first, unless it is a dry run. The behaviour is unchanged.
- `func (r *Refresher) recheck(run *serverRun, batch store.Item) (row store.Item, skip string, err error)`:
  - no row: `gone`
  - `!row.StoppedAt.IsZero()`: `stopped`
  - `row.Attempts != batch.Attempts`, or `row.Reasons&^r.opts.IgnoreReasons == 0`: `changed`
  - otherwise `skip == ""`
- **One item**, `func (r *Refresher) runItem(ctx context.Context, run *serverRun, batch store.Item) error`:
  1. `run.remaining--`
  2. `ItemStarted`
  3. `lock`
  4. `recheck`
  5. When `skip != ""`: `run.skipped++`, the debug log line, and `itemDone(…, OutcomeSkipped)`. When `skip == ""`: `refreshItem(ctx, run, row)` with the **re-read** row.
  6. `unlock` in a defer

  The existing `ctx.Err()` check before the send in `refreshItem` stays.

`refreshDue` calls `refreshBatch(ctx, run, due)`.

- [ ] **Step 1: Write the failing tests**

```go
func TestItemLocksExclusive(t *testing.T)            // lock key A; lock A with a canceled ctx -> ctx error; other key -> ok; after unlock -> ok
func TestItemLockRechecksContext(t *testing.T)       // ctx canceled while waiting, lock freed at the same time -> ctx error and the key is free again
func TestItemLocksForgetReleasedKeys(t *testing.T)   // 100 lock/unlock -> len(held) == 0
func TestRunSkipsItemStoppedAfterBatch(t *testing.T)
// fakePlex blocks item 1's Refresh on a channel; the test calls SetStopped on item 2, then releases ->
// no request for item 2, its attempts unchanged, run row skipped 1, Progress outcome "skipped"
func TestRunSkipsSameSecondManualRequest(t *testing.T)
// fixed clock (r.now returns one instant); while item 1 blocks, a RecordRequest on item 2 at that same
// second -> item 2 skipped (Attempts differ)
func TestRunUsesReReadReasons(t *testing.T)
// unmatched item (Refreshes 1); while item 1 blocks, ApplyCheck clears Unmatched -> plain Refresh, no SearchMatches
func TestRunWaitsForItemLock(t *testing.T)           // test holds r.locks.lock on item 1; run blocks; unlock -> request sent
func TestStaleReadAfterCheckIsCorrectedNextRead(t *testing.T)
// Ruling 7: a read staged before ApplyCheck applies older reasons; the next read with current data restores them
func TestStatusCountsSkipped(t *testing.T)           // ItemDone Counts{Skipped: 2} -> RunProgress.Skipped 2
```

- [ ] **Step 2: Run** `go test ./internal/refresher -run 'Lock|Skips|ReRead|WaitsForItemLock|StaleRead' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): lock each item and re-check it before a request`

---

### Task 5: Recorded run starts and run identity in the status

**Files:**
- Modify: `internal/refresher/refresher.go`, `internal/refresher/progress.go`, `internal/web/status.go`, `internal/web/server.go`, `internal/web/events.go`
- Test: `internal/refresher/refresher_test.go` (or `run_test.go`), `internal/web/status_test.go`, `internal/web/events_test.go`

**Interfaces:**
- Produces:

```go
type startedRun struct {
	srv   Server
	id    int64 // 0 in a dry run
	start store.RunStart
}
// begin calls pruneBeforeRun (always with the full configured server list), then
// records a running row (kind run) for each of servers with source. If a StartRun
// fails, it finishes the rows it already started as canceled and returns the error.
// A dry run records nothing.
func (r *Refresher) begin(servers []Server, source store.RunSource) ([]startedRun, error)
// execute runs each started run in its own goroutine with the error rules of today's Run.
func (r *Refresher) execute(ctx context.Context, runs []startedRun) error
```

- **`Run(ctx)`:** if `ctx` already ended, it returns nil. Otherwise it calls `begin(r.servers, store.RunSourceSchedule)`, then `execute`.
- **`runServer`** takes a `startedRun`. When `ctx` ended before it started, it logs `Run canceled` at debug, as today, and finishes the row as canceled.
- **`serverRun`** gets:
  - `id int64`
  - `startInfo store.RunStart`. It replaces the method `start()`, so every use of `run.start()` reads `run.startInfo`.
  - `source store.Source`. `request()` uses it. Runs set `store.SourceAuto` (ruling 5).
- **`Progress.RunStarted`** becomes `RunStarted(id int64, start store.RunStart)`. The server is `start.Server`. Update `noProgress`, `Status`, and every test fake.
- **`RunProgress`** gets:
  - `ID int64` (`id`)
  - `Kind store.RunKind` (`kind`)
  - `Source store.RunSource` (`source`)
  - `Status store.RunStatus` (`status`; `running` while the run is live)
  - `FinishedAt time.Time` (`finishedAt,omitzero`)
  - `Error string` (`error,omitempty`)
- **`ServerStatus`** gets `Last *RunProgress` (`last`). `RunFinished` copies the run into `Last` with the final `Refreshed`, `Matched`, `Failed`, `Skipped`, `Selected`, `Due`, `Status`, `FinishedAt`, and `Error` of `res`, then sets `Run` to nil. `Snapshot` deep-copies `Last`.
- **Redaction:** `func (s *Server) redactStatus(v StatusView) StatusView` redacts `Last.Error`. `serveStatus` and the event stream both use it.

- [ ] **Step 1: Write the failing tests**

```go
func TestBeginRecordsRowsBeforeExecute(t *testing.T)   // 2 servers, manual -> 2 running rows, source manual, kind run
func TestBeginFailureFinishesStartedRows(t *testing.T) // embedded store fails the 2nd StartRun -> error; 1st row canceled; none running
func TestSubsetRunKeepsOtherServer(t *testing.T)
// servers a and b; b has items (one stopped), events, runs; begin([a]) + execute -> b keeps all of them
func TestCanceledBeforeExecuteFinishesRow(t *testing.T) // canceled ctx -> row canceled
func TestStatusKeepsLastRun(t *testing.T)
// RunStarted(7, {kind bulk-refresh, source manual}) -> Run.ID 7, Kind, Source, Status running;
// RunFinished(res{finished, Skipped 1, Refreshed 2}) -> Run nil, Last.ID 7, Last.Status finished, Last.Skipped 1
func TestStatusRedactsLastError(t *testing.T)          // Last.Error with a configured secret -> "<redacted>" in /api/status and the stream
```

- [ ] **Step 2: Run** these tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): record runs before they start and show their identity`

---

### Task 6: Manual item actions and the action gate

**Files:**
- Create: `internal/refresher/manual.go`, `internal/refresher/manual_test.go`
- Modify: `internal/refresher/locks.go` (`tryLock`), `internal/refresher/refresher.go`

**Interfaces:**
- Consumes:
  - Task 2: `KnownItem`, `SetStopped`, `ApplyCheck`, `LibraryInfo`, `RecordManual`, and `ErrNoItem`. Add `SetStopped`, `ApplyCheck`, `LibraryInfo`, and `RecordManual` to the refresher's `Store` interface.
  - Task 1: `Plex.Item`.
- Produces:

```go
var (
	ErrItemBusy      = errors.New("item busy")
	ErrUnknownItem   = errors.New("unknown item")
	ErrNotCached     = errors.New("item not cached")
	ErrUnknownServer = errors.New("unknown server")
	ErrClosed        = errors.New("shutting down")
	ErrStore         = errors.New("store") // wraps a store failure: fmt.Errorf("%w: %w", ErrStore, err)
)

func (l *itemLocks) tryLock(server, ratingKey string) (unlock func(), ok bool)
func (r *Refresher) serverNamed(name string) (Server, error) // ErrUnknownServer

// RefreshItem sends a refresh of one item and records it (source manual). nil: Plex
// accepted it; otherwise the Plex error after the outcome was recorded.
func (r *Refresher) RefreshItem(ctx context.Context, server, ratingKey string) error
func (r *Refresher) ApplyMatch(ctx context.Context, server, ratingKey string, cand plex.Candidate) error
// CheckItem reads one cached item from Plex and writes its item reasons. No attempt.
func (r *Refresher) CheckItem(ctx context.Context, server, ratingKey string) error
// SetStopped checks ctx after it takes the item lock and passes ctx to store.SetStopped.
func (r *Refresher) SetStopped(ctx context.Context, server, ratingKey string, stop bool) (changed bool, err error)
// Close closes the action gate, so every later action returns ErrClosed, and waits
// for the running actions. Jobs.Close calls it.
func (r *Refresher) Close()

// sendManual is the send-and-record path of a manual refresh or match, shared with the
// bulk job (Task 7). it is the snapshot read at admission; cached says whether it was a row.
// sent reports whether the Plex call started. err is the Plex error (after its outcome was
// recorded), ctx.Err() when ctx ended before the call (sent false, nothing recorded), or a
// recording failure wrapped with ErrStore (sent true).
func (r *Refresher) sendManual(ctx context.Context, srv Server, it store.Item, cached bool, action store.Action, cand *plex.Candidate) (sent bool, err error)
```

**Action gate.** `Refresher` gets `gateMu sync.Mutex`, `closed bool`, and `actions sync.WaitGroup`. Each public action begins with `if !r.enter() { return ErrClosed }; defer r.actions.Done()`. `enter` adds to the wait group under `gateMu` only while the gate is open. `Close` sets `closed` under `gateMu`, then calls `actions.Wait()`.

**Common steps of each action:**
1. Pass the gate.
2. `serverNamed`.
3. `tryLock`, or else `ErrItemBusy`.
4. `KnownItem`:
   - not found: `ErrUnknownItem`
   - not cached, for check and stop or resume: `ErrNotCached`
5. Apply the cancellation boundary of Global Constraints: check `ctx` just before the Plex call.
6. Call Plex.
7. Record the result. A store error is wrapped with `ErrStore`.

**Outcomes of `sendManual`.** Every outcome is one `RecordManual` call:

```go
store.ManualRecord{
	Request:  store.Request{Server, RatingKey, At: now, Source: SourceManual, Action, Accepted, Error, Match},
	Snapshot: it,
	Cached:   cached,
	Gone:     errors.Is(err, plex.ErrNotFound),
}
```

- A match sets `Match: &MatchTarget{GUID, Title: cand.Name, Year: cand.Year}`.
- A context error after the call started is recorded as a failed request.
- The store applies the table of Task 2.
- A stopped item stays stopped.

**`CheckItem`** builds the row with `storeItem(server, plex.Library{ID: row.Section, Type: row.Type}, item, itemReasons(item))` and calls `ApplyCheck`. A 404 calls `RecordManual{Cached: true, Gone: true}`. `ErrNoItem` from the store (the row went away in between) is returned as `ErrNotCached`.

**`SetStopped`** maps `ErrNoItem` to `ErrNotCached`, and returns a context error as it is.

**New log lines.** None of them carries an error text (see Global Constraints, "Log lines"):
- `itemEvent(log.Info(), …).Str("action", a).Bool("accepted", ok).Msg("Sent manual request")`
- `itemEvent(log.Info(), …).Bool("completed", done).Msg("Checked item")`
- `log.Info().Str("server").Str("title").Bool("stopped", stop).Msg("Changed stopped state")`

`itemEvent` gets the library title from `LibraryInfo`, or an empty `plex.Library`. Manual actions report nothing to `Progress`.

- [ ] **Step 1: Write the failing tests**

```go
func TestRefreshItemRecordsManualAttempt(t *testing.T)   // attempts+1, refreshes+1, event manual accepted
func TestRefreshItemFailureRecordsAttempt(t *testing.T)  // Plex 500 -> error returned, failed event, attempts+1
func TestRefreshItemRecordsAfterContextCancel(t *testing.T) // fake Refresh cancels ctx and returns ctx.Err() -> failed event recorded
func TestRefreshItemCanceledBeforeSendRecordsNothing(t *testing.T) // ctx canceled before the call -> no Plex call, no event
func TestRefreshItemNotFoundRecordsGone(t *testing.T)    // 404 cached -> row gone, one gone event manual, no request event
func TestRefreshHistoryItemWritesOnlyEvent(t *testing.T) // completed item -> request event, no row
func TestRefreshRowDeletedDuringRequest(t *testing.T)
// fakePlex blocks Refresh; the test deletes the row and prunes its events (ApplyRead completion + PruneHistory);
// release with success -> one request event from the admission snapshot, no row;
// same with a 404 answer -> exactly one manual gone event from the snapshot
func TestRefreshHistoryItemIgnoresNewRow(t *testing.T)
// history-only admission; fakePlex blocks; a read inserts a row; release -> event only, the new row's attempts 0
// and last_outcome ""
func TestRefreshStoppedItemStaysStopped(t *testing.T)
func TestApplyMatchRecordsTarget(t *testing.T)           // Match got guid/name/year; event match fields; last_match set
func TestManualActionBusyItem(t *testing.T)              // r.locks.lock held -> ErrItemBusy, no Plex call
func TestManualActionUnknown(t *testing.T)               // unknown server -> ErrUnknownServer; unknown key -> ErrUnknownItem
func TestCheckItemUpdatesReasons(t *testing.T)           // NoSummary cleared, attempts unchanged
func TestCheckItemCompletes(t *testing.T)                // row gone, completed event
func TestCheckItemNotCached(t *testing.T)                // history-only -> ErrNotCached, no Plex call
func TestCheckItemNotFound(t *testing.T)                 // 404 -> gone event manual
func TestSetStoppedItem(t *testing.T)                    // changed, unchanged, history-only -> ErrNotCached
func TestManualActionStoreFailure(t *testing.T)          // embedded store fails RecordManual -> errors.Is(err, ErrStore), sent true
func TestSetStoppedItemContext(t *testing.T)             // canceled ctx -> context error, not changed
func TestCloseWaitsForRecording(t *testing.T)
// Plex answers at once; an embedded store blocks RecordManual on a channel and signals entry. Start Close in a
// goroutine; wait until an action started after it returns ErrClosed (the gate is closed, so Close is draining);
// assert Close has not returned; release RecordManual -> Close returns, and the event is committed
func TestSendManualReportsSent(t *testing.T)
// ctx canceled before the call -> sent false, ctx error, no event; fake Refresh cancels ctx and returns its error
// -> sent true, failed event recorded
```

- [ ] **Step 2: Run** the new tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): add manual refresh, match, check, and stop actions`

---

### Task 7: Jobs: one run lock for scheduled runs, manual runs, and bulk refresh

**Files:**
- Create: `internal/refresher/jobs.go`, `internal/refresher/jobs_test.go`
- Modify: `cmd/autorefresh/schedule.go`, `cmd/autorefresh/ui.go` (`serveUntilStopped`), `cmd/autorefresh/main.go`, `cmd/autorefresh/schedule_test.go`, `cmd/autorefresh/ui_test.go`
- Move: `TestRunnerSkipsOverlappingStart` and `TestRunnerLogsRunError` become `TestJobsSkipOverlappingScheduledStart` and `TestJobsLogScheduledRunError`, with the same assertions.

**Interfaces:**
- Consumes: `begin` and `execute` (Task 5); `lock`, `recheck`, and `pace` (Task 4); `sendManual`, `serverNamed`, and `Close` (Task 6); `FinishJob` and the outcomes (Task 3).
- Produces:

```go
var (
	ErrBusy  = errors.New("a run is busy")
	ErrNoJob = errors.New("no such job")
)
type Jobs struct {
	r      *Refresher
	run    func(context.Context) error // the scheduled run: r.Run; tests replace it
	ctx    context.Context             // owned by the process; manual jobs use children of it
	stop   context.CancelFunc
	mu     sync.Mutex // the run lock, held for a whole job
	curMu  sync.Mutex // guards cur and closed
	cur    *job       // the busy manual job, nil otherwise
	closed bool
	wg     sync.WaitGroup // admissions in progress and running manual jobs
}
type job struct {
	ids    []int64
	cancel context.CancelFunc
}

func NewJobs(r *Refresher) *Jobs
func (j *Jobs) Scheduled(ctx context.Context) // the old runner.start, same log lines and behaviour
func (j *Jobs) RunNow(servers []string) ([]int64, error) // nil servers: all
func (j *Jobs) BulkRefresh(server string, keys []string) (int64, error)
func (j *Jobs) Cancel(id int64) error
// Close closes admission (later RunNow/BulkRefresh return ErrClosed), cancels the manual
// jobs, waits for the admissions in progress and the jobs, then calls r.Close().
func (j *Jobs) Close()
```

**Admission.** `RunNow` and `BulkRefresh` do these steps in order:
1. **Register.** Under `curMu`: if `closed`, return `ErrClosed`. Otherwise `wg.Add(1)`. Every exit path before the job goroutine starts calls `wg.Done()`, and the goroutine calls it when it ends. `Close` can therefore not return while an admission is still reading or writing.
2. **Take the run lock** with `mu.TryLock()`, or else return `ErrBusy`. This happens before any store write.
3. **Prepare.**
   - `RunNow`: resolve the names with `serverNamed` (`ErrUnknownServer`), then `begin(servers, store.RunSourceManual)`.
   - `BulkRefresh`: `serverNamed`, then `KnownItem` for each key. It keeps `cached` for each key, so a key with no row at admission is recorded as `not-cached` and never sent, even if a row appears later. Then `StartRun(RunStart{Server, Kind: RunKindBulkRefresh, Source: RunSourceManual, StartedAt: now})`.

   A failure here unlocks the run lock and returns the error wrapped with `ErrStore`, or `ErrUnknownServer`.
4. **Start the job.** Set `cur` under `curMu`, with the IDs and the cancel of a child context of `j.ctx`. Start the goroutine. Its `defer` clears `cur`, unlocks `mu`, and calls `wg.Done()`, so a panic cannot leave the lock held. Return the IDs.

**`Scheduled`** keeps the behaviour of `runner.start`: `TryLock`, or else log the warning; run `j.run(ctx)`; log `Run failed` on an error. It does not register in `wg`. The schedule's own wait covers it.

**The bulk job** is a `serverRun` with `id`, `startInfo`, `source: SourceManual`, and `selected = len(keys)`.
- **Its loop** uses `pace` between the items it sends. For each admitted item, in order:
  1. `lock(ctx)`. A context error ends the loop, and the remaining items are not reached.
  2. Re-read the row: `KnownItem`.
  3. Classify the item with the same rules as `recheck`:
     - no row at admission or now: `not-cached`
     - stopped: `stopped`
     - `Attempts` differ: `changed`
  4. Otherwise call `sent, err := sendManual(ctx, srv, row, true, ActionRefresh, nil)`. Map the result by the first rule that matches:
     - `!sent`, so `ctx` ended before the call: the item is `not-reached`, and the loop ends.
     - `errors.Is(err, ErrStore)`: the item is `failed` with the error text `recording failed`. The loop ends, and the job ends as failed with that store error, as a run ends on a store error.
     - `err == nil`: `accepted`
     - `plex.ErrNotFound`: `gone`
     - `plex.ErrUnavailable`: `failed` with the error text. The loop ends, and the job ends as failed.
     - any other error: `failed`, with the error text
  5. Unlock.
- **Counters.** Each comes from the outcome and the `sent` flag, never from the error type alone:
  - `run.refreshes` counts the items with `sent == true`.
  - `run.failures` counts `failed`.
  - `run.skipped` counts `stopped`, `changed`, and `not-cached`.
  - `ItemDone` reports each item with these counts.
- **Terminal write.** The results are collected in memory in key order. When the job ends, by cancel, `Close`, store failure, unavailable server, or completion, every key it did not reach is added as `not-reached`. Then one `FinishJob(id, start, res, items)` call writes the run row and the results in one transaction.
  - The bulk job has its own `finishJob` next to `finishRun`. It uses the same status rules: canceled when `ctx` ended, failed with the error, or else finished.
  - A `FinishJob` failure is returned as the job's error also when the job was canceled. It is never dropped.
  - The job goroutine logs a job error with `log.Error().Str("server").Int64("run", id).Err(err).Msg("Bulk refresh failed")`. This is the store or unavailable error, which today's run logging already logs raw.
- **Progress** gets `RunStarted`, `Batch(server, n, n)`, `ItemStarted` and `ItemDone` for each item, and `RunFinished`. `RunFinished` is called after `FinishJob` returned. So when an event stream client sees `Last.ID` with a final status, the results are committed and `GET /api/runs/items?id=` returns them. That is how the stream delivers the item results (Task 10).
- **New log line,** at info: `log.Info().Str("server").Int("selected").Int("refresh_requests").Int("failed_requests").Int("skipped_items").Dur("duration").Msg("Bulk refresh summary")`. The bulk job logs no `Run summary`.

**CLI:**
- `runner` is removed.
- `serve(ctx, s scheduledStarter, sched)` takes `type scheduledStarter interface{ Scheduled(context.Context) }`.
- `serveUntilStopped(ctx, ui *web.Server, jobs scheduledJobs, sched)` takes `type scheduledJobs interface{ Scheduled(context.Context); Close() }` and calls `jobs.Close()` after `served.Wait()`.
- `main.go` builds `jobs := refresher.NewJobs(r)`.
- The `serve` tests use a fake starter.

- [ ] **Step 1: Write the failing tests**

```go
func TestJobsSkipOverlappingScheduledStart(t *testing.T) // moved
func TestJobsLogScheduledRunError(t *testing.T)          // moved
func TestRunNowRefusesWhileBusy(t *testing.T)            // j.run blocked -> RunNow and BulkRefresh ErrBusy, no new row
func TestScheduledStartSkipsWhileManualJobBusy(t *testing.T)
func TestRunNowReturnsRowIDs(t *testing.T)               // 2 servers -> IDs of the rows, source manual
func TestRunNowUnknownServer(t *testing.T)               // ErrUnknownServer, a later RunNow works
func TestRunNowStoreFailureReleasesLock(t *testing.T)    // begin fails -> ErrStore; a later RunNow works
func TestBulkRefreshResults(t *testing.T)
// keys [a b c d e]: a accepted, b stopped, c no row at admission, d 404, e Plex 500 ->
// run row: kind bulk-refresh, finished, refreshed 3, failed 1, skipped 2, selected 5;
// RunItems outcomes accepted, stopped, not-cached, gone, failed (with error text); events source manual;
// no SearchMatches call (plain refresh)
func TestBulkRefreshSkipsSameSecondChange(t *testing.T)  // fixed clock; RecordRequest on key 2 after admission -> changed
func TestBulkRefreshIgnoresRowCreatedAfterAdmission(t *testing.T) // key without row at admission, row inserted before its turn -> not-cached, no request
func TestBulkRefreshPaces(t *testing.T)                  // fake sleep records RefreshDelay between sends
func TestCancelStopsBulkAtNextItem(t *testing.T)
// item 1's Refresh blocks; Cancel; release -> item 2's ItemStarted never fires (barrier on a recording progress);
// row canceled; RunItems: accepted, not-reached, not-reached
func TestCancelBulkBeforeDispatch(t *testing.T)
// the embedded store's KnownItem (the re-check of item 1) cancels the job -> no Plex call, item 1 not-reached,
// refreshed 0
func TestCancelBulkDuringCall(t *testing.T)
// item 1's fake Refresh cancels the job and returns ctx.Err() -> item 1 failed (sent), refreshed 1, failed 1,
// items 2-3 not-reached, row canceled
func TestBulkRecordingFailureEndsJob(t *testing.T)
// embedded store fails RecordManual on item 1 -> item 1 failed "recording failed", others not-reached, row failed,
// "Bulk refresh failed" logged
func TestBulkFinishJobFailureDuringCancel(t *testing.T)
// canceled job whose FinishJob fails -> the job error is logged (not dropped), the row stays running for
// InterruptRuns at the next start
func TestCancelUnknownOrScheduled(t *testing.T)          // wrong id, or during a scheduled run -> ErrNoJob
func TestCloseDrainsAdmission(t *testing.T)
// embedded store blocks KnownItem inside BulkRefresh admission; Close in a goroutine does not return until the
// block is released; afterwards RunNow -> ErrClosed
func TestCloseCancelsAndWaits(t *testing.T)              // Close during a bulk job returns after the row is canceled and results written
```

- [ ] **Step 2: Run** `go test ./internal/refresher ./cmd/autorefresh -run 'Jobs|RunNow|Bulk|Cancel|Close|ScheduledStart|Serve' -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): run manual runs and bulk refreshes as jobs`

---

### Task 8: Manual match search and the episode check

**Files:**
- Create: `internal/refresher/search.go`, `internal/refresher/search_test.go`
- Modify:
  - `internal/refresher/tmdb.go`: rename `errTMDB` to `ErrTMDB` everywhere
  - `internal/refresher/episodes.go`: extract `compareEpisodes`
  - `internal/refresher/ids.go`: add `parseID`

**Interfaces:**
- Produces:

```go
var (
	ErrNotShow = errors.New("not a show")
	ErrTMDBOff = errors.New("tmdb is off")
	ErrTMDB    = errors.New("tmdb lookup") // renamed from errTMDB
)
type MatchQuery struct {
	Text string // "": the opening searches
	Year int    // 0: none; only for a title search
}
type FolderID struct{ ID, Link string }
type MatchResult struct {
	IDs              []FolderID
	Locations, Files []string
	Searches         []store.MatchSearch // Source "title", "id", "path", or "tmdb"
	TMDBFailed       bool
}
type EpisodeResult struct {
	Match    bool
	Episodes int
}
func (r *Refresher) SearchMatches(ctx context.Context, server, ratingKey string, q MatchQuery) (MatchResult, error)
func (r *Refresher) EpisodeCheck(ctx context.Context, server, ratingKey, tmdbID string) (EpisodeResult, error)
// compareEpisodes compares local regular episode numbers with TMDB; a TMDB failure wraps
// ErrTMDB, a Plex failure is returned as is. confirmEpisodeNumbers wraps it and keeps
// its logging and run handling unchanged.
func compareEpisodes(ctx context.Context, p Plex, t TMDB, ratingKey, tmdbID string) (count int, ok bool, err error)
func parseID(text string) (pathID, bool) // the rules of parseGroup, for a typed "tvdb-31207"
```

`SearchMatches` passes the action gate. It takes no item lock and **has no side effect** apart from the TMDB ID cache: it records no decision and saves no path. Its steps:
1. Resolve the server and the item (`KnownItem`). Get the agent and the language from `LibraryInfo`, or send empty values for an unknown library.
2. **Typed query:**
   - If `parseID(Text)` matches, search `id.title()` with year 0 (source `id`).
   - Otherwise search `Text` with `q.Year` (source `title`).
   - `IDs`, `Locations`, and `Files` stay empty.
3. **Opening:**
   - First, a title search of `it.Title` with year 0 (source `title`).
   - Then `srv.Plex.ItemPaths` directly, without `SetPath`, for `Locations` and `Files`. `parseIDs(paths.All())` gives `IDs`, each with `id.link(show)`.
   - Then one search for each path ID (source `path`).
   - Then, when `r.opts.TMDB != nil`, the TMDB step. It uses the IDs that `tmdbResults` uses: `imdb`, and `tvdb` for a show, and nothing when the paths hold a `tmdb` ID. Each ID goes through the existing `tmdbID`. Its `SaveTMDBID` is now a no-op without a row (Task 2), so a deleted row cannot fail it. Then one search for each new TMDB ID (source `tmdb`, `FromID` = the path ID).
4. **Failures:**
   - A failed search with `stopError(ctx, err) == nil` that is not a 404 is marked `Failed`, and the searches go on.
   - A 404, a stop error, or an `ItemPaths` failure returns the error.
   - `ErrTMDB` sets `TMDBFailed` and ends the TMDB step.
   - `errCache` and other store errors are wrapped with `ErrStore`.

`EpisodeCheck` passes the gate. Its guards, in order:
- an unknown server or item gives the errors of Task 6
- a type other than `show` gives `ErrNotShow`
- no TMDB client gives `ErrTMDBOff`
- a non-numeric `tmdbID` gives `ErrUnknownItem`

Then it calls `compareEpisodes`.

- [ ] **Step 1: Write the failing tests**

```go
func TestSearchMatchesOpening(t *testing.T)
// file ".../Film (2020) {imdb-tt123}/f.mkv", TMDB fake tt123->77 -> searches: title "Film" year 0, path "imdb-tt123",
// tmdb "tmdb-77" FromID "imdb-tt123"; IDs [{imdb-tt123, https://www.imdb.com/title/tt123}]; Files from the fake;
// item path unchanged in the store (no SetPath)
func TestSearchMatchesTypedID(t *testing.T)      // "tvdb-31207" -> one search, source id, year 0
func TestSearchMatchesTypedTitle(t *testing.T)   // "Other", 1999 -> title "Other", year 1999
func TestSearchMatchesFailedSearchContinues(t *testing.T)
func TestSearchMatchesTMDBFailure(t *testing.T)  // Find fails -> TMDBFailed, earlier searches kept, nil error
func TestSearchMatchesRowDeletedDuringTMDB(t *testing.T)
// fake Find blocks; the test deletes the row; release -> nil error, candidates returned, tmdb_id empty
func TestSearchMatchesHistoryItemCachesNothing(t *testing.T)
func TestSearchMatchesNotFound(t *testing.T)     // ItemPaths 404 -> plex.ErrNotFound
func TestEpisodeCheck(t *testing.T)              // equal -> Match, Episodes 2; differing -> !Match
func TestEpisodeCheckGuards(t *testing.T)        // movie -> ErrNotShow; no TMDB -> ErrTMDBOff
func TestParseID(t *testing.T)                   // "tmdb-48213", "TVDB-31207", "imdb-tt7712040" ok; "tmdb-x", "foo-1", "" not
```

- [ ] **Step 2: Run** these tests. Expected: FAIL.
- [ ] **Step 3: Implement.** The existing fix match tests pass unchanged.
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(refresher): search fix match candidates and check episodes on request`

---

### Task 9: POST plumbing and the item write endpoints

**Files:**
- Create: `internal/web/body.go`, `internal/web/actions.go`, `internal/web/actions_test.go`
- Modify: `internal/web/server.go`, `internal/web/item.go` (`writePlexError` becomes a method that logs redacted)

**Interfaces:**
- Produces:

```go
type Actions interface {
	RefreshItem(ctx context.Context, server, ratingKey string) error
	ApplyMatch(ctx context.Context, server, ratingKey string, cand plex.Candidate) error
	CheckItem(ctx context.Context, server, ratingKey string) error
	SetStopped(ctx context.Context, server, ratingKey string, stop bool) (bool, error)
	SearchMatches(ctx context.Context, server, ratingKey string, q refresher.MatchQuery) (refresher.MatchResult, error)
	EpisodeCheck(ctx context.Context, server, ratingKey, tmdbID string) (refresher.EpisodeResult, error)
}
// Options.Actions; nil: every route of this task answers 503 {"error":"actions unavailable"}.

func requireJSON(next http.Handler) http.Handler // the 415 rule for POST; wraps only the /api/ routes
func noBody(w http.ResponseWriter, r *http.Request) bool // reads at most 1 byte; 400 body not allowed
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool // MaxBytesReader, one object, EOF; 400/413
func (s *Server) writeActionError(w http.ResponseWriter, r *http.Request, err error)
// writePlexError(w, r, err) becomes func (s *Server) writePlexError(w http.ResponseWriter, r *http.Request, err error):
// it logs log.Warn().Str("error", s.redact(err.Error())).Str("path", r.URL.Path).Msg("Plex request failed")
// (debug and no answer when r.Context() is done), then answers as today. serveItemLive uses it.
```

- **Middleware order:** `securityHeaders → admit → crossOriginProtection → basicAuth → mux`. Each `/api/` route is registered through `requireJSON`. Register the API routes with one helper so that none is missed, and so that `GET /{$}` is not wrapped.

**Routes.** Each takes `srv` and `key` through `itemParams`, and answers 400 when they are invalid.

| Route | Body | Success |
| --- | --- | --- |
| `POST /api/item/refresh` | none | `200 {"accepted":true}` |
| `POST /api/item/match` | `{"guid","name","year"}` | `200 {"accepted":true}` |
| `POST /api/item/check` | none | `200`, the JSON of `GET /api/item`, read again after the check. Extract the body of `serveItem` into `writeItemDetail(w, r, srv, it)` and use it from both. |
| `POST /api/item/stop` | none | `200 {"changed":bool}` |
| `POST /api/item/resume` | none | `200 {"changed":bool}` |
| `GET /api/item/matches?q=&year=` | none | `200`, see `matchesJSON` below |
| `POST /api/item/episode-check?tmdb=` | none | `200 {"match":bool,"episodes":n}` |

**Validation:**
- **Match body:**
  - `guid` is required, has at most 1024 bytes, and has no control character. Check that with a new `containsControl(s string) bool`: any rune below 0x20, or 0x7f.
  - `name` has at most 1024 bytes and no control character.
  - `year` is 0 to 9999.
  - Otherwise: 400 `invalid match`.
- **`q`:** trimmed, at most `maxSearchBytes`. Otherwise 400 `invalid q`.
- **`year`:** empty or 1 to 9999. Otherwise 400 `invalid year`.
- **`tmdb`:** 1 to 12 digits. Otherwise 400 `invalid tmdb`.

`matchesJSON`:

```go
type matchesJSON struct {
	IDs        []folderIDJSON `json:"ids"`
	Locations  []string       `json:"locations"`
	Files      []string       `json:"files"`
	Searches   []searchJSON   `json:"searches"` // via toSearchJSON (thumb checks)
	TMDBFailed bool           `json:"tmdbFailed"`
}
type folderIDJSON struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}
```

**`writeActionError` mapping:**

| Error | Answer |
| --- | --- |
| `ErrClosed` | 503 `shutting down` |
| `ErrItemBusy` | 409 `item busy` |
| `ErrUnknownItem` | 404 `not found` |
| `ErrNotCached` | 409 `item not cached` |
| `ErrNotShow` | 400 `not a show` |
| `ErrTMDBOff` | 400 `tmdb is off` |
| `ErrUnknownServer` | 400 `invalid srv` |
| `ErrStore` | `writeStoreError` |
| `ErrTMDB` wrapping `tmdb.ErrUnauthorized` | 502 `TMDB rejected the token` |
| other `ErrTMDB` | 502 `TMDB unavailable` |
| a context error | `writeStoreError`'s canceled-request rule (debug, no answer) when `r.Context()` is done; else 504 `timed out` |
| anything else | `s.writePlexError` |

Both TMDB answers log `log.Warn().Str("error", s.redact(err.Error())).Str("path", …).Msg("TMDB request failed")`.

- [ ] **Step 1: Write the failing tests** (a fake `Actions` records its calls)

```go
func TestPostNeedsJSONContentType(t *testing.T)    // none / text/plain -> 415, no call; "application/json; charset=utf-8" ok
func TestPostBodyTooLarge(t *testing.T)            // 2 MiB match body -> 413, no call
func TestBodylessRouteRejectsBody(t *testing.T)    // chunked 2 MiB body on /api/item/refresh -> 400 body not allowed, no call; "{}" -> 400 too
func TestBodylessRouteReadError(t *testing.T)      // body reader returns (0, io.ErrUnexpectedEOF) -> 400 invalid body, no call
func TestRequireJSONOnlyOnAPI(t *testing.T)        // POST / without content type -> not 415 (405 from the mux)
func TestDecodeBodyRules(t *testing.T)             // null, two objects, unknown field -> 400 invalid body
func TestCrossSitePostRejected(t *testing.T)       // Sec-Fetch-Site cross-site -> 403, no call
func TestRefreshItemEndpoint(t *testing.T)         // 200 accepted; fake got the server name of srv and the key
func TestStopResumeEndpoints(t *testing.T)         // /stop -> SetStopped(stop true), {"changed":true}; /resume -> stop false
func TestActionErrorMapping(t *testing.T)          // table over every row -> status and message
func TestMatchEndpointValidates(t *testing.T)      // empty guid, control char, 1025-byte name, year 10000 -> 400 invalid match
func TestCheckEndpointAnswersDetail(t *testing.T)  // fake CheckItem completes the real row -> 200, cached false, timeline has completed
func TestMatchesEndpoint(t *testing.T)             // thumb with a configured token dropped; ids/locations/files [] not null
func TestEpisodeCheckEndpointValidates(t *testing.T) // tmdb "x" or "" -> 400; "77" -> fake got "77"
func TestWriteRoutesWithoutActions(t *testing.T)   // 503 actions unavailable
func TestActionTimeoutIsSet(t *testing.T)          // fake reads ctx.Deadline() -> within actionTimeout
func TestPlexFailureLogIsRedacted(t *testing.T)    // fake error text holding a configured token -> log has "<redacted>", not the token; answer 502
```

- [ ] **Step 2: Run** these tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(web): add the item write endpoints`

---

### Task 10: Runs, cancel, job results, and bulk endpoints

**Files:**
- Create: `internal/web/bulk.go`, `internal/web/bulk_test.go`
- Modify: `internal/web/runs.go`, `internal/web/server.go` (`Options.Jobs`, the `Store` interface gets `RunItems`), `internal/web/items.go` (share the filter parsing)

**Interfaces:**
- Produces:

```go
type Jobs interface {
	RunNow(servers []string) ([]int64, error)
	BulkRefresh(server string, keys []string) (int64, error)
	Cancel(id int64) error
}
// Options.Jobs; nil: the run and bulk POST routes answer 503 actions unavailable.
const maxBulkItems = 5000
func (s *Server) bulkItems(w http.ResponseWriter, r *http.Request, tab store.Tab) (srv int, keys []string, ok bool)
```

**Routes:**

| Route | Body | Success | Errors |
| --- | --- | --- | --- |
| `POST /api/runs` | `{"srv":null}` for all servers, or `{"srv":0}` | `202 {"ids":[…]}` | `ErrBusy` → 409 `a run is busy`; a bad `srv` → 400 `invalid srv` |
| `POST /api/runs/cancel?id=` | none | `200 {"canceled":true}` | `ErrNoJob` → 404 `no such job`; a bad `id` → 400 `invalid id` |
| `GET /api/runs/items?id=` | none | `200 {"items":[{"key","title","outcome","error"}]}` | a bad `id` → 400 `invalid id`; an unknown run → `{"items":[]}`. `error` passes `redact` and is left out when empty. |
| `POST /api/items/refresh?srv=…&library=&reason=&type=&q=&all=` | `{"keys":[…]}` or `{"matching":true}` | `202 {"id":n,"count":n}` | `ErrBusy` → 409 |
| `POST /api/items/stop` | as above | `200 {"changed":n,"skipped":[{"key","reason"}]}` | |
| `POST /api/items/resume` | as above | as `stop` | |

- The tab is Attention for refresh and stop, and Stopped for resume.
- **Stop and resume** call `Actions.SetStopped` for each key, within one `actionTimeout`:
  - `ErrItemBusy` gives the skip reason `busy`.
  - `ErrNotCached` gives `not-cached`.
  - `changed == false` gives `unchanged`.
  - Each call gets the deadline context. A call that returns a context error, which means its transaction rolled back and nothing changed, gives `timeout` for that key.
  - Every key after it gets `timeout` without a call.
  - Any other error stops the loop and answers with `writeActionError`. The changes made so far stay.
- `ErrClosed` from any job call answers 503.

**`bulkItems` rules:**
- **`srv` is required.** Otherwise: 400 `invalid srv`.
- **A `tab` parameter answers 400 `invalid tab`.**
- **The filter parameters** are parsed by the same code as `GET /api/items`. Extract `func (s *Server) filterQuery(v url.Values, tab store.Tab, srv int) (store.ListQuery, error)` and use it from `itemsQuery` too. `all=1` is valid only on the Attention tab.
- **The body** is `{"keys":[…]}` or `{"matching":true}`. Both, or neither, answer 400 `invalid body`.
- **`keys`:**
  - must not be empty
  - each key must pass `plex.ValidRatingKey`, or else 400 `invalid key`
  - duplicates are removed, keeping the first order
- **`matching`** pages through `ListItems` (`Limit: maxPageLimit`, following `next`) with `Rules` as in `serveItems`, and collects the rating keys. It stops with 400 `too many items` as soon as more than `maxBulkItems` are collected.
- **No items** answers 400 `no items`.
- **More than `maxBulkItems`** after duplicates are removed answers 400 `too many items`.

- [ ] **Step 1: Write the failing tests**

```go
func TestRunNowEndpoint(t *testing.T)       // null -> all names, 202 ids; 1 -> [name1]; 9 -> 400
func TestRunNowBusy(t *testing.T)           // 409 a run is busy
func TestCancelEndpoint(t *testing.T)       // 200; ErrNoJob -> 404; id 0 / x -> 400; any body -> 400 body not allowed
func TestRunItemsEndpoint(t *testing.T)     // real store items; error with a configured token -> "<redacted>"; unknown id -> []
func TestBulkRefreshKeys(t *testing.T)      // ["2","1","2"] -> fake got ["2","1"], 202 count 2
func TestBulkRefreshMatching(t *testing.T)  // 3 attention rows, reason=no-poster -> only that key
func TestBulkInputEdges(t *testing.T)
// -> 400 + message: no srv; tab param; library without srv; keys []; key "../1"; keys+matching; {}; 5001 distinct keys;
// matching that finds nothing ("no items"); unknown body field
func TestBulkDedupeBeforeLimit(t *testing.T) // 5000 distinct + 10 duplicates -> accepted
func TestBulkStopAndResume(t *testing.T)
// changed / ErrItemBusy / ErrNotCached / unchanged -> {"changed":1,"skipped":[busy, not-cached, unchanged]}
func TestBulkStopTimeout(t *testing.T)
// actionTimeout shortened; fake SetStopped(ctx, …) for key 2 blocks until ctx.Done() and returns ctx.Err() ->
// key 1 changed, key 2 "timeout" (its call ended by the deadline), keys 3-4 "timeout" without a call
func TestBulkResumeUsesStoppedTab(t *testing.T)
```

- [ ] **Step 2: Run** these tests. Expected: FAIL.
- [ ] **Step 3: Implement.** Make `actionTimeout` a `Server` field set from the constant, so a test can shorten it.
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit**: `feat(web): add run, cancel, job result, and bulk endpoints`

---

### Task 11: Carried-over fixes: transport texts, identity retry, ErrorLog

**Files:**
- Create: `internal/neterr/neterr.go`, `internal/neterr/neterr_test.go`
- Modify: `internal/plex/client.go`, `internal/tmdb/client.go`, `internal/web/servers.go`, `internal/web/server.go`
- Test: `internal/tmdb/client_test.go`, `internal/web/servers_test.go`, `internal/web/server_test.go`

**Interfaces:**
- **`package neterr`:** `func Reason(err error) string`. It is the body of `plex.transportReason`, moved unchanged with its tests. `plex` calls it.
- **TMDB transport errors.** A transport failure in `tmdb.Client.get` returns `fmt.Errorf("GET %s: %s", path, neterr.Reason(err))`. When `ctx` ended, it wraps the context error: `fmt.Errorf("GET %s: %s: %w", path, reason, ctx.Err())`. No text of the underlying error stays. Status, decode, and validation errors stay as they are.
- **Identity retry.** `serveServers` asks Plex for an unread identity at most once per `identityRetry = time.Minute` for each server. It keeps `identityTried []time.Time` under `idMu`, and between tries it answers without an identity at once.
- **`ErrorLog`.** `http.Server` gets `ErrorLog: stdlog.New(serverErrorWriter{s}, "", 0)`. `Write` logs `log.Warn().Str("error", s.redact(strings.TrimSpace(string(p)))).Msg("UI server error")` and returns `len(p), nil`.

- [ ] **Step 1: Write the failing tests**

```go
func TestReasonFixedTexts(t *testing.T)                 // moved from plex
func TestTMDBTransportErrorHidesAddress(t *testing.T)   // closed httptest listener -> "connection refused", no host:port in the text
func TestServersRetryIdentityOncePerMinute(t *testing.T) // failing Identity: 2 requests -> 1 call; s.now + 61 s -> 2 calls
func TestServerErrorLogIsRedacted(t *testing.T)         // ErrorLog.Print("boom <token>") -> warn "UI server error", "<redacted>", no token
```

- [ ] **Step 2: Run** these tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit** these as separate commits:
  - `fix(tmdb): keep addresses out of transport error texts`
  - `fix(web): retry an unreachable server identity once a minute`
  - `fix(web): send server errors to the log`

---

### Task 12: CLI wiring, TMDB health from use, stop order, and docs

**Files:**
- Create: `cmd/autorefresh/tmdbhealth.go`, `cmd/autorefresh/tmdbhealth_test.go`
- Modify: `cmd/autorefresh/main.go`, `cmd/autorefresh/ui.go`, `README.md`, `AGENTS.md`
- Test: `cmd/autorefresh/ui_test.go`, `cmd/autorefresh/main_test.go`

**Interfaces:**

```go
// tmdbHealth reports each TMDB request result to rec, so the TMDB health follows use.
type tmdbHealth struct {
	tmdb refresher.TMDB
	rec  interface{ TMDBChecked(error) }
}
// Find, Year, SeasonEpisodes: call the client, then rec.TMDBChecked(err), and return.
type uiStopper interface{ Stop(context.Context) error }
// serveUntilStopped(ctx, ui uiStopper, jobs scheduledJobs, sched) — ui is a nil interface when the UI is off
// (never a typed nil *web.Server).
```

**Wiring:**
- `newUIServer` passes `Actions: r` and `Jobs: jobs`.
- With the UI on and a TMDB token, `Options.TMDB` is `tmdbHealth{client, status}`. Otherwise it is the client, or nil.

**Stop order** after the signal:
1. `stopUI(ui)`. Admission closes, then a graceful shutdown, then a forced close.
2. `cancelSched()`
3. `served.Wait()`
4. `jobs.Close()`. It closes job admission, cancels the manual jobs, drains the admissions in progress and the jobs, then calls `r.Close()`, which drains the manual actions that are still recording.

Then `run` closes the store. So no store write can follow `Close`, also after a forced UI stop whose handlers outlived `forceWait`.

**Docs:**
- **README, Dashboard section:**
  - The dashboard can send item refreshes, matches, Check now, stop and resume, bulk refreshes, and manual runs.
  - Bulk refresh is paced by `refresh-delay` and never refreshes a whole library.
  - A manual run or a bulk refresh is refused while a run is busy.
  - Keep the auth warning.
  - No API reference.
- **AGENTS.md:**
  - The refresher row gains the item locks, the re-check, `Jobs`, the manual actions, and the match search.
  - The web row says the API now writes through the refresher, and gives the POST rules.
  - The cmd row gives the stop order.
  - The store row gains stop and resume, `KnownItem`, `ApplyCheck`, the manual snapshot, and the run items.
  - Add a row for `internal/neterr`.

- [ ] **Step 1: Write the failing tests**

```go
func TestServeUntilStoppedOrder(t *testing.T)
// fakes record "ui-stop", "scheduled-return", "jobs-close"; cancel ctx -> exactly that order; the fake UI Stop sees
// the schedule ctx still live
func TestServeUntilStoppedWithoutUI(t *testing.T)  // nil uiStopper -> jobs closed, no panic
func TestStopDrainsActionAfterForcedUIStop(t *testing.T)
// real web.Server + refresher; fakePlex answers Refresh at once; an embedded store blocks RecordManual on a channel
// and signals entry. POST /api/item/refresh in flight and blocked in RecordManual; stopUI with a short budget
// returns (forced). Start jobs.Close in a goroutine; wait until r.RefreshItem returns ErrClosed (the gate is
// closed, Close is draining); assert Close has not returned; release RecordManual -> Close returns, the event is
// committed, then closeStore succeeds
func TestTMDBHealthReports(t *testing.T)           // Find ErrUnauthorized -> rec got it; Year ok -> rec got nil
func TestRunWiresWriteAPI(t *testing.T)
// run() with ui.listen (listenFunc replaced), fake Plex via httptest: POST /api/runs {"srv":null}
// (Content-Type application/json, Sec-Fetch-Site same-origin) -> 202; again while busy -> 409; signal -> run returns
```

- [ ] **Step 2: Run** `go test ./cmd/autorefresh -v`. Expected: FAIL.
- [ ] **Step 3: Implement.**
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
- `POST /api/runs` for one server, then a second call while it is busy, which must answer 409.
- A bulk refresh of two explicit keys, then `GET /api/runs/items`.

**Do not apply a match, and do not start a bulk refresh from a filter, on the real server without asking the maintainer first.** Never refresh a whole library. Record what ran in the PR, apart from the local checks.
