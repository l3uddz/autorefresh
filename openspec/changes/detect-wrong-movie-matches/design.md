# Design

## Context

See proposal.md for the motivation. The facts that shape the approach:

- A library read decides the item reasons from the listing alone. `readKey.reasons` in `internal/store/item.go` replaces every item reason bit of a listed item, and keeps the old bits of an item that the read does not list. A check after a manual request (`ApplyCheck`) goes through the same function.
- Fix match reads path IDs with `parseIDs` and `parseID` in `internal/refresher/ids.go`. They strip leading zeros from numeric IDs, lowercase IMDb IDs, and accept bare IMDb IDs.
- `internal/plex` already decodes the `Guid` list into `metadata.ExternalGuids` as raw JSON, but nothing parses or uses it. Listings do not send `includeGuids=1` today.
- A read-only check against the deployed server confirmed two facts:
  - With `includeGuids=1`, movie and show section listings carry `Guid` entries with the prefixes `imdb`, `tmdb`, and `tvdb`. Without it, they carry none.
  - Show listings carry no `Location`, and movie listings carry `Media[].Part[].file`.
- The queue state comes from one SQL `CASE` in `DueRules.state` (`internal/store/list.go`). `Due` and the dashboard lists both build on `DueRules`, so the scheduler and the dashboard cannot disagree.

## Goals / Non-Goals

**Goals:**

- Detect a wrong movie match with no extra Plex request and no TMDB request.
- Never send a request because of `mismatched` alone.
- Never record a false completion when Plex gives too little to compare.

**Non-Goals:**

- Series, episodes, and cross-source comparisons through TMDB (see proposal.md).
- A stored copy of the compared IDs. The dashboard reads them live.

## Decisions

### 1. External IDs come from the same requests

`listQuery` adds `includeGuids=1` for `TypeMovie` only. `itemMetadata` (the `/library/metadata/<key>` read behind `Item`, `ItemWithPaths`, `ItemPaths`, and `ItemDetails`) adds it for every item, because the parameter costs nothing there and keeps one code path. `plex.Item` and `plex.Details` get `ExternalIDs []string`, such as `tmdb://100`.

A `Guid` value that does not decode as a list of `{id}` objects gives no IDs, so the comparison becomes undecided. It does not fail the listing. A failed listing would stop every other reason of the library over a field that only this check uses.

Alternative: one item request per movie. Rejected: it adds a request per movie for data that the listing already gives.

### 2. One comparison function, shared by the read, the check, and the live view

`internal/refresher/wrongmatch.go` holds `CompareMovieIDs(guid string, files, externalIDs []string) WrongMatch`. `WrongMatch` holds a `Result` (`unmatched`, `mismatched`, `agrees`, `undecided`, `no-path-ids`) and the compared sources, each with the path ID and the Plex IDs. The rules:

- A `local://` GUID gives `unmatched` at once, so no caller can flag an unmatched movie.
- No files give `undecided`, because the paths are then unknown, not free of IDs.
- The path side runs `parseIDs` on the movie's file paths. The callers always pass files: `plex.Item.Files` in a read and a check, and `Details.Paths.Files` in the live view, never `Paths.All()`.
- The Plex side splits each external ID with `strings.Cut(id, "://")`. It keeps only the schemes `imdb`, `tmdb`, and `tvdb`, compared without case. It then normalizes the value with `normalizeIDValue(source, value)`, a helper split out of `parseID` (`imdbValue` and lowercase for `imdb`, `normalizePositiveNumber` for `tmdb` and `tvdb`). `parseID` calls the same helper, so both sides follow one normalization. The value goes to the helper as it is, not through `idPattern`, so a malformed ID such as `tmdb://-100` or `tmdb:///100` is dropped instead of read as `100`. Values are deduplicated for each source, in first-seen order.
- A source is compared when the paths name exactly one value of it and Plex lists at least one value of it.
- Compared sources come out in the fixed order `tmdb`, `imdb`, `tvdb`, so the API answer and the log are stable.

`internal/web` calls the exported function for the live view, so the API cannot drift from the read. The function and its helpers stay below a cognitive complexity of 15: parse the path side, parse the Plex side, then compare.

Alternative: compare in `internal/plex`. Rejected: path ID rules belong to the refresher, and fix match already owns them.

### 3. "Undecided" keeps the old bit through one store rule

`store.Item` gets `Keep Reasons`: the item reason bits that the reader could not decide. `readKey.reasons` becomes:

```text
final = (meta.Reasons &^ meta.Keep) & ItemReasons | prev & meta.Keep & ItemReasons
```

The refresher sets `Keep |= Mismatched` for an `undecided` result. `ApplyRead` and `ApplyCheck` both use `readKey.reasons`, so a read and a check follow one rule. A new row has `prev = 0`, so it does not get the reason.

`Mismatched` is in `ItemReasons`, so a listed movie that agrees clears the bit, and a movie that a read does not list keeps it. For a series, the bit is never set, and the bits of `EpisodeSkipped` never include it.

Alternative: put `Mismatched` outside `ItemReasons`, like the episode bits. Rejected: movie reads would then never clear it, because the episode path of `readKey.reasons` applies only to show reads.

### 4. A flag-only reason and the `manual` queue state

`store` gets `const ManualReasons = Mismatched`. These reasons never make a row due:

- `Due` filters `reasons & ~(ignored | ManualReasons) != 0`.
- `DueRules.state` adds `WHEN reasons & ~(ignored | manual) = 0 THEN 'manual'` after `ignored` and before `too-new`. The order is `stopped`, `ignored`, `manual`, `too-new`, `waiting`, `due`. A new movie with only `mismatched` then shows "Needs a manual fix", not a countdown to a due time that never comes.
- `recheck` in `internal/refresher/refresh.go` skips a row when `row.Reasons &^ (IgnoreReasons | ManualReasons) == 0`. This covers a row that entered the batch with `mismatched` and another reason, and lost the other reason before its turn without a new attempt.
- `CountIncomplete` and the attention lists and counts keep `reasons & ~ignored != 0`, so a `manual` row still needs attention and counts as incomplete in the summary.
- `NextDue` stays set only for `waiting` and `too-new`.
- `seeksMatch` is unchanged, so automatic fix match still needs `unmatched`.

Manual Refresh now and bulk refresh still act on a `manual` row, because the user asked for it. Stop trying gives a per-item dismissal of a false positive.

Alternative: refresh a mismatched movie with the usual backoff. Rejected: a refresh keeps the current match, so it only adds load and attempts.

### 5. Read and check wiring

- `storeItem` gets the comparison result: `mismatched` sets the bit, and `undecided` sets the `Keep` bit. An unmatched movie skips the comparison.
- `readLibrary` keeps the `WrongMatch` of each `mismatched` movie from its item loop and passes the list to `logWrongMatches`, which logs `Wrong match` at debug level with the server, library, title, and each compared source. `store.Item` gets no field for this, and nothing compares twice.
- `checkHeld` already reads the item with `Plex.Item`, whose answer for a movie includes the files and now the external IDs. It builds the row through the same `storeItem`, so a manual Fix match that corrects the movie clears the bit, and `settleMatch` moves it to Fixed. This also covers the follow-up check after an automatic match. Its `Checked item` line logs the saved reasons, `row.Reasons | prev.Reasons & row.Keep`, so a kept `mismatched` shows.

- A manual request can change a movie while a read lists its library. The listing is then older than the check after the request. `ApplyRead` already adds no row for an item that a check or a manual action settled after `Read.Mark`. It now also skips an item with a request that Plex accepted after the mark: for new and existing rows, for the 404s of a full read (`deleteGone`), and for its series folders (`writeFolders`). An older listing that agrees cannot delete the row that a newer check flagged and credit the wrong match as the fix. A later read applies its listing again.

### 6. Dashboard

- `api-types.ts` adds the state `manual` and the live fields `externalIds` and `mismatch`.
- `format.ts` gives `manual` the text "Needs a manual fix". The reason keeps the shared `label()` rule, which gives "mismatched" like the other lowercase reason labels. The five callers of `label()` for reasons (`items.tsx`, `media.tsx`, `table.ts`, `timeline.ts`, and `now.ts`) then need no change, and a Refresh now toast reads "Still missing: mismatched".
- `items.tsx` adds `mismatched` to the reason options, and `media.tsx` gives it the Astryx Badge variant `pink`.
- `detail.tsx` shows an Astryx warning banner in `DetailBody` with one line per compared source, using the names TMDB, IMDb, and TVDB. It hides the banner while the live query has an error, because TanStack Query keeps the data of the last success beside the error of a failed refetch. The banner has no button. It points to the existing Fix match action in `DetailActions`, which keeps its own dialog and its `busy` state, so no second action can start while one is pending.

It uses Astryx components and tokens only, by `web/AGENTS.md`. The Fix match dialog needs no change, because its automatic preview already searches the path IDs for any known item.

### 7. Sort by next due time

A row whose reasons outside `ignore-reasons` are all `ManualReasons`, in state `manual` or `ignored`, is never due, but the due sort computed a time for it, so an older `manual` row came before rows that are due. `DueRules.dueSort` gives such a row `math.MaxInt64`, a time after every other: it comes last when the soonest comes first (`sort=due`) and first when the latest comes first (`sort=-due`). The column stays not null, so the keyset cursor sees every row, and server and rating key still break ties. The listed `nextDue` comes from `nextDue` as before and stays empty for these rows. The web cursor version goes from 1 to 2, so a cursor of the old order gets the usual `400 invalid cursor` instead of repeating or skipping rows.

### Load on Plex and TMDB

- Requests: no change. Reads keep their pages of 500, and a check keeps its 1 to 3 item reads.
- Payload: each movie answer gets about three `Guid` objects, roughly 100 bytes. That is about 3 MB more for a full read of 30,000 movies, spread over the same pages.
- Pacing: none needed, because there is no new request.
- TMDB: no request.

## Risks / Trade-offs

- [The folder ID is wrong, not the Plex match] → The reason tells the user to look, and nothing changes in Plex without a manual Fix match. Stop trying dismisses one item. `ignore-reasons: [mismatched]` mutes the reason everywhere.
- [A request is in flight when a read applies its listing] → The event of a request is written only after Plex answers. A request that Plex accepted but whose answer has not arrived, or whose answer was lost, is not yet an accepted event, so an older listing that shows the movie complete can still delete its row and record a spurious Fixed. A follow-up check can likewise read the old metadata that Plex still serves after it accepted a match, and complete the item. The next full read adds the row again. This race exists on `main` for every reason. Closing it needs per-request markers, a fence for their pruning, a typed outcome of each write, re-reads of skipped items, and checks that confirm the new match, so it is planned as its own change: see #161.
- [Many movies are flagged after the first full read] → They send no request and only fill Attention. The reason filter and Stop trying handle them. The README says so.
- [Plex returns a sparse or odd `Guid` list] → The comparison is undecided and keeps the old bit. It cannot cause a false Fixed event.
- [Multi-version movies whose folders name different IDs] → Two values of one source are not compared, so the result is undecided.
- [A movie with only `mismatched` logs "Item too young to refresh" while it is newer than `minimum-age`] → Accepted. The line is debug only, and it ends once the movie is older.
- [Rollback to an older binary] → An older binary does not know bit 64 and keeps it on movie rows as a non-item bit, so it refreshes those rows with the usual backoff. Its config validation also rejects `mismatched` in `ignore-reasons`. Remove that entry and stop the affected items before a rollback. The next read of a newer binary sets the bit correctly again.

## Migration Plan

- No database migration. The `reasons` column is an integer, and the new bit is `1 << 6`.
- Deploy as usual. Recently changed movies get the reason at the next incremental read, and all movies at the next full read, which "Full cache refresh, then run" starts at once.
