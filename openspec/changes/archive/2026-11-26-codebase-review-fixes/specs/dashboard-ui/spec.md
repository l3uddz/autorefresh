# Spec Delta: dashboard-ui

## ADDED Requirements

### Requirement: Route error recovery

The dashboard SHALL catch uncaught page errors and failed lazy route chunk loads in an error boundary and SHALL show an error state with a Reload action instead of a blank page.

#### Scenario: Stale route chunk after service upgrade

- **GIVEN** the dashboard tab was opened before an upgrade changed the built frontend asset hashes
- **WHEN** the user navigates to a lazy-loaded page whose old chunk fails to load
- **THEN** the dashboard shows a recovery message with a Reload button

## MODIFIED Requirements

### Requirement: Server cards

The Status page SHALL show one card for each server in scope. Each card SHALL show the Plex health, the name, "Idle" or the run state, the last run, when the next full cache refresh is due, and the counts of items that need attention, were fixed recently, and are stopped. The attention count SHALL link to the Attention tab of that server and use singular grammar when the count is 1 ("1 item needs attention").

#### Scenario: Singular attention count

- **WHEN** a server has exactly 1 item needing attention
- **THEN** its card shows "1 item needs attention"

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

### Requirement: Item tabs, paging, and empty states

The Items page SHALL show a level-1 heading and Attention, Fixed, and Stopped tabs with counts, keeping server scope on tab change. Single-server loads without a library filter SHALL NOT double-fetch or drop early selections. Paging SHALL show "Showing <n> of <total>" and Load more (ignoring concurrent fetches). Initial load errors SHALL show an error banner with Try again and no empty table headers. Empty lists without error SHALL say why.

#### Scenario: Change tab

- **WHEN** the user picks the Stopped tab
- **THEN** the hash becomes `#/items/stopped` with the current scope, and the list shows the stopped items

#### Scenario: Initial load fails

- **WHEN** the first page request of the Items page fails
- **THEN** the page shows an error banner with a Try again button and does not render empty table headers below it

#### Scenario: Single-server initial load

- **GIVEN** one server is configured
- **WHEN** the user opens `#/items/attention` directly and server inventory finishes loading
- **THEN** the Items query key stays stable without re-fetching or clearing a selection

#### Scenario: Before the first scan

- **WHEN** no server in scope has been read yet
- **THEN** the page shows "Waiting for the first scan"

#### Scenario: Filters match nothing

- **WHEN** a search or a filter is set and no item matches
- **THEN** the page shows "No items match"

#### Scenario: Empty tab

- **WHEN** a tab has no item without filters
- **THEN** Attention shows "Nothing needs attention", Fixed "Nothing fixed yet", and Stopped "No stopped items"

### Requirement: Bulk Stop trying and Resume

Bulk Stop trying SHALL ask for a confirmation that names the number of items and says that they move to Stopped. Resume SHALL run without a confirmation. Each action SHALL send one request for each server of the selection and report every server in one toast. Clear selection SHALL be disabled while a bulk stop or resume is in flight, and an in-flight bulk action SHALL finish and report its result even if filters or tabs change.

#### Scenario: Stop succeeds

- **WHEN** the user confirms Stop trying for 12 items and 2 are skipped
- **THEN** a notice toast says "Stopped 10 items · 2 skipped"

#### Scenario: Clear selection disabled while pending

- **WHEN** a bulk stop or resume request is in flight
- **THEN** Clear selection is disabled and no duplicate concurrent bulk stop or resume can start

#### Scenario: No matching items

- **WHEN** no server has an item that matches the filters
- **THEN** a notice toast says "No items match the filters."

#### Scenario: One server fails

- **WHEN** one server fails and another stops items
- **THEN** an error toast names the stopped items and the server that failed

#### Scenario: Every server fails

- **WHEN** every server fails or has no matching item, and at least one failed
- **THEN** an error toast names the failures and the selection stays for another try

### Requirement: Typed match search

The dialog SHALL offer a search by title with an optional year, or by an ID such as tmdb-48213, tvdb-31207, or imdb-tt7712040. Enter or Search SHALL send it. The candidates of the typed search SHALL join those of the opening search. The same search again SHALL retry it, and Clear search SHALL drop the typed results and clear the text, year, and validation error inputs. Validation errors SHALL attach to the field that failed.

#### Scenario: Empty search

- **WHEN** the user searches without text
- **THEN** the search field shows "Enter a title or an ID." and sends nothing

#### Scenario: Bad year

- **WHEN** the year is not a number from 1 to 9999
- **THEN** the year field shows "The year must be a number from 1 to 9999."

#### Scenario: Clear search resets inputs

- **WHEN** the user clicks Clear search after a typed search or validation error
- **THEN** the typed candidates disappear and the search text, year, and validation error all clear

#### Scenario: ID search with a year

- **WHEN** the user searches tmdb-48213 with a year
- **THEN** the dialog ignores the year

#### Scenario: Search too long

- **WHEN** the trimmed text is longer than 200 UTF-8 bytes
- **THEN** the search field shows "Use at most 200 bytes."
