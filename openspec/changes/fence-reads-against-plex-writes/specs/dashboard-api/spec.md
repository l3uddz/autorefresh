## ADDED Requirements

### Requirement: An item that a write holds is waiting

The item list and the item detail SHALL give the state `waiting` to a cached item that would otherwise be `waiting` or `due`, when a write attempt for it is live or it has a hold. A held item that is too new SHALL stay `too-new`. Its next due time SHALL be the later of its usual next due time and the end of its live attempt, at most 9999-12-31T23:59:59Z.

#### Scenario: Answer of a request lost

- **GIVEN** an item that is due by the usual rules
- **WHEN** a refresh of it times out, and its attempt ends at 12:10:30
- **THEN** the item list shows it as `waiting` with the next due time 12:10:30 or later

#### Scenario: Hold after the attempt ended

- **WHEN** a read saved a hold for a due item and its attempt has ended
- **THEN** the item list shows it as `waiting` with its usual next due time until a full read of its library deletes the hold

#### Scenario: Attempt without an end

- **WHEN** a server sets `timeout` so long that the end of an attempt saturates
- **THEN** the item list shows the item as `waiting` with the next due time 9999-12-31T23:59:59Z, and sorts it before the items without a next due time

### Requirement: An item action that passes the server timeout

An item action whose attempt save or Plex write passes the deadline of the server timeout while the request is still open SHALL answer 504 `timed out`, also when only the write timed out, where it answered 502 before.

#### Scenario: Refresh now times out

- **WHEN** Plex does not answer a Refresh now within the server timeout
- **THEN** the API answers 504 `timed out`, and the attempt of the refresh stays live until its end

