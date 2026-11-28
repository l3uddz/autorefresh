# Proposal

GitHub issue: #161

## Why

A library read takes its event mark, lists the library, and then applies the listing. A Plex write for an item of the same server, such as Refresh now or Fix match, can run during that time, because it holds only its item lock. `ApplyRead` skips an item only when a completed or gone event, or an accepted request, was recorded after the mark. It cannot see a request whose answer has not arrived yet, or a request whose answer was lost after Plex applied it, which is recorded with `accepted = 0`. Then an older listing can change or delete the row of that item. A store test on `main` confirmed this for every reason. The wrong match detection of #157 makes it more visible, because the fix for `mismatched` is a manual Fix match.

## What Changes

- **Write attempts.** Before each Plex `Refresh` or `Match`, autorefresh saves a write attempt for the item in its own write transaction: the server, the item, the action, and the time until which it protects the item. Each send has its own attempt. If the save fails or is cancelled, autorefresh sends nothing and counts no attempt. This adds one database write for each Plex write.
- **Typed outcome of each write.** The Plex client reports the outcome of each write: `accepted` (a 2xx answer), `rejected` (400, 401, 403, 404, or 405, which say that Plex refused the write), or `unknown` (anything else, such as a timeout, a lost answer, a refused connection, a 3xx, a 5xx, or another 4xx such as a proxy's 408). Write requests no longer follow redirects, so one write is always one request. Every path that records a request, also the run paths that record a gone item, saves this outcome with the attempt in the same transaction. A rejected attempt is deleted. An accepted or unknown attempt keeps protecting the item until its end time. The result of a later read or search never changes an attempt.
- **Reads skip and hold protected items.** A read skips each item that has an attempt that was live at or after the start of the read, in addition to the items that the mark already protects. It saves a hold for each item that it skipped this way. A library with a hold gets a full read next time. An applied full read clears the hold of each item that no attempt protects anymore and that the read reconciled. Any other hold stays, and the library stays on full reads.
- **No automatic send while protected or held.** A run, unlike an item action or a bulk refresh, sends no request to an item with a live attempt or a hold. The transaction that saves the attempt of an automatic send checks this again, so another process cannot slip a write in between.
- **No sends without an applied read.** A run sends no automatic request to the items of a library whose read it did not apply, because the read failed or was older than a pruning. Today a failed read still lets the run refresh the cached items of that library.
- **Fence for pruning.** Each pruning that deletes attempts, events, or the cache of a library or server increments a generation in the same transaction. A read whose generation changed before it is applied is not applied, and its read times stay.
- **Checks confirm the change.** After a match, a check completes the item only when Plex shows the intended GUID. A check result, also a 404, is not saved when another write for the item may still change it, or when the attempts of its own action have ended, and the item gets a hold instead. An item action within `timeout` plus 10 minutes after an accepted match therefore leaves the item `waiting` until the next run.
- **One deadline.** The attempt save and the send share one deadline. Autorefresh checks it again after the save commits. Times are Unix seconds and comparisons are inclusive. The end time adds the request timeout and an allowance of 10 minutes to the start, each with a saturating add, because `timeout` has no upper bound.
- **Rollout.** Migration 18 adds the tables. The README says to stop every autorefresh process, also a `--once` run, before the upgrade, and gives the rollback SQL. A dry run sends nothing, so it saves no attempt.

## Non-Goals

- A completion signal from Plex. Plex has none. The allowance of 10 minutes after the request timeout is an explicit assumption about how late Plex applies an accepted write.
- Protection against a forward clock step that is larger than the allowance.
- A new kind of Plex request. The only change to the requests that autorefresh sends is that a write no longer follows a redirect.
- A change to the manual actions that the dashboard offers. A manual action on an item that an attempt protects or a hold keeps still sends, with its own attempt.
- Locks between processes other than SQLite's write lock.
- Two reads of the same library that overlap, which only two autorefresh processes on one database can run, such as a `--once` run beside the service. The later apply wins, as before this change.

### Surface summary

- **Configuration keys**: none added, changed, or removed.
- **API routes**: none added or removed. An item action whose attempt cannot be saved fails like any other database error (500), or with 504 when its deadline passed, and sends nothing. An item action whose send passes the server timeout answers 504 `timed out` instead of 502.
- **Dashboard pages**: none added. The item list and the item detail show an item that a write attempt or a hold keeps as `waiting`, and a waiting item whose next due time has passed as "Waiting · next run".
- **Plex**: no new kind of request. A write that Plex answers with a redirect fails instead of being sent again. A hold keeps its library on full reads until a full read reconciles the item. A held item that the listing omits while Plex still has it keeps the library on full reads until then.
- **Database**: migration 18 adds three tables.

## Capabilities

### New Capabilities

- `write-attempts`: the record of each Plex write attempt, its typed outcome, its end time, its deadline, and the pruning of old attempts.

### Modified Capabilities

- `library-scan`: a read skips and holds the items that a write attempt protects, a hold makes the next read full, a read is not applied when a pruning ran during it, and a library whose read was not applied gets no automatic sends in that run.
- `refresh-runs`: due items exclude items that an attempt protects or a hold keeps, and each request of a run saves an attempt with its outcome.
- `manual-actions`: each manual write saves an attempt, and a check result is not saved while another write may still change the item.
- `fix-match`: a check after a match completes the item only when Plex shows the intended GUID.
- `history`: history pruning increments the generation of the fence of reads.
- `dashboard-api`: an item that a write attempt or a hold keeps has the state `waiting`, and an item action that passes the server timeout answers 504.
- `dashboard-ui`: a waiting item whose next due time has passed shows "Waiting · next run".

## Impact

- `internal/plex`: the outcome of `Refresh` and `Match`, and no redirects for them.
- `internal/store`: migration 18, the attempt methods, `ApplyRead`, the holds, `DueRules`, the request recording methods, `ApplyCheck`, and pruning.
- `internal/refresher`: one send helper for every Plex write, the full read of a library with a hold, and the GUID check after a match.
- Docs: `README.md` (upgrade, rollback, redirects of writes, failed library reads, and overlapping reads), `AGENTS.md` (the `internal/plex`, `internal/store`, and `internal/refresher` rows), and the specs above.
