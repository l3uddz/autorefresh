# library-scan Specification

## Purpose

Define how autorefresh reads Plex libraries to find movies and series with missing metadata, and how it keeps its cache of incomplete items up to date between runs.

## Requirements

### Requirement: Supported libraries

Autorefresh SHALL read only movie libraries with the agent `tv.plex.agents.movie` and show libraries with the agent `tv.plex.agents.series`. It SHALL skip every other library and log one warning for each skipped library per process.

#### Scenario: Library with another agent

- **GIVEN** a server with a movie library that uses the agent `tv.plex.agents.none`
- **WHEN** two runs read the server
- **THEN** autorefresh lists no item of that library
- **AND** it logs the warning `Skipping library with unsupported agent` once, with the server, library, type, and agent

#### Scenario: No library list

- **WHEN** a server has no `libraries` list
- **THEN** autorefresh reads every supported library of that server

### Requirement: Configured library list

When a server has a `libraries` list, autorefresh SHALL read only the supported libraries whose title exactly matches a listed title. It SHALL log one warning per process for each listed title that the server does not have.

#### Scenario: Listed title not found

- **GIVEN** a server whose `libraries` list is `Movies` and `Anime`, and which has only the libraries `Movies` and `TV`
- **WHEN** two runs read the server
- **THEN** autorefresh reads only `Movies`
- **AND** it logs the warning `Configured library not found` once for `Anime`

#### Scenario: Listed library is not supported

- **WHEN** a listed title names a library with another agent
- **THEN** autorefresh still skips that library and logs the unsupported agent warning

### Requirement: Item reasons

Autorefresh SHALL give each listed movie or series every reason that applies to it: `unmatched` when its GUID starts with `local://`, `no-summary` when its summary is empty, `no-poster` when it has no thumb, and `no-art` when it has no art. An item with no reason SHALL count as complete.

#### Scenario: Unmatched series with nothing

- **WHEN** a listed series has a `local://` GUID and no summary, thumb, or art
- **THEN** it gets the reasons `unmatched`, `no-summary`, `no-poster`, and `no-art`

#### Scenario: Complete item

- **WHEN** a listed item has a matched GUID, a summary, a thumb, and art
- **THEN** it has no reason and autorefresh adds no cache row for it

### Requirement: Episode reasons

On each read of a show library, autorefresh SHALL send two series listings. The first finds matched series with matched regular episodes that have an empty summary (`episode-no-summary`). The second finds matched series with unmatched regular episodes (`episode-unmatched`). Episodes in season 0 SHALL NOT count.

#### Scenario: Episode listings of a show library

- **WHEN** autorefresh reads a show library
- **THEN** after the item listing it lists series with the filters `unmatched=0&episode.unmatched=0&episode.summary=&episode.addedAt<<=A&season.index>>=1`
- **AND** then series with the filters `unmatched=0&episode.unmatched=1&episode.addedAt<<=A&season.index>>=1`, where A is the start of the read minus `minimum-age`

#### Scenario: Series in both listings

- **WHEN** a series is in both episode listings
- **THEN** its cache row has both `episode-no-summary` and `episode-unmatched`

#### Scenario: Movie library

- **WHEN** autorefresh reads a movie library
- **THEN** it sends no episode listing

### Requirement: Minimum age during a read

A read SHALL cache an incomplete movie or series also when Plex added it less than `minimum-age` ago, and SHALL log it at debug level as too young. The episode listings SHALL count only episodes that Plex added at or before the start of the read minus `minimum-age`.

#### Scenario: Young incomplete item

- **WHEN** a read lists an incomplete movie that Plex added 30 minutes ago and `minimum-age` is 2 hours
- **THEN** autorefresh adds the movie to the cache
- **AND** it logs `Item too young to refresh` at debug level with its title

#### Scenario: Young complete item

- **WHEN** a read lists a young item with no reason
- **THEN** autorefresh adds no row and logs no young item line for it

### Requirement: Ignored reasons during a read

A read SHALL still detect and cache the item reasons that `ignore-reasons` lists. For an ignored episode reason, autorefresh SHALL NOT send that episode listing, and each cache row SHALL keep the value of that reason from before the read.

#### Scenario: Ignored item reason

- **WHEN** `ignore-reasons` lists `no-art` and a read lists a movie without art
- **THEN** the cache row of the movie has the reason `no-art`

#### Scenario: Ignored episode reason

- **GIVEN** `ignore-reasons` lists `episode-no-summary`
- **WHEN** autorefresh reads a show library
- **THEN** it sends only the `episode-unmatched` listing
- **AND** a row that had `episode-no-summary` keeps it, while the `episode-unmatched` reason follows the new listing

### Requirement: Full read decision

Autorefresh SHALL decide for each library whether a read is full. A read SHALL be full when the library has no full read yet, when a `cache-refresh` time after the last full read is at or before the start of the read, or when a manual scan asks for a full read. Every other read SHALL be incremental.

#### Scenario: First read

- **WHEN** autorefresh reads a library for the first time
- **THEN** it does a full read and lists every item of the library with no filter

#### Scenario: Cache refresh time reached

- **GIVEN** `cache-refresh` is `0 3 * * 0` and the last full read was on a Saturday
- **WHEN** a run starts at 02:59 on the next Sunday
- **THEN** the read is incremental
- **AND** a run at 03:00 that Sunday does a full read

#### Scenario: Manual full scan

- **WHEN** a manual scan asks for a full read
- **THEN** autorefresh reads every selected library fully, also when its next full read is not due

### Requirement: Incremental read

An incremental read SHALL send three listings and merge them by item: items updated at or after T, items added at or after T, and all unmatched items. T SHALL be the time of the last read of the library minus `oversample`.

#### Scenario: Incremental listings

- **GIVEN** the last read of a library was at 12:00 and `oversample` is `48h`
- **WHEN** an incremental read runs
- **THEN** autorefresh lists the library with `updatedAt>>=T`, then `addedAt>>=T`, then `unmatched=1`, where T is 12:00 two days earlier
- **AND** an item in more than one listing counts once

#### Scenario: Unmatched item outside the window

- **WHEN** an unmatched item has no change in the window
- **THEN** the `unmatched=1` listing still finds it

### Requirement: Library read times

Autorefresh SHALL keep for each library the time of its last full read and the time of its last read of any kind. Each applied read SHALL set the last read to the start of the read. Only a full read SHALL also set the last full read.

#### Scenario: Full read

- **WHEN** a full read that started at 12:00 is applied
- **THEN** both read times of the library are 12:00

#### Scenario: Incremental read

- **WHEN** an incremental read that started one hour later is applied
- **THEN** the last read changes to that time and the last full read stays 12:00

#### Scenario: Library metadata

- **WHEN** a run selects a library
- **THEN** autorefresh saves its title, type, agent, and language, and keeps its read times

### Requirement: Listing pages

Autorefresh SHALL read every listing in pages of at most 500 items, sorted by the time Plex added the item. Each page after the first SHALL start at the last item of the previous page. A listing SHALL fail when that item is not first on the next page, when a page has no `totalSize`, when its `size` differs from its entry count, or when a page adds no new item before the total.

#### Scenario: Several pages

- **WHEN** a library has 1001 items
- **THEN** autorefresh requests pages at the offsets 0, 499, and 998 and returns all 1001 items

#### Scenario: Listing changes before the offset

- **WHEN** Plex removes or inserts an item before the offset of the next page during the read
- **THEN** the listing fails because the listing changed during the read
- **AND** the failure does not count as an unavailable server

#### Scenario: Items added during the read

- **WHEN** Plex adds items at the end of the listing during the read
- **THEN** the listing returns them too

#### Scenario: Missing total

- **WHEN** a page has no `totalSize`
- **THEN** the listing fails after that one request

### Requirement: Failed library read

Autorefresh SHALL apply the result of a library read only when every request of that read succeeds. When a request fails, the cache rows and read times of that library SHALL stay unchanged.

#### Scenario: Request fails part of the way

- **WHEN** the full listing, an incremental listing, or an episode listing of a library fails in a run that is not a manual full scan
- **THEN** the rows and read times of that library stay as they were
- **AND** autorefresh logs `Reading library failed` and reads the next library

#### Scenario: Full scan with a failed read

- **WHEN** a manual full scan cannot read a library
- **THEN** the run of that server fails with an error that names the library, and refreshes no item
- **AND** the runs of the other servers continue

#### Scenario: Server unavailable

- **WHEN** Plex is unavailable during a library listing or an unseen item check
- **THEN** autorefresh stops reading that server, keeps its cache, and continues with the other servers

### Requirement: Cache rows of incomplete items

The cache SHALL hold one row for each incomplete movie or series, with its library, type, title, year, added time, reasons, thumb and art paths, and a folder path. A read SHALL add a row for each listed item with a reason. It SHALL update the metadata of a listed cached row and keep its refresh history.

#### Scenario: Only incomplete items

- **WHEN** a read lists one item with `no-summary` and one item with no reason
- **THEN** the cache has a row only for the first item

#### Scenario: Metadata changes

- **WHEN** a read lists a cached item with a new title and year
- **THEN** its row has the new title and year and keeps its refreshes and last refresh

#### Scenario: Movie folder

- **WHEN** a read lists a movie with files
- **THEN** its row gets the folder of its first file as its path
- **AND** a later listing without a path keeps the path that the row has

### Requirement: Reasons that a read does not list

An item that a read does not list SHALL keep its item reasons. Each read of a show library SHALL replace the episode reasons of every row of that library with the result of its episode listings.

#### Scenario: Item missing from an incremental read

- **WHEN** an incremental read does not list a cached item
- **THEN** its row keeps its reasons and is not written

#### Scenario: Episode reason gone

- **WHEN** a series had `episode-unmatched` and the new `episode-unmatched` listing does not have it
- **THEN** the row loses that reason

#### Scenario: Episode reason added to an item reason

- **WHEN** a series row has `no-art` and a read lists it as complete but in the `episode-no-summary` listing
- **THEN** its row has only `episode-no-summary` and keeps its refresh history

### Requirement: Removal of complete items

When a read leaves a cached row with no reason, autorefresh SHALL delete the row and report the item as complete. If the item later becomes incomplete again, a read SHALL add a new row with no refresh history.

#### Scenario: Item becomes complete

- **WHEN** a read lists a cached item with no reason
- **THEN** autorefresh deletes its row and counts it as complete in the run

#### Scenario: Item incomplete again

- **WHEN** a later read lists that item with a reason
- **THEN** its new row has no refreshes and no last attempt

### Requirement: Unseen rows in a full read

A full read SHALL ask Plex for each cached row of the library that its listing did not return. It SHALL delete the row only when Plex answers 404. Any other answer or failure SHALL keep the row with its reasons.

#### Scenario: Item deleted from Plex

- **WHEN** a full read does not list a cached item and Plex answers 404 for it
- **THEN** autorefresh deletes its row and does not count it as complete

#### Scenario: Item still on Plex

- **WHEN** Plex answers with the unseen item
- **THEN** its row keeps its reasons
- **AND** for a series, its row gets the first location of the series as its path

#### Scenario: Check fails

- **WHEN** the check of an unseen item fails with another error
- **THEN** its row stays and autorefresh logs `Checking unseen item failed` with its title

#### Scenario: Incremental read

- **WHEN** an incremental read does not list a cached item
- **THEN** autorefresh sends no request for it

### Requirement: Item moved between libraries

When a read lists an item whose rating key has a cache row in another library of the same server, autorefresh SHALL move that row to the library of the read. The row SHALL keep its refresh history and the reasons that the read does not replace.

#### Scenario: Movie moved to another library

- **WHEN** a read of library 2 lists an item that has a row in library 1
- **THEN** the row is in library 2 with the new item reasons and its earlier refreshes

#### Scenario: Moved item is complete

- **WHEN** the read lists the moved item with no reason
- **THEN** autorefresh deletes the row and reports the item as complete

### Requirement: Items settled during a read

Before a read lists a library, autorefresh SHALL take the newest event as its mark. When a check or a manual action marks an item of that server complete or gone after the mark, the read SHALL NOT add a new row for that item, because its listing can be older. A later read that lists the item as incomplete SHALL add it again.

#### Scenario: Check during the read

- **GIVEN** a read took its mark
- **WHEN** a check completes item `a` before the read is applied, also in the same second as the start of the read
- **THEN** the read adds no row for `a`, although its listing shows `a` as incomplete
- **AND** the other listed items are added as usual

#### Scenario: Manual request found the item gone

- **WHEN** a manual request that started before the read finds the item gone after the mark
- **THEN** the read adds no row for the item

#### Scenario: Settled before the mark

- **WHEN** the item was completed before the read took its mark
- **THEN** the read adds the item again when its listing shows it as incomplete

### Requirement: Cache of servers that are not configured

At the start of each scheduled or manual scan, autorefresh SHALL remove the cache of every server that the configuration no longer lists: its libraries, their item rows, and its runs. It SHALL log `Removed cache of library` for each removed library with the server, section, and item count.

#### Scenario: Server removed from the configuration

- **WHEN** the cache has libraries of a server named `old` that is not configured
- **THEN** the next run removes those libraries and their rows and keeps the cache of the configured servers

#### Scenario: Run canceled before it starts

- **WHEN** the run is canceled before it starts
- **THEN** autorefresh removes no cache

#### Scenario: Dry run

- **WHEN** the run is a dry run
- **THEN** autorefresh removes no cache of a server

### Requirement: Cache of libraries that are not selected

After it lists the libraries of a server, autorefresh SHALL remove the cache of each library of that server that the run did not select: a library that Plex no longer has, that the `libraries` list leaves out, or that is not supported. The removal SHALL include the item rows of the library and their history events in that library.

#### Scenario: Unselected and deleted libraries

- **GIVEN** the `libraries` list is `Movies`, and the cache has `Movies`, `TV`, and a library that Plex no longer lists
- **WHEN** a run reads the server
- **THEN** only the cache of `Movies` stays
- **AND** the removal log names the library title when Plex still lists the library

#### Scenario: Server lists no library

- **WHEN** the library listing succeeds but has no library, as when the server is still starting
- **THEN** autorefresh keeps the cache of all libraries of that server

#### Scenario: Library listing fails

- **WHEN** the library listing fails
- **THEN** autorefresh keeps the cache and treats the server as unavailable for that run

#### Scenario: Dry run

- **WHEN** the run is a dry run
- **THEN** autorefresh removes no library cache and logs `Dry run keeps cache of removed libraries` once for the server
