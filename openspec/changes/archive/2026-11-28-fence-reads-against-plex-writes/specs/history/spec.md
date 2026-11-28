## MODIFIED Requirements

### Requirement: History retention removes old events

At the start of each scheduled run and manual scan, autorefresh SHALL remove the events older than `history-retention` of every server, also when the scan covers one server, with their searches and candidates, in one transaction, and SHALL increment in it the generation of the fence of reads when it removed any event (see `library-scan`). It SHALL log the numbers of removed events and runs at debug level when it removed any.

#### Scenario: Old decision removed

- **GIVEN** a fix match decision older than the cutoff and a newer request
- **WHEN** retention runs
- **THEN** only the request stays in the item history, and no search or candidate of the decision remains

#### Scenario: Removed match event of a cached item

- **GIVEN** a cached item whose accepted match request event is older than the cutoff
- **WHEN** retention removes that event and the item later completes
- **THEN** the `completed` event credits the match kind without a match event

#### Scenario: Log line

- **WHEN** retention removes one event and one run
- **THEN** the log has one `Pruned history` debug line with `events` 1 and `runs` 1

#### Scenario: Fence of events

- **WHEN** retention removes an event while a read of another process runs
- **THEN** that read is not applied

#### Scenario: Read after all events were removed

- **WHEN** retention removed every event before a read takes its mark
- **THEN** the read is applied as usual
