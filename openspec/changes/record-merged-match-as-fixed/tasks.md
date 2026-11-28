# Tasks

## 1. Plex: a bounded lookup by GUID

Commit: `feat(plex): find the items of a library with a GUID`

- [x] 1.1 Add `FindByGUID(ctx, section, itemType, guid)` to the Plex client: one request with `X-Plex-Container-Start=0` and `X-Plex-Container-Size=50`, the `listQuery` of the type, and the `guid` filter, with no further page. Reject an invalid section, an unknown type, or an empty GUID before any request.
- [x] 1.2 Add tests with `httptest`: the query parameters, one request also when `totalSize` is larger than the page, the guards, and a 404 or 503 answer (`ErrNotFound`, `ErrUnavailable`).

## 2. Store: record a merge as a completion

Commit: `feat(store): record a match that Plex merged as a completion`

- [x] 2.1 Add migration 19, which adds `merged_key TEXT NOT NULL DEFAULT ''` to `event`. Add `Event.MergedKey`, and read and write it through `eventColumns`.
- [x] 2.2 Give `RecordGone` and `ApplyCheckGone` a merged rating key and make them return the removed row and whether they wrote the merged completion (`wroteMerge`). Give `ManualRecord` the field `Merged`, and make `RecordManual` return whether it wrote the merged completion. With a key and a row with an accepted match, they write `mergedCompletion` (source `auto`, `FixedAction` and `FixedMatch` from the row's last match, `FixedMatchEventID`, `MergedKey`, the run ID) in place of the `gone` event, in the same transaction and with the same attempt and yield handling. A row without an accepted match gets the gone event. In `writeManual`, a merged key without a cache row writes no event and no snapshot but finishes the attempt, and a merged key with `Cached` false returns an error. Update every caller and the doc comments.
- [x] 2.3 Add store tests:
  - a merged completion through each recorder: its fields (`FixedAction` and `FixedMatch` from `LastMatch` also when `LastAction` is `refresh`), source `auto` also for a manual record, no `gone` event, the row removed, the returned row and `wroteMerge`, and the attempt finished as for gone;
  - a check that yields (`ErrCheckYielded`) saves nothing, and a missing row gives `ErrNoItem`;
  - `writeManual` with a merged key whose row left the cache writes no event;
  - a merged key on a row without an accepted match writes `gone` and returns `wroteMerge` false;
  - the migration on an older database, and a plain removal that still writes `gone` without a key.

## 3. Refresher: look up the merge in the follow-up of a match

Commit: `fix(refresher): record a match that Plex merged as fixed`

- [x] 3.1 Add `mergedInto` (the merge rule of the design, with the item type of the row's library type), with the log line `Merge lookup failed` (warn) for an error, and `Plex merged the item` (info) after the commit of a merge.
- [x] 3.2 Pass the GUID through `followUpRefresh` to `sendManual` and `runRefresh`. On a 404 of a cached item with a GUID, they look up the merge (`recordFollowUpGone`) and record it with the merged key. In `checkHeld`, a 404 with `followUp.guid` looks up the merge before the gone record (`checkGone`), and the gone or merged record gives `stepSettled`.
- [x] 3.3 `followUpRefresh` returns whether it recorded a merge. `settleMatch` handles a merge before `settleFailed`: no further check, no hold, `checked` true. `followUpCheck` ends on `stepSettled` with `checked` true before it handles an error, with no hold. A lookup error that stops the server ends a run after the gone record: `recordFollowUpGone` joins it to the 404, and `checkGone` returns it with `stepSettled`. A run calls `run.complete` with the row that the store removed. `refreshItem` keeps `req.Outcome()`.
- [x] 3.4 Keep every function at a cognitive complexity of 15 or less.
- [x] 3.5 Add tests with the fake Plex server (an entry for the GUID request):
  - a manual Refresh now that applies the automatic match, whose follow-up refresh gets 404 and finds the merge, with `checked` true;
  - a run match with the same merge, counted in `completed_items` and `completed_after_match` from the removed row, with the outcome of the accepted match;
  - a check after the match that gets 404 and finds the merge, in a run and in a manual action;
  - a series library, with the type `TypeShow` in the request;
  - a failed request and an empty answer, which record gone, and an unavailable server during the lookup in a run, at the follow-up refresh and at a check, which records gone, saves no hold, and ends the run;
  - a listed item with another GUID, with the row's own key, or with an invalid key, which is not a merge;
  - a plain refresh that gets 404, and a match of an item with only history that gets 404, with no GUID request.

## 4. API: the merged key in the timeline

Commit: `feat(web): add the merged key to timeline events`

- [ ] 4.1 Add `mergedKey` (omitted when empty) to the timeline event DTO of `internal/web`, and tests for the item detail route, the run details, and a Refresh now answer that finds a merge.

## 5. Dashboard: show a merged fix

Commit: `feat(web): show a match that Plex merged as fixed`

- [ ] 5.1 Add `mergedKey` to `TimelineEvent` in `web/src/api-types.ts`.
- [ ] 5.2 In `timeline.ts`, give the history texts of a merged fix, and the Refresh now result `merged` with its toast only when the answer has an applied match. In `detail.tsx`, show the Fixed fact as "<match> · merged into another item", and in place of the 404 warning, the info banner with "Open the merged item in Plex". Use Astryx components only.
- [ ] 5.3 Add Vitest tests for the history texts, the Fixed fact, the toast with and without an applied match, the banner with and without a server identity, and the 404 warning after a removal.

## 6. Documentation

Commit: `docs(readme): describe a match that Plex merged`

- [ ] 6.1 Say in `README.md` that a match which makes Plex merge two copies shows as fixed, and update the `internal/plex`, `internal/store`, `internal/refresher`, and `web/` rows of `AGENTS.md`.

## 7. Checks

- [ ] 7.1 Run the CI sequence of AGENTS.md: `mise run vendor`, `mise run test`, `mise run lint`, `mise run web-install`, `mise run web-lint`, `mise run web-test`, and `mise run test-webui`. Then run `pnpm exec astryx doctor --built .` in `web/`, and `openspec validate --all --strict`.
