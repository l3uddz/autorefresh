## MODIFIED Requirements

### Requirement: Due items

After the reads, a run SHALL take as due only the cached items of the selected libraries of that server that are not stopped, have at least one reason that is not in `ignore-reasons` and is not `mismatched`, were added at or before `minimum-age` ago, and have no attempt within their current wait. An attempt is any recorded refresh request, match request, or failed match search, also a failed one.

#### Scenario: Stopped item

- **WHEN** a cached item is stopped
- **THEN** no run sends it a request until it is resumed

#### Scenario: Only ignored reasons

- **GIVEN** `ignore-reasons` contains `no-art`
- **WHEN** a cached item lacks only its art
- **THEN** the item is not due and the summary does not count it as incomplete

#### Scenario: New item

- **GIVEN** `minimum-age` is `24h`
- **WHEN** Plex added an incomplete item one hour ago
- **THEN** the item is not due, and it becomes due once it is 24 hours old

#### Scenario: Only a wrong match

- **WHEN** a cached movie has only the reason `mismatched`, and `ignore-reasons` does not list it
- **THEN** the item is not due, no run sends it a refresh or a match, and the summary counts it as incomplete

#### Scenario: Wrong match and a missing summary

- **WHEN** a cached movie has the reasons `mismatched` and `no-summary`
- **THEN** the item is due by the usual rules and gets a plain refresh, and the refresh does not clear `mismatched`

### Requirement: Recheck before a send

Before it handles a due item, a run SHALL wait until no manual action holds that item, and then read the item again. It SHALL skip the item when it left the cache, was stopped, got another attempt since the queue was read, or has no reason left outside `ignore-reasons` other than `mismatched`. A skipped item SHALL get no request, a debug line `Skipped item` with the cause, and a count in the skipped items.

#### Scenario: Manual refresh in between

- **WHEN** a manual refresh records an attempt on a queued item before the run reaches it
- **THEN** the run skips the item and sends it nothing

#### Scenario: Stopped while queued

- **WHEN** a user stops a queued item before the run reaches it
- **THEN** the run skips the item and sends it nothing

#### Scenario: Only a wrong match is left

- **GIVEN** a queued movie with the reasons `mismatched` and `no-summary`
- **WHEN** a check saves a summary for it before the run reaches it, without a new attempt
- **THEN** the run skips the movie and sends it nothing
