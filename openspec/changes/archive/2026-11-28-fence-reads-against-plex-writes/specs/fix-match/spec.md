## MODIFIED Requirements

### Requirement: Follow-up of an accepted match

After Plex accepts a match, autorefresh SHALL send a refresh of the item, unless a hold or another write attempt keeps a run from it, and then check a cached item after waits of 1, 2, and 4 seconds. A check that shows the chosen GUID and finds the item complete SHALL move it to Fixed, unless it yields to another write. The checks SHALL end when the item is complete, gone, or no longer cached, or when the waits run out. A run SHALL record these steps and count a completion as its own.

#### Scenario: Match fixes the item

- **WHEN** the second check finds the matched item complete
- **THEN** the item leaves the cache with a completion that credits the refresh after the match, and the run counts it as completed after a match

#### Scenario: Refresh answers 404

- **WHEN** the refresh after the match answers 404
- **THEN** autorefresh records the item as gone, makes no check, and the run goes on

#### Scenario: Server becomes unavailable

- **WHEN** the refresh after the match finds the server unavailable
- **THEN** the follow-up ends, and the run of that server ends as failed

#### Scenario: Refresh answers 500

- **WHEN** the refresh after the match answers 500
- **THEN** autorefresh logs `Following up a request failed` and still checks the item

## ADDED Requirements

### Requirement: A check after a match confirms the GUID

A check after a match SHALL save the reasons of the item only when Plex shows the GUID of the chosen candidate. A check that shows another GUID SHALL save nothing. When the waits run out before Plex shows that GUID, autorefresh SHALL leave the row as it was and save a hold for the item, so the next read of its library is full.

#### Scenario: Plex still shows the old match

- **GIVEN** Plex accepted a match of item `a` to `plex://movie/200`
- **WHEN** the first check still shows `plex://movie/100` with complete metadata
- **THEN** the check saves nothing and does not move `a` to Fixed, and the next check reads `a` again

#### Scenario: Match to the current GUID

- **WHEN** the user applies a match of `a` to the GUID that `a` already has, and the check shows that GUID with complete metadata
- **THEN** the check sees the intended GUID and moves `a` to Fixed, as after a plain refresh

#### Scenario: Match not seen in time

- **WHEN** no check shows `plex://movie/200` before the waits run out
- **THEN** the row of `a` stays as it was, and `a` gets a hold
