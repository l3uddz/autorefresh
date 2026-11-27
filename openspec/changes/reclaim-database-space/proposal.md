# Proposal

GitHub issue: #158

## Why

The SQLite database never gives free pages back to the file system. History retention and the cache cleanup delete rows, but `auto_vacuum` is off and nothing runs `VACUUM`, so the file keeps its largest size. Nothing runs `PRAGMA optimize` either, so the query planner has no statistics for the indexes that #149 added.

The measured benefit is modest. The deployed database is 2.4 MB with no free pages, and its history starts five days ago. At the default retention of `720h`, it should level off near 15 MB, and SQLite reuses the pages that hourly retention frees. Reclaiming space matters after a large removal: a removed server or library, or a shorter `history-retention`. The change is small and keeps the file close to the size of its data.

## What Changes

- A new database is created with incremental auto-vacuum. The setting must come before WAL mode; a probe with the bundled SQLite driver showed that a database that is already in WAL mode ignores it.
- At start, outside a dry run, autorefresh converts an existing database without incremental auto-vacuum once: `PRAGMA auto_vacuum = INCREMENTAL`, then `VACUUM`, then a WAL checkpoint. A probe on a copy of the deployed database confirmed that this converts a WAL database.
  - A conversion that fails, for example because another process holds the database for 30 s, logs a warning, the start continues, and the next start tries again.
  - A database over 256 MiB is not converted, and autorefresh logs why.
- At the start of each scheduled run and manual scan, outside a dry run, autorefresh does the space upkeep once, before the servers run:
  - It releases the free pages when they make up at least a fifth of the file and at least 1 MiB.
  - It checkpoints the WAL with truncation, because the probe showed that the file shrinks only after a checkpoint. It does this every time, so a checkpoint that another reader blocked is done at a later run.
  - It runs `PRAGMA optimize`.
  - The upkeep runs in the background job, so `POST /api/runs` still answers when the job starts. A failed step logs a warning and never fails the run.
- When the service stops, outside a dry run, autorefresh runs `PRAGMA optimize` once more, bounded to 5 s, before it closes the database.
- The store logs nothing itself. It reports what it did, and the callers log, as for every other store method.

### Surface summary

- **Configuration keys**: none added, changed, or removed.
- **API routes**: none changed. `POST /api/runs` keeps its timing.
- **Dashboard pages**: none.
- **Plex and TMDB**: no request added or changed.
- **Logs**: a debug line when space is reclaimed, and an info or warning line about the one-time conversion.

## Non-Goals

- A limit on the WAL file size between runs. With the default autocheckpoint of 1000 pages, the WAL stays near 4 MB (4.1 MB measured), which is normal.
- A scheduled full `VACUUM`, or a configuration key to tune any of this.
- Converting a database over 256 MiB. `temp_store` is memory, because the image has no writable temporary directory, so `VACUUM` holds a copy of the database in memory.
- Space upkeep in a dry run. A dry run changes no file layout and writes no statistics.
- Reclaiming space after each library removal within a run. The next run start releases those pages.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `history`: new requirements for incremental auto-vacuum, the one-time conversion, the space upkeep at run start, and planner statistics at stop.

## Impact

- **Go packages**:
  - `internal/store`: a busy retry around the pre-WAL setup in `Open`, and the new methods `ConvertAutoVacuum`, `Reclaim`, and `Optimize`.
  - `internal/refresher`: upkeep at the start of `execute`, with the `Store` interface extended.
  - `cmd/autorefresh`: the conversion after `Open`, and `Optimize` before `Close`.
- **Database**: no schema migration. The file layout changes once, at the conversion.
- **Docs**: `README.md` (a sentence on file size beside the WAL note) and `AGENTS.md` (the `internal/store` and `cmd/autorefresh` rows).
- **Startup**: the one-time conversion takes well under a second at the measured size. It needs memory, and temporary WAL space, about the size of the database.
