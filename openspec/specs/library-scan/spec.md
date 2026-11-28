# library-scan Specification

## Purpose

Define how autorefresh reads Plex libraries to find movies and series with missing metadata, and how it keeps its cache of incomplete items up to date between runs.

## Requirements

### Requirement: Supported libraries

Autorefresh SHALL read only movie libraries with the agent `tv.plex.agents.movie` and show libraries with the agent `tv.plex.agents.series`. It SHALL skip every other library and log one warning for each skipped library per process. A library that the `libraries` list of the server leaves out SHALL get no such warning.

#### Scenario: Library with another agent

- **GIVEN** a server with a movie library that uses the agent `tv.plex.agents.none`
- **WHEN** two runs read the server
- **THEN** autorefresh lists no item of that library
- **AND** it logs the warning `Skipping library with unsupported agent` once, with the server, library, type, and agent

#### Scenario: Unlisted library with another agent

- **GIVEN** a server whose `libraries` list is `Movies`, and which also has a library `Other` with the agent `tv.plex.agents.none`
- **WHEN** a run reads the server
- **THEN** autorefresh logs no unsupported agent warning for `Other`

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

Autorefresh SHALL give each listed movie or series every reason that applies to it: `unmatched` when its GUID starts with `local://`, `no-summary` when its summary is empty, `no-poster` when it has no thumb, and `no-art` when it has no art. A listed movie SHALL also get `mismatched` by the rule of a wrong movie match. An item with no reason SHALL count as complete.

#### Scenario: Unmatched series with nothing

- **WHEN** a listed series has a `local://` GUID and no summary, thumb, or art
- **THEN** it gets the reasons `unmatched`, `no-summary`, `no-poster`, and `no-art`

#### Scenario: Complete item

- **WHEN** a listed item has a matched GUID, a summary, a thumb, and art, and is not a wrong movie match
- **THEN** it has no reason and autorefresh adds no cache row for it

#### Scenario: Complete movie with a wrong match

- **WHEN** a listed movie has a matched GUID, a summary, a thumb, and art, and its file path names `{tmdb-100}` while Plex lists only `tmdb://200` as its TMDB ID
- **THEN** it gets only the reason `mismatched` and autorefresh adds a cache row for it

### Requirement: Episode reasons

On each read of a show library, autorefresh SHALL send a series listing for each episode reason that `ignore-reasons` does not list. The first finds matched series with matched regular episodes that have an empty summary (`episode-no-summary`). The second finds matched series with unmatched regular episodes (`episode-unmatched`). Episodes in season 0 SHALL NOT count.

#### Scenario: Episode listings of a show library

- **WHEN** autorefresh reads a show library, and `ignore-reasons` lists neither episode reason
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

Autorefresh SHALL decide for each library whether a read is full. A read SHALL be full when the library has no full read yet, when a `cache-refresh` time after the last full read is at or before the start of the read, when the library has a hold, or when a manual scan asks for a full read. Every other read SHALL be incremental.

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

Autorefresh SHALL apply the result of a library read only when each listing of that read succeeds. When a listing fails, the cache rows and read times of that library SHALL stay unchanged. When Plex stays available, a normal run SHALL log `Reading library failed` and read the next library, and a manual full scan SHALL fail the server run with an error that names the library. An unavailable server or the end of the run SHALL stop the read of that server in both modes.

#### Scenario: Request fails part of the way

- **WHEN** the full listing, an incremental listing, or an episode listing of a library fails in a run that is not a manual full scan, and Plex stays available
- **THEN** the rows and read times of that library stay as they were
- **AND** autorefresh logs `Reading library failed` and reads the next library

#### Scenario: Full scan with a failed read

- **WHEN** a library listing fails in a manual full scan and Plex stays available
- **THEN** the run of that server fails with an error that names the library, and refreshes no item
- **AND** the runs of the other servers continue

#### Scenario: Server unavailable

- **WHEN** Plex is unavailable during a library listing or an unseen item check, in a normal run or a manual full scan
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

An item that a read does not list SHALL keep its item reasons. Each read of a show library SHALL set the episode reasons of every row of that library from the episode listings that it sent; an ignored episode reason keeps its earlier value. A row with no reason left SHALL be removed as complete.

#### Scenario: Item missing from an incremental read

- **WHEN** an incremental read does not list a cached movie
- **THEN** its row keeps its reasons and is not written

#### Scenario: Series missing from an incremental read

- **GIVEN** a cached series with only the reason `episode-no-summary`
- **WHEN** an incremental read does not list the series, and its `episode-no-summary` listing does not have it
- **THEN** the row loses that reason and is removed as complete

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

A full read SHALL ask Plex for each cached row of the library that its listing did not return. It SHALL delete the row as gone only when Plex answers 404. After any other answer or failure, the row SHALL keep its item reasons, and its episode reasons SHALL follow the episode listings of the read, as for any row that a read does not list. A row with no reason left SHALL be deleted as complete.

#### Scenario: Item deleted from Plex

- **WHEN** a full read does not list a cached item and Plex answers 404 for it
- **THEN** autorefresh deletes its row and does not count it as complete

#### Scenario: Item still on Plex

- **WHEN** Plex answers with the unseen item
- **THEN** its row keeps its item reasons
- **AND** for a series, its row gets the first location of the series as its path

#### Scenario: Check fails

- **WHEN** the check of an unseen item fails with another error, such as a 500 answer, and Plex stays available
- **THEN** autorefresh does not delete its row as gone and logs `Checking unseen item failed` with its title
- **AND** the read of the library still applies

#### Scenario: Unseen series with no reason left

- **GIVEN** an unseen series row that has only `episode-unmatched`
- **WHEN** Plex answers with the item and the `episode-unmatched` listing of the read does not have it
- **THEN** autorefresh deletes its row and counts it as complete in the run

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

Before a read lists a library, autorefresh SHALL take the newest event as its mark. The read SHALL leave the row, the gone state, and the folder of an item unchanged when, after the mark, a check or a manual action marked the item complete or gone or Plex accepted a request for it, or when a write attempt for it was live at or after the start of the read, because its listing can be older.

#### Scenario: Check during the read

- **GIVEN** a read took its mark
- **WHEN** a check completes item `a` before the read is applied, also in the same second as the start of the read
- **THEN** the read adds no row for `a`, although its listing shows `a` as incomplete
- **AND** the other listed items are added as usual

#### Scenario: Manual request found the item gone

- **WHEN** a manual request that started before the read finds the item gone after the mark
- **THEN** the read adds no row for the item

#### Scenario: Settled before the mark

- **WHEN** the item was completed before the read took its mark, and no write attempt for it was live at or after the start of the read
- **THEN** the read adds the item again when its listing shows it as incomplete

#### Scenario: Manual match during the read

- **GIVEN** a read took its mark and listed movie `a` with `tmdb://100` and a path that names `{tmdb-100}`
- **WHEN** a manual Fix match that Plex accepts changes `a` to `tmdb://200` before the read is applied, and its check saves `mismatched`
- **THEN** the read keeps the row of `a` with `mismatched` and records no completed event for it
- **AND** the other listed items are applied as usual

#### Scenario: Answer of a request still pending

- **GIVEN** a cached row for item `a` with `no-summary`, and a read that took its mark
- **WHEN** Refresh now saved its attempt for `a` and Plex has not answered yet when the read is applied, and the listing shows `a` complete
- **THEN** the read keeps the row of `a`, records no completed event for it, and saves a hold for `a`

#### Scenario: Answer of a request lost

- **WHEN** a request for item `a` whose answer was lost is recorded as failed before the read is applied, and its attempt is still live
- **THEN** the read neither changes nor deletes the row of `a`, and saves a hold for `a`

#### Scenario: Rejected request during the read

- **WHEN** Plex rejects a request for the item with `401 Unauthorized` after the mark, and no other attempt for it was live at or after the start of the read
- **THEN** the read applies the listing of the item as usual

#### Scenario: Unlisted series of an incremental read

- **GIVEN** a cached series `s` with an episode reason, and a live attempt for `s`
- **WHEN** an incremental read lists neither `s` nor an episode of it
- **THEN** the read keeps the episode reason of `s` and saves a hold for `s`

#### Scenario: Full read of an item with a newer request

- **GIVEN** Plex accepted a request for a series after the mark of a full read
- **WHEN** the read staged a 404 or a new folder for it
- **THEN** the read neither deletes the row nor changes its path

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

After it lists the libraries of a server, autorefresh SHALL remove the cache of each library of that server that the run did not select: a library that Plex no longer has, that the `libraries` list leaves out, or that is not supported. When Plex lists no library at all, or the run is a dry run, it SHALL keep that cache. The removal SHALL include the item rows of the library and their history events in that library.

#### Scenario: Unselected and deleted libraries

- **GIVEN** the `libraries` list is `Movies`, and the cache has `Movies`, `TV`, and a library that Plex no longer lists
- **WHEN** a run that is not a dry run reads the server, and Plex lists a supported `Movies` library
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

### Requirement: Movie listings ask for external IDs

Every listing of a movie library SHALL add the query parameter `includeGuids=1`, so that each listed movie carries its external IDs. A read SHALL send no extra request for them. Series listings SHALL NOT add the parameter.

#### Scenario: Full read of a movie library

- **WHEN** autorefresh does a full read of a movie library
- **THEN** each page request of the listing has `includeGuids=1`
- **AND** the number of requests is the same as without it

#### Scenario: Incremental read of a movie library

- **WHEN** autorefresh does an incremental read of a movie library
- **THEN** each of its three listings has `includeGuids=1`

#### Scenario: Show library

- **WHEN** autorefresh reads a show library
- **THEN** neither its item listing nor its episode listings have `includeGuids=1`

### Requirement: Wrong movie match

A matched movie SHALL be `mismatched` when at least one source can be compared and no compared source agrees. Path IDs SHALL come from the file paths of the movie by the rules of fix match path IDs. Plex IDs SHALL be the `imdb://`, `tmdb://`, and `tvdb://` external IDs of the listing. A source SHALL be compared when the paths name one ID of it and Plex lists at least one ID of it. A compared source SHALL agree when Plex lists the path ID.

#### Scenario: TMDB ID differs

- **WHEN** the file path of a matched movie names `{tmdb-100}` and Plex lists `tmdb://200` and `imdb://tt1`
- **THEN** the movie is `mismatched`

#### Scenario: One source agrees

- **WHEN** the file path names `{tmdb-100}` and `{imdb-tt5}`, and Plex lists `tmdb://100` and `imdb://tt6`
- **THEN** the movie is not `mismatched`

#### Scenario: IDs are written differently

- **WHEN** the file path names `{tmdb-0100}` or `[IMDB-TT0115097]`, and Plex lists `tmdb://100` or `imdb://tt0115097`
- **THEN** the IDs agree, because both sides are read by the same ID rules

#### Scenario: Other Plex IDs

- **WHEN** Plex lists `plex://movie/5d77`, `imdbid://tt9`, or `tt9` without a scheme beside `tmdb://200`, and the path names `{imdb-tt1}` and `{tmdb-100}`
- **THEN** only `tmdb` is compared, because a Plex ID counts only with the scheme `imdb://`, `tmdb://`, or `tvdb://`, and the movie is `mismatched`

#### Scenario: Malformed Plex ID

- **WHEN** Plex lists `tmdb://-100` or `tmdb:///100`, and the path names `{tmdb-100}`
- **THEN** that Plex ID counts as no `tmdb` ID

#### Scenario: Duplicate Plex IDs

- **WHEN** Plex lists `tmdb://0200` and `tmdb://200`
- **THEN** the Plex IDs of `tmdb` are the one value `200`

#### Scenario: No path ID

- **WHEN** the file paths of a matched movie name no ID
- **THEN** the movie is not `mismatched`

#### Scenario: Unmatched movie

- **WHEN** a listed movie has a `local://` GUID
- **THEN** it gets `unmatched` and not `mismatched`

### Requirement: A read keeps an undecided wrong match

When Plex gives no file of a listed matched movie, or its paths name at least one ID but no source can be compared, the read SHALL keep the `mismatched` value that the cache row had before the read. A new row SHALL then not get `mismatched`. No source can be compared when Plex lists no ID of the path sources, or when the paths name two different IDs of each such source.

#### Scenario: Plex lists no ID of the path source

- **GIVEN** a cached movie with the reason `mismatched` from an earlier read
- **WHEN** a read lists the movie with a path that names `{tvdb-7}` and with only `imdb://` and `tmdb://` IDs
- **THEN** the row keeps `mismatched` and is not removed as complete

#### Scenario: Two IDs of one source

- **WHEN** the files of a matched movie name `{tmdb-1}` and `{tmdb-2}` and no other ID, and the movie has no cache row
- **THEN** the read adds no `mismatched` reason

#### Scenario: No file listed

- **GIVEN** a cached movie with the reason `mismatched`
- **WHEN** a read lists the movie without a file
- **THEN** the row keeps `mismatched`

#### Scenario: Plex fixes the match

- **GIVEN** a cached movie whose only reason is `mismatched`
- **WHEN** a read lists it with a path that names `{tmdb-100}` and with `tmdb://100`
- **THEN** autorefresh removes the row as complete and records a completed event

### Requirement: Wrong match log

For each listed movie that is `mismatched`, a read SHALL log `Wrong match` at debug level with the server, library, title, and for each compared source the path ID and the Plex IDs.

#### Scenario: Wrong match found

- **WHEN** a read lists a movie whose path names `{tmdb-100}` and Plex lists `tmdb://200`
- **THEN** autorefresh logs `Wrong match` at debug level with the title, the path ID `tmdb-100`, and the Plex ID `tmdb-200`

### Requirement: Holds of skipped items

A read SHALL save a hold for each item that it names or would change and that it skipped because of a write attempt, or because of a request after the mark that Plex accepted, also for a complete item that has no cached row. It SHALL save no hold for an item that it skipped only because of a completed or gone event after the mark.

#### Scenario: Item moved between two libraries

- **GIVEN** reads of libraries A and B overlap while an attempt for item `a` is live, and Plex moves `a` from A to B in between
- **WHEN** both reads skip `a`
- **THEN** `a` has a hold in A and a hold in B, and a full read of B applies `a` and clears only the hold in B
- **AND** the next full read of A, which has no row for `a`, clears the hold in A

#### Scenario: Complete item made incomplete during the read

- **GIVEN** movie `m` is complete and has no cached row, and a full read listed it as complete
- **WHEN** the user applies Fix match to `m` from the Fixed tab before the read is applied
- **THEN** the read saves a hold for `m`, so the next full read lists it again

#### Scenario: Recording after the attempt ended

- **GIVEN** Plex accepted a bulk refresh of item `a`, and its process records the answer only after the attempt ended
- **WHEN** a read took its mark before that recording and applies after it
- **THEN** the read skips `a` and saves a hold for `a`

### Requirement: No sends without an applied read

A run SHALL send no automatic request to an item of a library whose read it did not apply in that run, because the read failed or was older than a pruning. The read could not save the holds of the items that it skipped. The next run reads the library again.

#### Scenario: Failed library read

- **WHEN** the incremental listing of a library fails in a normal run, and Plex stays available
- **THEN** the run logs `Reading library failed`, reads the next library, and sends no request to the items of the failed library in that run

### Requirement: Full read after a hold

A read of a library with a hold SHALL be a full read. An applied full read SHALL delete the hold of each item that the read did not skip, neither for a write attempt nor for an event after the mark, and that the read reconciled: it applied the item listing of the item, its 404 check answered 404, or the item has no cached row in that library and is not listed. It SHALL keep the other holds. A dry run SHALL save and delete holds as a live run does, because they are cache state.

#### Scenario: Attempt ended

- **GIVEN** a read saved a hold for item `a` while its attempt was live
- **WHEN** the next read of the library starts after the end of the attempt
- **THEN** the read is full, applies `a` from its listing, its 404 checks, and its folders, and deletes the hold of `a`

#### Scenario: Attempt still live

- **WHEN** the next read runs while the attempt for `a` is still live
- **THEN** the read is full, skips `a`, and keeps the hold of `a`

#### Scenario: 404 check of a held item fails

- **GIVEN** a hold for cached movie `a`, whose attempt has ended
- **WHEN** the full read omits `a`, and its 404 check answers 500 or answers with the item
- **THEN** the read is applied for the other items, `a` keeps its row and its hold, and the next read is full again

#### Scenario: Full read fails

- **WHEN** the full read of a library with a hold fails
- **THEN** the hold stays, and the next read of the library is full again

### Requirement: Read older than pruning

A read SHALL take the generations of the fence with its mark and its read times, in one snapshot. It SHALL NOT be applied when a generation changed before the apply, because a pruning of attempts, of events, or of the cache of a library or of that server ran during it. It SHALL keep the cache and the read times of the library, and count as a failed library read: a full run ends the run of the server as failed; another run logs `Read is older than pruning` and goes on with the next library.

#### Scenario: Pruning during a read

- **GIVEN** a read of a library took its mark
- **WHEN** another autorefresh process removes the cache of a library, or prunes attempts or events, before the read is applied
- **THEN** the read is not applied, the read times of the library stay, the run sends no request to the items of that library, and the next read lists the library again

#### Scenario: Pruning before the read

- **WHEN** a pruning ran before the read took its mark
- **THEN** the read is applied as usual

#### Scenario: Library of another server removed

- **WHEN** the run of server B removes the cache of an unselected library of B during a read of server A
- **THEN** the read of server A is applied as usual

#### Scenario: Pruning that deletes nothing

- **WHEN** a pruning during the read deletes no row
- **THEN** the generation stays, and the read is applied as usual
