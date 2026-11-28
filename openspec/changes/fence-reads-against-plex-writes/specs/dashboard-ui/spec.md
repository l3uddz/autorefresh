## ADDED Requirements

### Requirement: A waiting item whose next due time has passed

A row or detail whose queue state is `waiting` and whose next due time is not in the future SHALL show "Waiting · next run" instead of a time in the past, because a hold keeps the item until the next run of its server reads its library.

#### Scenario: Held item

- **WHEN** an item has the state `waiting` and a next due time three days ago
- **THEN** its row shows "Waiting · next run"

#### Scenario: Waiting item in its backoff

- **WHEN** an item has the state `waiting` and a next due time in three hours
- **THEN** its row shows "Waiting · in 3h", as before
