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

At start, outside a dry run, autorefresh SHALL convert a database without incremental auto-vacuum with `VACUUM` and then a truncating WAL checkpoint, and SHALL log `Converted database` at info level with the size before and after (pages times page size). A failure SHALL log the warning `Database conversion failed`, and the start SHALL continue. A database over 256 MiB SHALL NOT be converted; autorefresh SHALL log `Database too large to convert` at info level.

#### Scenario: Existing database

- **GIVEN** a database from an older version with `auto_vacuum` `0` and stored history
- **WHEN** autorefresh starts
- **THEN** `PRAGMA auto_vacuum` reads `2`, every event and run is kept, and the log has one `Converted database` line

#### Scenario: Another process holds the database

- **GIVEN** another process holds a write transaction on an old database for longer than 30 s
- **WHEN** autorefresh starts
- **THEN** it logs `Database conversion failed`, `PRAGMA auto_vacuum` still reads `0`, the service runs, and the next start tries again

#### Scenario: Large database

- **WHEN** autorefresh starts with an old database of 300 MiB
- **THEN** it does not convert the database and logs `Database too large to convert` with the size

#### Scenario: Dry run

- **WHEN** a dry run starts with an old database
- **THEN** the database is not converted

### Requirement: Space upkeep at run start

At the start of each scheduled run and manual scan, outside a dry run, autorefresh SHALL do the space upkeep once, before the servers run and without delaying the answer of a Run now request. It SHALL release the free pages when they are at least a fifth of the pages and at least 1 MiB, then always checkpoint the WAL with truncation, then run `PRAGMA optimize`. A failed step SHALL log a warning, and the run SHALL continue.

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
- **THEN** autorefresh releases no page, checkpoints nothing, and runs no `PRAGMA optimize`

### Requirement: Query planner statistics at stop

When the service stops, outside a dry run, autorefresh SHALL run `PRAGMA optimize` once, within 5 seconds, before it closes the database. A failure or a timeout SHALL log a warning, and the stop SHALL continue.

#### Scenario: Normal stop

- **WHEN** the service stops after a signal
- **THEN** it runs `PRAGMA optimize` before it closes the database

#### Scenario: Another process holds the write lock

- **WHEN** `PRAGMA optimize` cannot finish within 5 seconds
- **THEN** autorefresh logs a warning and closes the database
