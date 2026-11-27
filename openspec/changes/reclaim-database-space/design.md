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
  - **Release:** `PRAGMA incremental_vacuum` empties the free list, but the main file shrinks only after `PRAGMA wal_checkpoint(TRUNCATE)`.
  - **Blocked checkpoint:** with a reader of another connection open, the checkpoint answers `busy = 1` and the file does not shrink.
  - **Pointer-map pages:** with 4096-byte pages, incremental auto-vacuum keeps one pointer-map page per 819 data pages. Releasing N free pages can shrink the page count by N plus the pointer-map pages it frees.
  - **`PRAGMA optimize`:** it writes `sqlite_stat1`, and it fails at once with `SQLITE_BUSY` while another process holds the write lock.

## Goals / Non-Goals

**Goals:**

- Keep the file close to the size of its data after a large removal.
- Keep the query planner statistics current without a schedule.
- Never fail a start, a run, or a stop over space upkeep, and never delay the answer of Run now.
- Run no upkeep step during a dry run. SQLite's own checkpoint when the last connection closes is not an upkeep step and stays as before.

**Non-Goals:**

- Anything in the Non-Goals of proposal.md.

## Decisions

### 1. Set auto-vacuum before WAL on a new file, with the busy retry

In `Open`, before `enableWAL`, read `PRAGMA page_count`. When it is `0`, the file is new, so run `PRAGMA auto_vacuum = INCREMENTAL`. The busy retry of `enableWAL` becomes a small helper, `retryBusy(func() error)`, bounded by `busyTimeout`. Both the pre-WAL setup and `enableWAL` use it. `TestOpenConcurrentMigrations` gets a twin that opens a new file from two goroutines with their own `sql.DB` handles.

A new file then never needs the conversion. A dry run that creates a new file creates it with auto-vacuum, which changes no existing data.

### 2. A short lock wait for every upkeep step

The store has one connection, so a statement that waits 30 s for another process holds up every other query of the service, also the dashboard. `withUpkeepLockWait` therefore pins the connection, sets `PRAGMA busy_timeout` to `upkeepLockWait` (2 s, a package variable for tests), runs the step, and sets the usual 30 s again, also when the context has ended. When a step fails, it closes the connection instead of returning it to the pool, because the step can have left it in any state. That covers the setter too: SQLite applies the pragma while it prepares the statement, so a setter that fails after that point (such as on a cancelled context) has still changed the connection. A connection whose restore fails is closed as well. The next connection gets the usual settings from the data source name. `Reclaim` and `Optimize` run through it. A step that gives up runs again at the next start or run.

The limit applies to each statement that waits. `Reclaim` has two such statements, the release and the checkpoint, so under a held writer it can pin the connection for about 4 s, and the dashboard queries wait up to that long.

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

### 4. Space upkeep at the start of `execute`

`execute` calls `r.upkeep(ctx)` once, before it starts the server goroutines, unless `r.opts.DryRun`. Scheduled runs and manual scans both pass through `execute`. `RunNow` runs `execute` inside the launched job, so `POST /api/runs` answers as before. `pruneBeforeRun` and its early return for a non-positive retention stay unchanged.

`upkeep` runs two store methods, `Reclaim` and then `Optimize`. It logs each error as a warning and returns nothing, so no step can end the run. It stops early when `ctx` ends.

`Store.Reclaim(ctx) (ReclaimResult, error)`:

1. Reads `auto_vacuum`, `page_count`, `freelist_count`, and `page_size` without a lock. When `auto_vacuum` is not `2`, or the free pages are under a fifth of `page_count` or under 1 MiB, it releases nothing and takes no write lock.
2. Otherwise it runs `BEGIN IMMEDIATE`, reads the same values again, and, when they still pass the threshold, runs `PRAGMA incremental_vacuum`, reads `page_count` again, and commits. The second check is needed because another process can reuse the free pages between the first check and the write lock. On any error it rolls back, so that the checkpoint can run.
3. Always runs `PRAGMA wal_checkpoint(TRUNCATE)` while the context is live, also after a failed release, and scans its three result columns. The error joins the errors of the release and the checkpoint.
4. Returns `ReclaimResult{Pages, Bytes, CheckpointBusy}`. `Pages` is the fall in `page_count`, clamped at 0, so it counts freed pointer-map pages too. `Bytes` is `int64(Pages) * page_size`, and every size product uses `int64`, so a 32-bit build cannot overflow.

`upkeep` logs `Reclaimed database space` at debug level with `pages`, `bytes`, and `checkpoint_busy` when `Pages > 0`.

The checkpoint runs on every run start. A checkpoint that a reader blocked therefore needs no saved state: the next run start retries it, even when the free list is already empty. The checkpoint also resets the WAL file to zero bytes each run, which costs no more than the autocheckpoint work it replaces.

The threshold avoids churn. Hourly retention frees a few pages that new events reuse within the hour, so releasing them on every run would shrink and regrow the file for nothing.

Alternatives considered:

- **Upkeep in `pruneBeforeRun`.** Rejected: `begin` runs inside the Run now request, so a 30 s lock wait would delay the answer, and the retention guard would skip upkeep.
- **Full auto-vacuum (`auto_vacuum = FULL`).** Rejected: it moves pages at every commit, which costs more on each write than a release at run start.
- **A pending-checkpoint flag on the store.** Rejected: an unconditional checkpoint is stateless and also correct.

### 5. `PRAGMA optimize`

`Store.Optimize(ctx) error` runs `PRAGMA optimize`. `upkeep` calls it after `Reclaim`. At a normal stop, `main` calls it before `closeStore` when `--dry-run` is off, with a 5 s context from `context.Background()`, as `stopUI` does, because the run context has ended by then. It logs an error as a warning. It runs with the 2 s upkeep lock wait, and the bundled driver interrupts a statement when its context ends, so the 5 s timeout bounds it as well. The early-error exits of `main` keep closing the store without it, so a failed start writes nothing.

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
- [The release and the checkpoint at a run start wait for another process] → They run inside the job, and each statement waits at most 2 s. Under a held writer, the release and the checkpoint can each use that wait, so the dashboard queries, such as an Items request, can wait about 4 s for the one database connection. The Run now answer does not wait for them.
- [`PRAGMA optimize` fails while another process writes] → It logs a warning, and the next run start tries again.
- [Rollback to an older binary] → An older binary reads an `auto_vacuum = 2` file normally and never calls `incremental_vacuum`. Free pages then stay in the file, as today.

## Migration Plan

- No schema migration. The first normal start of the new binary converts the file once.
- To roll back, deploy the older binary. No step is needed.
