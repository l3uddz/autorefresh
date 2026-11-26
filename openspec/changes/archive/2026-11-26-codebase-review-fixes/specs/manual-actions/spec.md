# Spec Delta: manual-actions

## MODIFIED Requirements

### Requirement: Refresh now runs the automatic fix match first

When refreshing an unmatched cached item with `unmatched` not ignored, autorefresh SHALL read the item from Plex first. If Plex already matched it, autorefresh SHALL skip automatic fix match and send a plain refresh. Otherwise it SHALL run automatic fix match with those paths and record the decision, even before the first refresh, applying any picked candidate and following it up, or sending a plain refresh.

#### Scenario: The decision picks a candidate

- **GIVEN** an unmatched cached item whose folder ID finds one candidate
- **WHEN** the user refreshes it
- **THEN** Plex gets the match request and then a refresh request
- **AND** the history has an automatic decision that belongs to no run and an accepted manual match request
- **AND** the action reports the applied match

#### Scenario: Already matched in Plex before refresh

- **GIVEN** a cached item whose stored reasons include `unmatched`, but Plex already shows it as matched
- **WHEN** the user refreshes it
- **THEN** autorefresh runs no match search, records no match decision, and sends only the plain refresh

#### Scenario: Unmatched is ignored

- **GIVEN** `unmatched` is in `ignore-reasons`
- **WHEN** the user refreshes an unmatched item
- **THEN** autorefresh runs no match search, records no decision, and sends only the refresh

### Requirement: Refresh now keeps a match that the item got meanwhile

Just before it applies a picked match after searching, autorefresh SHALL read the item from Plex again. It SHALL apply the match only when Plex still has the item unmatched. Otherwise, also when Plex cannot answer, it SHALL send only the plain refresh, because a library read or user action can match the item during the search.

#### Scenario: Matched during the search

- **WHEN** Plex shows the item as matched just before the match
- **THEN** autorefresh sends only the refresh and the action reports no applied match

#### Scenario: Match state unknown

- **WHEN** the read just before the match fails
- **THEN** autorefresh sends no match and sends only the refresh
