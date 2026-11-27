# history Specification

## Purpose

Define the history that autorefresh records and keeps in its SQLite database: the events of each item, the runs of each server, how they link, and when they are removed, so the dashboard can show what happened after an item leaves the cache.

## Requirements

### Requirement: Item events keep a snapshot

Each event SHALL record the server, the library section, the rating key, the time in whole seconds, the kind, the source (`auto` or `manual`), and a snapshot of the item: title, year, type, poster, art, and path. The kinds SHALL be `request`, `match-decision`, `completed`, `gone`, `stopped`, and `resumed`. The snapshot SHALL keep the history readable after the cached item row is removed.

#### Scenario: Event of a cached item

- **WHEN** autorefresh records a request for a cached item with a poster, art, and path
- **THEN** the event holds the title, year, type, poster, art, path, and section of the cached row at that moment

#### Scenario: Automatic event for an item that is no longer cached

- **WHEN** an automatic request, fix match decision, or gone item is recorded for an item that has no cached row
- **THEN** autorefresh records no event and reports no error

### Requirement: Request events

Each refresh or match request to Plex, except a 404 for a cached item, SHALL record a `request` event with the action (`refresh`, `match`, `tmdb-match`, or `title-match`), whether Plex accepted it, its outcome, and the error text. The outcome SHALL be the action, with `-failed` when Plex did not accept it. A match request SHALL also keep its target: the candidate GUID, title, and year, the links of the IDs that found it, and the path year, TMDB year, and episode count that confirmed it.

#### Scenario: Accepted refresh

- **WHEN** Plex accepts a refresh request of a run
- **THEN** a `request` event with action `refresh`, accepted, and outcome `refresh` is recorded

#### Scenario: Failed match request

- **WHEN** Plex answers a match request with an error
- **THEN** the `request` event has outcome `match-failed`, the error text, and the target GUID of the match

#### Scenario: Search failed before any request

- **WHEN** a fix match search fails before autorefresh sends any request
- **THEN** no `request` event is recorded, and the last outcome of the cached item becomes `search-failed`

### Requirement: A 404 of a cached item replaces the request event

When Plex answers a request of a cached item with 404, autorefresh SHALL record a `gone` event and no `request` event. When Plex answers a manual refresh of an item with only history with 404, autorefresh SHALL record a failed `request` event and no `gone` event.

#### Scenario: Cached item

- **WHEN** Plex answers the refresh of a cached item with 404
- **THEN** a `gone` event with action `refresh` is recorded and no `request` event

#### Scenario: Item with only history

- **GIVEN** an item that has history but no cache row
- **WHEN** Plex answers its manual refresh with 404
- **THEN** a `request` event with outcome `refresh-failed` is recorded and no `gone` event

### Requirement: Fix match decisions are stored with their searches

Autorefresh SHALL record each automatic fix match decision as a `match-decision` event with its outcome (`matched`, `no-match`, `no-ids`, or `failed`), the conflict, the error, the path year, the TMDB year, and the episode count. It SHALL store every Plex search of the decision in order, and every candidate of each search in order, in the same transaction as the event.

#### Scenario: Decision with searches and candidates

- **WHEN** a decision ran a path search with two candidates and a failed TMDB search
- **THEN** the history returns both searches in order, each with its query, its source (`path`, `tmdb`, `tie`, or `title`), the path ID that a TMDB search came from, and whether it failed
- **AND** each candidate keeps its GUID, name, year, type, summary, poster URL, score, and verdict

#### Scenario: Decision without IDs

- **WHEN** the item paths hold no ID and the decision ends with `no-ids`
- **THEN** the event is recorded without searches

#### Scenario: Last decision on the cached item

- **WHEN** a decision is recorded
- **THEN** the cached item keeps its outcome as its last fix match outcome

### Requirement: Fix match evidence is stored

A `match-decision` event SHALL also store the evidence of the decision: the TMDB ID that the year check compared or that the Plex metadata provider gave, why TMDB was not asked (`off`, `failed`, `tmdb-id`, or `no-source`), the path IDs that TMDB has no single entry for, the alternate TMDB episode group that confirmed a series, and the season whose episodes differ, with the counts of Plex and TMDB and the episode numbers that only TMDB or only Plex has.

#### Scenario: Evidence round trip

- **WHEN** a decision records a TMDB ID, a skip reason, two missing path IDs, an episode group, and season 3 with 8 Plex and 10 TMDB episodes
- **THEN** the history returns the same values, with the episode numbers that only TMDB has and that only Plex has in their stored order

#### Scenario: Decision without evidence

- **WHEN** a decision has no evidence
- **THEN** the history returns empty evidence, with no missing path IDs and season 0 for no differing season

### Requirement: Some fix match decisions are not recorded

Autorefresh SHALL NOT record a fix match decision in a dry run, when a store error stopped it, when the end of the run interrupted it, or when Plex answered that the item is gone. A gone event records the last case.

#### Scenario: Dry run decision

- **WHEN** a dry run finds a match, or its search fails or ends with a Plex 404
- **THEN** the item history gains no event

#### Scenario: Item gone during the search

- **WHEN** Plex answers the fix match search of a run with 404
- **THEN** autorefresh records a `gone` event and no `match-decision` event

### Requirement: Completed events

When a read or a metadata check finds a cached item complete, autorefresh SHALL remove the row and record a `completed` event. The snapshot SHALL prefer the title, year, poster, and art of that read, and the path of the read or its series folder, over the cached row. The event SHALL credit the last accepted action, the last accepted match kind, and the ID of the accepted match request event of the current occurrence of the item.

#### Scenario: Completion after a plain refresh

- **WHEN** an item that last had an accepted refresh becomes complete
- **THEN** the `completed` event credits action `refresh`, no match kind, and no match event

#### Scenario: Same rating key completes twice

- **WHEN** an item completes after an accepted match, enters the cache again, and completes after a second accepted match
- **THEN** each `completed` event credits the match request event of its own occurrence

#### Scenario: Read metadata wins

- **WHEN** the completing read gives a new title and poster but no path
- **THEN** the event has the new title and poster and keeps the cached path

#### Scenario: Plex completed the item by itself

- **WHEN** a cached item without any accepted request becomes complete
- **THEN** a `completed` event is still recorded, without a credited action

#### Scenario: Item moved to another library

- **WHEN** the read of another library section completes the item
- **THEN** the `completed` event has the new section

### Requirement: Gone events keep the action that found them

When Plex no longer has a cached item, autorefresh SHALL record a `gone` event and remove the cached row in the same transaction. A `gone` event that a request found SHALL keep the action of that request. A `gone` event from a full read or a metadata check SHALL keep no action.

#### Scenario: Refresh answered with 404

- **WHEN** Plex answers a refresh request of a run with 404
- **THEN** the row is removed and a `gone` event with source `auto` and action `refresh` is recorded

#### Scenario: Match answered with 404

- **WHEN** Plex answers a match request of a run with 404
- **THEN** the `gone` event keeps action `match`

#### Scenario: Full read finds the item gone

- **WHEN** a full read reports a cached key and an unknown key as gone
- **THEN** the cached key gets a `gone` event without an action, and the unknown key gets no event

#### Scenario: Manual request finds the item gone

- **WHEN** Plex answers a manual refresh of a cached item with 404
- **THEN** the `gone` event has source `manual` and action `refresh`

### Requirement: Stop and resume events

When a person stops or resumes a cached item, autorefresh SHALL change its stop state and record a `stopped` or `resumed` event with source `manual` in the same transaction. A request that does not change the state SHALL record nothing.

#### Scenario: Stop twice, then resume

- **WHEN** a person stops a cached item, stops it again, and then resumes it
- **THEN** the history has one `stopped` event followed by one `resumed` event

#### Scenario: Item not cached

- **WHEN** a person stops an item that has no cached row
- **THEN** the store reports that the item is not cached and records no event

### Requirement: Manual action events

In a manual item action, the `request` event, the `stopped` and `resumed` events, and a `gone` event that the action found SHALL have source `manual`. A fix match decision and a `completed` event SHALL have source `auto`, also in a manual action. A manual request for an item without a cached row SHALL be recorded with the item snapshot that the action took when it started.

#### Scenario: Manual refresh of a history-only item

- **GIVEN** an item that has only history and no cached row when the manual refresh starts
- **WHEN** Plex accepts the refresh
- **THEN** a `request` event with source `manual` and the title of the start snapshot is recorded

#### Scenario: Refresh now with an automatic match

- **WHEN** Refresh now records an automatic fix match decision, sends the match, and the check after it completes the item
- **THEN** the decision and the `completed` event have source `auto`, and the `request` event has source `manual`

#### Scenario: Row removed during a manual action

- **WHEN** the cached row leaves the cache while a manual refresh of that item is in flight, Plex answers other than 404, and the row is still absent when the outcome is recorded
- **THEN** the `request` event is still recorded with the snapshot of the start

#### Scenario: Row removed during a manual action that finds the item gone

- **GIVEN** an item that had a cache row when the manual action started
- **WHEN** the row leaves the cache while the refresh is in flight, Plex answers 404, and the row is still absent when the outcome is recorded
- **THEN** a `gone` event is recorded with the snapshot of the start, because the action keeps the cache state of its start

### Requirement: Event order

Event IDs SHALL increase with each new event and SHALL never be reused, also after the newest event is removed. Because event times have whole seconds, events of the same second SHALL be ordered by their ID.

#### Scenario: Two events in one second

- **WHEN** two requests of one item are recorded in the same second
- **THEN** the item history lists the later one first, newest first by time and then by ID

#### Scenario: Newest event removed

- **WHEN** the event with the highest ID is removed and a new event is recorded
- **THEN** the new event gets a higher ID than the removed one

#### Scenario: Events of one run

- **WHEN** the events of one run are read
- **THEN** they come oldest first, by time and then by ID

### Requirement: Run records

Autorefresh SHALL record one run for each server of a scheduled run or a manual scan, with kind `run`, and one run for each bulk refresh, with kind `bulk-refresh`. The source SHALL be `schedule` for a scheduled run and `manual` for a manual scan or a bulk refresh. Run IDs SHALL never be reused.

#### Scenario: Scheduled run of one server

- **WHEN** a scheduled run of one server ends
- **THEN** one run with kind `run`, source `schedule`, its start time, and its end time is recorded

#### Scenario: Bulk refresh

- **WHEN** a person starts a bulk refresh of one server
- **THEN** one run with kind `bulk-refresh` and source `manual` is recorded

### Requirement: Run status

A run SHALL start with status `running`. It SHALL end as `finished`, as `failed` with its error text, or as `canceled`, and SHALL record its end time. A run that a stopped process left `running` SHALL become `interrupted` when the scheduled service starts.

#### Scenario: Unavailable server

- **WHEN** the Plex server of a run is unavailable
- **THEN** the run ends as `failed` with an error text that says the server is unavailable

#### Scenario: Run canceled after a refresh

- **WHEN** a run is canceled after it sent one of two due refreshes
- **THEN** the run ends as `canceled` with no error and keeps the counts so far: 1 refresh, 2 due, and 2 selected

#### Scenario: One server cannot start its run

- **WHEN** the start of a later server's run cannot be recorded
- **THEN** the runs that the same start already recorded end as `canceled`

### Requirement: Run counts

When a run ends, autorefresh SHALL record its counts: items read, refresh requests, match requests, failed requests, completed items, completed items that had an accepted request, incomplete items, due items, selected items, skipped items, and the libraries that it read fully and incrementally.

#### Scenario: Run with one refresh

- **WHEN** a first run fully reads one library, finds one incomplete item, and refreshes it
- **THEN** the run records 1 item read, 1 refresh, 1 incomplete, 1 due, 1 selected, and 1 full read

#### Scenario: Completion without a request

- **WHEN** a read finds a cached item complete that had no accepted request
- **THEN** the run counts 1 completed item, 0 completed items after a request, and 0 refreshes

#### Scenario: Mixed reads

- **WHEN** a manual scan reads one library fully and another incrementally
- **THEN** the run records 1 full read and 1 incremental read

#### Scenario: Skipped bulk items

- **WHEN** a bulk refresh finds a selected item stopped, changed, or no longer cached
- **THEN** the run counts it as skipped

### Requirement: Bulk refresh item results

A bulk refresh SHALL store one result for each selected item in selection order: `accepted`, `failed` with its error, `gone`, `stopped`, `changed`, `not-cached`, or `not-reached`. It SHALL store the results and the end of the run in one transaction.

#### Scenario: Canceled bulk refresh

- **WHEN** a person cancels a bulk refresh of two items while the first request is in flight
- **THEN** the run ends as `canceled`, the first result is `failed`, and the second result is `not-reached`

#### Scenario: Results cannot be stored

- **WHEN** storing the results fails
- **THEN** the run stays `running` and no result is stored

#### Scenario: Result of an item that left the cache

- **WHEN** the results are read and an item has no cached row
- **THEN** the result shows the section, type, year, and poster of the newest event of that item on the same server

### Requirement: Events link to their run

Each event that a run writes SHALL link to that run: requests, fix match decisions, completed events of reads and follow-up checks, and gone events. Events of manual item actions, including the requests of a bulk refresh, SHALL link to no run. An event that names a run that no longer exists SHALL be stored without a link.

#### Scenario: Run with a decision, a failure, and a removal

- **WHEN** a run records a fix match decision, a failed refresh, and an item that Plex no longer has
- **THEN** the `match-decision`, `request`, and `gone` events all link to that run

#### Scenario: Manual refresh

- **WHEN** a person refreshes one item
- **THEN** none of its events links to a run

#### Scenario: Run row already removed

- **WHEN** an event names a run that the database no longer has
- **THEN** the event is stored without a run link and the write succeeds

### Requirement: Item history survives the removal of a run

When retention removes a run, its events SHALL stay in the item history and lose only their link to the run.

#### Scenario: Old run removed

- **GIVEN** a run that ended before the retention cutoff and wrote a newer event
- **WHEN** retention removes the run
- **THEN** the item history still has the event, without a run link

### Requirement: Interrupted runs at start

When the scheduled service starts, it SHALL mark each run that is still `running` as `interrupted`, with the time when it marks the run as its end time, and SHALL log the number of runs that it marked. A process with `--once` or `--dry-run` SHALL leave running runs unchanged, because it can share the database with a running service.

#### Scenario: Service restart

- **GIVEN** a run left as `running` by a stopped process
- **WHEN** the scheduled service starts
- **THEN** the run becomes `interrupted` and the log says `Marked interrupted runs`

#### Scenario: Once or dry run

- **WHEN** autorefresh starts with `--once` or `--dry-run`
- **THEN** a `running` run of another process stays `running`

#### Scenario: Finished runs

- **WHEN** the scheduled service starts
- **THEN** runs that already ended keep their status and end time

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

### Requirement: History retention removes old events

At the start of each scheduled run and manual scan, autorefresh SHALL remove the events older than `history-retention` of every server, also when the scan covers one server, with their searches and candidates, in one transaction. It SHALL log the numbers of removed events and runs at debug level when it removed any.

#### Scenario: Old decision removed

- **GIVEN** a fix match decision older than the cutoff and a newer request
- **WHEN** retention runs
- **THEN** only the request stays in the item history, and no search or candidate of the decision remains

#### Scenario: Removed match event of a cached item

- **GIVEN** a cached item whose accepted match request event is older than the cutoff
- **WHEN** retention removes that event and the item later completes
- **THEN** the `completed` event credits the match kind without a match event

#### Scenario: Log line

- **WHEN** retention removes one event and one run
- **THEN** the log has one `Pruned history` debug line with `events` 1 and `runs` 1

### Requirement: History retention removes old runs

Retention SHALL remove the runs that ended before the cutoff. A `running` run SHALL stay, however long it runs. An `interrupted` run SHALL count from the time it was marked.

#### Scenario: Long run

- **GIVEN** a run that started before the cutoff and is still `running`
- **WHEN** retention runs
- **THEN** the run stays, and the next retention after it ended before the cutoff removes it

#### Scenario: Run interrupted recently

- **GIVEN** a run that started long ago and was marked `interrupted` after the cutoff
- **WHEN** retention runs
- **THEN** the run stays

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

### Requirement: Dry runs record and remove nothing

A dry run SHALL record no event and no run, and SHALL remove no history, no library, and no server. It still updates the item cache, so an item that completes or disappears while only a dry run reads it SHALL get no `completed` or `gone` event later.

#### Scenario: Dry run with old history

- **GIVEN** a run older than the retention
- **WHEN** a dry run runs
- **THEN** no new run is recorded, the old run stays, and no `Pruned history` line is logged

#### Scenario: Dry run read completes an item

- **WHEN** a dry run read finds one cached item complete and another gone
- **THEN** both rows leave the cache and neither item gets an event

#### Scenario: Dry run with a removed server and library

- **WHEN** a dry run runs with a server and a library that the configuration no longer selects
- **THEN** their cache and history stay

### Requirement: Waiting for another process

The store SHALL open SQLite in WAL journal mode with `synchronous=NORMAL` (requiring local POSIX storage for the database directory) and let a second process, such as a dry run, use the same database file. Each connection SHALL wait up to 30 seconds for a lock that another process holds, and each write transaction SHALL take the write lock at its start, so a write that reads first does not fail at once.

#### Scenario: Write while another process writes

- **GIVEN** one process holds an open write transaction
- **WHEN** a second process applies a read or removes a library
- **THEN** the second process waits, and its write succeeds after the first commits

#### Scenario: Concurrent reads during a write

- **WHEN** the store opens the database
- **THEN** WAL mode is active so readers do not block behind background writes

### Requirement: Foreign keys are enforced

The store SHALL turn on foreign keys for its connection, and SHALL refuse to open the database if they are off. A migration SHALL fail and change nothing if a row then breaks a foreign key.

#### Scenario: Open the database

- **WHEN** the store opens the database
- **THEN** foreign keys are on, so the removal of a library removes its history

#### Scenario: Migration breaks a foreign key

- **WHEN** a migration leaves a row that breaks a foreign key
- **THEN** opening the database fails with an error that names the table, and the migration is not recorded

### Requirement: Upgrades keep older history

Opening a database from an older version SHALL apply the missing migrations and keep its events and runs. Events from before runs linked their events SHALL link to no run, those runs SHALL be marked as runs without linked events, and fix match decisions from before the evidence columns SHALL read with empty evidence.

#### Scenario: Run from before event links

- **WHEN** a database from before event links is opened
- **THEN** its old run reads with no events and is marked as a run without linked events, and a new run is marked as one that links its events

#### Scenario: Decision from before evidence

- **WHEN** a database from before the evidence columns is opened
- **THEN** an old decision keeps its outcome, conflict, and path year, and reads with empty evidence

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
