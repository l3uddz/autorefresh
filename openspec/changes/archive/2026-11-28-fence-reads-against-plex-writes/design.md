# Design

## Context

See proposal.md for the problem. The facts that shape the approach:

- **Writes.** Plex has two write requests, `Refresh` (`PUT …/refresh`) and `Match` (`PUT …/match`). Both go through `Client.send`, and any 2xx answer counts as accepted. A transport error, a timeout, and a 5xx answer all reach the caller as an error, and the store records the request with `accepted = 0`.
- **Redirects.** The Plex client follows same-origin redirects (`neterr.SameOrigin`). Go turns a `PUT` into a `GET` after a 301, 302, or 303, and sends the `PUT` again after a 307 or 308. So a followed redirect of a write is either no write or a second write.
- **Callers.** Six paths send a write:
  - the run item (`refreshItem`);
  - the run follow-up refresh (`runRefresh`);
  - the manual send (`sendManual`), which Refresh now, Apply match, the automatic fix match inside Refresh now, and the follow-up refresh after a manual match use;
  - the bulk item (`bulkItem`, through `sendManual`).

  Item actions hold only the in-process item lock. Runs and bulk refreshes also hold the run lock. Library reads hold neither.
- **Recording.** `RecordRequest`, `RecordManual`, and `RecordGone` each write in one immediate transaction. Each write method of the store begins with `_txlock=immediate`, so a write transaction serializes with `ApplyRead`, also across processes. The store has one connection (`SetMaxOpenConns(1)`) and a busy timeout of 30 s.
- **Reads.** `readLibrary` takes `at`, then the event mark, then lists the library, and then calls `ApplyRead`. `ApplyRead` skips the keys of `newerKeys`: completed or gone events, or accepted requests, after the mark. It advances `last_read` also when it skipped keys. An incremental read lists only the items that changed since `last_read`, and `applyOldRows` also changes cached series that the incremental listing does not name (their episode reasons).
- **Full reads.** A read is full in a full run, on the first read, or when `cache-refresh` is due. A full read lists every item, checks unseen rows for 404, and repairs series folders.
- **Schedule.** The default `schedule` is hourly. The reads of a server come before its due items. So a read rarely runs while an attempt of the same server is live: only when an item action, a bulk refresh, or another process writes during the read.
- **Checks.** `checkHeld` reads `Plex.Item` and calls `ApplyCheck`. A 404 goes to `recordCheckGone`. A check that does not complete the item writes no event. No check compares the live GUID with the GUID of the match.
- **Events and pruning.** `event.id` is `AUTOINCREMENT`, so an ID never comes back. `PruneHistory` deletes old events. `PruneSections` and `PruneServers` delete a library or a server, and with it, through the foreign keys, its items and events.
- **Times.** Times are Unix seconds in `INTEGER` columns. Migrations are numbered SQL files, currently 1 to 17. A binary refuses a database whose schema version is newer than it knows.
- **Timeout.** The request timeout of a server (`timeout`, default `30s`) has no upper bound. It is the `http.Client` timeout of each request.

## Goals / Non-Goals

**Goals:**

- A read never applies a listing of an item while a write for that item may have changed it after the listing, within the bound of the allowance.
- An item that a read skipped is read again by a later full read, so no change is lost.
- No automatic send goes to an item that a write attempt protects or that a hold keeps.
- Each requirement of the review of the first plan (issue #161) is met.

**Non-Goals:**

- See proposal.md.

## Decisions

### 1. Tables

Migration 18:

```sql
CREATE TABLE write_attempt (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server TEXT NOT NULL,
    rating_key TEXT NOT NULL,
    action TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    ends_at INTEGER NOT NULL,
    outcome TEXT NOT NULL DEFAULT 'pending', -- pending, accepted, or unknown
    answered_seq INTEGER -- the newest attempt id when the outcome was recorded
);
CREATE INDEX write_attempt_key ON write_attempt (server, rating_key, ends_at);
CREATE INDEX write_attempt_ends ON write_attempt (ends_at);

CREATE TABLE read_hold (
    server TEXT NOT NULL,
    section TEXT NOT NULL,
    rating_key TEXT NOT NULL,
    PRIMARY KEY (server, section, rating_key),
    FOREIGN KEY (server, section) REFERENCES library (server, section) ON DELETE CASCADE
);
CREATE INDEX read_hold_key ON read_hold (server, rating_key);

CREATE TABLE prune_fence (
    server TEXT PRIMARY KEY, -- '' for the prunings of every server
    generation INTEGER NOT NULL DEFAULT 0
);
INSERT INTO prune_fence (server) VALUES ('');
```

- Each send has its own `write_attempt` row. A rejection deletes only its own row, so the follow-up refresh, or a request of another process, can never remove the protection of an earlier accepted match (requirement 1).
- `AUTOINCREMENT` gives a total order of the saves of attempts across processes. `answered_seq` places the answer of an attempt in that order: an attempt with `answered_seq < n` was answered before attempt `n` was saved. The checks use both.
- A `read_hold` row says that a read or a check could not apply the state of the item. It has no attempt reference. A key can have a hold in more than one library, for example when Plex moves an item between libraries during two overlapping reads, so the read of one library never replaces the hold of another. The foreign key removes the holds of a removed library.
- `prune_fence` counts the prunings that removed rows that a read can need: the row `''` for the prunings of every server (history and attempts), and one row for each server for the removal of its libraries or of the server itself. A row of a server is added on its first bump and never deleted.

### 2. The outcome of a write comes from the Plex client

`Refresh` and `Match` return a `plex.Outcome` with the error:

- **No redirects on writes.** The two write requests use a client whose `CheckRedirect` returns `http.ErrUseLastResponse`. A 3xx answer then reaches `send` as the answer. `send` returns an error for it that is neither `ErrNotFound` nor `ErrUnavailable`, so no caller treats it as gone or as an outage. Reads keep `neterr.SameOrigin`.
- **Classes:**
  - `accepted`: a 2xx status.
  - `rejected`: a status that says that the write was refused before it took effect: 400, 401, 403, 404, or 405.
  - `unknown`: everything else, so a transport error, a timeout, a cancel, a refused connection, a 3xx, a 5xx, and every other 4xx. A proxy in front of Plex can answer 408 or 429 after it forwarded the write, so these do not prove a refusal.
- **Agreement.** The outcome follows from the status of the one answer, as the error does. So `accepted` is exactly "no error", and `ErrNotFound` (404) is always `rejected`. No caller can see a success with an outcome other than `accepted`.
- **No trace.** The client does not try to prove that a request was never written. A request that fails before an answer is `unknown` and keeps its protection until its end. That costs at most one held item for `timeout` plus 10 minutes after a failed connection.

The outcome describes only the original write. A later `Item` read or search never changes an attempt (requirement 3).

### 3. One send helper with one deadline

`Refresher.sendWrite(ctx, srv, spec, send func(context.Context) (plex.Outcome, error)) (store.AttemptResult, error)` wraps every write. `spec` names the key, the action, whether the send is automatic (a scheduled run or a Run now scan), and, for the follow-up refresh of a match, the attempt of that match.

1. **Deadline.** `wctx` is `context.WithTimeout(ctx, timeout)`, so its deadline is the earlier of the deadline of `ctx` and the wall clock plus `timeout`, also in tests whose `r.now` is a fixed past time. The save and the send share it (requirement 6).
2. **Save.** `store.BeginAttempt(wctx, spec, timeout)` runs one immediate transaction:
   - **Admission.** For an automatic send, it first checks that the item has no `read_hold` row and no live attempt (`ends_at >= now`) other than the match that the send follows up, and that the current section of its row is one of the sections whose read the run applied (`spec.Sections`, from `run.readSections()`). Otherwise it saves nothing and returns `ErrHeld`. So a row that another process moved into a library that this run did not read gets no send.
     - An item action and a bulk refresh skip this check, because the user asked for them.
     - **Follow-up refresh held.** When a hold or another live attempt came in between, the follow-up refresh of a run gets `ErrHeld` after Plex accepted its match. The run keeps the accepted match as recorded and counted, sends no refresh, makes no check, and logs `Skipped follow-up refresh` with the cause `held`. For a follow-up refresh, the refusing transaction itself inserts a `read_hold` row for the library of the item, if that library row still exists, and commits it. So the hold is durable also when only a live attempt refused the send and Plex later rejects that attempt. A failure of that transaction is a store error, which ends the run as today, after the match was recorded. The next full read reconciles the item, and no automatic send extends the protection before that (requirement 5).
   - **Row.** It inserts the row. `started_at` is the time inside the transaction, after any lock wait. `ends_at` is `satAdd(satAdd(started_at, ceilSeconds(timeout)), 600)`.
   - The bundled driver interrupts a statement when its context ends, so the save is cancellable.
3. **Recheck.** After the commit, `sendWrite` checks `wctx.Err()` again. If the deadline has passed, it sends nothing and deletes the row with a background context of 5 s.
   - If that delete fails, for example because another process holds the write lock, it logs the warning `Dropping write attempt failed` and leaves the row. The row then protects the item until its end, `timeout` plus 600 s after its start, which is the safe side and the same bound as a lost answer.
4. **Not sent.** A failed save, `ErrHeld`, and a passed deadline return an error that wraps `ErrNotSent`. The callers record no request for it and count no sent request:
   - A run treats `ErrHeld` as a skip (`Skipped item` with the cause `held`), and every other `ErrNotSent` as a database or context error, which ends the run as today.
   - An item action answers through the existing `writeActionError`: 500 for a store error, 504 `timed out` when the deadline of the save or the send passed while the request is still open, and the store error answer when the request itself ended.
5. **Send.** It calls `send(wctx)` and returns `AttemptResult{ID, Outcome}` with the error. Every recording call takes that result.

The server timeout reaches the refresher as `Server.Timeout`, set in `main` from `RequestTimeout()`. A `Timeout` of 0 or less, as in tests that build a `Server` by hand, means `config.DefaultTimeout`.

- **Clock.** `BeginAttempt` (its `started_at` and the live test of its admission) and `ApplyCheck` and `ApplyCheckGone` (the live tests at the save) read one clock of the store inside the transaction, after any lock wait. It defaults to `time.Now`, and tests set it to the fake clock of the refresher. The other methods keep the times that their callers pass: `ApplyRead` compares with `Read.At`, `Due` and `DueRules` with their `Now`, and `PruneAttempts` with its `now`.
- **Errors.** `sendWrite` wraps every `ErrNotSent` that is neither `ErrHeld` nor the end of the caller's `ctx` in `ErrStore`: a failed save, the end of `wctx` during the save, and the end of `wctx` at the recheck after the commit. So `settleFailed` ends the follow-up instead of checking an item that got no refresh, and the bulk refresh logs it as a store failure. `writeActionError` checks `context.DeadlineExceeded` before `ErrStore`, so an item action still answers 504 for it.
- **The refresh after a match.** `followUpRefresh` returns its `AttemptResult` also when Plex answers it with an error, such as 500, so the chain of the checks after it holds the match and that refresh, and the checks never yield to their own refresh.

- **Saturation.** `satAdd` saturates at `math.MaxInt64`, so a very long `timeout` gives an attempt that stays live, never one that wraps into the past.
- **Comparisons.** Every comparison with `now` is inclusive (`ends_at >= now`).
- **Clock.** A forward clock step can make a live attempt look ended. The allowance of 10 minutes bounds that risk, and the proposal names it as a non-goal.

### 4. Recording keeps or deletes the attempt in the same transaction

- **Who records it.** `RecordRequest`, `RecordManual`, and `RecordGone` get an `Attempt AttemptResult` field or argument.
- **Gone paths.** The run 404 paths (`refreshItem`, `runRefresh`) pass the result of the request that found the item gone.
- **In the transaction:**
  - `rejected` deletes the row with that id.
  - `accepted` or `unknown` sets its `outcome`, and `answered_seq` to the newest attempt id (`seq` of `write_attempt` in `sqlite_sequence`).
  - An id of 0 changes nothing.
- **Without a cache row or a library.** `RecordRequest` and `RecordGone` return early today when the cache row is gone. `RecordManual` writes its event from the admission snapshot, whose library foreign key fails when the library is gone, and that error rolls the transaction back. All three now update or delete the attempt first, and commit that also when the row is gone. When the library row of the event is gone, for example because another process removed the library after the send, they write no event and no row change. Otherwise the event and the row changes keep their current rules.
- **A crash.** A process that crashes before it records leaves a `pending` row. That row protects the item until `ends_at`, which is the safe side.

### 5. `ApplyRead` skips protected keys and holds them

The start of the read is the existing `Read.At`, the `at` of `readLibrary`. `store.ReadStart(server, section)` replaces the separate `Library` and `EventMark` calls of `readLibrary`: in one read transaction, it returns the read times and the hold flag of the library, the newest event id as the mark, and the generations of the rows `''` and `server` of `prune_fence`. So the watermarks, the mark, and the generations come from one snapshot.

- **Fence.** In its transaction, `ApplyRead` first reads both generations. If one differs from the snapshot, it returns `ErrReadPruned`.
  - It writes nothing and keeps `last_read` and `last_full_read`.
  - `readLibrary` passes it to `readFailed`, as a failed library read: in a full run, the run of the server ends as failed, because a full run promises a full read of every library before it refreshes. Otherwise it logs `Read is older than pruning`, and the run goes on with the next library. The library counts as not read in this run.
- **No sends without an applied read.** The due items of a run come only from the libraries whose read the run applied (`run.readSections()`, next to `run.sections()`, which stays the list of selected libraries for `PruneSections`). A library whose read failed, or that the fence rejected, gets no automatic send in that run, because the read could not save the holds of the keys it skipped. The next run reads it again. This also changes a failed library read without a race, which today still refreshes the cached items of that library.
- **Protected keys.** The protected set is `newerKeys(mark)` together with the keys of this query:

  ```sql
  SELECT rating_key FROM write_attempt WHERE server = ? AND ends_at >= ?  -- Read.At
  ```

  An attempt that was live at any time from the start of the read to the apply is in the set, also one that ended during the listing.
- **Skipping.** `deleteGone`, `applyOldRows`, `insertNewRows`, and `writeFolders` skip the protected set, as they skip `newerKeys` today.
- **Holds.** Each key that the second query protected, and that the read names (a listed item, also a complete one without a row, a staged 404, or a folder) or whose row it would have changed, gets a `read_hold` row, by upsert. That covers a complete item of the Fixed tab that a write made incomplete during the read, and the cached series whose episode reasons an incremental read would have changed without listing them (requirement 5). Only keys with an attempt in the last `timeout` plus 10 minutes can be protected, so this adds no cost to a normal read. A key that `newerKeys` protected because of an accepted request after the mark also gets a hold, because the listing can be older than that write, also when its attempt ended before the read started (a process that paused between the answer and the recording). A key that `newerKeys` protected only because of a completed or gone event gets no hold, as today: a check or an action settled it after the mark.
- **A full read clears reconciled holds.** After its row changes, an applied full read deletes the hold of a key of its library that is not in the protected set (neither a live attempt nor `newerKeys`) and that the read reconciled:
  - the item listing named the key (`inItems`, not only an episode listing), and the read applied it;
  - the key had a cached row that the listing omitted, and its 404 check answered 404, so the read deleted the row as gone;
  - or the key had no cached row in this library before the read (the `old` rows, not the rows after `applyOldRows`) and the listing omits it, so the read has nothing to apply. A key that had a row in this library and is neither in `inItems` nor gone keeps its hold, also when `applyOldRows` deleted its row from the episode listings. The test is for this library: after Plex moved an item from A to B, and a read of B took its row, the next full read of A clears the hold of A.

  `goneKeys` returns the keys whose 404 check answered 404, as today, and also the keys whose check answered with the item, which `Read.Present` passes to `ApplyRead`. A key whose 404 check answered with the item, or failed, keeps its hold: `ItemPaths` reads no metadata, so the read did not reconcile it, and the next read of the library is full again.
- **Only a full read clears a hold.** A check never deletes a hold. A check can observe the item before a listing that a read skipped, and it does not read the folder or the episode reasons of a series, so it cannot reconcile what a read held.
- **An omitted item that Plex still has.** A held key that every full listing omits, while its 404 check answers with the item, keeps its hold. Plex then keeps an item that its own library listing does not show, so the cache cannot reconcile it. The item gets no automatic send while the hold lasts, and its library gets a full read on every run. Each such full read logs the warning `Held item not in listing` with the item, so the user can see the cost. Item actions still work, and the dashboard shows it as `waiting`.
- **Overlapping reads.** Two reads of the same library that overlap, which only two processes can run, are out of scope (see proposal.md, Non-Goals). The later apply wins, as before this change.
- **Read times.** The read times advance as before.
- **Dry run.** A dry run writes the rows and the read times of a read, because they are cache state. It writes the holds for the same reason.

### 6. A hold forces the next read of the library to be full

- **Full read.** `ReadStart` also returns whether the library has a hold. `readLibrary` makes the read full when it has one: `Full = run.full || … || state.Held`.
- **Why a full read.** It reuses the listing, the 404 checks, the folder repair, and the episode reasons of a series, all from one listing that starts after the attempt ended. A single-item read would need its own copy of each (see Alternatives).
- **Cost.** One full listing of the library, rarely: only after a write during a read, a lost answer, or a match that the checks did not see in time.
- **A hold during the read.** A hold that a check adds while a full read runs belongs to an attempt that is live at the check, so its key is in the protected set of that read, and the read keeps the hold.

**No automatic send.** `DueRules` gets a new part, `held`: a live attempt or a `read_hold` row for the item.

```sql
EXISTS (SELECT 1 FROM write_attempt a WHERE a.server = item.server AND a.rating_key = item.rating_key AND a.ends_at >= ?)
OR EXISTS (SELECT 1 FROM read_hold h WHERE h.server = item.server AND h.rating_key = item.rating_key)
```

- `waiting` becomes "in its backoff wait, or held", so `Due` and the lists of the dashboard agree, as `DueRules` intends. The order of the states stays `stopped`, `ignored`, `manual`, `too-new`, `waiting`, `due`: a held item that is too new stays `too-new`, which is not due either, and a held item that would be `waiting` or `due` is `waiting`.
- `nextDue` becomes the larger of the usual next due time and the latest live `ends_at` of the item. The SQL clamps `ends_at` to 9999-12-31T23:59:59Z before it turns it into milliseconds, so a saturated end neither overflows into `REAL`, nor fails the JSON encoding of the time, nor reaches the never-due value of `SortDue`. An item that only a hold keeps has its usual next due time, because the next run of its server reads the library in full first.
- `recheck` reads the same `held` part for the one item, and skips a row whose section is not one that the run read (cause `changed`). The admission of `BeginAttempt` checks it again in the transaction that saves the attempt, so another process that writes between `recheck` and the send stops the send (requirement 5).

### 7. Checks confirm the change and yield to other writes

- **Chain.** `checkHeld` gets the chain of its action: the attempt IDs of the match and its follow-up refresh, or of the one refresh. After a match, it also gets the intended GUID.
- **Observation.** Before each metadata read of a check, `checkHeld` takes `observed` (the time) and the generations of `prune_fence`, as a read does.
- **Yield.** In the transaction that saves a check result, `ApplyCheck` and a new `ApplyCheckGone` (which replaces the `RecordGone` and `RecordManual` calls of `recordCheckGone`) get the chain, `observed`, and the generations, and share one helper, `checkMaySave`. `RecordRequest`, `RecordGone`, and `RecordManual` stay for the outcomes of requests only, so the snapshot fallback of `RecordManual` for a missing row never applies to a check. The check yields when a generation changed, or when an attempt for the key outside the chain has `ends_at >= observed` and is not exempt. An attempt that ended during the metadata read therefore still yields it.
- **Own attempts ended.** The check also yields, with a hold, when no attempt of its chain is live at the save (`ends_at >= now`). While one is live, every read that is applied skips the key, because its `ends_at` is at or after the start of that read, so no read can have applied a listing of the key since the chain began. Once every attempt of the chain ended, a read may have applied a newer listing, and the check cannot tell, so it saves nothing and the next full read reconciles the key. With the default `timeout`, the checks end within seconds of the send and long before the end of the attempt, so this only stops a check that waited longer than the allowance.
- **Exempt.** Only an earlier plain refresh that Plex accepted before the chain began is exempt: `action = 'refresh'`, `outcome = 'accepted'`, and `answered_seq < min(chain)`. Its late effect is a refresh of the then current match, so it cannot bring back the state before the chain. Every match outside the chain, and every refresh that was answered later or not at all, yields the check, in any process. That keeps the usual flow working: Refresh now, then Fix match within 10 minutes. The reverse order does not settle at once: after an accepted match, including the automatic fix match of Refresh now and the match of a run, the checks of every later item action on the item within `timeout` plus 10 minutes yield, and the item stays `waiting` until the next full read of its library (see Risks).
- **Holds do not yield a check.** A check that does not yield saves its result, also when the key has a hold, and the hold stays. Its result can be older than a listing that a read skipped, so only the next full read clears the hold (section 5). This includes a hold that another process saved because of an attempt of the chain itself.
- **On a yield,** the check saves nothing, adds a `read_hold` row, reports the reasons as not current, and the checks end. This covers a metadata result and a 404 alike. `followUpCheck` then returns `checked = false`, also when an earlier check of the same action saved.
- **Library removed.** The save transaction resolves the row of the item first. When the row is gone, for example because a pruning removed the library during the check, the check saves nothing, adds no hold, and ends as for an item that left the cache. A hold row is inserted only while its library row exists.
- **GUID.** After a match, a check whose live GUID differs from the intended GUID saves nothing and counts as not settled. When the checks end without the GUID, because the waits ran out, a check failed, or the context ended, `followUpCheck` adds a `read_hold` row (requirement 4). When `settleMatch` ends before its checks for a cached item for any reason other than a 404 or `ErrHeld` (which already committed its hold), such as an unavailable server, the end of the context, or a store error, it adds a hold too. A failure of that `AddHold` after the context ended is only logged, so the run keeps the context error and ends as stopped, not failed. Both use `store.AddHold(server, key)`, which inserts the hold only while the row and its library exist, with its own short context, so it also runs after the end of the context.
- **Match to the current GUID.** The API accepts a match to the GUID that the item already has. The check still requires the intended GUID, which Plex then shows at once, so no lookup before the match is needed. The GUID cannot tell the old metadata from the new, and Plex fetches the metadata of the same match again, so after that the check works as after a plain refresh (below): it saves what Plex shows, and the rule that a completion is not final applies.
- **Plain refresh.** A refresh has no GUID to confirm. A check after a plain refresh saves what Plex shows at the check, also when Plex completed the item before the refresh took effect: the item is then complete in Plex, and moving it to Fixed is right. If the pending refresh later removes metadata again, a later read lists the item as incomplete and adds it back. A read while the attempt is live skips the key and holds it, so the next full read adds it back at the latest. That is the existing rule that a completion is not final.
- **Late apply.** Plex has no completion signal. The bound on a late apply is the allowance of the attempt: a read that starts after `ends_at` trusts Plex.

### 8. Pruning bumps the generation

Each transaction that deletes rows that a read can need increments a generation of `prune_fence` when it deletes at least one row:

- `PruneHistory` (events) and `PruneAttempts(now)` increment the row `''`. `PruneAttempts` runs in `pruneBeforeRun` outside a dry run and deletes the rows with `ends_at < now - 86400`.
- `PruneSections` increments the row of its server. `PruneServers` increments the row of each server that it removes. Their cascades delete items, events, and holds.

A read compares generations for equality (section 5), so it never depends on an ID or a time that a pruning removed (requirement 2).

- **Normal runs.** `pruneBeforeRun` runs `PruneHistory`, `PruneAttempts`, and `PruneServers` before any server starts, so the reads of the run start after those bumps. `PruneSections` runs in `readLibraries` of its own server before its reads, and it bumps only the row of that server, so the reads of the other servers of the same run keep their generation.
- **Rejections.** Only a pruning of another process during a read rejects that read. The next run reads the library again. Prunings of another process that come again and again can keep a hold for as long, which is the safe side. `newerKeys` keeps reading `MAX(event.id)` as the mark. After all events are deleted, the mark is 0 and every later event counts as newer, which is the safe side.

### 9. Rollout and rollback

- **Upgrade.**
  - Migration 18 creates the three tables and the fence row.
  - The README tells the user to stop every autorefresh process that uses the database, also a `--once` run in cron, before the upgrade.
  - After the upgrade, an older binary refuses the newer schema, so an older process cannot send writes without attempts (requirement 7).
- **Rollback.** The README gives the SQL, which runs in one transaction with every autorefresh process stopped. A test applies migration 18, runs the rollback, and applies migration 18 again:

  ```sql
  BEGIN IMMEDIATE;
  DROP TABLE read_hold;
  DROP TABLE write_attempt;
  DROP TABLE prune_fence;
  DELETE FROM schema_migration WHERE version = 18;
  COMMIT;
  ```

- **Cost.** Each Plex write adds one database write before it, and its outcome joins the existing recording transaction. A hold adds one full read of its library.
- **Redirects.** A Plex server that answers a write with a redirect now gets the write once, and the write fails. The README says so.
- **Dry run.** A dry run sends nothing, so it saves no attempt. It skips the keys that live attempts of other processes protect, and writes their holds, as cache state.

## Alternatives considered

- **One marker per item (the first plan).** Rejected: a rejection of a later request removes the protection of an earlier accepted match (requirement 1).
- **A single-item re-read of each skipped key (the second plan).** Rejected after review. It needed its own fences between the selection, the read, and the apply; its own episode and folder logic for a series; a fair order with a batch limit; and its own dry-run rules. A full read already has all of these.
- **A request trace (`httptrace`) to tell "never written" from "written".** Rejected: the trace can report late, more than once, or not at all on a cancel, so a "never written" answer could delete the protection of a write that Plex got.
- **Prune fences by the latest pruned end time and event ID.** Rejected: the event mark is 0 after all events are deleted, and the cascades of a removed library delete events without a time bound. One generation counter covers every pruning.
- **An in-memory barrier.** Rejected in the review of #157: it cannot see other processes, and its lock table held across a transaction can wait 30 s.
- **Stop advancing `last_read` on a skip.** Rejected: one live item would hold every incremental read of its library at the same window, and a full read would keep repeating its 404 checks.

## Tests

Store tests use temporary files. The interleavings use two `Store` values on one file, so they run on two connections:

- `BeginAttempt`:
  - It writes `started_at` and `ends_at` with the saturating add, also for a timeout near `math.MaxInt64`.
  - A cancelled context saves nothing, also while the second connection holds the write lock.
  - An automatic attempt returns `ErrHeld` for a live attempt or a hold that the second connection saved, and a manual one does not.
  - The follow-up refresh of a run after its own accepted match is saved, although the match attempt is live. After a hold or an attempt of the second connection came in between, it returns `ErrHeld` and commits a hold, which stays after the second connection's attempt is rejected and deleted.
- Recording with each outcome deletes or keeps the row in the same transaction, also for `RecordGone`. When the second connection removed the library of the item after the send, `RecordRequest`, `RecordGone`, and `RecordManual` (also a 404 of a manual or bulk refresh) commit the attempt change and write no event.
- `ApplyRead`:
  - The test of #161: seed a row with `no-summary`, take the mark, begin an attempt on the second connection, apply a read that shows the item complete, and expect the row to stay with a hold.
  - It skips a key with a live attempt, also one that ended between the start of the read and its apply, and one whose attempt the second connection rejected after the start, while another of its attempts is live.
  - It holds listed rows, 404s, folders, and an unlisted series of an incremental read whose episode reasons would change.
  - It holds a key with an accepted request after the mark whose attempt ended before the start, and does not hold a key with only a completed event after the mark.
  - It applies a key whose attempt ended before the start.
  - A full read clears the holds of the keys that it reconciled, and keeps the holds of a key with a live attempt and of an omitted key whose 404 check failed or answered with the item.
  - The read times still advance.
- The fence:
  - `ApplyRead` returns `ErrReadPruned` after `PruneAttempts`, `PruneHistory`, or `PruneSections` of the same server on the second connection deleted rows after `ReadStart`, and keeps the read times then.
  - `PruneSections` of another server does not reject the read.
  - A pruning that deletes nothing leaves the generation.
  - A read right after `PruneHistory` deleted the last event is applied, with the mark 0.
- `Due` excludes keys with a live attempt or a hold. The item list gives them the state `waiting`, with the end of the attempt as the next due time.
- `ApplyCheck` and `ApplyCheckGone` save nothing and add a hold:
  - after a newer attempt outside the chain;
  - after an older match, or an older refresh that is `pending` or `unknown`, or that the second connection answered after the chain began;
  - after an attempt that ended between `observed` and the save;
  - after a change of a generation.

  An older refresh answered with 2xx before the chain began does not stop them, and neither does an attempt in the chain or a hold. A save keeps the hold of the key.
- A check whose row the second connection removed with its library saves nothing and adds no hold.
- A check whose chain has no live attempt at the save saves nothing and adds a hold, also for a 404. A check while its chain is live saves its 404 after an incremental read on the second connection that does not list the key.

Plex client tests with `httptest`:

- Each outcome class: 200, 400, 401, 403, 404, 405, 408, 429, 500, a timeout, and a refused connection.
- A 307 answer to a write: the server sees exactly one `PUT`, the error is not `ErrNotFound`, and the outcome is `unknown`. A read still follows a same-origin redirect.

Refresher tests:

- Every write path saves an attempt before the send, and records its outcome.
- A failed save sends nothing and records no request.
- A save whose deadline passes after the commit deletes its row, sends nothing, records no request, and leaves the sent and failed counts of the run unchanged. When the second connection holds the write lock during the delete, the row stays, with the warning, and nothing is sent.
- A full run whose read the generation rejects ends the run of that server as failed; another run goes on with the next library.
- A run skips an item with `ErrHeld` when another process saved an attempt between `recheck` and the send.
- A library with a hold gets a full read, which applies the item after the attempt ended. A held movie that the listing omits and whose 404 check answers 500 keeps its hold.
- After an accepted match of a run, a hold or an attempt of another process before the follow-up refresh skips the refresh and its checks, keeps the match as recorded and counted, and leaves a hold.
- Two overlapping reads of two libraries that both hold a moved key keep both holds. The full read of the new library applies the row and clears its hold, and the next full read of the old library then clears the old hold, because the old library has no row for the key.
- A check after a match to the GUID that the item already has saves what Plex shows.
- A check after a match that still shows the old GUID saves nothing, and the item gets a hold when the waits run out, or when a later check fails.
- A check that saves and a later check of the same action that yields return `checked = false`.
- With a store clock set to the fake clock of the refresher, attempts, reads, and checks agree on the time.
- A check whose 404 comes after a newer attempt deletes nothing.

## Risks / Trade-offs

- [Each write adds one database write] → One small immediate transaction before each single-item request. It is the same kind of write as the recording after it.
- [A lost answer keeps an item protected for `timeout` plus 10 minutes] → Runs do not send to it in that time. The waits of the backoff are much longer than that, so this delays no due item in practice.
- [A hold of an item that the full listing omits while Plex still has it] → The hold stays, the item gets no automatic send, and its library gets a full read on every run, with the warning `Held item not in listing`. Plex then lists its library without an item that it still has, which the cache cannot reconcile either way. Item actions still work.
- [A hold costs a full read] → Holds come only from writes during a read, from lost answers, and from matches that the checks did not see. The default schedule is hourly, and the reads of a server come before its sends, so this is rare. The full read also repairs what the skipped read could not.
- [A check does not move the item to Fixed while another write may still change it] → The hold makes the next full read settle it, at the next run. That covers an unanswered write of another process, and every earlier match within `timeout` plus 10 minutes: Fix match and then Refresh now, a second Fix match, or Refresh now after a run matched the item, report the reasons as not current, and the item stays `waiting` until the next run. The usual flow, an answered Refresh now and then Fix match, still settles at once.
- [A failed library read no longer lets the run refresh the cached items of that library] → The next run reads it again. The README says so.
- [A Plex server that redirects writes] → The write now fails instead of becoming a `GET` or a second `PUT`. Plex does not redirect these requests by itself, so only a proxy in front of Plex can do it.
- [A clock step forward larger than 10 minutes] → It can end a protection early. See Non-Goals.
- [Plex applies an accepted write later than the allowance] → A read after `ends_at` can apply an older state. The allowance is the explicit assumption of the design.

## Migration Plan

- Migration 18 adds the tables. There is no data migration.
- Stop every autorefresh process before the upgrade. The rollback SQL is in the README.
