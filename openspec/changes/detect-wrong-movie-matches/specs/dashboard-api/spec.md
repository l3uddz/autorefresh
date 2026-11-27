## MODIFIED Requirements

### Requirement: Live Plex view route

`GET /api/item/live?srv=&key=` SHALL ask Plex about a known item within 5 s and answer `{summary, guid, locations, files, path, externalIds}`, and for a show also `seasons` and `episodes`, and for a matched movie also `mismatch`. `locations`, `files`, and `externalIds` SHALL be `[]` when empty. A show without a cached path SHALL get its first location as `path`. Plex errors SHALL get fixed messages: `404` `not found in Plex`, `502` `Plex rejected the token`, or `502` `Plex unavailable`.

#### Scenario: Unknown item

- **WHEN** the item is neither cached nor in the history
- **THEN** the server answers `404` with `not found` and sends no request to Plex

#### Scenario: Plex is slow

- **WHEN** Plex does not answer within the live timeout
- **THEN** the server answers `502` with `{"error":"Plex unavailable"}`

#### Scenario: Plex error text

- **WHEN** the Plex request fails with an error that names the Plex host
- **THEN** the answer holds only the fixed message and not the host

#### Scenario: External IDs

- **WHEN** Plex lists `imdb://tt1`, `tmdb://200`, and `tvdb://3` for the item
- **THEN** `externalIds` is `["imdb://tt1","tmdb://200","tvdb://3"]`, and the request to Plex asked for them with `includeGuids=1`

## ADDED Requirements

### Requirement: Live wrong match result

For a matched movie, `mismatch` SHALL give the result of the wrong movie match rule for the live file paths and external IDs: `result` is `mismatched`, `agrees`, `undecided`, or `no-path-ids`. `sources` SHALL list each compared source as `{source, path, plex}` in the order `tmdb`, `imdb`, `tvdb`, and SHALL be `[]` when none is compared. It SHALL send no extra request.

#### Scenario: Wrong match

- **WHEN** the live movie has a path that names `{tmdb-100}` and Plex lists `tmdb://200`
- **THEN** `mismatch` is `{"result":"mismatched","sources":[{"source":"tmdb","path":"100","plex":["200"]}]}`

#### Scenario: No path ID

- **WHEN** the live movie paths name no ID
- **THEN** `mismatch` is `{"result":"no-path-ids","sources":[]}`

#### Scenario: Show or unmatched movie

- **WHEN** the item is a show, or a movie with a `local://` GUID
- **THEN** the answer has no `mismatch`

#### Scenario: Several compared sources

- **WHEN** the live movie path names `{imdb-tt5}` and `{tmdb-100}`, and Plex lists `imdb://tt6` and `tmdb://200`
- **THEN** `sources` lists `tmdb` first and then `imdb`

### Requirement: Queue state of a wrong match

The item list and the item detail SHALL give the state `manual` to a cached item that is not stopped and whose only reasons outside `ignore-reasons` are `mismatched`, however new it is. The order of the states SHALL be `stopped`, `ignored`, `manual`, `too-new`, `waiting`, then `due`. A `manual` item SHALL have no next due time. The attention count SHALL include such an item. `reason=mismatched` SHALL be a valid filter.

#### Scenario: Only a wrong match

- **WHEN** a cached movie that Plex added two days ago has only the reason `mismatched`, and `minimum-age` is `24h`
- **THEN** `/api/items` lists it with state `manual` and the attention count includes it

#### Scenario: Wrong match of a new item

- **WHEN** Plex added that movie one hour ago
- **THEN** its state is `manual` and it has no next due time

#### Scenario: Wrong match and another reason

- **WHEN** a cached movie has the reasons `mismatched` and `no-art`
- **THEN** its state follows the usual rules, such as `due` or `waiting`

#### Scenario: Filter by wrong match

- **WHEN** a client requests `/api/items?reason=mismatched`
- **THEN** the list and its counts hold only items with the reason `mismatched`
