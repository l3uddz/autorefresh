# Proposal

## Why

A matched movie whose path IDs disagree with its Plex IDs has the reason `mismatched`. No action of autorefresh applies a match to it on its own. A run refreshes it only for its other reasons, and Refresh now sends a plain refresh. A refresh only reloads the wrong entry, so such a movie stays wrong until the user clicks Match on the right candidate, even when the automatic fix match preview already found that candidate. Issue #165 describes this.

## What Changes

- Refresh now on a cached movie with the reason `mismatched` runs the automatic fix match first, as it does for an unmatched item, and records the decision.
- Refresh now applies the pick only when the decision picks a candidate whose GUID differs from the current match of the movie. Otherwise it sends the plain refresh.
- Just before the match, autorefresh reads the movie again. It applies the pick only when Plex still shows the same match and the paths still disagree with it.
- A match that Plex accepts is followed up as every match: a refresh, then checks that confirm the GUID of the pick. A check that then finds the IDs in agreement and no other reason moves the movie to Fixed.
- With `mismatched` in `ignore-reasons`, Refresh now sends only the plain refresh, as it does for an unmatched item when `unmatched` is ignored.
- The dashboard texts say that Refresh now applies the automatic pick to a mismatched movie: the help text of the automatic fix match preview, the wrong match warning of the item detail, the tooltip of Refresh now, which no longer says that it acts as a scheduled run does, and the empty state of the decision panel.

This sends a kind of Plex request that Refresh now did not send before: a match request for an item that is already matched. It is the same single-item match request that Fix match sends, and it goes only to the one item that the user refreshes.

## Non-goals

- Scheduled runs do not change. A run never sends a match for a mismatched movie, and the reason `mismatched` alone never makes a movie due.
- No change for a flag that the current match itself causes, such as an outdated IMDb ID in a file name while Plex already has the right entry. The pick then equals the current match, and Refresh now sends only the plain refresh.
- No change for series. Only movies have the reason `mismatched`.
- No change to the decision rules of the automatic fix match.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `manual-actions`: Refresh now runs the automatic fix match for a mismatched movie, and the re-read before the match checks that the movie keeps the same wrong match. The plain refresh and the wrong match check name the mismatched movie as a case of their own.
- `dashboard-ui`: the preview help text, the wrong match warning, the tooltip of Refresh now, and the empty decision panel name Refresh now as a way to apply the automatic pick.

## Impact

- Configuration keys: none added, changed, or removed. `ignore-reasons` with `mismatched` now also turns off this match.
- API routes: none added or removed. `POST /api/item/refresh` can now answer an applied match for a mismatched movie.
- Dashboard pages: none added. Four texts change.
- Code: `internal/refresher` (the fix match of Refresh now), `web/src/fixmatch.tsx`, `web/src/wrongmatch.tsx`, `web/src/detail.tsx`, `web/src/history.tsx`, `README.md`, and `AGENTS.md`.
- Plex: Refresh now can send a match request for a matched movie, as described above.
- GitHub issue: #165.
