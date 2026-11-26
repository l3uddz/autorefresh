# Spec Delta: history

## MODIFIED Requirements

### Requirement: Late end of an interrupted or removed run

When a run ends after another process marked it `interrupted`, its end SHALL replace that status. When retention removed the run row meanwhile, the end SHALL store the run again under the same ID with its events marked as unlinked, unless its server no longer has a library row.

#### Scenario: Once run marked interrupted

- **WHEN** a run that the service marked `interrupted` ends as `finished`
- **THEN** the run has status `finished` and its counts

#### Scenario: Row removed by retention

- **WHEN** a run ends after retention removed its interrupted row
- **THEN** the run is stored again with the same ID and marked as having unlinked events, and a later run still gets a higher ID

#### Scenario: Server removed

- **WHEN** a run or bulk refresh ends after its server was removed from the database
- **THEN** the run is not stored again and no bulk result is stored

### Requirement: History goes with its library or server

When autorefresh removes the cache of a library that is no longer listed or configured, it SHALL also remove the events of that library with their searches and candidates and clear any match event reference held by items that moved to another library. When it removes a server that is no longer configured, it SHALL also remove the runs of that server.

#### Scenario: Library removed

- **WHEN** a library row is removed
- **THEN** its item rows, events, searches, and candidates are removed with it

#### Scenario: Item moved before its old library was removed

- **GIVEN** an item with an accepted match in its old library, which then moved to a new library
- **WHEN** the old library is removed and the item later completes in the new library
- **THEN** the old request event is gone, the item no longer holds a dangling reference to it, and the `completed` event credits the match kind without a match event

#### Scenario: Server renamed or removed

- **WHEN** a run starts and a server is no longer in the configuration
- **THEN** the libraries, history, and runs of that server name are removed

### Requirement: Waiting for another process

The store SHALL open SQLite in WAL journal mode with `synchronous=NORMAL` (requiring local POSIX storage for the database directory) and let a second process, such as a dry run, use the same database file. Each connection SHALL wait up to 30 seconds for a lock that another process holds, and each write transaction SHALL take the write lock at its start, so a write that reads first does not fail at once.

#### Scenario: Write while another process writes

- **GIVEN** one process holds an open write transaction
- **WHEN** a second process applies a read or removes a library
- **THEN** the second process waits, and its write succeeds after the first commits

#### Scenario: Concurrent reads during a write

- **WHEN** the store opens the database
- **THEN** WAL mode is active so readers do not block behind background writes
