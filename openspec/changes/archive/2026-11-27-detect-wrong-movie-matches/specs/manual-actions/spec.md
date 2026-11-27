## ADDED Requirements

### Requirement: A check saves a wrong match

Each metadata read of a check of a movie SHALL ask for its external IDs with `includeGuids=1`. The check SHALL save `mismatched` by the rules of a library read: it sets or clears the reason when a source can be compared, and keeps the earlier value when the paths name IDs that no source can compare. The check SHALL send no extra request for this.

#### Scenario: A manual match corrects a wrong match

- **GIVEN** a cached movie whose only reason is `mismatched`, with a path that names `{tmdb-100}`
- **WHEN** the user applies a candidate and the first check reads `tmdb://100` among the external IDs
- **THEN** autorefresh removes the row, records a completed event, and makes no second check

#### Scenario: A refresh keeps a wrong match

- **GIVEN** a cached movie with the reasons `mismatched` and `no-summary`
- **WHEN** a manual refresh and its checks find a summary but Plex still lists `tmdb://200` for a path that names `{tmdb-100}`
- **THEN** the row keeps only `mismatched`

#### Scenario: A check cannot compare

- **GIVEN** a cached movie with the reason `mismatched`
- **WHEN** a check reads the movie without an external ID of any path source
- **THEN** the row keeps `mismatched`
