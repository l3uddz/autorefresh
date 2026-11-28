## MODIFIED Requirements

### Requirement: Refresh route answer

`POST /api/item/refresh?srv=&key=` SHALL answer `200` with the item detail after the refresh (`item` and `timeline`), `match` after a fix match, with `guid` and, when known, `title` and `year`, and `checked`, whether a check after the request saved what it read, or found that a run completed or removed the item, or that Plex merged it. When the detail read fails, the answer SHALL leave out `item` and `timeline` and keep the rest. The answer SHALL get up to 3 minutes to write.

#### Scenario: Detail read fails

- **GIVEN** Plex accepted the refresh and applied a match
- **WHEN** the timeline read fails
- **THEN** the server answers `200` without `item` but with `match`

#### Scenario: Slow refresh

- **WHEN** the refresh succeeds, and the server finishes writing its answer after the 1 minute write timeout of the server but within 3 minutes of the request
- **THEN** the client still receives the `200` answer with `checked`

## ADDED Requirements

### Requirement: Merged fix in the timeline

A `completed` event of a match that Plex merged into another item SHALL carry `mergedKey`, the rating key of the item that Plex kept, in the item timeline and in run details. Every other event SHALL have no `mergedKey`.

#### Scenario: Merged fix

- **WHEN** the timeline holds a `completed` event of a match that Plex merged into item `564849`
- **THEN** the event has `"kind":"completed"` and `"mergedKey":"564849"`

#### Scenario: Ordinary fix

- **WHEN** the timeline holds a `completed` event after a check found the item complete
- **THEN** the event has no `mergedKey`

#### Scenario: Refresh now finds a merge

- **WHEN** Refresh now applies a match, and the refresh after it finds that Plex merged the item
- **THEN** the answer sets `checked`, and its timeline holds the `completed` event with `mergedKey`
