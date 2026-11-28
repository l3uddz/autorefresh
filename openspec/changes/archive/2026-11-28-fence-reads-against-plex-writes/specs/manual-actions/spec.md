## MODIFIED Requirements

### Requirement: Check after a manual request

After Plex accepts a manual refresh of a cached item, also the refresh after a match, autorefresh SHALL check that one item. It SHALL wait 1 s, 2 s, and 4 s before up to three metadata reads of the item. Each check SHALL save the reasons that Plex shows, unless a newer write yields it (see below). The checks SHALL end at the first check that completes the item or finds it gone. The action SHALL report whether a check saved the current reasons.

#### Scenario: Check completes the item

- **WHEN** the second check finds the item complete
- **THEN** autorefresh removes its cache row, records a completed event, and makes no third check

#### Scenario: Reasons remain

- **WHEN** each check still finds a missing summary
- **THEN** autorefresh makes three checks and the cache row keeps only the reasons that Plex shows

#### Scenario: Check finds the item gone

- **WHEN** Plex answers a check with 404
- **THEN** autorefresh removes the cache row and records a manual gone event without an action

#### Scenario: A run removed the row meanwhile

- **WHEN** a run completes or removes the item during a wait
- **THEN** the checks end without a request to Plex and without a warning, and the action reports the reasons as current

## ADDED Requirements

### Requirement: A check yields to another write

A check SHALL save nothing, also for a 404, when a pruning ran since its metadata read began, or another write attempt for the item, outside the attempts of the action, was live at or after that start. Only an earlier refresh that Plex accepted before the action began SHALL NOT stop it. On a yield, the checks SHALL end, the item SHALL get a hold, and the action SHALL report the reasons as not current. A hold SHALL NOT stop a check, and a check SHALL NOT delete a hold.

#### Scenario: Newer write from another process

- **GIVEN** Plex accepted a manual refresh of item `a`
- **WHEN** another autorefresh process saves a write attempt for `a` before the first check
- **THEN** the check saves nothing, the checks end, `a` gets a hold, and the action reports the reasons as not current

#### Scenario: Check finds the item gone after another write

- **WHEN** another process saved a write attempt for `a` after the request, and the check gets a 404
- **THEN** the check keeps the row of `a`, and `a` gets a hold

#### Scenario: Check of a held item

- **GIVEN** item `a` has a hold, and no attempt for it is live
- **WHEN** the user clicks Refresh now on `a`, and the check finds `a` still missing a summary
- **THEN** the check saves the reasons, and the hold of `a` stays until a full read of its library

#### Scenario: Library removed during the check

- **WHEN** another process removes the library of `a` while a check of `a` waits for Plex
- **THEN** the check saves nothing, adds no hold, and the checks end

#### Scenario: Earlier match of another process

- **GIVEN** another process saved a match attempt for `a` before the user applied a match to `a`
- **WHEN** a check of the user's match shows its GUID with complete metadata
- **THEN** the check saves nothing, and `a` gets a hold

#### Scenario: Competing attempt ends during the check

- **WHEN** another write attempt for `a` is live when a check starts its metadata read, and ends before the check saves
- **THEN** the check saves nothing, and `a` gets a hold

#### Scenario: Earlier accepted match

- **GIVEN** Plex accepted a Fix match of `a` five minutes ago
- **WHEN** the user clicks Refresh now on `a`, and the check shows complete metadata
- **THEN** the check saves nothing, `a` gets a hold and shows as `waiting`, and the next full read of its library settles `a`

#### Scenario: Earlier accepted refresh

- **GIVEN** Plex accepted a Refresh now of `a` five minutes ago
- **WHEN** the user applies a match to `a`, and a check shows the GUID of the match with complete metadata
- **THEN** the check moves `a` to Fixed

### Requirement: A check yields when its own attempts ended

A check SHALL save nothing, also for a 404, and SHALL add a hold when no write attempt of its action is live at the save, because a read may then have applied a newer listing of the item. The checks SHALL end, and the action SHALL report the reasons as not current.

#### Scenario: Check after the end of its attempt

- **GIVEN** a server with a `timeout` so long that a check waits past the end of the attempt of its refresh
- **WHEN** the check saves after that end
- **THEN** it saves nothing, `a` gets a hold, and the next full read reconciles `a`

#### Scenario: Incremental read that does not list the item

- **GIVEN** Plex accepted a Refresh now of movie `a`, then the movie was deleted, and the attempt of the refresh is still live
- **WHEN** the check gets a 404, and an incremental read that does not list `a` is applied before the check saves
- **THEN** the check records `a` as gone
