## MODIFIED Requirements

### Requirement: Gone item

When Plex answers a request of a run with 404, the run SHALL remove the item from the cache, record it as gone with the action of that request (see `history`), or as merged after a match (see `fix-match`), and continue with the next item. The request SHALL count as sent but not as a failure, and the run SHALL log no warning for it.

#### Scenario: Refresh answered with 404

- **WHEN** Plex answers the refresh of a cached item with 404
- **THEN** the item is no longer cached, and the summary has `refresh_requests` 1 and `failed_requests` 0

#### Scenario: 404 in a dry run

- **WHEN** Plex answers a match search read of a dry run with 404
- **THEN** the item stays in the cache unchanged, and the run logs a warning

### Requirement: Follow-up after a request

After an accepted plain refresh, a run SHALL do nothing more for that item: it SHALL NOT wait for or check the result. The item SHALL leave the queue when a later read finds it complete (see `library-scan`). After an accepted match request, the run SHALL follow up the item as `fix-match` describes, and a follow-up that finds the item complete, or merged into another item, SHALL count as a completion of this run.

#### Scenario: Plain refresh

- **WHEN** a run refreshes three items with a plain refresh
- **THEN** it makes no request to the items after their refresh and waits only the `refresh-delay` between them

#### Scenario: Match that fixes the item

- **WHEN** Plex accepts a match, and a follow-up check finds the item complete
- **THEN** the item leaves the queue in the same run
- **AND** the summary counts it in `completed_items` and `completed_after_match`

#### Scenario: Match that Plex merged

- **WHEN** Plex accepts a match, and the follow-up finds that Plex merged the item into another item
- **THEN** the item leaves the queue in the same run, and the summary counts it in `completed_items` and `completed_after_match`
