## MODIFIED Requirements

### Requirement: Refresh now sends and records one refresh

When the user refreshes one item and no fix match applies, autorefresh SHALL send one refresh request for that item to Plex at once and record it with the source manual. For a cached item, a request that Plex accepts SHALL count as an attempt and a refresh, and a failed request SHALL count only as an attempt. The next wait SHALL follow the backoff rule of the `refresh-runs` capability. A 404 of a cached item SHALL remove the item instead.

#### Scenario: Refresh a cached item

- **WHEN** the user refreshes a cached item that is neither unmatched nor a mismatched movie
- **THEN** Plex gets one refresh request for that item
- **AND** the item has one more attempt and one more refresh, and its history has an accepted manual request

#### Scenario: Plex rejects the refresh

- **WHEN** Plex answers the refresh of a cached item with an error other than 404
- **THEN** autorefresh records a failed manual request and the action fails with that error
- **AND** the item has one more attempt, the same number of refreshes, and a new wait of the same length

#### Scenario: Attempt time

- **WHEN** the user refreshes a cached item
- **THEN** its attempt time is the time just before autorefresh sends the request, not the time of the answer

#### Scenario: Plex no longer has the item

- **WHEN** Plex answers the refresh of a cached item with 404
- **THEN** autorefresh records the item as gone instead of a request event, removes its cache row, and the action fails as not found

#### Scenario: Plex no longer has an item with only history

- **GIVEN** an item that has history but no cache row
- **WHEN** Plex answers its refresh with 404
- **THEN** autorefresh records a failed manual request event and no gone event, and the action fails as not found

### Requirement: A check saves a wrong match

Each metadata read of a check of a movie SHALL ask for its external IDs with `includeGuids=1`. The check SHALL save `mismatched` by the rules of a library read: it sets or clears the reason when a source can be compared, and keeps the earlier value when the paths name IDs that no source can compare. The check SHALL send no extra request for this.

#### Scenario: A manual match corrects a wrong match

- **GIVEN** a cached movie whose only reason is `mismatched`, with a path that names `{tmdb-100}`
- **WHEN** the user applies a candidate and the first check reads `tmdb://100` among the external IDs
- **THEN** autorefresh removes the row, records a completed event, and makes no second check

#### Scenario: A refresh keeps a wrong match

- **GIVEN** a cached movie with the reasons `mismatched` and `no-summary`
- **WHEN** the fix match search of a manual refresh finds no candidate, and the plain refresh and its checks find a summary but Plex still lists `tmdb://200` for a path that names `{tmdb-100}`
- **THEN** the row keeps only `mismatched`

#### Scenario: A check cannot compare

- **GIVEN** a cached movie with the reason `mismatched`
- **WHEN** a check reads the movie without an external ID of any path source
- **THEN** the row keeps `mismatched`

### Requirement: Refresh now keeps a match that the item got meanwhile

Just before it applies a picked match after searching, autorefresh SHALL read the item from Plex again. It SHALL apply the match only when Plex still has an unmatched item unmatched, or still shows a mismatched movie with the same match and IDs that disagree with its paths. Otherwise, also when Plex cannot answer, it SHALL send only the plain refresh, because a library read or user action can change the match during the search.

#### Scenario: Matched during the search

- **WHEN** Plex shows the item as matched just before the match
- **THEN** autorefresh sends only the refresh and the action reports no applied match

#### Scenario: Match state unknown

- **WHEN** the read just before the match fails
- **THEN** autorefresh sends no match and sends only the refresh

#### Scenario: Mismatched movie got another match during the search

- **GIVEN** a mismatched movie matched to `plex://movie/200` at the first read
- **WHEN** Plex shows `plex://movie/300` just before the match, whose IDs also disagree with the path
- **THEN** autorefresh sends only the refresh and the action reports no applied match

#### Scenario: Mismatched movie no longer disagrees just before the match

- **GIVEN** a mismatched movie matched to `plex://movie/200` at the first read
- **WHEN** Plex still shows `plex://movie/200` just before the match, but now lists an external ID that agrees with the path, or none that can be compared
- **THEN** autorefresh sends only the refresh and the action reports no applied match

## ADDED Requirements

### Requirement: Refresh now runs the automatic fix match for a mismatched movie

When refreshing a cached movie with the reason `mismatched` and `mismatched` not ignored, autorefresh SHALL read the movie from Plex first. If Plex shows it without a GUID or no longer mismatched, it SHALL send a plain refresh without a search. Otherwise it SHALL run automatic fix match with those paths, record the decision, and apply and follow up a pick whose GUID differs from the current match. A decision without such a pick SHALL give a plain refresh.

#### Scenario: Mismatched movie with a pick

- **GIVEN** a cached movie with the reason `mismatched`, matched to `plex://movie/200`, whose path names `{tmdb-100}`
- **AND** the search of `tmdb-100` finds one candidate, `plex://movie/100`
- **WHEN** the user refreshes it
- **THEN** Plex gets the match request to `plex://movie/100` and then a refresh request
- **AND** the history has an automatic decision that belongs to no run and an accepted manual match request
- **AND** the action reports the applied match

#### Scenario: Pick is the current match

- **GIVEN** a cached movie with the reason `mismatched`, matched to `plex://movie/100`
- **WHEN** the user refreshes it and the decision picks `plex://movie/100`
- **THEN** autorefresh records the decision, logs `Skipped fix match` with the reason `already matched`, and sends only the plain refresh

#### Scenario: No pick

- **GIVEN** a cached movie with the reason `mismatched` whose folder ID searches find two different candidates
- **WHEN** the user refreshes it
- **THEN** autorefresh records the decision with its conflict and sends only the plain refresh

#### Scenario: Movie no longer mismatched in Plex

- **GIVEN** a cached movie with the stored reason `mismatched`, but Plex now lists an external ID that agrees with its path
- **WHEN** the user refreshes it
- **THEN** autorefresh runs no match search, records no match decision, and sends only the plain refresh

#### Scenario: Mismatched is ignored

- **GIVEN** `mismatched` is in `ignore-reasons`
- **WHEN** the user refreshes a movie with the reason `mismatched`
- **THEN** autorefresh runs no match search, records no decision, and sends only the refresh

#### Scenario: A check confirms the fix

- **GIVEN** Plex accepted the match of a mismatched movie whose only reason is `mismatched`
- **WHEN** a check shows the GUID of the pick and an external ID that agrees with the path
- **THEN** autorefresh removes the row and records a completed event
