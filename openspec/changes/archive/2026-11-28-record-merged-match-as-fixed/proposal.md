# Proposal

## Why

When Plex accepts a match and another item of the same library already has the chosen match, Plex merges the two items. It keeps the other item with all files and deletes the matched one. The follow-up of the match then gets a 404 for the old rating key, and autorefresh records the item as removed: "Removed: Plex no longer had it · Its refresh request found it gone". The match worked, but the history and the item detail say that the item went away. Issue #167 describes this. A live example: a movie in two folders, where one copy was already matched right and Refresh now fixed the other.

## What Changes

- When the follow-up of an accepted match finds a cached item gone, either at its refresh or at a check, autorefresh asks Plex for another item with the chosen GUID in the same library. This is one request for at most 50 items, filtered by `guid`.
- If Plex lists such an item, autorefresh records a `completed` event instead of a `gone` event. The event credits the match and keeps the rating key of the item that Plex kept. The item moves to Fixed, and a run counts it as completed after a match. The event has the source `auto`, as every completion.
- If Plex lists no such item, or the listing fails, autorefresh records the item as gone, as today.
- The API adds the kept rating key to the completed event (`mergedKey`).
- The dashboard shows a merged fix as fixed:
  - The history says "Fixed: Plex merged it into another item".
  - The item detail shows the Fixed fact with "merged into another item". In place of the "Plex no longer has this item" warning, it says that Plex merged the item, with a link that opens the kept item in Plex.
  - The Refresh now toast says that Plex merged the item into the item with this match.

This adds no Plex write. It adds one read: a filtered request of one library, only after an accepted match of a cached item whose follow-up got a 404.

## Non-goals

- No refresh or check of the item that Plex kept. If it is incomplete, the next read of the library adds it as its own item.
- No change for a 404 outside the follow-up of an accepted match. A plain refresh, a read, or a check that finds an item gone still records it as removed.
- No change for a match request that Plex answers with 404 itself.
- No lookup for an item with only history: its 404 is recorded as before.
- No rewrite of older `gone` events. Only new follow-ups record merges.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `fix-match`: the follow-up of an accepted match checks for a merge before it records the item as gone.
- `history`: a `completed` event can record a merge, with the kept rating key, and a merge records no `gone` event.
- `manual-actions`: a match action whose follow-up finds a merge succeeds and records the item as fixed.
- `refresh-runs`: a run counts a merge as a completion after a match.
- `dashboard-api`: the timeline event of a merged fix carries `mergedKey`, and the Refresh now answer covers a merge.
- `dashboard-ui`: the history, the item detail, and the Refresh now toast show a merged fix.

## Impact

- Configuration keys: none.
- API routes: none added or removed. Timeline events can carry `mergedKey`.
- Dashboard pages: none added. The item detail, the history, and the Refresh now toast change texts.
- Database: migration 19 adds the `merged_key` column to `event`.
- Code: `internal/plex` (`FindByGUID`), `internal/store` (the merge record and the column), `internal/refresher` (the lookup in the follow-up of a match), `internal/web` (the event field), `web/src` (texts and the link), `README.md`, and `AGENTS.md`.
- Plex: one bounded, filtered request after a 404 in the follow-up of an accepted match of a cached item.
- GitHub issue: #167.
