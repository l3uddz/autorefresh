## MODIFIED Requirements

### Requirement: List and table modes

The Items page SHALL offer a List and a Table mode, and SHALL remember the choice in the browser. A list row SHALL link to the item and show a poster, the title, the year, the server (with several servers), the library, the queue state, and the reason badges. Table mode SHALL show the same facts in columns for each tab and open a row on click, Enter, or Space.

#### Scenario: Reason badges

- **WHEN** an item has reasons
- **THEN** each reason shows as a coloured badge: unmatched red, no poster and no art orange, no summary blue, episode reasons purple, mismatched pink
- **AND** an ignored reason is muted and marked "(ignored)"

#### Scenario: Row state per tab

- **WHEN** the list shows the Attention tab
- **THEN** each row shows its queue state, such as "Due" or "Waiting · in 3h", and its number of tries when it has at least one
- **AND** a Fixed row shows what fixed it and when, and a Stopped row shows when it was stopped

#### Scenario: Row that needs a manual fix

- **WHEN** an Attention row has the queue state `manual`
- **THEN** the row shows "Needs a manual fix" as its queue state

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

#### Scenario: Filter wrong matches

- **WHEN** the user opens the reason filter on Attention
- **THEN** it offers "mismatched" beside the other reasons, and choosing it lists only items with the reason `mismatched`

## ADDED Requirements

### Requirement: Wrong match in the item detail

When the live Plex view of a movie reports the result `mismatched`, the item detail SHALL show a warning "Plex matched this movie to a different entry than its path names. Use Fix match to choose the right one." It SHALL list each compared source with its name (TMDB, IMDb, or TVDB), the path ID, and the Plex IDs. With another result, without a live view, or while the live view fails, it SHALL show no such warning.

#### Scenario: Movie with a wrong match

- **WHEN** the live view of a movie gives `mismatch` with the result `mismatched` and the source `tmdb`, path `100`, and Plex `200`
- **THEN** the detail shows the warning with the line "TMDB: path 100, Plex 200"
- **AND** the Fix match action of the detail stays the only way to fix the match

#### Scenario: Live view unavailable

- **WHEN** the live view fails, also after an earlier answer with the result `mismatched`, and the cached item has the reason `mismatched`
- **THEN** the detail shows the reason badge and no wrong match warning
