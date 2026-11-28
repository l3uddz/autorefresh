## MODIFIED Requirements

### Requirement: Follow-up of an accepted match

After Plex accepts a match, autorefresh SHALL send a refresh of the item, unless a hold or another write attempt keeps a run from it, and then check a cached item after waits of 1, 2, and 4 seconds. A check that shows the chosen GUID and finds the item complete SHALL move it to Fixed, unless it yields to another write. The checks SHALL end when the item is complete, gone, or no longer cached, or when the waits run out. A run SHALL record these steps and count a completion as its own.

#### Scenario: Match fixes the item

- **WHEN** the second check finds the matched item complete
- **THEN** the item leaves the cache with a completion that credits the refresh after the match, and the run counts it as completed after a match

#### Scenario: Refresh answers 404

- **WHEN** the refresh after the match answers 404, and Plex did not merge the item (see "A match that Plex merged is a fix")
- **THEN** autorefresh records the item as gone, makes no check, and the run goes on

#### Scenario: Server becomes unavailable

- **WHEN** the refresh after the match finds the server unavailable
- **THEN** the follow-up ends, and the run of that server ends as failed

#### Scenario: Refresh answers 500

- **WHEN** the refresh after the match answers 500
- **THEN** autorefresh logs `Following up a request failed` and still checks the item

## ADDED Requirements

### Requirement: A match that Plex merged is a fix

When the refresh or a check after an accepted match finds a cached item gone, autorefresh SHALL ask Plex once for the first 50 items of the same movie or series library with the chosen GUID. If one has that GUID and another rating key, Plex merged the item into it: autorefresh SHALL record the item as fixed by the match with that rating key, log `Plex merged the item`, and make no further check. Otherwise, also when the request fails, it SHALL record the item as gone.

#### Scenario: Refresh after the match finds a merge

- **GIVEN** Plex accepted the match of cached item `a` to `plex://movie/100`, and item `b` of the same library already has `plex://movie/100`
- **WHEN** the refresh after the match answers 404 for `a`, and Plex lists `b` for the GUID `plex://movie/100`
- **THEN** `a` leaves the cache with a completion that credits the match and names `b`, no gone event is recorded, and autorefresh makes no check of `a`

#### Scenario: Check after the match finds a merge

- **WHEN** the refresh after the match was accepted, a later check answers 404, and Plex lists `b` for the chosen GUID
- **THEN** `a` leaves the cache with a completion that names `b`, and the checks end

#### Scenario: Series library

- **WHEN** the refresh after the match of a series answers 404, and Plex lists another series with the chosen `plex://show/` GUID
- **THEN** the series is recorded as fixed by the match and merged into that series

#### Scenario: No item with the GUID

- **WHEN** Plex lists no item for the chosen GUID, or only items with another GUID or with the rating key of the matched item
- **THEN** autorefresh records the item as gone

#### Scenario: Lookup fails

- **WHEN** the refresh after the match answers 404, and the request for the items with the GUID fails
- **THEN** autorefresh records the item as gone, and a run applies the rules of an unavailable server to that failure

#### Scenario: No lookup without a match or a cache row

- **WHEN** Plex answers a plain refresh with 404, or the refresh after a match of an item with only history
- **THEN** autorefresh asks Plex for no item by GUID and records the 404 as before
