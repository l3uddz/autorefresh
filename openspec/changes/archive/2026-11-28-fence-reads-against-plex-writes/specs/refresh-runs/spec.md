## MODIFIED Requirements

### Requirement: Due items

After the reads, a run SHALL take as due only the cached items of the selected libraries of that server whose read it applied, that are not stopped, have at least one reason that is not in `ignore-reasons` and is not `mismatched`, were added at or before `minimum-age` ago, have no attempt within their current wait, have no live write attempt, and have no hold. An attempt is any recorded refresh request, match request, or failed match search, also a failed one.

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

#### Scenario: Live write attempt

- **WHEN** a cached item is due by the other rules, and an attempt from an earlier request whose answer was lost is still live
- **THEN** no run sends it a request until the attempt ends and no hold keeps it

#### Scenario: Hold after the attempt ended

- **WHEN** a read saved a hold for a cached item, and its attempt has ended
- **THEN** the next run reads its library in full before it takes the due items, and only then can the item be due

### Requirement: Recheck before a send

Before it handles a due item, a run SHALL wait until no item action holds it, and then read it again. It SHALL skip the item when it left the cache, was stopped, got another attempt since the queue was read, got a live write attempt or a hold since the queue was read, moved into a library whose read the run did not apply, or has no reason left outside `ignore-reasons` other than `mismatched`. A skipped item SHALL get no request, a debug line `Skipped item` with its cause, and a count.

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

#### Scenario: Manual write in between from another process

- **WHEN** another autorefresh process saves a write attempt for a queued item before the run reaches it
- **THEN** the run skips the item and sends it nothing

## ADDED Requirements

### Requirement: Admission in the save of the attempt

The transaction that saves the write attempt of a run SHALL first check that the item has no hold, no live write attempt other than the match that the send follows up, and a cached row in a library whose read the run applied. Otherwise it SHALL save no write attempt, and the run SHALL skip the item with the cause `held` as in the recheck. Only the refused refresh after a match saves a hold (see below). Item actions and bulk refreshes SHALL skip this check.

#### Scenario: Write of another process after the recheck

- **WHEN** another autorefresh process saves a write attempt for an item after the run rechecked it and before the run saves its own attempt
- **THEN** the run saves no attempt, skips the item with the cause `held`, and sends it nothing

#### Scenario: Row moved into a library that the run did not read

- **GIVEN** the read of library B failed in this run, and the run queued item `a` of library A
- **WHEN** another process moves the row of `a` into B before the run saves its attempt
- **THEN** the run saves no attempt and sends `a` nothing

#### Scenario: Refresh after the match, nothing in between

- **WHEN** Plex accepted the match of a run, and nothing else saved a hold or an attempt for the item
- **THEN** the run saves the attempt of the refresh and sends it, although the attempt of the match is live

### Requirement: A refused refresh after a match

When the admission refuses the refresh after a match that Plex accepted, the run SHALL keep the match as recorded and counted, send no refresh, make no check, log `Skipped follow-up refresh`, and save a hold for the item in the transaction that refused it.

#### Scenario: Refresh after the match of the run

- **WHEN** Plex accepted the match of a run, and another process saved a hold or an attempt for the item before the refresh after it
- **THEN** the run keeps the accepted match as recorded and counted, sends no refresh, makes no check, and logs `Skipped follow-up refresh`
- **AND** the item has a hold, also after Plex rejects the attempt of the other process, so the next full read of its library reconciles it
