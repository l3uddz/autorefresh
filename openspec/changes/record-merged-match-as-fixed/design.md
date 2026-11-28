# Design

## Context

Plex merges two items of one library section when they have the same match. It keeps one item with all files and deletes the other rating key. A fix match can cause this: autorefresh matches item `a` to a GUID that item `b` of the same library already has, and Plex then deletes `a`.

Today the follow-up of an accepted match (`settleMatch`) finds `a` gone in one of two places:

- **The refresh after the match** (`followUpRefresh`). For a manual action, `sendManual` records `RecordManual{Gone: true}`. For a run, `runRefresh` records `RecordGone` with the action `refresh`.
- **A check after the match** (`followUpCheck`). `checkHeld` gets a 404 and calls `recordCheckGone`, which calls `ApplyCheckGone` with the `Observation` of the check.

`settleMatch` knows the GUID of the chosen candidate and hands it to the checks (`followUp.guid`), but not to the follow-up refresh.

A live probe showed that `GET /library/sections/<id>/all?type=1&guid=<plex GUID>` lists exactly the item that Plex kept, and nothing for an unknown GUID. `Plex.List` reads every page of a listing, so it cannot bound the lookup if a Plex version ignores the filter.

## Goals / Non-Goals

**Goals:**

- A follow-up after an accepted match that finds a cached item gone records a merge as a fix, when Plex has another item with the chosen GUID in the same library.
- The merge record replaces the gone record in the same transaction, with the same write attempt and yield handling.
- A run counts a merge as a completion after a match, from the row that the store removed.
- The dashboard shows the merge as a fix and links to the kept item.

**Non-Goals:**

- No refresh or check of the kept item.
- No change to a 404 outside the follow-up of an accepted match, or of an item with only history.

## Decisions

### The Plex lookup

The Plex client adds `FindByGUID(ctx, section string, itemType int, guid string) ([]Item, error)`. It sends one request to `/library/sections/<section>/all` with `type`, `guid`, `X-Plex-Container-Start=0`, and `X-Plex-Container-Size=50`, and the same `excludeElements` and `includeGuids` as `List` (through `listQuery`). It reads no further page, so a Plex version that ignores the filter costs one page of 50 items. It checks `section` with `idPattern`, the type (`TypeMovie` or `TypeShow`), and that `guid` is not empty, and returns an error before any request otherwise. The request uses the client's request timeout, as every read does.

Alternative: `Plex.List` with a `guid` filter. It walks every page and would read a whole library if the filter were ignored, so the lookup gets its own bounded method.

### The merge rule

The refresher adds `mergedInto(ctx, srv Server, row store.Item, guid string) (string, error)`. It calls `FindByGUID` with the row's section and the item type of the row's library type: `plex.TypeMovie` for `plex.MovieLibrary`, `plex.TypeShow` for `plex.ShowLibrary`, and 0 otherwise, which `FindByGUID` rejects. It returns the rating key of the first listed item whose GUID equals `guid` exactly, whose key differs from the row's key, and whose key passes `plex.ValidRatingKey`. It returns "" with no error for no such item, and "" with the error for a failed request.

The callers call it only when all of these hold:

- a match came first in the same action, so the GUID is not empty;
- the item had a cache row when the action started (`manualItem.cached`; a run always has one);
- Plex answered the follow-up refresh or a check with 404.

On an error, the caller logs `Merge lookup failed` (warn) and records the item as gone, as today. The gone record is never skipped. After it, a lookup error that stops the server (`stopError`: a context error or `plex.ErrUnavailable`) ends a run at once:

- The follow-up refresh (`recordFollowUpGone`) returns `errors.Join(err, stopError(ctx, lookupErr))`. The error still wraps `plex.ErrNotFound`, so `settleMatch` saves no hold, and `settleFailed` sees the stop error and ends the run.
- A check (`checkGone`) returns `stepSettled` with the stop error for a run, and nil for a manual action. `followUpCheck` handles `stepSettled` before the error: it returns `checked = true` with `runError(run, err)`, and saves no hold, because the row is gone.

### The GUID reaches the follow-up refresh

`followUpRefresh(ctx, item, run, match, guid)` passes the GUID to `runRefresh` and `sendManual` as a new `merge` argument (a `mergeLookup{guid string}`; zero for every other request). `sendManual` looks up the merge only when `cached` is true. On a 404 with a GUID, both call `mergedInto` before they record, and pass the key to the recorder.

### The recorders

Each gone recorder takes the merged rating key, "" for a plain removal, and reports what it wrote:

- `RecordGone(server, key, at, source, action, runID, attempt, merged string) (removed store.Item, wroteMerge bool, err error)`: the removed row, and whether it wrote the merged completion instead of the gone event. A gone event, also the fallback for a row without an accepted match, gives false.
- `ManualRecord.Merged string`, beside `Gone`. `RecordManual` returns `(bool, error)`: true when it wrote the merged completion. In `writeManual`, a merged key with no cache row (the row left the cache during the action) writes no event and no snapshot, but still finishes the write attempt as for gone. A merged key with `Cached` false is a programming error, and `writeManual` returns an error.
- `ApplyCheckGone(server, key, at, source, runID, obs, merged string) (removed store.Item, wroteMerge bool, err error)`: the removed row, and whether it wrote the merged completion, with the same `checkRow` and yield rules (`ErrCheckYielded`, `ErrNoItem`).

The merged completion (`mergedCompletion(tx, row, at, runID, merged)`) is a `completed` event with source `auto`, also in a manual action, as every completed event. It sets both `FixedAction` and `FixedMatch` to the row's `LastMatch`, because the row's `LastAction` can be the refresh after the match, sets `FixedMatchEventID` to the row's `LastMatchEventID`, and keeps `merged` in `MergedKey`. The row is deleted in the same transaction. The run ID links the event to a run; a manual action gives 0.

A merged completion needs a row with an accepted match (`LastMatch` not empty). The recorder checks it: a row without one gets the gone event instead, so a completion never credits nothing.

Alternative: a separate `RecordMerged` method for each path. It would duplicate the attempt and yield handling of three recorders, so the recorders take the key instead.

### The column

Migration 19 adds `merged_key TEXT NOT NULL DEFAULT ''` to `event`. `Event.MergedKey` reads and writes it through `eventColumns`. No other event sets it.

### The follow-up after a merge

- `followUpRefresh` and `checkHeld` report a merge: `followUpRefresh` returns `(store.AttemptResult, bool, error)` with `merged` true when the recorder wrote the merged completion (`wroteMerge`), and `checkHeld` returns the new step `stepMerged`. A run counts a completion and logs the merge only when `wroteMerge` is true.
- `settleMatch` checks a merge first, before `settleFailed`: on `merged`, it ends the follow-up and returns `checked = true`, because the store now holds the final state of the item. A merged check ends `followUpCheck` with `checked = true` as `stepSettled` does, and saves no hold, because the row is gone.
- In a run, the caller that saw the merge calls `run.complete(lib, removed)` with the row that the store removed, after the commit. `removed.Refreshes` and `removed.LastMatch` are then current, so the summary counts the completion in `completed_items` and `completed_after_match`. A stale `it` is never counted.
- The log line `Plex merged the item` (info) comes after the commit and names the server, the library, the title, the GUID, and `merged_key`.
- `refreshItem` keeps `r.itemDone(run, it.RatingKey, req.Outcome())`: the item outcome stays the outcome of the accepted match.

Named helpers keep each function at a cognitive complexity of 15 or less: `recordFollowUpGone` (the 404 branch of `runRefresh` and `sendManual`: the lookup, the recorder, the log, and the run count), and `checkGone` (the 404 branch of `checkHeld`).

### API and dashboard

- `internal/web` adds `mergedKey` (omitted when empty) to the timeline event DTO, which the item detail and the run details share. The Refresh now answer needs no new field: its timeline holds the merged completion, and `checked` is true.
- `timeline.ts`:
  - `disposition` keeps `fixed` and exposes the merged key.
  - `eventView` gives the history texts.
  - `refreshResult` adds the kind `merged` only when the answer has an applied match and its newest completion has `mergedKey`; `refreshText` turns it into the toast. Without an applied match, the completion keeps the plain complete text.
- `detail.tsx` shows the Fixed fact as "<match> · merged into another item", through `fixedBy`. For a merged fix, `LiveError` shows the info banner in place of the 404 warning, with "Open the merged item in Plex". That link reuses the URL builder of "Open in Plex" with the kept key, and is left out without a server identity.
- All texts use Astryx components only.

## Load on Plex and TMDB

- No new request in the usual case.
- After an accepted match of a cached item whose follow-up gets a 404: one request for at most 50 items of one library, filtered by `guid`.
- No TMDB request.
- Runs keep their pacing. The lookup belongs to the item that the run handles.

## Risks / Trade-offs

- [Risk] Plex deleted the item for another reason, right after the match, while another item of the library had the same GUID. → The item is recorded as fixed. Its match was right, and the kept item has it, so the history is still true in practice.
- [Risk] The listing filter ignores `guid` on some Plex version. → The lookup reads one page of 50 items and takes only an item whose GUID equals the chosen one, so a wrong item is never named. If the kept item is not on that page, the item is recorded as gone, as today.
- [Trade-off] Older `gone` events of merged items stay as they are.
