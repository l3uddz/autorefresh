# refresh-runs Specification

## Purpose

Define what one automatic run does for each Plex server after it reads the libraries: which cached items are due, in which order, how fast and how many it refreshes, how it handles failures, and what it logs and reports while it works.

## Requirements

### Requirement: Servers run in parallel

A run SHALL handle each configured server at the same time as the other servers.

#### Scenario: Two servers

- **GIVEN** two servers that each have due items
- **WHEN** a run starts
- **THEN** each server starts its items without waiting for the other server to finish

### Requirement: Items of one server run one at a time

A run SHALL handle the items of one server one at a time.

#### Scenario: Two due items

- **WHEN** a server has two due items
- **THEN** the run sends the request of the second item only after the first item is done

### Requirement: An unavailable server ends only its own run

When a server becomes unavailable during a run, the run SHALL end for that server only.

#### Scenario: One server is down

- **GIVEN** two servers, and the first refresh request on server `down` fails because the server is unavailable
- **WHEN** a run starts
- **THEN** server `down` sends no more requests in this run
- **AND** the other server still refreshes its due items and logs its summary

### Requirement: A database error ends the runs of all servers

When a database error ends the run of one server, the run SHALL end for all servers. When the run of a server already ended with another error, such as an unavailable server, and recording its end also fails, autorefresh SHALL log `Recording run failed`, and the first error SHALL decide whether the other servers stop.

#### Scenario: Database error on one server

- **WHEN** a database error ends the run of one server
- **THEN** the runs of the other servers stop too
- **AND** the run returns that error together with the errors of any unavailable server

#### Scenario: Recording fails after an unavailable server

- **WHEN** a server is unavailable and recording the end of its run fails too
- **THEN** autorefresh logs `Recording run failed`, and the runs of the other servers go on

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

### Requirement: Backoff of the wait

The wait of an item SHALL start at its last attempt. It SHALL be `refresh-interval` for an item with at most one accepted request, and SHALL double with each later accepted refresh or match request, up to `max-refresh-interval`. A failed attempt SHALL start a new wait but SHALL NOT make the wait longer. When `max-refresh-interval` is below `refresh-interval`, the wait SHALL always be `refresh-interval`.

#### Scenario: Default waits

- **GIVEN** `refresh-interval` is `24h` and `max-refresh-interval` is `168h`
- **WHEN** an item stays incomplete after each accepted refresh
- **THEN** its waits are 24h, 24h, 48h, 96h, and then 168h for every later refresh

#### Scenario: Failed request

- **WHEN** the only request of an item fails with an error that does not stop the server
- **THEN** the item is not due 23 hours later and is due again 24 hours later with the default `refresh-interval`

#### Scenario: No backoff

- **WHEN** `max-refresh-interval` equals `refresh-interval`
- **THEN** an item with five accepted refreshes waits only `refresh-interval`

### Requirement: Longest wait warning

When Plex accepts a request that moves an item to the longest wait, the run SHALL log one warning `Item reached the longest wait` with the server, library, title, reasons, the number of refreshes, and the wait. Without backoff, it SHALL log no such warning.

#### Scenario: Third refresh with a 96h maximum

- **GIVEN** `refresh-interval` is `24h` and `max-refresh-interval` is `96h`
- **WHEN** Plex accepts the third refresh of an item
- **THEN** the run logs one `Item reached the longest wait` warning with `refreshes` 3

### Requirement: Queue order

A run SHALL handle the due items of a server in this order: the fewest attempts first, then items with no attempt, then the oldest last attempt, then the item that Plex added first, then the rating key. A failed attempt SHALL count like an accepted one, so an item that keeps failing moves back in the queue.

#### Scenario: New items first

- **WHEN** three due items have no attempt and were added at different times
- **THEN** the run refreshes the item that Plex added first, then the next oldest, then the newest

#### Scenario: Failing item gives up its place

- **GIVEN** `max-refreshes-per-run` is `1`, and an item fails in every run
- **WHEN** another due item has four more attempts than the failing item
- **THEN** the failing item takes the only place in four runs, and the other item takes it in the fifth run

### Requirement: Limit for each server

A run SHALL handle at most `max-refreshes-per-run` due items on each server, in queue order. The limit SHALL count handled items, not requests: a skipped item, a gone item, and an item whose match search failed each use one place, and the refresh that follows an accepted match uses none. A value of `0` SHALL mean no limit. A dry run SHALL apply the same limit.

#### Scenario: More due items than the limit

- **GIVEN** `max-refreshes-per-run` is `2` and three items are due
- **WHEN** Plex accepts a plain refresh of each of the first two items, and the run ends
- **THEN** it sent two refresh requests, and its summary has `due_items` 3 and `remaining_items` 1

#### Scenario: Failed match search uses a place

- **GIVEN** `max-refreshes-per-run` is `1`
- **WHEN** a `tmdb` path ID search of the first due item answers `500`, which ends its automatic decision
- **THEN** the run sends no request in this run and leaves the next item for a later run

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

### Requirement: Choice of the action

A run SHALL send a plain refresh, unless the item is unmatched, `unmatched` is not in `ignore-reasons`, and Plex accepted at least one request for the item before. Such an item SHALL go through the automatic fix match (see `fix-match`), which sends a match request or else a plain refresh. An item with only episode reasons SHALL always get a plain refresh.

#### Scenario: First attempt of an unmatched item

- **WHEN** an unmatched movie with a TMDB ID in its path has no accepted request yet
- **THEN** the run sends a plain refresh and makes no match search

#### Scenario: Unmatched episodes only

- **WHEN** a matched series is due only for unmatched episodes
- **THEN** the run sends a plain refresh of the series and makes no match search

### Requirement: Single-item refresh request

A refresh SHALL be one `PUT` request to `/library/metadata/<ratingKey>/refresh` below the server URL, for that one item. Autorefresh SHALL NOT send a library-wide refresh.

#### Scenario: Refresh of a movie

- **WHEN** a run refreshes the item with rating key `1001`
- **THEN** Plex receives `PUT /library/metadata/1001/refresh` and no other refresh request

### Requirement: Accepted request

When Plex accepts a request, the run SHALL record an accepted attempt at the time of the request, add one to the refresh count of the item, and log one info line `Sent request` with the server, library, title, reasons, and action. For a match request, the line SHALL also name the item year, the candidate and its year, and the links of the IDs that found it.

#### Scenario: Accepted plain refresh

- **WHEN** Plex accepts the refresh of an item
- **THEN** the item has one more refresh, its last refresh and last attempt are the request time, and its last action is `refresh`

### Requirement: Follow-up after a request

After an accepted plain refresh, a run SHALL do nothing more for that item: it SHALL NOT wait for or check the result. The item SHALL leave the queue when a later read finds it complete (see `library-scan`). After an accepted match request, the run SHALL follow up the item as `fix-match` describes, and a follow-up that finds the item complete, or merged into another item, SHALL count as a completion of this run.

#### Scenario: Plain refresh

- **WHEN** a run refreshes three items with a plain refresh
- **THEN** it makes no request to the items after their refresh and waits only the `refresh-delay` between them

#### Scenario: Match that fixes the item

- **WHEN** Plex accepts a match, and a follow-up check finds the item complete
- **THEN** the item leaves the queue in the same run
- **AND** the summary counts it in `completed_items` and `completed_after_match`

#### Scenario: Match that Plex merged

- **WHEN** Plex accepts a match, and the follow-up finds that Plex merged the item into another item
- **THEN** the item leaves the queue in the same run, and the summary counts it in `completed_items` and `completed_after_match`

### Requirement: Failed request

When a refresh, a match request, or a match search fails with an error that does not stop the server, the run SHALL record a failed attempt, count one failure, log a warning (`Request failed` with the action, or `Match search failed`), and go on to the next item. It SHALL NOT add to the refresh count. After a search error that ends the automatic decision, it SHALL send that item no request in this run. Errors that `fix-match` handles, such as a failed title search or TMDB lookup, follow it.

#### Scenario: Server error on refresh

- **WHEN** Plex answers a refresh with `500 Internal Server Error`
- **THEN** the item keeps its refresh count and last action, its last attempt is the request time, and the run logs `Request failed` with action `refresh`

#### Scenario: Failed match search

- **WHEN** the search for a `tmdb` path ID of an unmatched item fails with a server error
- **THEN** the run sends neither a match nor a plain refresh for that item, and records a failed attempt

#### Scenario: Failed title search

- **WHEN** the title search of the title fallback fails with a server error
- **THEN** the run records a `failed` decision and sends a plain refresh for that item

### Requirement: Gone item

When Plex answers a request of a run with 404, the run SHALL remove the item from the cache, record it as gone with the action of that request (see `history`), or as merged after a match (see `fix-match`), and continue with the next item. The request SHALL count as sent but not as a failure, and the run SHALL log no warning for it.

#### Scenario: Refresh answered with 404

- **WHEN** Plex answers the refresh of a cached item with 404
- **THEN** the item is no longer cached, and the summary has `refresh_requests` 1 and `failed_requests` 0

#### Scenario: 404 in a dry run

- **WHEN** Plex answers a match search read of a dry run with 404
- **THEN** the item stays in the cache unchanged, and the run logs a warning

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

### Requirement: Dry run

A dry run SHALL handle the same items as a normal run, within the same limit, with the same match search reads. For each item whose action it decides, it SHALL log one info line `Dry run` with that action and the match fields of a match; a search error that ends the decision logs `Match search failed` instead. It SHALL send no refresh or match, SHALL NOT wait between items, and SHALL record no attempts; `history` covers the rest.

#### Scenario: Due items in a dry run

- **WHEN** a dry run handles a due item with no summary and an unmatched item whose path ID finds one candidate
- **THEN** it logs a `Dry run` line with action `refresh` for the first item and one with action `match` and the candidate for the second
- **AND** Plex receives no refresh or match request, and the attempt times of both items stay the same

### Requirement: Summary log

At the end of the run of each server, autorefresh SHALL log one info line `Run summary` with the server, `full_read_items`, `incremental_read_items`, `incomplete_items`, `due_items`, `refresh_requests`, `match_requests`, `failed_requests`, `remaining_items`, `completed_items`, `completed_after_request`, `completed_after_match`, and `duration` rounded to milliseconds. It SHALL log it when the run ends normally and when the server became unavailable after its reads.

#### Scenario: Normal end

- **WHEN** the run of a server ends without an error
- **THEN** autorefresh logs one `Run summary` line for that server

#### Scenario: Unavailable before the refresh step

- **WHEN** a server does not list its libraries
- **THEN** autorefresh logs `Plex server unavailable` and no `Run summary` line for that server

#### Scenario: Canceled run

- **WHEN** a run is canceled
- **THEN** autorefresh logs no `Run summary` line for it

### Requirement: Canceled run

When the service stops during a run, the run SHALL stop before the next item, also during a `refresh-delay` wait, and SHALL log no error. A request that was in flight SHALL still count as a failed attempt of its item.

#### Scenario: Stop during the delay

- **WHEN** the service stops while a run waits before its second item
- **THEN** the second item gets no request, and the run returns without an error

#### Scenario: Stop during a request

- **WHEN** the service stops while a refresh request is in flight
- **THEN** the item gets a failed attempt at the request time and no refresh

### Requirement: Progress of run start and end

When live progress is on, a run SHALL report the start of the run of each server, with the server and the start time, before it reads the libraries. It SHALL report the end of each server run once, with the counts of the run, its status `finished`, `failed`, or `canceled`, and the error of a failed run.

#### Scenario: Server that does not list its libraries

- **WHEN** a server does not list its libraries
- **THEN** the progress gets only the start and an end with status `failed` and the unavailable error

#### Scenario: Canceled after a send

- **WHEN** the service stops during a refresh request
- **THEN** the end has status `canceled` and no error

### Requirement: Progress of reads and the batch

When live progress is on, a run SHALL report each library after its read was applied, with the library title. After it reads the queue, it SHALL report the number of items that this run handles and the number of due items.

#### Scenario: Limit below the due items

- **GIVEN** `max-refreshes-per-run` is `2` and three items are due
- **WHEN** the run reads the queue
- **THEN** the progress gets a batch with 2 handled items and 3 due items

#### Scenario: Nothing due

- **WHEN** no item is due
- **THEN** the progress gets the start, each library read, a batch of 0 and 0, and the end

### Requirement: Progress of the next item

When live progress is on, a run SHALL report the next item before each `refresh-delay` wait, with the time when the wait ends. It SHALL NOT report a next item for the first item, when `refresh-delay` is zero, in a dry run, or when the run has already ended.

#### Scenario: Second item

- **GIVEN** `refresh-delay` is `5s`
- **WHEN** the first item is done
- **THEN** the progress gets the second item as next, with an end 5 seconds later, before it gets the start of the second item

#### Scenario: No delay

- **WHEN** `refresh-delay` is `0s`
- **THEN** the progress gets no next item

### Requirement: Progress of each item

When live progress is on, a run SHALL report each item when it starts, with its rating key, title, year, type, library title, thumb, and the reasons that are not in `ignore-reasons`. When the item is done, it SHALL report its outcome and the counts of the run so far: refreshed, matched, failed, and skipped. The counts SHALL equal those that the stored run gets.

#### Scenario: Outcomes

- **WHEN** an item is done
- **THEN** its outcome is the accepted action, such as `refresh` or `match`, the action with `-failed`, such as `refresh-failed`, `search-failed`, `gone`, `skipped`, or `dry-run`

#### Scenario: No outcome without a result

- **WHEN** a database write fails for the item, or the service stops during its match search
- **THEN** the progress gets the start of the item but no outcome

#### Scenario: Counts after a failure

- **GIVEN** the first refresh is accepted and the second one fails
- **WHEN** both items are done
- **THEN** the counts are 1 refreshed after the first item and 2 refreshed with 1 failed after the second item

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
