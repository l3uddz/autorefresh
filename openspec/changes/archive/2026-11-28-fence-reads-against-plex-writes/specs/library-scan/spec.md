## MODIFIED Requirements

### Requirement: Items settled during a read

Before a read lists a library, autorefresh SHALL take the newest event as its mark. The read SHALL leave the row, the gone state, and the folder of an item unchanged when, after the mark, a check or a manual action marked the item complete or gone or Plex accepted a request for it, or when a write attempt for it was live at or after the start of the read, because its listing can be older.

#### Scenario: Check during the read

- **GIVEN** a read took its mark
- **WHEN** a check completes item `a` before the read is applied, also in the same second as the start of the read
- **THEN** the read adds no row for `a`, although its listing shows `a` as incomplete
- **AND** the other listed items are added as usual

#### Scenario: Manual request found the item gone

- **WHEN** a manual request that started before the read finds the item gone after the mark
- **THEN** the read adds no row for the item

#### Scenario: Settled before the mark

- **WHEN** the item was completed before the read took its mark, and no write attempt for it was live at or after the start of the read
- **THEN** the read adds the item again when its listing shows it as incomplete

#### Scenario: Manual match during the read

- **GIVEN** a read took its mark and listed movie `a` with `tmdb://100` and a path that names `{tmdb-100}`
- **WHEN** a manual Fix match that Plex accepts changes `a` to `tmdb://200` before the read is applied, and its check saves `mismatched`
- **THEN** the read keeps the row of `a` with `mismatched` and records no completed event for it
- **AND** the other listed items are applied as usual

#### Scenario: Answer of a request still pending

- **GIVEN** a cached row for item `a` with `no-summary`, and a read that took its mark
- **WHEN** Refresh now saved its attempt for `a` and Plex has not answered yet when the read is applied, and the listing shows `a` complete
- **THEN** the read keeps the row of `a`, records no completed event for it, and saves a hold for `a`

#### Scenario: Answer of a request lost

- **WHEN** a request for item `a` whose answer was lost is recorded as failed before the read is applied, and its attempt is still live
- **THEN** the read neither changes nor deletes the row of `a`, and saves a hold for `a`

#### Scenario: Rejected request during the read

- **WHEN** Plex rejects a request for the item with `401 Unauthorized` after the mark, and no other attempt for it was live at or after the start of the read
- **THEN** the read applies the listing of the item as usual

#### Scenario: Unlisted series of an incremental read

- **GIVEN** a cached series `s` with an episode reason, and a live attempt for `s`
- **WHEN** an incremental read lists neither `s` nor an episode of it
- **THEN** the read keeps the episode reason of `s` and saves a hold for `s`

#### Scenario: Full read of an item with a newer request

- **GIVEN** Plex accepted a request for a series after the mark of a full read
- **WHEN** the read staged a 404 or a new folder for it
- **THEN** the read neither deletes the row nor changes its path

## ADDED Requirements

### Requirement: Holds of skipped items

A read SHALL save a hold for each item that it names or would change and that it skipped because of a write attempt, or because of a request after the mark that Plex accepted, also for a complete item that has no cached row. It SHALL save no hold for an item that it skipped only because of a completed or gone event after the mark.

#### Scenario: Item moved between two libraries

- **GIVEN** reads of libraries A and B overlap while an attempt for item `a` is live, and Plex moves `a` from A to B in between
- **WHEN** both reads skip `a`
- **THEN** `a` has a hold in A and a hold in B, and a full read of B applies `a` and clears only the hold in B
- **AND** the next full read of A, which has no row for `a`, clears the hold in A

#### Scenario: Complete item made incomplete during the read

- **GIVEN** movie `m` is complete and has no cached row, and a full read listed it as complete
- **WHEN** the user applies Fix match to `m` from the Fixed tab before the read is applied
- **THEN** the read saves a hold for `m`, so the next full read lists it again

#### Scenario: Recording after the attempt ended

- **GIVEN** Plex accepted a bulk refresh of item `a`, and its process records the answer only after the attempt ended
- **WHEN** a read took its mark before that recording and applies after it
- **THEN** the read skips `a` and saves a hold for `a`

### Requirement: No sends without an applied read

A run SHALL send no automatic request to an item of a library whose read it did not apply in that run, because the read failed or was older than a pruning. The read could not save the holds of the items that it skipped. The next run reads the library again.

#### Scenario: Failed library read

- **WHEN** the incremental listing of a library fails in a normal run, and Plex stays available
- **THEN** the run logs `Reading library failed`, reads the next library, and sends no request to the items of the failed library in that run

### Requirement: Full read after a hold

A read of a library with a hold SHALL be a full read. An applied full read SHALL delete the hold of each item that the read did not skip, neither for a write attempt nor for an event after the mark, and that the read reconciled: it applied the item listing of the item, its 404 check answered 404, or the item has no cached row in that library and is not listed. It SHALL keep the other holds. A dry run SHALL save and delete holds as a live run does, because they are cache state.

#### Scenario: Attempt ended

- **GIVEN** a read saved a hold for item `a` while its attempt was live
- **WHEN** the next read of the library starts after the end of the attempt
- **THEN** the read is full, applies `a` from its listing, its 404 checks, and its folders, and deletes the hold of `a`

#### Scenario: Attempt still live

- **WHEN** the next read runs while the attempt for `a` is still live
- **THEN** the read is full, skips `a`, and keeps the hold of `a`

#### Scenario: 404 check of a held item fails

- **GIVEN** a hold for cached movie `a`, whose attempt has ended
- **WHEN** the full read omits `a`, and its 404 check answers 500 or answers with the item
- **THEN** the read is applied for the other items, `a` keeps its row and its hold, and the next read is full again

#### Scenario: Full read fails

- **WHEN** the full read of a library with a hold fails
- **THEN** the hold stays, and the next read of the library is full again

### Requirement: Read older than pruning

A read SHALL take the generations of the fence with its mark and its read times, in one snapshot. It SHALL NOT be applied when a generation changed before the apply, because a pruning of attempts, of events, or of the cache of a library or of that server ran during it. It SHALL keep the cache and the read times of the library, and count as a failed library read: a full run ends the run of the server as failed; another run logs `Read is older than pruning` and goes on with the next library.

#### Scenario: Pruning during a read

- **GIVEN** a read of a library took its mark
- **WHEN** another autorefresh process removes the cache of a library, or prunes attempts or events, before the read is applied
- **THEN** the read is not applied, the read times of the library stay, the run sends no request to the items of that library, and the next read lists the library again

#### Scenario: Pruning before the read

- **WHEN** a pruning ran before the read took its mark
- **THEN** the read is applied as usual

#### Scenario: Library of another server removed

- **WHEN** the run of server B removes the cache of an unselected library of B during a read of server A
- **THEN** the read of server A is applied as usual

#### Scenario: Pruning that deletes nothing

- **WHEN** a pruning during the read deletes no row
- **THEN** the generation stays, and the read is applied as usual
