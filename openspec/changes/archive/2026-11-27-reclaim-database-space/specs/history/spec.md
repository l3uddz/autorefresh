## ADDED Requirements

### Requirement: Incremental auto-vacuum for a new database

The store SHALL create a new database file with incremental auto-vacuum, set before WAL mode. When a second process sets up the same new file at the same time, each SHALL retry a busy answer for up to 30 seconds, as for the switch to WAL.

#### Scenario: New database

- **WHEN** the store creates a new database file
- **THEN** `PRAGMA auto_vacuum` reads `2` and the journal mode is WAL

#### Scenario: Two processes create the file

- **WHEN** two processes open the same new database file at the same time
- **THEN** both open it, and `PRAGMA auto_vacuum` reads `2`

### Requirement: One-time conversion of an existing database

At start, after the store opens and outside a dry run, autorefresh SHALL try to convert a database whose `auto_vacuum` does not read `2` with `VACUUM` and then a truncating WAL checkpoint. It SHALL log `Converted database` at info level with the sizes before and after when every step succeeds, and the warning `Database conversion failed` when one fails; the start SHALL go on. It SHALL NOT convert a database over 256 MiB, and SHALL log `Database too large to convert`.

#### Scenario: Existing database

- **GIVEN** a database from an older version of at most 256 MiB, with `auto_vacuum` `0` and stored history, that no other process has open
- **WHEN** autorefresh starts
- **THEN** `PRAGMA auto_vacuum` reads `2`, every event and run is kept, and the log has one `Converted database` line

#### Scenario: Another process holds the database

- **GIVEN** the store opened an old database that another process has open, with or without a transaction, for longer than the lock wait of the space upkeep
- **WHEN** autorefresh starts
- **THEN** it logs `Database conversion failed` after that wait, `PRAGMA auto_vacuum` still reads `0`, and the start goes on to its next step with its usual lock wait

#### Scenario: Large database

- **WHEN** autorefresh starts with an old database of 300 MiB that no other process has open
- **THEN** it does not convert the database and logs `Database too large to convert` with the size

#### Scenario: Dry run

- **WHEN** a dry run starts with an old database
- **THEN** the database is not converted

### Requirement: Exclusive lock of the conversion

The conversion SHALL hold the exclusive lock of the database file from its size check to its end, so that no other process can read or grow the file in between. It SHALL therefore run only when no other process has the file open.

#### Scenario: A writer prepares a large transaction

- **GIVEN** another process has an old database open with a large write transaction that it has not committed
- **WHEN** autorefresh starts
- **THEN** it does not convert the database, and the size that it checks is never smaller than the size that `VACUUM` rewrites

### Requirement: Space upkeep after a scan

After a scheduled run, a Run now scan, or a `--once` run, outside a dry run and unless its context ended, autorefresh SHALL do the space upkeep once, after the removals of the run and outside the run lock. With `auto_vacuum` `2`, it SHALL release the free pages under the write lock when they are then at least a fifth of the pages and 1 MiB. It SHALL then try a truncating WAL checkpoint and run `PRAGMA optimize`. A failed step SHALL log a warning.

#### Scenario: Large removal

- **GIVEN** a converted database whose free pages are a fifth of its pages and more than 1 MiB when the upkeep checks them, and no other process on the file
- **WHEN** the upkeep runs
- **THEN** the page count falls by at least the number of free pages, and at debug verbosity or higher the log has one `Reclaimed database space` line with the pages and bytes by which it fell

#### Scenario: Few free pages

- **GIVEN** a converted database whose free pages are under a fifth of its pages
- **WHEN** a run ends
- **THEN** autorefresh releases no page and logs no `Reclaimed database space` line, and it still tries the checkpoint and runs `PRAGMA optimize`

#### Scenario: A reader blocks the checkpoint

- **GIVEN** another process reads the database while the upkeep runs, and the context of the run stays live
- **WHEN** the checkpoint cannot finish
- **THEN** the checkpoint result is busy rather than an error, the run continues, and the upkeep of a later run tries the checkpoint again

#### Scenario: Another process reuses the free pages

- **GIVEN** a converted database whose free pages passed the threshold when the upkeep first read them
- **WHEN** another process reuses enough of them before the release takes the write lock that the rest no longer pass the threshold
- **THEN** autorefresh releases no page

#### Scenario: The release fails

- **GIVEN** another process holds the write lock longer than the lock wait of the space upkeep
- **WHEN** a run with many free pages ends
- **THEN** autorefresh logs a warning, still tries the checkpoint, and the run continues

#### Scenario: Database without incremental auto-vacuum

- **GIVEN** a database whose `auto_vacuum` does not read `2`
- **WHEN** a run with many free pages ends
- **THEN** autorefresh releases no page and the run continues

#### Scenario: Retention removes nothing

- **GIVEN** a converted database, a removed server whose cache the run start deletes, and no event or run older than `history-retention`
- **WHEN** a run ends
- **THEN** the space upkeep still runs and releases the free pages when they pass the threshold

#### Scenario: Stopped run

- **WHEN** the context of a run ends before its upkeep, such as at a stop of the service
- **THEN** that run does no space upkeep, and a context that ends during the upkeep stops it before its next step

#### Scenario: Run now during the upkeep

- **WHEN** a client starts a run while the space upkeep of the last run still works, and no other run holds the run lock
- **THEN** the new run is not refused as busy, because the upkeep leaves the run lock free

#### Scenario: Dry run

- **WHEN** a dry run with many free pages ends
- **THEN** autorefresh releases no page, tries no upkeep checkpoint, and runs no `PRAGMA optimize`

### Requirement: Short lock waits of the space upkeep

Each statement of the conversion and of the space upkeep SHALL wait at most 2 seconds for a lock that another process holds, and then give up. Every other database step SHALL keep its usual wait of 30 seconds.

#### Scenario: A reader of another process during the upkeep

- **GIVEN** another process keeps a read transaction open
- **WHEN** a run ends
- **THEN** the checkpoint waits at most about 2 seconds, the store connection has its usual 30 second wait again, and the run continues

### Requirement: Query planner statistics at the end

Outside a dry run, autorefresh SHALL run `PRAGMA optimize` once, within 5 seconds, before it closes the database at the end of a `--once` run and at the end of the service. Only an exit on an error of the start, or, in service mode, on a signal that the start notices at its check right before it binds the dashboard (also when no dashboard is set), SHALL close the database without it. A failure or a timeout SHALL log a warning, and the stop SHALL continue.

#### Scenario: Normal stop

- **WHEN** the service stops after a signal while its schedule runs
- **THEN** it runs `PRAGMA optimize` before it closes the database

#### Scenario: Another process holds the write lock

- **WHEN** `PRAGMA optimize` cannot finish within 5 seconds
- **THEN** autorefresh logs a warning and closes the database
