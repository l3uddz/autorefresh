## MODIFIED Requirements

### Requirement: Wrong match in the item detail

When the live Plex view of a movie reports the result `mismatched`, the item detail SHALL show a warning "Plex matched this movie to a different entry than its path names. Use Fix match to choose the right one, or Refresh now to apply the automatic pick." It SHALL list each compared source with its name (TMDB, IMDb, or TVDB), the path ID, and the Plex IDs. With another result, without a live view, or while the live view fails, it SHALL show no such warning.

#### Scenario: Movie with a wrong match

- **WHEN** the live view of a movie gives `mismatch` with the result `mismatched` and the source `tmdb`, path `100`, and Plex `200`
- **THEN** the detail shows the warning with the line "TMDB: path 100, Plex 200"
- **AND** the warning has no button of its own

#### Scenario: Live view unavailable

- **WHEN** the live view fails, also after an earlier answer with the result `mismatched`, and the cached item has the reason `mismatched`
- **THEN** the detail shows the reason badge and no wrong match warning

## ADDED Requirements

### Requirement: Texts about the automatic pick of Refresh now

The dashboard SHALL say where Refresh now applies the automatic pick: for an unmatched item or a mismatched movie in the queue. A preview in the automatic fix match panel SHALL also say that it recorded and sent nothing, and that Match on its pick applies the pick. The tooltip of Refresh now SHALL NOT say that it acts as a scheduled run does.

#### Scenario: Preview help text

- **WHEN** the panel shows a preview
- **THEN** it says "Autorefresh searched as a run would, but recorded nothing and sent nothing to Plex. To apply its pick, click Match on that candidate. For an unmatched item or a mismatched movie in the queue, Refresh now in the item details also applies the automatic pick."

#### Scenario: Refresh now tooltip

- **WHEN** the user points at Refresh now in the item detail
- **THEN** the tooltip says "Handle this item now: an unmatched item or a mismatched movie gets the automatic fix match, then Plex refreshes the item and autorefresh checks it again"

#### Scenario: No decision to explain

- **WHEN** the automatic fix match panel has no decision and no preview to show
- **THEN** it says "Autorefresh has not tried a fix match on this item within the history it keeps. It tries one for unmatched items with IDs in their folder or file names and, with a TMDB token, for unmatched series by their title. Refresh now also tries one for a mismatched movie."
