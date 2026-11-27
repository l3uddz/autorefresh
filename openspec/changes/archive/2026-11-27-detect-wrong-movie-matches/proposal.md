# Proposal

GitHub issue: #157

## Why

Autorefresh finds a movie only when Plex did not match it. It does not find a movie that Plex matched to the wrong entry, although the folder or file name names the right ID, such as `{tmdb-747059}`. Such a movie has a summary, a poster, and art, so it looks complete, and only a person who opens it in Plex sees the error.

Plex can give the external IDs of each item (`imdb://`, `tmdb://`, `tvdb://`) in a section listing when the request adds `includeGuids=1`. A read-only check against the deployed server confirmed this for movie and show listings. Movie listings already carry the file paths. So a read can compare the path IDs with the Plex IDs without one extra request.

## What Changes

- Movie listings, the metadata reads of a check, and the live Plex view add `includeGuids=1`. The request count stays the same; each movie answer gets a few more fields.
- A new reason, `mismatched`: a matched movie whose paths name an ID for which Plex lists a different ID of the same source, and no shared source agrees. The rule reads path IDs exactly as fix match does.
- When the paths name IDs but no source can be compared, the read keeps the earlier value of `mismatched`, so a sparse Plex answer cannot move a movie to Fixed by mistake.
- `mismatched` is a flag only. It never makes an item due, and the recheck before a send skips an item that has no other counted reason left. A cached item whose only reasons outside `ignore-reasons` are `mismatched` gets the new queue state `manual`, also while it is newer than `minimum-age`, and no run sends it a refresh or a match. Automatic fix match still runs only for unmatched items.
- Each check after a manual request also reads the external IDs, so a manual Fix match that corrects the movie clears the reason and moves the movie to Fixed.
- `ignore-reasons` accepts `mismatched`. Movies are still checked, as for every other item reason, but the reason no longer counts.
- The live Plex view of a matched movie adds the result of the same comparison, and every live view adds the external IDs. The item detail shows which path ID and which Plex ID disagree, and points to the existing Fix match action.
- The dashboard shows a pink `mismatched` reason badge, the reason filter option, and the queue state "Needs a manual fix". The reason uses the same label rule as the other reasons, so the badge, the table, the toasts, and the now panel all read "mismatched".

### Surface summary

- **Configuration keys**: no key added or removed. `ignore-reasons` accepts the new value `mismatched`. The default stays `[]`.
- **API routes**: no route added or removed. `GET /api/items` and `GET /api/item` can answer the reason `mismatched` and the state `manual`, and `reason=mismatched` is a valid filter. `GET /api/item/live` adds `externalIds` and `mismatch`.
- **Dashboard pages**: no page added or removed. Items and Item detail show the new reason and state, and Item detail shows the disagreeing IDs.
- **Plex requests**: no new kind of write request, no library-wide refresh, and no extra read request. Movie listings, the metadata reads of a check, and the live Plex view add the query parameter `includeGuids=1`. No request goes to TMDB or another host.

## Non-Goals

- Series. A show listing has no folder (`Location`). A series check would need one item request for each series in every full read. On the deployed server that is tens of thousands of requests at about 0.2 s each, so it waits for a separate change.
- Episodes.
- An automatic re-match. A wrong match is fixed only by hand, with the Fix match dialog. Its automatic preview already searches the path IDs.
- TMDB lookups to compare IDs of different sources, such as a path `tvdb` ID against Plex `tmdb` IDs. A source that only one side names is not compared.
- A new configuration key to turn the check off. `ignore-reasons: [mismatched]` stops the reason from counting.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `library-scan`: Movie listings read the external IDs, the new item reason `mismatched` with its comparison rule, and what a read keeps when it cannot compare.
- `refresh-runs`: An item whose only counted reason is `mismatched` is not due, and the recheck before a send skips it.
- `configuration`: `ignore-reasons` accepts `mismatched`.
- `manual-actions`: The check after a manual request reads the external IDs and saves the `mismatched` reason by the same rule.
- `dashboard-api`: The `manual` queue state, the `mismatched` reason and filter, and the external IDs and comparison in the live Plex view.
- `dashboard-ui`: The `mismatched` reason badge and filter option, the "Needs a manual fix" state, and the disagreeing IDs in the item detail.

## Impact

- **Go packages**: `internal/plex` (`includeGuids` on movie listings and item reads, external ID decoding), `internal/refresher` (the comparison, which reuses the path ID parser of fix match), `internal/store` (the new reason bit, the keep rule of a read and a check, and the `manual` queue state in the due rules), `internal/config` (no code change beyond the reason list, which comes from the store), `internal/web` (live view fields).
- **Database**: no migration. The reason bits are stored as an integer, and the new bit fits in the existing column.
- **Frontend**: `web/src` reason lists, reason colours, state texts, API types, and the item detail.
- **Docs**: `README.md` (feature list, reasons, `ignore-reasons` values) and `AGENTS.md` (package descriptions).
- **Upgrade**: existing rows keep their reasons. Movies get the new reason when a read lists them: an incremental read for recently changed movies, and the next full read for all others. The Run now choice "Full cache refresh, then run" flags them at once.
