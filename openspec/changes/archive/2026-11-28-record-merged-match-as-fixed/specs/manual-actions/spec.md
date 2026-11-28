## MODIFIED Requirements

### Requirement: Apply a chosen candidate

The user SHALL be able to apply a chosen candidate, given by its Plex GUID, name, and year, to any known item, also to a matched item or an item on the Fixed tab, to correct a wrong match. Autorefresh SHALL send the match to Plex and record a manual match request with that target. After Plex accepts the match, it SHALL send a refresh of the item and then check a cached item as after a manual request.

#### Scenario: Match completes the item

- **WHEN** Plex accepts the match and a check then finds the item complete
- **THEN** Plex got the match and then the refresh, the item has two more attempts, and the cache row is removed
- **AND** the completed event is credited to the refresh after the match, so the item shows as fixed by match

#### Scenario: Plex rejects the match

- **WHEN** Plex answers the match with an error
- **THEN** the action fails, and autorefresh sends no refresh and makes no check

#### Scenario: Refresh after the match finds the item gone

- **WHEN** Plex answers the refresh after an accepted match with 404, the item had a cache row when the action started, and Plex did not merge it (see `fix-match`)
- **THEN** autorefresh records the item as gone by that refresh, makes no check, and the match action succeeds

#### Scenario: Refresh after the match of an item with only history answers 404

- **GIVEN** an item that has history but no cache row
- **WHEN** Plex accepts the match and answers the refresh after it with 404
- **THEN** autorefresh records a failed manual refresh request event and no gone event, makes no check, and the match action succeeds

#### Scenario: Server unavailable after the match

- **WHEN** the refresh after an accepted match finds the server unavailable
- **THEN** autorefresh makes no check and the match action succeeds

#### Scenario: Plex merged the item after the match

- **WHEN** Plex answers the refresh after an accepted match with 404, and Plex merged the cached item into another item with the chosen GUID
- **THEN** autorefresh records the item as fixed by the match and merged, makes no check, and the match action succeeds

### Requirement: Check after a manual request

After Plex accepts a manual refresh of a cached item, also the refresh after a match, autorefresh SHALL check that one item. It SHALL wait 1 s, 2 s, and 4 s before up to three metadata reads of the item. Each check SHALL save the reasons that Plex shows, unless a newer write yields it (see below) or Plex shows another GUID than the chosen match. The checks SHALL end at the first check that completes the item or finds it gone. The action SHALL report whether a check saved the current reasons.

#### Scenario: Check completes the item

- **WHEN** the second check finds the item complete
- **THEN** autorefresh removes its cache row, records a completed event, and makes no third check

#### Scenario: Reasons remain

- **WHEN** each check still finds a missing summary
- **THEN** autorefresh makes three checks and the cache row keeps only the reasons that Plex shows

#### Scenario: Check finds the item gone

- **WHEN** Plex answers a check with 404, and Plex did not merge the item after a match (see `fix-match`)
- **THEN** autorefresh removes the cache row and records a manual gone event without an action

#### Scenario: A run removed the row meanwhile

- **WHEN** a run completes or removes the item during a wait
- **THEN** the checks end without a request to Plex and without a warning, and the action reports the reasons as current
