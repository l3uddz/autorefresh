# manual-actions Specification

## Purpose

Define what the manual actions of the dashboard ask Plex and do to the state of an item, and how they share the servers and the items with scheduled runs, other jobs, and shutdown.

## Requirements

### Requirement: Actions act only on known items

An item action SHALL act only on a configured server and on an item that autorefresh knows: an item in the cache, or an item that has only history, such as an item on the Fixed tab. It SHALL refuse an unknown server or an unknown item before any Plex request. Stop and resume SHALL also refuse an item that is not in the cache.

#### Scenario: Unknown item

- **WHEN** the user refreshes an item that has no cache row and no history
- **THEN** autorefresh refuses the action as not found and sends no request to Plex

#### Scenario: Item with only history

- **GIVEN** an item that has history but no cache row
- **WHEN** the user refreshes it
- **THEN** autorefresh sends the refresh and records a manual request event from the last snapshot of the item
- **AND** it creates no cache row, runs no fix match, and runs no check after the request

#### Scenario: Stop an item with only history

- **WHEN** the user stops an item that has history but no cache row
- **THEN** autorefresh refuses the action as not cached

### Requirement: Refresh now sends and records one refresh

When the user refreshes one item and no fix match applies, autorefresh SHALL send one refresh request for that item to Plex at once and record it with the source manual. For a cached item, a request that Plex accepts SHALL count as an attempt and a refresh, and a failed request SHALL count only as an attempt. The next wait SHALL follow the backoff rule of the `refresh-runs` capability. A 404 of a cached item SHALL remove the item instead.

#### Scenario: Refresh a cached item

- **WHEN** the user refreshes a cached item that is not unmatched
- **THEN** Plex gets one refresh request for that item
- **AND** the item has one more attempt and one more refresh, and its history has an accepted manual request

#### Scenario: Plex rejects the refresh

- **WHEN** Plex answers the refresh of a cached item with an error other than 404
- **THEN** autorefresh records a failed manual request and the action fails with that error
- **AND** the item has one more attempt, the same number of refreshes, and a new wait of the same length

#### Scenario: Attempt time

- **WHEN** the user refreshes a cached item
- **THEN** its attempt time is the time just before autorefresh sends the request, not the time of the answer

#### Scenario: Plex no longer has the item

- **WHEN** Plex answers the refresh of a cached item with 404
- **THEN** autorefresh records the item as gone instead of a request event, removes its cache row, and the action fails as not found

#### Scenario: Plex no longer has an item with only history

- **GIVEN** an item that has history but no cache row
- **WHEN** Plex answers its refresh with 404
- **THEN** autorefresh records a failed manual request event and no gone event, and the action fails as not found

### Requirement: Refresh now runs the automatic fix match first

When the user refreshes a cached item that is unmatched, and `unmatched` is not in `ignore-reasons`, autorefresh SHALL first run the automatic fix match of `fix-match` and record its decision, also before the first refresh of the item. When the decision picks a candidate, autorefresh SHALL apply that match instead of the plain refresh and follow it up as an applied match. Otherwise it SHALL send the plain refresh.

#### Scenario: The decision picks a candidate

- **GIVEN** an unmatched cached item whose folder ID finds one candidate
- **WHEN** the user refreshes it
- **THEN** Plex gets the match request and then a refresh request
- **AND** the history has an automatic decision that belongs to no run and an accepted manual match request
- **AND** the action reports the applied match

#### Scenario: Unmatched is ignored

- **GIVEN** `unmatched` is in `ignore-reasons`
- **WHEN** the user refreshes an unmatched item
- **THEN** autorefresh runs no match search, records no decision, and sends only the refresh

### Requirement: Refresh now falls back to a plain refresh

When the fix match search of Refresh now fails, also when it runs out of its 45 s limit, autorefresh SHALL record a failed decision and still send the plain refresh. When the action ends during the search, autorefresh SHALL send nothing and record nothing.

#### Scenario: Search fails

- **WHEN** the match search of Refresh now fails because the server is unavailable
- **THEN** the history has a failed decision and Plex still gets the refresh

#### Scenario: Slow search

- **WHEN** the match search takes longer than 45 s
- **THEN** autorefresh stops the search, records a failed decision, and sends the plain refresh

#### Scenario: Action ends during the search

- **WHEN** the action is cancelled while the search runs
- **THEN** autorefresh sends no request and records no event, and the action fails as cancelled

#### Scenario: Item gone during the search

- **WHEN** Plex answers a search request with 404
- **THEN** autorefresh records no decision, and the refresh after it records the item as gone once

### Requirement: Refresh now keeps a match that the item got meanwhile

Just before it applies the picked match, autorefresh SHALL read the item from Plex again. It SHALL apply the match only when Plex still has the item unmatched. Otherwise, also when Plex cannot answer, it SHALL send only the plain refresh, because a library read of a run can match the item during the search.

#### Scenario: Matched during the search

- **WHEN** Plex shows the item as matched just before the match
- **THEN** autorefresh sends only the refresh and the action reports no applied match

#### Scenario: Match state unknown

- **WHEN** the read just before the match fails
- **THEN** autorefresh sends no match and sends only the refresh

### Requirement: Check after a manual request

After Plex accepts a manual refresh of a cached item, also the refresh after a match, autorefresh SHALL check that one item. It SHALL wait 1 s, 2 s, and 4 s before up to three metadata reads of the item. Each check SHALL save the reasons that Plex shows. The checks SHALL end at the first check that completes the item or finds it gone. The action SHALL report whether a check saved the current reasons.

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

### Requirement: A failed check does not fail the action

Plex accepted the request, so a failed check SHALL NOT fail the action. Autorefresh SHALL log the failure and make the next check. An unavailable server or the end of the action SHALL end the checks.

#### Scenario: Check fails

- **WHEN** each check fails with an error other than an unavailable server
- **THEN** autorefresh makes three checks, keeps the accepted request and the cached reasons, and the action succeeds

#### Scenario: Server unavailable during the checks

- **WHEN** the first check finds the server unavailable
- **THEN** autorefresh makes no further check and the action succeeds and reports that no check saved the reasons

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

- **WHEN** Plex answers the refresh after an accepted match with 404, and the item had a cache row when the action started
- **THEN** autorefresh records the item as gone by that refresh, makes no check, and the match action succeeds

#### Scenario: Refresh after the match of an item with only history answers 404

- **GIVEN** an item that has history but no cache row
- **WHEN** Plex accepts the match and answers the refresh after it with 404
- **THEN** autorefresh records a failed manual refresh request event and no gone event, makes no check, and the match action succeeds

#### Scenario: Server unavailable after the match

- **WHEN** the refresh after an accepted match finds the server unavailable
- **THEN** autorefresh makes no check and the match action succeeds

### Requirement: Manual match search

The match search SHALL list Plex match candidates for a known item and SHALL record nothing in the history. Without typed text, it SHALL search the item title without a year, and each ID in its folder paths, or in its file paths when Plex gives no folder. With `tmdb-token` set and no TMDB ID in these paths, it SHALL also search each TMDB ID that TMDB gives for an IMDb ID, or for a TVDB ID of a show. It SHALL also return the folder IDs with links, the locations, and the files.

#### Scenario: Opening searches

- **GIVEN** a movie whose folder holds `{imdb-tt123}` and a set `tmdb-token`
- **WHEN** the user opens the match search
- **THEN** Plex gets a title search, a search for `imdb-tt123`, and a search for the TMDB ID that TMDB gives for it

#### Scenario: Folder and file with different IDs

- **GIVEN** an item whose folder holds `{imdb-tt123}` and whose file holds `{tmdb-9}`
- **WHEN** the user opens the match search
- **THEN** Plex gets no search for `tmdb-9`, and the answer still lists the folder and the file

#### Scenario: Item with only history

- **WHEN** the search translates an ID with TMDB for an item that has no cache row
- **THEN** autorefresh caches no TMDB ID, also when a run adds the cache row during the search

### Requirement: Typed match search

When the user types a search, autorefresh SHALL run only that one search. Text in an ID form, such as `imdb-tt123`, `tvdb-31207`, or `tmdb-48213`, SHALL search that ID. Other text SHALL search that title with the typed year, if one is given.

#### Scenario: Typed ID

- **WHEN** the user searches for `imdb-tt123`
- **THEN** Plex gets one ID search for `imdb-tt123` and the history gets no event

#### Scenario: Typed title and year

- **WHEN** the user searches for a title with a year
- **THEN** Plex gets one title search with that year

### Requirement: Match search failures

A failed Plex search SHALL show as failed in the result while the other searches stay. A failed TMDB request SHALL mark the result as TMDB failed and keep the Plex results. A 404 answer of Plex, an unavailable server, or the end of the action SHALL fail the whole search.

#### Scenario: TMDB fails

- **WHEN** TMDB fails while the opening searches translate a folder ID
- **THEN** the result keeps the Plex searches and says that TMDB failed

#### Scenario: Plex no longer has the item

- **WHEN** Plex answers a search with 404
- **THEN** the search fails as not found

### Requirement: Episode-number check

On request, autorefresh SHALL compare the regular episode numbers that Plex has for a known show with a TMDB series given by its numeric ID, by the rules of `fix-match`. It SHALL return whether they match, the matching episode count, the confirming episode group, the first season that differs with its missing and extra numbers, and each compared season. It SHALL write nothing to the store.

#### Scenario: Numbers match

- **WHEN** season 1 has episodes 1 and 2 in Plex and in TMDB
- **THEN** the check reports a match of 2 episodes with season 1 and both lists

#### Scenario: Numbers differ

- **WHEN** Plex has episodes 1 and 2 and TMDB has 1, 3, and 4
- **THEN** the check reports no match, with 3 and 4 missing and 2 extra in season 1

#### Scenario: Not a show or no TMDB

- **WHEN** the item is a movie, or `tmdb-token` is not set
- **THEN** autorefresh refuses the check without a request to Plex or TMDB

#### Scenario: TMDB fails

- **WHEN** a TMDB request of the check that is not an episode group request fails
- **THEN** the check fails with the TMDB error

#### Scenario: Episode group request rejected

- **WHEN** an episode group request of the check answers 401, or still answers 429 after the TMDB retries
- **THEN** the check fails with the TMDB error

#### Scenario: One episode group fails

- **WHEN** the season read of one episode group of the check answers 500
- **THEN** autorefresh logs a warning, tries the next group, and the check does not fail

#### Scenario: Episode group list fails

- **WHEN** the episode group list of the check answers 500
- **THEN** autorefresh logs a warning, the check does not fail, and it reports the result of the default seasons

#### Scenario: Episode group budget runs out

- **WHEN** the 15 second budget of the episode groups ends before a group confirms
- **THEN** the check does not fail and reports the result of the default seasons

### Requirement: Stop trying and resume

Stop SHALL mark a cached item as stopped and record a manual stopped event. Resume SHALL clear the mark and record a manual resumed event. A request that does not change the state SHALL record nothing and report no change. Runs SHALL skip a stopped item. A manual refresh or match SHALL keep the item stopped.

#### Scenario: Stop an item

- **WHEN** the user stops an item that is not stopped
- **THEN** the item is stopped, the history has a stopped event, and the action reports a change

#### Scenario: Stop a stopped item

- **WHEN** the user stops an item that is already stopped
- **THEN** autorefresh records nothing and reports no change

#### Scenario: Match of a stopped item

- **GIVEN** a stopped item
- **WHEN** a run reaches it and the user then applies a match
- **THEN** the run skips it, the match is sent and recorded, and the item stays stopped

### Requirement: Bulk stop and resume

Bulk stop and resume SHALL change the selected items of one server one after another. They SHALL report the number of changed items and each skipped item with its reason: `busy`, `not-cached`, `unchanged`, or `timeout`. When the 30 s limit of the action runs out, the remaining items SHALL be skipped with `timeout`.

#### Scenario: Mixed items

- **WHEN** the user resumes six items, of which one changes, one is locked, one is not cached, one is not stopped, and the limit runs out on the fifth
- **THEN** the answer counts one change and skips the others as `busy`, `not-cached`, `unchanged`, `timeout`, and `timeout`
- **AND** autorefresh does not try the sixth item

### Requirement: Run now

Run now SHALL start a run of all servers, or of one server, that works as a scheduled run of `refresh-runs` and records the source manual. With full set, the run SHALL read every library fully, also when its full read is not due. A run of one server SHALL NOT remove the cache or the stopped state of the other configured servers. Its history retention SHALL still remove the old events and runs of every server. The action SHALL return the run ID of each server at once.

#### Scenario: Full run

- **WHEN** the user starts Run now with full set
- **THEN** each library gets a full listing, also when its full read is not due

#### Scenario: One server

- **GIVEN** two configured servers
- **WHEN** the user starts Run now for one server
- **THEN** the other server keeps its items and stopped state

#### Scenario: Retention of one-server run

- **GIVEN** two configured servers, each with an event older than `history-retention`
- **WHEN** the user starts Run now for one server
- **THEN** the old events of both servers are removed

#### Scenario: The run cannot start

- **WHEN** recording the start of the run fails
- **THEN** the action fails and a later Run now can start

### Requirement: Background jobs

Run now and bulk refresh SHALL run as background jobs. The action SHALL answer when the job starts, not when it ends. A job SHALL keep running when the browser leaves, and SHALL have no time limit. Only cancellation or shutdown SHALL stop it early.

#### Scenario: Browser closes

- **WHEN** the user starts a bulk refresh and closes the browser
- **THEN** the job keeps working through its items

### Requirement: Bulk refresh

Bulk refresh SHALL refresh the selected items of one server in a background job with its own run record of the kind bulk refresh and the source manual. It SHALL send a plain refresh for each item in the order of the selection, record each request with the source manual, and wait `refresh-delay` between requests. It SHALL run no fix match and no check, and SHALL also refresh an item whose only reasons are ignored.

#### Scenario: Ignored reason

- **GIVEN** `no-summary` is in `ignore-reasons`
- **WHEN** the user bulk refreshes an item whose only reason is a missing summary
- **THEN** Plex gets the refresh and the item result is `accepted`

#### Scenario: Pacing

- **WHEN** a bulk refresh sends a request for an item and the next item is cached
- **THEN** the job waits `refresh-delay` before the next item

### Requirement: Bulk refresh checks each item again

Before each request, a bulk refresh SHALL wait for the item lock and read the item again. It SHALL skip the item without a request when it is no longer cached or was never cached, when it is stopped, or when its attempt count changed since the job started.

#### Scenario: Item refreshed by hand during the job

- **WHEN** the user refreshes a selected item before the job reaches it
- **THEN** the job skips that item with the result `changed`

#### Scenario: Unknown item

- **WHEN** a selected key has no cache row and no history
- **THEN** the job sends no request for it and stores the result `not-cached`

### Requirement: Result of each bulk item

A bulk refresh SHALL store one result for each selected item: `accepted`, `failed` with the error, `gone`, `stopped`, `changed`, `not-cached`, or `not-reached`. A 404 answer or another failed request SHALL not end the job. An unavailable server, a store failure, or the end of the job SHALL end it, and the items that it did not reach SHALL keep `not-reached`.

#### Scenario: Server unavailable

- **WHEN** a request of the job finds the server unavailable
- **THEN** that item is `failed`, the job ends as failed, and the later items are `not-reached`

#### Scenario: Recording fails

- **WHEN** the store cannot record a request that Plex answered
- **THEN** that item is `failed` with the error `recording failed` and the job ends as failed

### Requirement: Cancel a job

The user SHALL be able to cancel the running Run now or bulk refresh by any of its run IDs. The job SHALL stop and its run SHALL end as canceled. A request that is in flight SHALL still be recorded. Cancel SHALL refuse a scheduled run and a job that is not running as no such job.

#### Scenario: Cancel a bulk refresh

- **GIVEN** a bulk refresh of two items whose first request waits for Plex
- **WHEN** the user cancels it
- **THEN** the run ends as canceled, the first item is `failed` with its attempt recorded, and the second is `not-reached`

#### Scenario: Cancel a scheduled run

- **WHEN** the user cancels the ID of a scheduled run
- **THEN** autorefresh refuses it as no such job and the run goes on

### Requirement: One run at a time

Scheduled runs, Run now, and bulk refresh SHALL share one run lock, so only one of them works at a time. Run now or bulk refresh SHALL be refused as "a run is busy" while one of them works, and SHALL then write no run record. A scheduled start SHALL be skipped with a warning while one of them works.

#### Scenario: Run now during a bulk refresh

- **WHEN** the user starts Run now while a bulk refresh works
- **THEN** autorefresh refuses it as "a run is busy"

#### Scenario: Scheduled start during a manual job

- **WHEN** the schedule fires while a scheduled run, a manual scan, or a bulk refresh still works
- **THEN** autorefresh starts no second run and logs `Skipped scheduled start, a run is busy` at warn level

### Requirement: Item locks

Each item of a server SHALL have one lock, which runs, bulk refreshes, and the item actions refresh, apply match, stop, and resume share. Autorefresh SHALL hold it from the start of an item action to the end of its checks. An item action SHALL be refused as item busy when the item is locked. A run SHALL wait for the lock, read the item again, and skip it when it is gone, stopped, or has a new attempt.

#### Scenario: Manual action while a run handles the item

- **WHEN** the user refreshes an item whose lock a run or another action holds
- **THEN** autorefresh refuses the action as item busy and sends nothing

#### Scenario: Manual refresh in the same second as the batch

- **GIVEN** a run loaded an item into its batch
- **WHEN** the user refreshes the item before the run reaches it, also in the same second
- **THEN** the run skips the item and adds no attempt

#### Scenario: Read-only actions

- **WHEN** the user runs a match search, a preview, or an episode check
- **THEN** autorefresh takes no item lock, so a run or another action on the item goes on

### Requirement: A library read keeps a manual result

When Refresh now completes an item or finds it gone while a run reads the library of the item after it listed the item, the read SHALL NOT add the item back, and the run SHALL send no request for it. A later read that lists the item as incomplete SHALL add it again.

#### Scenario: Refresh now during a read

- **WHEN** Refresh now completes an item after the run listed it as incomplete
- **THEN** the run leaves the cache row out and sends no request for the item

### Requirement: Time limits of item actions

Refresh now SHALL have 90 s in all, with 45 s for its fix match search. The match search, the preview, and the episode check SHALL have 45 s each. Apply match, stop, resume, and bulk stop and resume SHALL have 30 s. An action that runs out of time SHALL fail as timed out, except in the cases of the next requirement. When the browser leaves, the action SHALL end. A request that Plex accepted SHALL still be recorded.

#### Scenario: Request ends after Plex accepted the refresh

- **WHEN** the dashboard is forced to close the connection after Plex accepted the refresh but before it was recorded
- **THEN** autorefresh still records the accepted manual request

### Requirement: Time limits after the work started

Refresh now and Apply match SHALL succeed when their time ends after Plex accepted their refresh or match, also during the follow-up refresh or checks of a match. Bulk stop and resume SHALL succeed when their time ends after they started on the items, and SHALL skip the remaining items with `timeout`. When their time ends while they still read the matching items, they SHALL fail as timed out.

#### Scenario: Time ends during the checks of Refresh now

- **WHEN** Plex accepted the refresh of a cached item and the 90 s end during the checks after it
- **THEN** the action succeeds and reports the refresh

#### Scenario: Time ends after an accepted match

- **WHEN** Plex accepted the match of Apply match, or the automatic match of Refresh now, and the time ends during the follow-up refresh or checks
- **THEN** the action succeeds, and Apply match answers `200` with `{"accepted":true}`

#### Scenario: Time ends during bulk stop

- **WHEN** the 30 s of a bulk stop end after it changed some items
- **THEN** the action succeeds and skips the remaining items with `timeout`

#### Scenario: Time ends while bulk stop finds its items

- **WHEN** the 30 s of a bulk stop of all matching items end before it has read the matching items
- **THEN** the action fails as timed out

### Requirement: Shutdown drains manual work

On SIGINT or SIGTERM, autorefresh SHALL stop the dashboard first, then cancel the scheduled run and wait for it. It SHALL then refuse new jobs and cancels as shutting down, cancel the running job, and wait for it. Then it SHALL refuse new item actions as shutting down and wait for the admitted ones before it closes the store.

#### Scenario: Shutdown during a manual refresh

- **GIVEN** a manual refresh that Plex accepted and that is not recorded yet
- **WHEN** autorefresh shuts down
- **THEN** a new item action is refused as shutting down
- **AND** the store closes only after the accepted request is recorded
