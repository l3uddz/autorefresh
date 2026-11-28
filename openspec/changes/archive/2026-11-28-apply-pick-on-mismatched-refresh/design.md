# Design

## Context

Refresh now (`RefreshItem` in `internal/refresher/manual.go`) runs the automatic fix match through `manualMatch` only when `seeksMatch` holds, that is, for an unmatched item whose reason `unmatched` is not ignored. `manualMatch` reads the item with `ItemWithPaths` (`preCheckUnmatched`), runs `autoMatch`, records the decision, and re-reads the item just before the match (`keepMatch`). Both reads accept only an item that Plex still shows unmatched. `applyMatch` then sends the match, and `settleMatch` follows it up with a refresh and checks that confirm the GUID of the pick.

A run uses `seeksMatch` too (`findMatch` in `refresh.go`). A check sets or clears `mismatched` through `flagWrongMatch`, which calls `CompareMovieIDs` with the GUID, the files, and the external IDs of the item. Every item read asks Plex for the external IDs, so the reads of `manualMatch` already have what the comparison needs.

## Goals / Non-Goals

**Goals:**

- Refresh now applies the automatic pick to a mismatched movie, with the same decision rules, records, and follow-up as for an unmatched item.
- It never sends a match that only repeats the current match.
- It never replaces a match that changed during the search.

**Non-Goals:**

- No change to runs. `findMatch` keeps `seeksMatch`.
- No change to the decision rules of `autoMatch`, and no new Plex or TMDB request type.
- No change to the item detail API or its DTOs.

## Decisions

### A separate predicate for Refresh now

Add `refreshSeeksMatch(it)` beside `seeksMatch`. It holds for an item that `seeksMatch`, or for a movie with the reason `mismatched` whose reason is not in `ignore-reasons`. `manualMatch` calls it. `findMatch` keeps `seeksMatch`, so runs keep their behavior. An item never has both reasons, because a `local://` item is not compared.

Alternative: widen `seeksMatch`. This would also change runs, which the issue rules out.

### One match state for both reads

The first read and the read just before the match decide whether the item still needs the match. Generalize them with one small struct, `matchState{unmatched bool; guid string}`, taken from the first read. The flag and the GUID are separate fields, so an empty GUID can never pass as the unmatched state:

- unmatched item: `unmatched` is true. Both reads require a `local://` GUID, as today.
- mismatched movie: `guid` holds the current GUID. The first read requires a GUID that is not empty, and both reads require that `CompareMovieIDs` with the live GUID, files, and external IDs gives `mismatched`. The second read also requires the same GUID as the first.

One method of `matchState` answers whether a live read still needs the match, so `preCheck` and `keepMatch` share the rule.

`preCheckUnmatched` becomes `preCheck`, which returns the paths and the state, and logs `Skipped fix match` with the reason `no longer unmatched` or `no longer mismatched`. `keepMatch` takes the state and checks it the same way. Both keep their current failure handling: a failed first read records a failed decision, unless it is a 404, and a failed second read sends no match.

### A pick that is the current match

After `autoMatch` picks a candidate for a mismatched movie, `manualMatch` compares the GUID of the pick with the GUID of the state. When they are equal, it logs `Skipped fix match` with the reason `already matched` and returns no match. The decision is still recorded, as for an unmatched item whose match `keepMatch` drops. This covers a path ID that is outdated while Plex already has the right entry. The next Refresh now ends the same way, so it never sends a loop of matches.

### Follow-up and the reason

Nothing changes after the match. `settleMatch` sends the refresh and runs the checks, and each check saves only when Plex shows the GUID of the pick. Through `flagWrongMatch`, a check clears `mismatched` when a compared source now agrees. Without another reason, the check moves the movie to Fixed. When the new entry still disagrees with the path, the movie keeps `mismatched`. Refresh now then picks the current match next time and sends only a refresh.

### Dashboard texts

`fixmatch.tsx` changes the preview help text, `wrongmatch.tsx` changes the warning text, `detail.tsx` changes the tooltip of Refresh now, which no longer says that it acts as a scheduled run does, and `history.tsx` adds Refresh now to the empty state of the decision panel. All four follow the texts of the specs. No component or layout changes. The README sections on wrong matches say that Refresh now applies the automatic pick.

## Load on Plex and TMDB

The change adds requests only when the user clicks Refresh now on a mismatched movie. Runs send nothing new.

- 1 item read before the search (`ItemWithPaths`).
- 1 Plex match search for each path ID, at most 3, as for an unmatched item. TMDB is asked only by the existing rules: a fallback when no path search found a candidate, or a year confirmation.
- With a pick that differs from the current match: 1 item read just before the match, 1 match, 1 refresh, and up to 3 checks after waits of 1 s, 2 s, and 4 s.
- Without such a pick: the plain refresh and its checks, as today.

Refresh now holds the item lock, as before, so one item gets at most one such sequence at a time. The search keeps its limit of 45 s.

## Risks / Trade-offs

- [Risk] A Plex search returns a wrong candidate for a path ID. → The pick follows the same rules as for an unmatched item: the searches of all path IDs must agree, and year conflicts must be confirmed. The user starts each match with a click, and the history records the decision and the request.
- [Risk] Plex changes the match during the search. → The read just before the match requires the same GUID and a disagreement that still holds.
- [Trade-off] A pick that equals the current match records a decision that applies nothing. The decision panel shows the pick as for any matched decision. Only the log line `Skipped fix match` with the reason `already matched` says why, and the Fix match dialog marks the pick as "currently matched". This matches today's handling of a match that `keepMatch` drops.
- [Trade-off] The reason `mismatched` stays a reason that runs never act on. A movie whose only reason is `mismatched` still waits as "Needs a manual fix" until the user acts.
