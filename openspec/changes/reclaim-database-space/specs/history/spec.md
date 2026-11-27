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

At start, outside a dry run, autorefresh SHALL convert a database without incremental auto-vacuum with `VACUUM` and then a truncating WAL checkpoint, and SHALL log `Converted database` at info level with the size before and after (pages times page size). A failure SHALL log the warning `Database conversion failed`, and the start SHALL go on to its next step. A database over 256 MiB SHALL NOT be converted; autorefresh SHALL log `Database too large to convert` at info level.

#### Scenario: Existing database

- **GIVEN** a database from an older version with `auto_vacuum` `0` and stored history
- **WHEN** autorefresh starts
- **THEN** `PRAGMA auto_vacuum` reads `2`, every event and run is kept, and the log has one `Converted database` line

#### Scenario: Another process holds the database

- **GIVEN** another process has an old database open, with or without a transaction, for longer than the lock wait of the space upkeep
- **WHEN** autorefresh starts
- **THEN** it logs `Database conversion failed` after that wait, `PRAGMA auto_vacuum` still reads `0`, the start goes on to its next step with its usual lock wait, and the next start tries the conversion again

#### Scenario: Large database

- **WHEN** autorefresh starts with an old database of 300 MiB
- **THEN** it does not convert the database and logs `Database too large to convert` with the size

#### Scenario: Dry run

- **WHEN** a dry run starts with an old database
- **THEN** the database is not converted

### Requirement: Exclusive lock of the conversion

The conversion SHALL hold the exclusive lock of the database file from its size check to its end, so that no other process can read or grow the file in between. It SHALL therefore run only when no other process has the file open, and otherwise wait for a later start.

#### Scenario: A writer prepares a large transaction

- **GIVEN** another process has an old database open with a large write transaction that it has not committed
- **WHEN** autorefresh starts
- **THEN** it does not convert the database, and the size that it checks is never smaller than the size that `VACUUM` rewrites

### Requirement: Space upkeep at run start

At the start of each scheduled run and manual scan, outside a dry run, autorefresh SHALL do the space upkeep once, before the servers run and without delaying the answer of a Run now request. It SHALL release the free pages when they are at least a fifth of the pages and at least 1 MiB under the write lock, then always checkpoint the WAL with truncation, then run `PRAGMA optimize`. A failed step SHALL log a warning, and the run SHALL continue.

#### Scenario: Large removal

- **GIVEN** a converted database of 2000 pages of 4096 bytes, 600 of them free after retention
- **WHEN** a run starts
- **THEN** the database shrinks by at least 600 pages, and the log has one `Reclaimed database space` debug line with the pages and bytes by which the page count fell

#### Scenario: Few free pages

- **GIVEN** a converted database of 2000 pages, 100 of them free
- **WHEN** a run starts
- **THEN** autorefresh releases no page and logs no `Reclaimed database space` line, and it still checkpoints the WAL and runs `PRAGMA optimize`

#### Scenario: A reader blocks the checkpoint

- **GIVEN** another process reads the database while a run releases free pages
- **WHEN** the checkpoint cannot finish
- **THEN** the run continues, and the checkpoint of a later run start shrinks the file

#### Scenario: Another process reuses the free pages

- **GIVEN** a converted database whose free pages passed the threshold when the run start first read them
- **WHEN** another process reuses most of them before the release takes the write lock
- **THEN** autorefresh releases no page

#### Scenario: The release fails

- **GIVEN** another process holds the write lock longer than the lock wait of the space upkeep
- **WHEN** a run starts with many free pages
- **THEN** autorefresh logs a warning, still tries the checkpoint, and the run continues

#### Scenario: Database without incremental auto-vacuum

- **GIVEN** a database whose conversion failed at start
- **WHEN** a run starts with many free pages
- **THEN** autorefresh releases no page and the run continues

#### Scenario: Retention removes nothing

- **GIVEN** a removed server whose cache the run start deletes, and no event older than `history-retention`
- **WHEN** a run starts
- **THEN** the space upkeep still runs and releases the pages of that cache when they pass the threshold

#### Scenario: Dry run

- **WHEN** a dry run starts with many free pages
- **THEN** autorefresh releases no page, runs no upkeep checkpoint, and runs no `PRAGMA optimize`

### Requirement: Short lock waits of the space upkeep

Each statement of the one-time conversion and of the space upkeep SHALL wait at most 2 seconds for a lock that another process holds, and then give up until the next start or run. The release and the checkpoint of a run start can each use that wait. Every other database step SHALL keep its usual wait of 30 seconds. A dry run SHALL run no upkeep step; the checkpoint that SQLite makes itself when the last connection closes stays as before.

#### Scenario: A reader of another process during the upkeep

- **GIVEN** another process keeps a read transaction open
- **WHEN** a run starts
- **THEN** the checkpoint gives up after about 2 seconds, and the dashboard queries of the service wait no longer than that

### Requirement: Query planner statistics at stop

When the service stops, outside a dry run, autorefresh SHALL run `PRAGMA optimize` once, within 5 seconds, before it closes the database. A failure or a timeout SHALL log a warning, and the stop SHALL continue.

#### Scenario: Normal stop

- **WHEN** the service stops after a signal
- **THEN** it runs `PRAGMA optimize` before it closes the database

#### Scenario: Another process holds the write lock

- **WHEN** `PRAGMA optimize` cannot finish within 5 seconds
- **THEN** autorefresh logs a warning and closes the database
