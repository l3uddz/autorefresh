# Spec Delta: refresh-runs

## MODIFIED Requirements

### Requirement: Pacing

A run SHALL wait `refresh-delay` before the next item of a server only after an item that sent a request to Plex. A skipped item SHALL NOT trigger a pace wait before the next item. A dry run SHALL NOT wait. A match search is a read and SHALL NOT be paced on its own.

#### Scenario: Three due items

- **GIVEN** `refresh-delay` is `5s`
- **WHEN** a run refreshes three items on one server
- **THEN** it waits 5 seconds twice: before the second item and before the third item

#### Scenario: Skipped item between due items

- **GIVEN** `refresh-delay` is `5s` and the first due item was stopped before its turn
- **WHEN** the run skips the first item and moves to the second
- **THEN** it starts the second item without waiting `refresh-delay`

#### Scenario: Request time

- **WHEN** a run waits `refresh-delay` between items
- **THEN** the attempt time of each item is taken when Plex answers its own request, and the summary duration includes the waits

### Requirement: Unavailable server

Except in the title fallback of `fix-match`, a run SHALL end a server's run when Plex answers 401, 429, 502, 503, or 504, fails in transport, ends early, or cannot list libraries. It SHALL log one error `Plex server unavailable` with the server and send no more requests to it in this run. The failing item SHALL still record its failed attempt. Other errors, such as 500, SHALL fail only the item.

#### Scenario: Unavailable during the refresh step

- **GIVEN** two due items on one server
- **WHEN** Plex answers the first refresh with `503 Service Unavailable`
- **THEN** the first item gets a failed attempt, the second item gets no request, and the summary has `remaining_items` 1
- **AND** the next run sends the second item first, because the first item waits

#### Scenario: Rate limited by a proxy

- **GIVEN** two due items on one server
- **WHEN** Plex or its reverse proxy answers the first refresh with `429 Too Many Requests`
- **THEN** the first item gets a failed attempt, the run of that server ends as failed, and the second item gets no request

#### Scenario: Token rejected

- **WHEN** Plex answers a refresh with `401 Unauthorized`
- **THEN** the run of that server ends as failed after that item

#### Scenario: Title fallback search answers 503

- **WHEN** Plex answers the title search of the title fallback with `503 Service Unavailable`
- **THEN** the item gets a failed decision and a plain refresh, and only the answer to that refresh can end the run
