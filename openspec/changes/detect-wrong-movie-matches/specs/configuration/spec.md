## MODIFIED Requirements

### Requirement: Ignored reasons

Each entry of `ignore-reasons` SHALL be one of `unmatched`, `no-summary`, `no-poster`, `no-art`, `episode-no-summary`, `episode-unmatched`, or `mismatched`.

#### Scenario: Unknown reason

- **WHEN** `ignore-reasons` lists a name outside that set
- **THEN** autorefresh fails at startup with an error that names `ignore-reasons`

#### Scenario: Ignore wrong matches

- **WHEN** `ignore-reasons` lists `mismatched`
- **THEN** autorefresh starts, and an item whose only reason is `mismatched` has the queue state `ignored`
