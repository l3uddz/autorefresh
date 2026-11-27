## MODIFIED Requirements

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

### Requirement: Items settled during a read

Before a read lists a library, autorefresh SHALL take the newest event as its mark. When a check or a manual action marks an item of that server complete or gone after the mark, or Plex accepts a request for such an item after the mark, the read SHALL neither add nor change the row of that item, because its listing can be older. A later read that lists the item SHALL apply its listing again.

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

#### Scenario: Manual match during the read

- **GIVEN** a read took its mark and listed movie `a` with `tmdb://100` and a path that names `{tmdb-100}`
- **WHEN** a manual Fix match that Plex accepts changes `a` to `tmdb://200` before the read is applied, and its check saves `mismatched`
- **THEN** the read keeps the row of `a` with `mismatched` and records no completed event for it
- **AND** the other listed items are applied as usual

#### Scenario: Rejected request during the read

- **WHEN** Plex rejects a request for the item after the mark
- **THEN** the read applies the listing of the item as usual

#### Scenario: Full read of an item with a newer request

- **GIVEN** Plex accepted a request for a series after the mark of a full read
- **WHEN** the read staged a 404 or a new folder for it
- **THEN** the read neither deletes the row nor changes its path

## ADDED Requirements

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
