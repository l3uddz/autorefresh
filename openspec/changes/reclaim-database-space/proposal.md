# Proposal

GitHub issue: #158

## Why

The SQLite database never gives free pages back to the file system. History retention and the cache cleanup delete rows, but `auto_vacuum` is off and nothing runs `VACUUM`, so the file keeps its largest size. Nothing runs `PRAGMA optimize` either, so the query planner has no statistics for the indexes that #149 added.

The measured benefit is modest. The deployed database is 2.4 MB with no free pages, and its history starts five days ago. At the default retention of `720h`, it should level off near 15 MB, and SQLite reuses the pages that hourly retention frees. Reclaiming space matters after a large removal: a removed server or library, or a shorter `history-retention`. The change is small and keeps the file close to the size of its data.

## What Changes

- A new database is created with incremental auto-vacuum. The setting must come before WAL mode; a probe with the bundled SQLite driver showed that a database that is already in WAL mode ignores it.
- At start, outside a dry run, autorefresh tries to convert an existing database without incremental auto-vacuum: `PRAGMA auto_vacuum = INCREMENTAL`, then `VACUUM`, then a WAL checkpoint, under the exclusive lock of the file, so no other process can grow it past the size check. A probe on a copy of the deployed database confirmed that this converts a WAL database.
  - A conversion that fails, for example because another process holds the database, logs a warning, and the start goes on. A later start tries again while the database lacks incremental auto-vacuum.
  - A database over 256 MiB is not converted, and autorefresh logs why.
- After a scheduled run, a Run now scan, or a `--once` run, outside a dry run and unless the context of the run ended (a stop or a cancel), autorefresh does the space upkeep once, after every removal of the run and outside the run lock. A bulk refresh does none.
  - In a database with incremental auto-vacuum, it releases the free pages when they are at least a fifth of the pages and at least 1 MiB.
  - It tries a truncating WAL checkpoint, because in the probe the file did not shrink before a checkpoint. It tries one in every upkeep, so a checkpoint that another reader blocked needs no saved state.
  - It runs `PRAGMA optimize`.
  - A failed step logs a warning and never fails the run. The upkeep leaves the run lock free, so it never makes a Run now answer that a run is busy.
- The conversion and each upkeep statement wait at most 2 s for a lock of another process, instead of the usual 30 s. The store has one connection, so a long wait would hold up every other query, such as those of the dashboard.
- Outside a dry run, autorefresh runs `PRAGMA optimize` once more, bounded to 5 s, before it closes the database after its schedule stops and at the end of `--once`. Other exits close the database without it.
- The store logs nothing itself. It reports what it did, and the callers log, as for every other store method.

### Surface summary

- **Configuration keys**: none added, changed, or removed.
- **API routes**: none changed. A Run now during the upkeep starts its run; its answer can wait for the one database connection while the upkeep holds it.
- **Dashboard pages**: none.
- **Plex and TMDB**: no request added or changed.
- **Logs**: a debug line when space is reclaimed, and an info or warning line about the one-time conversion.

## Non-Goals

- A limit on the WAL file size between runs. With the default autocheckpoint of 1000 pages, the deployed WAL measured 4.1 MB, which is normal. A reader that keeps a snapshot open can let it grow further, as before this change.
- A scheduled full `VACUUM`, or a configuration key to tune any of this.
- Converting a database over 256 MiB. `temp_store` is memory, because the image has no writable temporary directory, so `VACUUM` holds a copy of the database in memory.
- Space upkeep in a dry run. A dry run runs none of the upkeep steps and writes no statistics. The checkpoint that SQLite makes itself when the last connection closes stays as before.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `history`: new requirements for incremental auto-vacuum, the one-time conversion, the space upkeep after a run, and planner statistics at stop.

## Impact

- **Go packages**:
  - `internal/store`: a busy retry around the pre-WAL setup in `Open`, and the new methods `ConvertAutoVacuum`, `Reclaim`, and `Optimize`.
  - `internal/refresher`: upkeep after each run, outside the run lock (`Run`, `Jobs.Scheduled`, and the Run now job), with the `Store` interface extended.
  - `cmd/autorefresh`: the conversion after `Open`, and `Optimize` before `Close`.
- **Database**: no schema migration. The file layout changes once, at the conversion.
- **Docs**: `README.md` (a sentence on file size beside the WAL note) and `AGENTS.md` (the `internal/store` and `cmd/autorefresh` rows).
- **Startup**: the one-time conversion takes well under a second at the measured size. It needs memory, and temporary WAL space, about the size of the database.
