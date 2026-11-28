# write-attempts Specification

## Purpose

Keeps a record of each Plex write that autorefresh sends, so that a library read that started before the write took effect does not overwrite its result.

## Requirements

### Requirement: An attempt before each write

Before each Plex refresh request and each Plex match request, from a run, a bulk refresh, or an item action, autorefresh SHALL save one write attempt in its own database write transaction. The attempt SHALL name the server, the item, the action, its start, and its end. If the save fails, is cancelled, or ends after the deadline of the send, autorefresh SHALL send nothing, record no request, and count no attempt of the item. A dry run SHALL save no attempt.

#### Scenario: Refresh of a run

- **WHEN** a run sends a refresh for item `a`
- **THEN** the database has a write attempt for `a` with the action `refresh` that it saved before Plex got the request

#### Scenario: Save fails

- **GIVEN** another process holds the write lock for longer than the deadline of an item action
- **WHEN** the user clicks Refresh now on item `a`
- **THEN** autorefresh sends no request to Plex and records no request event
- **AND** the action answers 504 `timed out` when its deadline passed first, or fails as a database error (500) when the lock wait of SQLite ended first

#### Scenario: Each send has its own attempt

- **WHEN** a match of item `a` is followed by its refresh
- **THEN** the database has two write attempts for `a`, one with the action `match` and one with the action `refresh`

### Requirement: An attempt that was not sent

When the deadline of the send passes after the attempt was saved, autorefresh SHALL send nothing and delete the attempt. When that delete fails, it SHALL log the warning `Dropping write attempt failed` and leave the attempt, which then protects the item until its end.

#### Scenario: Write lock held during the delete

- **GIVEN** the deadline of a refresh passed just after its attempt was saved
- **WHEN** another process holds the write lock while autorefresh deletes the attempt
- **THEN** autorefresh sends nothing, records no request, logs the warning, and the attempt ends as usual

### Requirement: End of an attempt

The end of an attempt SHALL be its start plus the request timeout of the server (`timeout`, default `30s`) plus an allowance of 10 minutes, in whole Unix seconds, rounded up. Each addition SHALL saturate at the largest time instead of overflowing. An attempt SHALL be live while its end is at or after the current time.

#### Scenario: Default timeout

- **WHEN** autorefresh saves an attempt at 12:00:00 for a server without `timeout`
- **THEN** the end of the attempt is 12:10:30

#### Scenario: Very long timeout

- **WHEN** a server sets `timeout` so long that the end would overflow
- **THEN** the end is the largest time, and the attempt stays live

### Requirement: Outcome of a write

Autorefresh SHALL send each write request once and SHALL NOT follow a redirect of it. It SHALL classify the answer as `accepted` for a 2xx status, as `rejected` for a status of 400, 401, 403, 404, or 405, and as `unknown` in every other case, such as a timeout, a lost answer, a refused connection, a 3xx status, a 5xx status, or another 4xx status such as 408 or 429. A 3xx answer SHALL fail the request, and SHALL NOT count as gone.

#### Scenario: Classes of answers

- **WHEN** Plex answers `200 OK`, `404 Not Found`, or `500 Internal Server Error`
- **THEN** the outcomes are `accepted`, `rejected`, and `unknown`

#### Scenario: Timeout answer of a proxy

- **WHEN** a proxy in front of Plex answers a match with `408 Request Timeout`
- **THEN** the outcome is `unknown`, and the attempt stays live until its end

#### Scenario: Redirect of a write

- **WHEN** Plex answers a refresh with `307 Temporary Redirect`
- **THEN** autorefresh sends no second request, records the request as failed, keeps the cache row, and the outcome is `unknown`

### Requirement: Recording the outcome

In the same transaction that records a request, also when it records the item as gone or the cache row is gone, autorefresh SHALL delete a `rejected` attempt and keep an `accepted` or `unknown` attempt until its end. A later read or search of the item SHALL NOT change an attempt.

#### Scenario: Accepted refresh

- **WHEN** Plex answers a refresh with `200 OK`
- **THEN** its attempt stays live until its end

#### Scenario: Answer lost

- **WHEN** a refresh times out after the request was sent
- **THEN** the request is recorded as failed, and its attempt stays live until its end

#### Scenario: Rejected refresh

- **WHEN** Plex answers a refresh with `404 Not Found`
- **THEN** the item is recorded as gone, and its attempt is deleted in the same transaction

#### Scenario: Cache row gone before the answer

- **WHEN** another process removes the library of item `a` after a refresh of `a` was sent, and Plex answers `404 Not Found`
- **THEN** the attempt is deleted, no event is written, and the deletion is committed, although no cache row and no library is left to record

#### Scenario: Library removed before a manual answer

- **WHEN** another process removes the library of item `a` after a Refresh now of `a` was sent, and Plex answers `404 Not Found`
- **THEN** the attempt is deleted and committed, and no gone event is written

#### Scenario: Server error

- **WHEN** Plex answers a refresh with `500 Internal Server Error`
- **THEN** the request is recorded as failed, and its attempt stays live until its end

#### Scenario: Connection refused

- **WHEN** the connection to Plex is refused
- **THEN** the request is recorded as failed, and its attempt stays live until its end, because autorefresh cannot prove that Plex never got it

### Requirement: Pruning of attempts

At the start of each scheduled run and manual scan, outside a dry run, autorefresh SHALL delete the attempts whose end is more than 24 hours ago. When it deletes any, it SHALL increment the generation of the fence of reads in the same transaction.

#### Scenario: Old attempt

- **WHEN** a run starts 25 hours after the end of an attempt
- **THEN** the attempt is deleted, and the generation of the fence grows by one

#### Scenario: Recent attempt

- **WHEN** a run starts one hour after the end of an attempt
- **THEN** the attempt is kept, and the generation stays
