# dashboard-ui Specification

## Purpose

Define what a user sees and does in the autorefresh web dashboard: its pages, its live status, its item and run views, and the dialogs that act on items, so that the frontend behaves the same after each change.

## Requirements

### Requirement: Hash routes under any path

The dashboard SHALL keep its page in the URL hash: `#/status`, `#/items/<tab>`, `#/items/<tab>/<server>/<key>`, `#/runs`, and `#/runs/<id>`. Every API request and the event stream SHALL use a URL relative to the page, so the dashboard works under any reverse proxy sub-path without configuration.

#### Scenario: Unknown hash

- **WHEN** the hash names no known page, or is empty
- **THEN** the dashboard shows the Status page

#### Scenario: Items without a tab

- **WHEN** the hash is `#/items`
- **THEN** the dashboard shows the Attention tab

#### Scenario: Malformed run or item

- **WHEN** the hash is `#/runs/0`, names an item key that is not a number, or names a tab that does not exist
- **THEN** the dashboard shows the Status page

#### Scenario: Served at a sub-path

- **GIVEN** a reverse proxy serves the dashboard at `/autorefresh/`
- **WHEN** the page loads
- **THEN** it reads `./api/...` and `./api/events` under `/autorefresh/`, and every link of the page stays under that path

### Requirement: Server scope in every link

With more than one server, the top bar SHALL show a server picker with "All servers" and each server name. A choice SHALL add `?srv=<n>` to the hash, every page SHALL show only that server, and every link of the dashboard SHALL keep the scope. With one server, the dashboard SHALL show no picker and ignore the scope.

#### Scenario: Pick a server

- **GIVEN** two servers
- **WHEN** the user picks the second server in the picker
- **THEN** the hash gets `?srv=1`, and the Status, Items, and Runs links keep it

#### Scenario: Scope that does not exist

- **WHEN** `?srv=` names a server that the install does not have, or is not a number
- **THEN** the dashboard shows every server

#### Scenario: Deep link before the servers load

- **WHEN** a link with `?srv=` opens before the dashboard knows the servers
- **THEN** the page shows a loading placeholder and keeps the scope until the servers confirm it

#### Scenario: Scope change with an open item or run

- **WHEN** the user picks another server while an item of a different server or a run is open
- **THEN** the dashboard closes that item or run and keeps the current tab or page

### Requirement: Top bar

The top bar SHALL link to the Status, Items, and Runs pages and mark the current page. While a run is busy, it SHALL show the running server, or the number of running servers, with the progress as done of selected items, or "reading" before the batch.

#### Scenario: One server runs

- **WHEN** the server Home runs and has refreshed 3 of 10 selected items
- **THEN** the top bar shows a pulsing dot and "Home 3 / 10"

#### Scenario: Narrow screen with several servers

- **GIVEN** several servers and a narrow screen
- **WHEN** a run is busy
- **THEN** the top bar leaves out the run progress, so the server picker and Run now keep their room

### Requirement: Run now

The top bar SHALL offer Run now without a confirmation. With one server, it SHALL be a button with a menu for "Full cache refresh, then run". With several servers, it SHALL be a menu with "All servers", each server, and a "Full cache refresh, then run" submenu with the same choices. It SHALL be disabled while any run is busy or a request is pending.

#### Scenario: Start a run

- **WHEN** the user chooses Run now and the service accepts it
- **THEN** a toast says "Run started", or "Full cache refresh started" for a full refresh

#### Scenario: A run is busy

- **WHEN** a run is busy
- **THEN** Run now is disabled with the tooltip "A run is busy"
- **AND** a request that the service refuses as busy shows the toast "A run is busy. Try again when it ends."

### Requirement: Live status stream

The dashboard SHALL read the live status from the event stream and update every page without a reload. It SHALL show a warning banner while the stream is down, and SHALL try to connect again every 3 seconds when the browser gives up. When a run ends, and on each connect, it SHALL reload the item lists and counts. When a run starts, it SHALL reload the run lists.

#### Scenario: Stream drops after it was open

- **WHEN** the open stream fails
- **THEN** the banner "Reconnecting to autorefresh" says that the live status is paused until the connection returns

#### Scenario: Stream never opened

- **WHEN** the first connection fails
- **THEN** the banner "Cannot reach the live status" says that autorefresh tries again every few seconds

#### Scenario: Run ends while a list is open

- **WHEN** the stream reports a new last run of a server
- **THEN** the item lists, the tab counts, and the runs reload

### Requirement: Order of status snapshots

Each status snapshot SHALL carry the ID of the service process (`boot`) and its change count (`seq`). The dashboard SHALL keep the newest snapshot of a process, SHALL take a snapshot of a new process whatever its count, and SHALL ignore every later snapshot of a process that a newer one replaced.

#### Scenario: Older snapshot of the same process

- **GIVEN** the dashboard shows the snapshot with `seq` 5
- **WHEN** a snapshot of the same process with `seq` 4 arrives
- **THEN** the dashboard keeps the snapshot with `seq` 5

#### Scenario: Service restart

- **WHEN** a snapshot of a new process arrives with a lower `seq`
- **THEN** the dashboard shows the new snapshot

#### Scenario: Late snapshot of the old process

- **GIVEN** a restart replaced the old process
- **WHEN** a snapshot of the old process arrives late, from the stream or a reload
- **THEN** the dashboard ignores it

#### Scenario: Restart during a status reload

- **WHEN** the stream switches to a new process while a status reload is in flight, and the reload answers for another process
- **THEN** the dashboard ignores that answer

### Requirement: Next scheduled run

The Status and Runs page headers SHALL show when the next scheduled run starts, as "Next run in <time> · <clock time>", from the live status. They SHALL show "starting" once that time has come, and nothing without a schedule. The Status header SHALL also show the TMDB health while a TMDB token is set.

#### Scenario: Run in 45 minutes

- **WHEN** the next run starts in 45 minutes at 23:00
- **THEN** the header shows "Next run in 45m · 23:00"

#### Scenario: Time has come

- **WHEN** the next start time is now or in the past
- **THEN** the header shows "Next run starting"

#### Scenario: TMDB health

- **WHEN** TMDB rejects the token
- **THEN** the Status header shows a red dot and "TMDB · TMDB rejected the token"
- **AND** without a TMDB token, the header shows no TMDB state

### Requirement: Server cards

The Status page SHALL show one card for each server in scope. Each card SHALL show the Plex health, the name, "Idle" or the run state, the last run, when the next full cache refresh is due, and the counts of items that need attention, were fixed recently, and are stopped. The attention count SHALL link to the Attention tab of that server.

#### Scenario: Token rejected

- **WHEN** Plex rejects the token of a server
- **THEN** its card shows a red dot and the banner "Plex rejected the token"

#### Scenario: Server unreachable

- **WHEN** Plex is unreachable
- **THEN** the card shows the banner "Plex is unreachable" and says that autorefresh tries again on the next run

#### Scenario: Idle server

- **WHEN** the server has no busy run
- **THEN** the card shows the status, age, and counts of its last run, or says that no run happened since autorefresh started

#### Scenario: First scan pending

- **WHEN** autorefresh has not yet read the server
- **THEN** the card says "Waiting for the first scan of this server" in place of the counts

#### Scenario: Run now on a card

- **GIVEN** several servers
- **WHEN** a server is idle
- **THEN** its card offers Run now for that server, disabled while any run is busy

#### Scenario: Cancel a run started by hand

- **WHEN** a manual run or a bulk refresh works on the server
- **THEN** its card offers Cancel, and a click shows the toast "Canceling at the next item"

#### Scenario: Busy server progress

- **WHEN** a run works on the server
- **THEN** the card shows a progress bar of done over selected items and the counts refreshed and matched, and failed and skipped when not zero
- **AND** a scheduled or manual run also shows the selected of due items and the libraries read

#### Scenario: Reading libraries

- **WHEN** a run reads libraries before it has selected items
- **THEN** the progress bar moves without a value and the card shows "Reading libraries", or "Read <library>" after a library

#### Scenario: Refreshing items

- **WHEN** the run has selected items
- **THEN** the card shows the now panel under the progress bar

### Requirement: Now panel

While a run works on its items, the now panel SHALL show the item in work as "Working", or the next item as "Up next" with a countdown to its start, each with a poster, the title, the year, the library, and, for a scheduled or manual run, the reasons. Below it, the panel SHALL show the last finished item with an outcome icon, the title, the year, the library, the outcome, and how long ago it finished, without a poster or reasons.

#### Scenario: Before the first item

- **WHEN** the run has no item in work, no next item, and no finished item
- **THEN** the panel shows nothing

#### Scenario: Pace wait

- **WHEN** the run waits 4 seconds before the next item
- **THEN** the panel shows "Up next" with "in 4s", counting down each second from the time of the status event, and "now" once the time has passed, never a negative number

#### Scenario: Last finished item

- **WHEN** an item finished 2 seconds ago with a match
- **THEN** the footer shows "Last <title>", a purple swap icon with "matched", and "2s ago"
- **AND** a refresh shows a green check with "refreshed", a failure a red cross with "failed", and a skip a grey dash with "skipped"

#### Scenario: End of the batch

- **WHEN** no item is in work or waits, but an item finished
- **THEN** the panel shows only the last finished item

### Requirement: Recently fixed and recent runs

The Status page SHALL list the 6 newest fixed items and the 6 newest runs of the scope, with links to all fixed items and all runs. A fixed item SHALL show its poster, title, year, server (with several servers), library, what fixed it, and when. A run SHALL show its server (with several servers), its source or kind, its counts or error, its status, and its start.

#### Scenario: Nothing yet

- **WHEN** no item was fixed or no run happened
- **THEN** the list shows "Nothing fixed yet" or "No runs yet"

#### Scenario: Open a fixed item

- **WHEN** the user clicks a fixed item
- **THEN** the Items page opens that item on the Fixed tab

### Requirement: List and table modes

The Items page SHALL offer a List and a Table mode, and SHALL remember the choice in the browser. A list row SHALL link to the item and show a poster, the title, the year, the server (with several servers), the library, the queue state, and the reason badges. Table mode SHALL show the same facts in columns for each tab and open a row on click, Enter, or Space.

#### Scenario: Reason badges

- **WHEN** an item has reasons
- **THEN** each reason shows as a coloured badge: unmatched red, no poster and no art orange, no summary blue, episode reasons purple
- **AND** an ignored reason is muted and marked "(ignored)"

#### Scenario: Row state per tab

- **WHEN** the list shows the Attention tab
- **THEN** each row shows its queue state, such as "Due" or "Waiting · in 3h", and its number of tries when it has at least one
- **AND** a Fixed row shows what fixed it and when, and a Stopped row shows when it was stopped

#### Scenario: Wide screen

- **GIVEN** a screen at least 1000 pixels wide
- **WHEN** the user opens an item
- **THEN** list mode shows the details in a resizable pane beside the list, and table mode shows them in a drawer

#### Scenario: Missing poster

- **WHEN** an item has no poster or its poster fails to load
- **THEN** the row shows a film or TV icon in its place

### Requirement: Search and filters

The Items page SHALL filter by a search of the title or path, by library, by reason, and by type. The library filter SHALL show only when the scope is one server with more than one library. The reason filter SHALL not show on the Fixed tab. A switch SHALL include items with only ignored reasons on Attention, and items that Plex completed alone on Fixed; Stopped has no switch.

#### Scenario: Search too long

- **WHEN** the trimmed search is longer than 200 UTF-8 bytes
- **THEN** the field shows an error, and the list keeps the last search that it accepted

#### Scenario: Switch per tab

- **WHEN** the user turns on "Include ignored reasons" on Attention and then opens Fixed
- **THEN** the Fixed switch "Include fixed by Plex alone" stays off

### Requirement: Sort

Each tab SHALL keep its own sort, also across modes. List mode SHALL offer a sort control with "Default order" and the keys of the tab, and a direction toggle. Table mode SHALL sort by the same keys through the column headers. Attention sorts by item, tries, last action, and next due; Stopped by item, tries, and stopped; Fixed by item and completed.

#### Scenario: New sort key

- **WHEN** the user picks Tries in the sort control
- **THEN** the list shows the most tries first

#### Scenario: Toggle direction

- **WHEN** the user clicks the direction toggle
- **THEN** the list reverses its order and keeps the sort key

### Requirement: Item tabs, paging, and empty states

The Items page SHALL show the tabs Attention, Fixed, and Stopped, each with the count of its items, and a tab change SHALL keep the server scope. It SHALL load items in pages and show "Showing <n> of <total>" with a Load more button while more items exist. When a list is empty, it SHALL say why.

#### Scenario: Change tab

- **WHEN** the user picks the Stopped tab
- **THEN** the hash becomes `#/items/stopped` with the current scope, and the list shows the stopped items

#### Scenario: Before the first scan

- **WHEN** no server in scope has been read yet
- **THEN** the page shows "Waiting for the first scan"

#### Scenario: Filters match nothing

- **WHEN** a search or a filter is set and no item matches
- **THEN** the page shows "No items match"

#### Scenario: Empty tab

- **WHEN** a tab has no item without filters
- **THEN** Attention shows "Nothing needs attention", Fixed "Nothing fixed yet", and Stopped "No stopped items"

### Requirement: Multi-select

In table mode, the Attention and Stopped tabs SHALL let the user select rows. While rows are selected, a band SHALL replace the tabs with the number of selected items, the bulk actions of the tab, and Clear selection. The header checkbox SHALL select every item that matches the filters, also items on pages that are not loaded. The Fixed tab SHALL have no selection.

#### Scenario: Select all

- **WHEN** the user checks the header checkbox on a tab with 1,200 matching items
- **THEN** the band shows "All 1,200 matching items selected", and the rows stay checked until the selection is cleared

#### Scenario: Filter change

- **WHEN** the user changes the tab or a filter while rows are selected
- **THEN** the selection no longer shows
- **AND** a change of the sort keeps it

#### Scenario: Bulk actions of each tab

- **WHEN** rows are selected
- **THEN** Attention offers Refresh and Stop trying, and Stopped offers Resume

### Requirement: Bulk Stop trying and Resume

Bulk Stop trying SHALL ask for a confirmation that names the number of items and says that they move to Stopped. Resume SHALL run without a confirmation. Each action SHALL send one request for each server of the selection and report every server in one toast.

#### Scenario: Stop succeeds

- **WHEN** the user confirms Stop trying for 12 items and 2 are skipped
- **THEN** a notice toast says "Stopped 10 items · 2 skipped"

#### Scenario: No matching items

- **WHEN** no server has an item that matches the filters
- **THEN** a notice toast says "No items match the filters."

#### Scenario: One server fails

- **WHEN** one server fails and another stops items
- **THEN** an error toast names the stopped items and the server that failed

#### Scenario: Every server fails

- **WHEN** every server fails or has no matching item, and at least one failed
- **THEN** an error toast names the failures and the selection stays for another try

### Requirement: Bulk refresh start

Bulk Refresh SHALL ask for a confirmation that names the number of refresh requests, says that they run one at a time with the configured refresh delay, and, for several servers, that the servers take turns. After the first server accepts its job, a toast SHALL name the item count and, for several servers, the first server and the servers that follow, and SHALL point to the Status page.

#### Scenario: Two servers

- **WHEN** the user confirms a refresh of items on Home and Cabin and Home accepts 40 items
- **THEN** the toast says "Bulk refresh started for 40 items on Home. Then Cabin, one server at a time. Progress shows on the Status page."

#### Scenario: Refresh already busy

- **WHEN** a bulk refresh is starting or running
- **THEN** Refresh is disabled with the tooltip "A bulk refresh is running. Wait until it ends."
- **AND** a second start that slips through fails

#### Scenario: First server refuses

- **WHEN** the first server with matching items refuses the job
- **THEN** an error toast shows and no server starts

### Requirement: Bulk refresh queue

A bulk refresh of several servers SHALL run as one job for each server, each started after the previous job ends, also when the user leaves the Items page. With two or more servers, the Status page SHALL show the state of each. The queue SHALL learn each end from the live status, or else from the stored runs, check at least every 15 seconds, and end a job as unknown after 4 checks in a row that find it neither running nor ended.

#### Scenario: Server without matching items

- **WHEN** a server answers that it has no matching items
- **THEN** the queue marks it "no matching items" and starts the next server

#### Scenario: Server refuses its job

- **WHEN** a later server refuses its job for another reason
- **THEN** the queue marks it not started with the reason and starts the next server

#### Scenario: Run lock still held

- **WHEN** the next server answers that a run is busy
- **THEN** the queue tries again every 2 seconds, up to 10 times, and then marks that server and the rest "not started: another run is busy"

#### Scenario: Job interrupted or unknown

- **WHEN** a job ends interrupted or with an unknown result
- **THEN** the servers after it do not start

#### Scenario: End from the live status

- **WHEN** the live status reports the job as ended
- **THEN** the queue starts the next server

#### Scenario: Service restarted

- **WHEN** the service restarts during a job and the stored runs show the job as finished
- **THEN** the queue marks the job finished and goes on

#### Scenario: Nothing knows the job

- **WHEN** neither the status nor the stored runs know the job, or both are unavailable, for 4 checks in a row
- **THEN** the queue ends the job as "result unknown"

#### Scenario: Job still running

- **WHEN** the status reports the job as running
- **THEN** the queue keeps waiting, also when the stored runs are unavailable

### Requirement: Bulk refresh cancel

The queue banner SHALL offer "Cancel the rest", which stops the running job at its next item and starts no waiting server. A Cancel on the server card of a queued job SHALL also stop the rest of the queue. A cancel that fails SHALL show on the banner and keep the queue canceled.

#### Scenario: Cancel the rest

- **WHEN** the user clicks "Cancel the rest" while the first of three servers runs
- **THEN** the running job stops at its next item and the other servers are marked not started: canceled

#### Scenario: Job ended before the cancel

- **WHEN** the user cancels a queued job on its card after the job ended
- **THEN** a notice toast says that the job had already ended and the other servers do not start

#### Scenario: Cancel fails

- **WHEN** the cancel request fails
- **THEN** the banner shows the error and says to cancel the running job on its server card

### Requirement: Bulk refresh end report

When a queue ends, the dashboard SHALL show a toast for 10 seconds with the result of each server: its state and its counts of accepted, failed, and skipped items, or that the results are unavailable. The toast SHALL be a success when Plex accepted every refresh, an error when a job failed, was interrupted, ended unknown, or did not start for a reason other than a cancel, or when Plex refused every refresh, and a notice otherwise.

#### Scenario: All accepted

- **WHEN** one server finished and Plex accepted all 40 refreshes
- **THEN** a success toast says "Bulk refresh finished: 40 accepted"

#### Scenario: Results unavailable

- **WHEN** a job finished but its item results cannot be read
- **THEN** the toast says "finished, results unavailable" and is a notice

### Requirement: Item detail

The item detail SHALL show the item art faded behind its head, the poster, the title, the type, the year, the Plex season and episode counts, the library, the reasons, and the live Plex summary and path. It SHALL list the queue state, stop time, fix, removal, attempts, last action, and last accepted match. Fixed and Removed SHALL come from the newest completed or removed event. With a known server identity, it SHALL offer "Open in Plex" in a new tab.

#### Scenario: No summary

- **WHEN** Plex has no summary for the item
- **THEN** the detail says "Plex has no summary for this item."

#### Scenario: Plex no longer has the item

- **WHEN** the live Plex view answers that the item is gone
- **THEN** a warning says "Plex no longer has this item" and the cached details stay

#### Scenario: Live view fails

- **WHEN** the live Plex view fails for another reason
- **THEN** a banner says "Live details are unavailable" and the cached details show

#### Scenario: Item no longer known

- **WHEN** autorefresh no longer knows the item
- **THEN** the detail shows "This item is no longer known" and no old details

#### Scenario: Detail fails

- **WHEN** the detail cannot be read for another reason
- **THEN** an error banner offers Try again

#### Scenario: Fixed item

- **WHEN** the newest completed event says that a match fixed the item
- **THEN** the detail shows "Fixed" with "match" and how long ago

#### Scenario: Removal of a tracked item

- **WHEN** the newest such event is a removal but autorefresh tracks the item again
- **THEN** the detail shows no Removed fact

#### Scenario: Last match

- **WHEN** Plex accepted a match that the user chose
- **THEN** the Match fact shows the match title and year with "matched by hand" and its date

#### Scenario: Open in Plex

- **WHEN** the machine identifier of the item's server is known
- **THEN** "Open in Plex" opens the item on app.plex.tv in a new tab
- **AND** without that identifier, the detail shows no such link

### Requirement: Refresh now

The item detail SHALL offer Refresh now without a confirmation; `manual-actions` gives the server rules. Its toast SHALL name the applied match, with its year when known, or the accepted refresh. When the answer has the item detail, the dashboard SHALL show it at once, and the toast SHALL add what the check found: complete, still missing reasons, or gone. For a plain refresh of a gone item, the toast SHALL be only the removal notice.

#### Scenario: Item complete

- **WHEN** the check after the refresh finds the item complete
- **THEN** a success toast says "Plex accepted the refresh of <title>. It is complete."

#### Scenario: Still missing

- **WHEN** the check finds reasons that remain
- **THEN** a notice toast says "Still missing:" with the reasons

#### Scenario: Match applied

- **WHEN** the refresh applied a fix match
- **THEN** the toast starts with "Matched <title> to <match>", followed by " (<year>)" when the year is known

#### Scenario: Answer without the item detail

- **WHEN** Plex accepted the refresh but the answer has no item detail
- **THEN** the dashboard keeps the detail that it shows and reads it again
- **AND** a success toast says "Plex accepted the refresh of <title>", or "Matched <title> to <match>" after a fix match, with the year when known, and no check result

#### Scenario: Item gone after the refresh

- **WHEN** the refresh succeeds and the answer reports that Plex no longer has the item
- **THEN** a notice toast says that Plex no longer has it and autorefresh removed it from the queue

#### Scenario: Plex answers the refresh with 404

- **WHEN** the refresh fails because Plex no longer has the item
- **THEN** an error toast says "Plex no longer has this item. It was deleted or moved."

### Requirement: Stop trying and Resume an item

A tracked item SHALL offer Stop trying, or Resume when it is stopped. Stop trying SHALL ask for a confirmation that names the item and says that it moves to Stopped. Resume SHALL run without a confirmation. An item that autorefresh no longer tracks SHALL offer neither and SHALL say that the next read adds it back if it is still incomplete.

#### Scenario: Stop an item

- **WHEN** the user confirms Stop trying
- **THEN** a toast says "Stopped trying <title>"

#### Scenario: Resume an item

- **WHEN** the user clicks Resume
- **THEN** a toast says "<title> is back in the queue"

#### Scenario: Another action runs

- **WHEN** one item action is pending
- **THEN** the other item actions of the detail are disabled

### Requirement: Item history

The item detail SHALL show the history of the item, newest event first. Each event SHALL show a coloured marker, a title, who made it (by hand or by autorefresh), its relative and absolute time, and any detail such as an error. A fix match decision SHALL open its searches with their candidates and verdicts on request.

#### Scenario: Empty history

- **WHEN** the item has no events
- **THEN** the history says "Nothing happened to this item yet."

#### Scenario: Removal found by a request

- **WHEN** a refresh request found the item gone
- **THEN** the history shows "Removed: Plex no longer had it" with "Its refresh request found it gone"

### Requirement: Fix match dialog

The item detail SHALL offer Fix match, which opens a dialog titled "Fix match · <title> (<year>)" with the library and the Plex counts. On open, the dialog SHALL run the opening search and show the folder IDs with links to their provider pages and the files of the item. Closing the dialog SHALL abort its searches and forget their answers.

#### Scenario: No folder ID

- **WHEN** the item path has no folder ID
- **THEN** the dialog says to add one, such as `{tmdb-12345}`, to the folder name

#### Scenario: Search fails

- **WHEN** the opening search request fails
- **THEN** a banner names the failure and offers Retry, or says that Plex no longer has the item when Plex answers so

#### Scenario: Some searches fail

- **WHEN** Plex answers some searches with an error, or the TMDB step fails
- **THEN** a warning names them, and the other candidates show

#### Scenario: Narrow screen

- **WHEN** the screen is narrow
- **THEN** the dialog fills the screen and the automatic decision panel folds under the candidates

### Requirement: Candidate cards

The dialog SHALL group the candidates of every search by Plex GUID and fill missing fields from other searches. It SHALL rank candidates of the typed search first, then those found by more folder IDs, then the higher Plex score. Each card SHALL show the poster, name, type, year, summary, GUID, Plex score, and evidence such as the folder ID that found it, the year check, and the searches that found it.

#### Scenario: Year check

- **WHEN** the candidate year equals the item year, differs by one, or differs by more
- **THEN** the card shows a green "year <y>", a yellow "year off by 1", or a red "year differs by <n>"

#### Scenario: Candidate without a GUID

- **WHEN** Plex gives a candidate without a GUID
- **THEN** it gets its own card and its Match button is disabled

#### Scenario: Current match

- **WHEN** the candidate is the current match of the item in Plex
- **THEN** the card shows "currently matched" and its Match button is disabled

#### Scenario: No candidates

- **WHEN** a search answered and found no candidate
- **THEN** the dialog shows "No candidates" and suggests another title or an ID such as tmdb-48213

### Requirement: Series candidate facts and TMDB ties

For a series item, the dialog SHALL ask the Plex metadata provider once while the page is open about each series candidate with a `plex://show/` GUID, and SHALL show its season and episode counts. A candidate SHALL tie to a TMDB ID only when a search by that ID found it as the only candidate and no search by that ID found another. A tie SHALL win over the TMDB ID of the provider.

#### Scenario: Episode count check

- **WHEN** the provider gives the candidate 62 episodes and Plex has 62 for the item
- **THEN** the card shows a green "62 episodes, same as yours"

#### Scenario: Searches of one ID disagree

- **WHEN** two searches of tmdb-48213 found different candidates
- **THEN** no candidate ties to that ID

#### Scenario: Provider fails

- **WHEN** the provider lookup fails
- **THEN** the card adds no provider facts, and reopening the dialog does not ask again

### Requirement: Typed match search

The dialog SHALL offer a search by title with an optional year, or by an ID such as tmdb-48213, tvdb-31207, or imdb-tt7712040. Enter or Search SHALL send it. The candidates of the typed search SHALL join those of the opening search. The same search again SHALL retry it, and Clear search SHALL drop it.

#### Scenario: Empty search

- **WHEN** the user searches without text
- **THEN** the dialog says "Enter a title or an ID." and sends nothing

#### Scenario: Bad year

- **WHEN** the year is not a number from 1 to 9999
- **THEN** the dialog says "The year must be a number from 1 to 9999."

#### Scenario: ID search with a year

- **WHEN** the user searches tmdb-48213 with a year
- **THEN** the dialog ignores the year

#### Scenario: Search too long

- **WHEN** the trimmed text is longer than 200 UTF-8 bytes
- **THEN** the dialog says "Use at most 200 bytes."

### Requirement: Automatic fix match panel and preview

The dialog SHALL explain the newer of the last automatic fix match and the preview: its searches, its candidates with verdicts, and why it picked one or none, and SHALL mark its findings on the candidate cards. Without a last decision, it SHALL offer "Try automatic fix match", which runs the preview on request only. The preview SHALL follow the decision rules of a run, as `fix-match` describes, send no match or refresh request, and write nothing to the store.

#### Scenario: Preview runs

- **WHEN** the user clicks "Try automatic fix match"
- **THEN** the panel shows the preview with "Run again", and its pick gets a card

#### Scenario: Dialog reopens

- **WHEN** the user closes and reopens the dialog for the same item
- **THEN** the panel shows the last preview again without running it

#### Scenario: Service restarted

- **WHEN** the service restarted since the preview
- **THEN** the dialog forgets the preview

#### Scenario: Status not known yet

- **WHEN** the live status has not named the service process
- **THEN** the preview button is disabled with "Waiting for the status of autorefresh"

### Requirement: Apply match confirmation

A click on Match SHALL ask "Apply this match?" with the item title and the candidate title. The dialog SHALL freeze the item and the candidate at the click, so the confirmation and the request use the same values while the searches change. After Plex accepts, a toast SHALL say so and the dialog SHALL close.

#### Scenario: Match accepted

- **WHEN** the user confirms Apply match
- **THEN** a toast says "Plex accepted the match of <item> to <candidate>. Plex now refreshes its metadata."

#### Scenario: Search ends during the confirmation

- **WHEN** a search adds or reorders candidates while the confirmation is open
- **THEN** the confirmation and the request still name the candidate that the user clicked

### Requirement: Compare episodes

A series candidate with a TMDB ID SHALL offer Compare episodes while TMDB is on. It SHALL apply nothing. Its result SHALL say whether the episode numbers confirm the candidate or where they differ. When the result has compared seasons, it SHALL open, on request, each season with its Plex and TMDB counts and each episode number marked "In both", "Only TMDB", or "Only Plex", each with its own colour and icon.

#### Scenario: Episodes confirm

- **WHEN** the TMDB seasons confirm the candidate
- **THEN** the result says that the episode numbers of the TMDB seasons of tmdb-<id> confirm it, with the number of episodes compared

#### Scenario: Episode group confirms

- **WHEN** a TMDB episode group confirms it
- **THEN** the result names that group, and the season comparison lists the groups that did not confirm it

#### Scenario: Duplicate episode

- **WHEN** Plex lists an episode number twice
- **THEN** the season shows the number once and says that Plex lists it more than once

#### Scenario: Check stopped early

- **WHEN** a season that differs stopped the check
- **THEN** the result says at which season it stopped and that it lists no later season

### Requirement: Runs page

The Runs page SHALL list the runs of the scope, newest first, 50 at a time, with Load more. A wide screen SHALL show a table with start, duration, server (with several servers), source, kind, status, the read, refreshed, matched, failed, fixed, and due counts, and the error. A narrow screen SHALL show a list with every count.

#### Scenario: No runs

- **WHEN** no run is stored
- **THEN** the page shows "No runs yet"

#### Scenario: Runs fail to load

- **WHEN** the runs cannot be read
- **THEN** an error banner offers Try again

#### Scenario: Open a run

- **WHEN** the user clicks a run, or presses Enter or Space on its row
- **THEN** the hash becomes `#/runs/<id>` and the run details open

### Requirement: Run details

The run details SHALL open in a drawer on a wide screen and replace the list on a narrow screen, and closing SHALL return to `#/runs`. They SHALL show the run title, status, start, duration, server (with several servers), libraries read, error, and counts. They SHALL group the items by outcome in a fixed order: In progress, Refreshed, Matched, Failed, Fixed, Completed by Plex, Removed, Not sent; a bulk refresh: Refreshed, Failed, Removed, Skipped, Not reached.

#### Scenario: Running run

- **WHEN** the run is still running
- **THEN** the details say "The counts show when the run ends." and reload every 5 seconds, except for a bulk refresh

#### Scenario: Run removed

- **WHEN** the run no longer exists
- **THEN** the details show "This run no longer exists" and stop reloading

#### Scenario: Open an item of the run

- **WHEN** the user clicks an item of the run
- **THEN** the item opens on the Items page, on Fixed for a fixed item and on Stopped for a bulk item stopped before its turn

#### Scenario: Decision and its request

- **WHEN** a fix match decision of an item is followed by a request or a removal of that item
- **THEN** the details show one entry for both, under the group of the request or removal
- **AND** empty groups do not show

#### Scenario: Decision without a request

- **WHEN** a decision of an ended run did not fail and has no request or removal after it
- **THEN** it shows under Not sent with "The run ended before the request"

#### Scenario: Failed decision without a request

- **WHEN** a failed decision has no request or removal after it
- **THEN** it shows under Failed, also when the run has ended

#### Scenario: No items listed

- **WHEN** the run lists no item
- **THEN** the details say why: the run is still at work, it is from before item details were recorded, its history expired, or it changed no item

### Requirement: Run count filters

A click on a run count that lists items, such as Refreshes sent, Matches sent, Failed, Fixed, or Skipped, SHALL show only the listed items that it counts. A second click on the same count SHALL show every item again. A count that lists no item SHALL not be clickable.

#### Scenario: Filter by failed

- **WHEN** the user clicks Failed
- **THEN** only the failed items show, also an accepted refresh after a failed fix match decision, with a note to click again to show every item

#### Scenario: Requests that found the item gone

- **WHEN** the user clicks Refreshes sent and a refresh found its item gone
- **THEN** that item shows too, under Removed

#### Scenario: Fewer items than the count

- **WHEN** the history does not list every counted request
- **THEN** the filter shows the listed items, which can be fewer than the count

### Requirement: Action toasts

Every action result SHALL show as a toast with an icon of its outcome: a check for a success, an information icon for a notice, and an exclamation icon with error colours for an error. A failed item action SHALL explain common failures in plain words.

#### Scenario: Item busy

- **WHEN** an item action fails because another request holds the item
- **THEN** the toast says "This item is busy with another request. Try again in a moment."

#### Scenario: Plex timeout

- **WHEN** Plex does not answer in time
- **THEN** the toast says "Plex did not answer in time. The request may still have reached Plex."

#### Scenario: Item gone from Plex

- **WHEN** an item action fails with `404` `not found in Plex`
- **THEN** the toast says "Plex no longer has this item. It was deleted or moved."

### Requirement: Phone layout

On the Items page below 1000 pixels wide, the page SHALL keep the tabs, filters, and posters in one column, an open item SHALL replace the list and its header, and a close button SHALL return to the list. On a narrow screen, the Fix match dialog SHALL fill the screen and the run details SHALL replace the run list.

#### Scenario: Open an item on a phone

- **WHEN** the user opens an item on a phone
- **THEN** the item detail fills the page, and Close details returns to the list

#### Scenario: Fix match on a phone

- **WHEN** the user opens Fix match on a phone
- **THEN** each candidate shows its Plex score and Match button below its details

### Requirement: Dark theme

The dashboard SHALL always render in dark mode with its own colour palette over the Neutral theme. It SHALL offer no light mode or theme switch.

#### Scenario: Light system preference

- **WHEN** the operating system prefers a light theme
- **THEN** the dashboard still renders dark
