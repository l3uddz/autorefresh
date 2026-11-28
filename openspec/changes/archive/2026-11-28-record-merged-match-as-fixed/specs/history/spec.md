## MODIFIED Requirements

### Requirement: Gone events keep the action that found them

When Plex no longer has a cached item, autorefresh SHALL record a `gone` event and remove the cached row in the same transaction, unless Plex merged it after a match (see `fix-match`). A `gone` event that a request found SHALL keep the action of that request. A `gone` event from a full read or a metadata check SHALL keep no action.

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

### Requirement: A 404 of a cached item replaces the request event

When Plex answers a request of a cached item with 404, autorefresh SHALL record a `gone` event, or the completion of a merge (see `fix-match`), and no `request` event. When Plex answers a manual refresh of an item with only history with 404, autorefresh SHALL record a failed `request` event and no `gone` event.

#### Scenario: Cached item

- **WHEN** Plex answers the refresh of a cached item with 404
- **THEN** a `gone` event with action `refresh` is recorded and no `request` event

#### Scenario: Item with only history

- **GIVEN** an item that has history but no cache row
- **WHEN** Plex answers its manual refresh with 404
- **THEN** a `request` event with outcome `refresh-failed` is recorded and no `gone` event

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
- **WHEN** the row leaves the cache while the refresh is in flight, Plex answers 404, the row is still absent when the outcome is recorded, and Plex did not merge the item after a match
- **THEN** a `gone` event is recorded with the snapshot of the start, because the action keeps the cache state of its start

## ADDED Requirements

### Requirement: Completed events of a merge

When the follow-up of an accepted match finds that Plex merged a cached item into another item, autorefresh SHALL remove the row and record a `completed` event with source `auto` in the same transaction, also in a manual action. The event SHALL credit the row's last accepted match as both the action and the match kind, with the ID of its request event, and SHALL keep the rating key of the item that Plex kept. Every other event SHALL keep no such rating key.

#### Scenario: Plex merged the matched item

- **WHEN** the follow-up of an accepted match finds that Plex merged item `a` into item `b`
- **THEN** the row of `a` is removed, and its `completed` event has source `auto`, credits the match and its request event, and keeps the rating key of `b`

#### Scenario: Row left the cache during the action

- **WHEN** the row of `a` left the cache while the refresh after the match was in flight, and the follow-up then finds the merge
- **THEN** autorefresh records no event for `a`

#### Scenario: Ordinary completion

- **WHEN** a check finds an item complete after an accepted match
- **THEN** its `completed` event keeps no merged rating key
