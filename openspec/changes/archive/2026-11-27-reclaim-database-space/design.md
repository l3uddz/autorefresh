# Design

## Context

See proposal.md for the motivation and the measured sizes. The facts that shape the approach:

- `store.Open` opens one connection (`SetMaxOpenConns(1)`) with these settings: `temp_store(memory)`, `synchronous(NORMAL)`, a 30 s busy timeout, and `_txlock=immediate`. Then `enableWAL` switches to WAL, retrying a busy answer because two processes that switch a new file at once can get `SQLITE_BUSY` without the busy handler. Then `migrate` runs each migration in its own transaction. `internal/store` logs nothing; its callers log.
- `Refresher.begin` calls `pruneBeforeRun` (`PruneServers`, then `PruneHistory`, skipped in a dry run) synchronously.
  - `Jobs.RunNow` calls `begin` inside the `POST /api/runs` handler, before it launches the job and answers.
  - `Refresher.execute(ctx, runs)` then starts one goroutine for each server. Scheduled runs and manual scans both go through it.
- `cmd/autorefresh/main.go` opens and closes the store the same way for every mode, also `--dry-run`.
- A throwaway probe used the bundled `modernc.org/sqlite` v1.62.0 against a copy of the deployed database. Neon repeated and extended it in its review.
  - **New file:** `PRAGMA auto_vacuum = INCREMENTAL` has no effect once `journal_mode = WAL` has run on a new file.
  - **Conversion:** on an existing WAL database, `auto_vacuum = INCREMENTAL` and then `VACUUM` give `auto_vacuum = 2` and keep the data. When `VACUUM` fails with `SQLITE_BUSY`, `auto_vacuum` still reads `0`.
  - **Release:** `PRAGMA incremental_vacuum` emptied the free list, but the main file shrank only after `PRAGMA wal_checkpoint(TRUNCATE)`.
  - **Blocked checkpoint:** with a reader of another connection open, the checkpoint answered `busy = 1`, and the file kept its size in that probe.
  - **Pointer-map pages:** with 4096-byte pages, incremental auto-vacuum keeps one pointer-map page per 819 data pages. Releasing N free pages can shrink the page count by N plus the pointer-map pages it frees.
  - **`PRAGMA optimize`:** it writes `sqlite_stat1`, and it fails at once with `SQLITE_BUSY` while another process holds the write lock.

## Goals / Non-Goals

**Goals:**

- Give the space of a large removal back to the file system, when SQLite and the other processes on the file allow it.
- Keep the query planner statistics current without a schedule.
- Never fail a start, a run, or a stop over space upkeep, and never refuse a Run now over it.
- Run no upkeep step during a dry run. SQLite's own checkpoint when the last connection closes is not an upkeep step and stays as before.

**Non-Goals:**

- Anything in the Non-Goals of proposal.md.

## Decisions

### 1. Set auto-vacuum before WAL on a new file, with the busy retry

In `Open`, before `enableWAL`, read `PRAGMA page_count`. When it is `0`, the file is new, so run `PRAGMA auto_vacuum = INCREMENTAL`. The busy retry of `enableWAL` becomes a small helper, `retryBusy(func() error)`, bounded by `busyTimeout`. Both the pre-WAL setup and `enableWAL` use it. `TestOpenConcurrentMigrations` gets a twin that opens a new file from two goroutines with their own `sql.DB` handles.

A new file then never needs the conversion. A dry run that creates a new file creates it with auto-vacuum, which changes no existing data.

### 2. A short lock wait for every upkeep step

The store has one connection, so a statement that waits 30 s for another process holds up every other query of the service, also the dashboard. `withUpkeepLockWait` therefore pins the connection, sets `PRAGMA busy_timeout` to `upkeepLockWait` (2 s, a package variable for tests), runs the step, and sets the usual 30 s again, also when the context has ended. When a step fails, it closes the connection instead of returning it to the pool, because the step can have left it in any state. That covers the setter too: SQLite applies the pragma while it prepares the statement, so a setter that fails after that point (such as on a cancelled context) has still changed the connection. A connection whose restore fails is closed as well. The next connection gets the usual settings from the data source name. `Reclaim` and `Optimize` run through it. A step that gives up runs again at the next start or run.

The limit applies to each statement that waits. `Reclaim` has two such statements, the release and the checkpoint. Risks gives what that means for the dashboard.

### 3. Convert an existing file once, from `main`, outside a dry run

`Store.ConvertAutoVacuum(ctx) (Conversion, error)` reads `PRAGMA auto_vacuum`. When it is `2`, it returns `Conversion{}` at once, without a lock. Otherwise it runs the conversion under the exclusive lock of the file (`withExclusiveLock`):

1. Pin a connection, set the short lock wait, set `PRAGMA locking_mode = EXCLUSIVE`, and run `BEGIN EXCLUSIVE` and `COMMIT`. In exclusive locking mode, the lock stays after the `COMMIT`, so no other process can read or write the file until the connection closes. When the lock is not free within the short wait, the call fails and changes nothing.
2. Read `auto_vacuum` again, then `page_count * page_size`. Above `maxConvertBytes` (256 MiB, a package variable for tests), return `Conversion{TooLarge: true, Before: size}`.
3. Run `PRAGMA auto_vacuum = INCREMENTAL`, then `VACUUM`, then `PRAGMA wal_checkpoint(TRUNCATE)`. No reader of another process can exist, so the checkpoint finishes.
4. Return `Conversion{Converted: true, Before, After}`, where both sizes are `page_count * page_size`.
5. Close the pinned connection instead of returning it to the pool. That gives the lock up and drops the exclusive locking mode and the short wait. The next connection gets the usual settings from the data source name.

The lock is held from the size check to the end, so another process cannot grow the file over the cap in between. In WAL mode, an open connection keeps a shared lock of the file also between its transactions. The conversion therefore runs only when no other process has the file open, and otherwise waits for a later start.

`main` calls it right after `store.Open` when `--dry-run` is off. It logs `Converted database` or `Database too large to convert` at info level, and an error as the warning `Database conversion failed`. The start always goes on. The later startup writes, such as marking interrupted runs, keep their usual 30 s wait and their existing failure rules.

The cap bounds the memory of the temporary copy that `temp_store(memory)` keeps. The deployed file is 2.4 MB and should level off near 15 MB.

Alternatives considered:

- **Convert inside `Open`.** Rejected: `Open` cannot tell a dry run, and a dry run must not rewrite the file.
- **A migration file.** Rejected: `VACUUM` cannot run inside the transaction of a migration.
- **`VACUUM INTO` a new file and a rename.** Rejected: it is unsafe while a second process, such as `--once`, has the file open.

### 4. Space upkeep after a run, outside the run lock

The upkeep runs once after every server of a scheduled run, a Run now scan, or a `--once` run ended, unless `r.opts.DryRun`. A bulk refresh launches its job without it. It then follows every removal of the run: `pruneBeforeRun` in `begin` and `pruneSections` in each server run, with their existing rules. A run whose context ended (a stop or a cancel) skips it, and the next run does it. A failed server does not skip it, because the upkeep gets the context of the run, not the one that `execute` cancels when a server fails.

The upkeep runs after the run lock is free again. The dashboard offers Run now as soon as no server runs, so an upkeep that held the lock could answer a click with `409 a run is busy` while it ran. (A click on the status update of the last server just before the scan gives the lock back can still get `409`, as before this change.) Therefore:

- `execute` no longer calls `upkeep`.
- `Refresher.Run`, the path of `--once`, runs `scan` (the scan of every server) and then `upkeep`.
- `Jobs.Scheduled` runs `scan` under the run lock, gives the lock back (also after a panic), and then runs `upkeep`. The scheduler waits for `Scheduled`, so a stop waits for the upkeep too.
- `RunNow` launches its job with `launchThen`: the scan under the lock, then the upkeep with the context of the job once the lock is free. `Jobs.Close` waits for both through the wait group, and it cancels the upkeep with the job context.

A new run can then start while the upkeep of the last run still works. The release and the checkpoint share the one connection with it, and their write lock orders them, so this is safe. The free pages that the upkeep counts are the ones still free then: a long run can have reused some of the pages that its removals freed. `RunNow` answers when the job starts, as before. Its `begin` needs the one database connection, so its answer can wait while the upkeep holds that connection, as every other dashboard request can (see Risks).

`upkeep` runs two store methods, `Reclaim` and then `Optimize`. It logs each error as a warning and returns nothing, so no step can end the run. It stops early when `ctx` ends.

`Store.Reclaim(ctx) (ReclaimResult, error)`:

1. Reads `auto_vacuum`, `page_count`, `freelist_count`, and `page_size` without a lock. When `auto_vacuum` is not `2`, or the free pages are under a fifth of `page_count` or under 1 MiB, it releases nothing and takes no write lock.
2. Otherwise it runs `BEGIN IMMEDIATE`, reads the same values again, and, when they still pass the threshold, runs `PRAGMA incremental_vacuum`, reads `page_count` again, and commits. The second check is needed because another process can reuse the free pages between the first check and the write lock. On any error it rolls back, so that the checkpoint can run.
3. Always runs `PRAGMA wal_checkpoint(TRUNCATE)` while the context is live, also after a failed release, and scans its three result columns. The error joins the errors of the release and the checkpoint.
4. Returns `ReclaimResult{Pages, Bytes, CheckpointBusy}`. `Pages` is the fall in `page_count`, clamped at 0, so it counts freed pointer-map pages too. `Bytes` is `int64(Pages) * page_size`, and every size product uses `int64`, so a 32-bit build cannot overflow.

`upkeep` logs `Reclaimed database space` at debug level with `pages`, `bytes`, and `checkpoint_busy` when `Reclaim` returns no error and `Pages > 0`. On an error, it logs the warning instead.

`Reclaim` tries the checkpoint each time it runs with a live context, also when the free list is empty, so a checkpoint that a reader blocked needs no saved state. How much the WAL and the main file shrink is up to SQLite and the other processes on the file (see Risks).

The threshold avoids churn. Hourly retention frees a few pages that new events reuse within the hour, so releasing them on every run would shrink and regrow the file for nothing.

Alternatives considered:

- **Upkeep in `pruneBeforeRun`.** Rejected: `begin` runs inside the Run now request, so a 30 s lock wait would delay the answer, and the retention guard would skip upkeep.
- **Upkeep at the start of `execute`.** Rejected: the cache of a library that a server run removes would then wait for the next run.
- **Upkeep at the end of `execute`, under the run lock.** Rejected: the dashboard would offer Run now while the lock is still held.
- **Full auto-vacuum (`auto_vacuum = FULL`).** Rejected: it moves pages at every commit, which costs more on each write than a release once a run.
- **A pending-checkpoint flag on the store.** Rejected: an unconditional checkpoint is stateless and also correct.

### 5. `PRAGMA optimize`

`Store.Optimize(ctx) error` runs `PRAGMA optimize`. `upkeep` calls it after `Reclaim`. At the end of the service or of `--once`, `main` calls it before `closeStore` when `--dry-run` is off, with a 5 s context from `context.Background()`, as `stopUI` does, because the run context has ended by then. It logs an error as a warning. It runs with the 2 s upkeep lock wait, and the bundled driver interrupts a statement when its context ends, so the 5 s timeout bounds it as well. `finishStore` calls it, and `run` calls `finishStore` after `serveUntilStopped` and at the end of `runOnce`. Only an exit of `run` on an error of the start, or on a signal that it notices before the dashboard (when set) binds, closes the store without it, also when the start already wrote the database.

`Store.Close` stays a plain close.

### 6. Interfaces and tests

The `refresher.Store` interface gains `Reclaim` and `Optimize`. The refresher tests wrap the real temporary-file `*store.Store` in small embedding structs, as `runStore` does, to record calls and inject errors. No fake store is needed.

Store tests use temporary files, so they need no network:

- A new file reads `auto_vacuum = 2`, also when two handles create it at once.
- `ConvertAutoVacuum` converts an old file built without auto-vacuum and keeps its rows.
- A write transaction held by a second `sql.DB` makes `ConvertAutoVacuum` fail, with `auto_vacuum` still `0`. The test shortens the busy timeout through a package variable.
- An open connection of a second `sql.DB`, outside any transaction, makes `ConvertAutoVacuum` fail, and the conversion works once that connection closes.
- Under `withExclusiveLock`, a second `sql.DB` can neither read nor write, and after it, the second handle writes again and the store's connection has the usual busy timeout and `locking_mode` `normal`.
- The size cap works through `maxConvertBytes`.
- `Reclaim` releases pages above the threshold and none below it, and counts the fall in `page_count`.
- A reader of a second handle makes `Reclaim` report `CheckpointBusy`, and a later `Reclaim` shrinks the file.
- `Optimize` succeeds on an open database, and returns an error within its context deadline while a second handle holds a write transaction.
- A reader of a second handle makes `Reclaim` give up its checkpoint after about the upkeep lock wait, and the usual busy timeout comes back after it.
- A write lock of a second handle fails the release, and `Reclaim` still tries and reports the checkpoint.
- When a second handle reuses most free pages between the first check and the write transaction, the release frees no page.
- A cancel in an authorizer callback of the short `busy_timeout` fails the setter after SQLite applied it, and the store's connection has the usual busy timeout afterwards.
- `TestMethodsReportDatabaseFailure` adds `ConvertAutoVacuum`, `Reclaim`, and `Optimize`.

Refresher tests check that:

- a dry run calls neither `Reclaim` nor `Optimize`;
- an error of each step does not fail the run;
- a manual scan calls them once, from the job and not from `RunNow`.

`cmd/autorefresh` tests check that `--dry-run` leaves an old database at `auto_vacuum = 0`, and that a normal run converts it. A direct test of `finishStore`, with no run before it, checks that a normal stop writes the planner statistics (`sqlite_stat1` exists) and that a `--dry-run` stop does not.

### Load on Plex and TMDB

None. The change sends no request.

## Risks / Trade-offs

- [The conversion makes the first start slower] → Well under a second at the measured size. The cap bounds the worst case, and the step runs once.
- [Memory and WAL space during the conversion, about the size of the database] → The 256 MiB cap. The truncating checkpoint after `VACUUM` gives the WAL space back.
- [A second process, such as `--once`, has the database open during the conversion] → The conversion waits up to 2 s, then logs a warning, and the next start tries again. It runs only at a start with no other process on the file. A later startup write can still wait 30 s and fail as before this change.
- [The release and the checkpoint at the end of a run wait for another process] → They run after the run, outside the run lock and outside any request, and each statement waits at most 2 s. While the upkeep holds the one connection, a dashboard request that reads or writes the database, also a Run now, waits for it: up to 2 s for each upkeep statement that waits, plus the work of the upkeep. Its own statements then keep the usual 30 s wait.
- [SQLite and the other processes decide the file size] → A reader of another process can keep a checkpoint from finishing or from truncating the WAL, and new writes can reuse released pages before a checkpoint. The next upkeep tries again. Autorefresh makes no promise about the size of the file.
- [A checkpoint attempt in every upkeep] → Its write and sync work depends on the WAL: usually little after an hourly run, more when a reader kept earlier attempts from finishing. The design accepts this cost.
- [`PRAGMA optimize` fails while another process writes] → It logs a warning, and the next run tries again.
- [Rollback to an older binary] → An older binary reads an `auto_vacuum = 2` file normally and never calls `incremental_vacuum`. Free pages then stay in the file, as today.

## Migration Plan

- No schema migration. Each start of the new binary that is not a dry run tries to convert a file without incremental auto-vacuum, until one conversion commits.
- To roll back, deploy the older binary. No step is needed.
