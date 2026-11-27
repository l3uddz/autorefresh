# Tasks

## 1. Read the external IDs from Plex

- [x] 1.1 Add `includeGuids=1` to movie listings and to the item metadata read. Decode the `Guid` list into `ExternalIDs` on `plex.Item` and `plex.Details`; a `Guid` value that does not decode gives no IDs. Record a redacted movie listing with `Guid` entries in `internal/plex/testdata/`, with no private values. Add tests that check the query of movie and show listings and of the metadata read, the decoded IDs, and the odd `Guid` shape. Verify with `go test ./internal/plex/`. Commit: `feat(plex): read the external IDs of movies`.

## 2. Store the reason and the queue state

- [x] 2.1 Add the reason `mismatched` (`1 << 6`) to `ItemReasons` and the reason names, and add `ManualReasons`. Add `Item.Keep` and apply it in `readKey.reasons`. Exclude `ManualReasons` in `Due`, and add the `manual` state to `DueRules.state` after `ignored` and before `too-new`, with no next due time. Keep `CountIncomplete` and the attention lists unchanged. Add store tests for the scenarios of the library-scan and refresh-runs deltas: keep on undecided, clear on agree with a completed event, no reason for a new undecided row, the `manual` state and its order, `Due` exclusion, attention counts, and `reason=mismatched`. Add a config test that `ignore-reasons: [mismatched]` decodes. Update the `ignore-reasons` values in `README.md`. Verify with `go test ./internal/store/ ./internal/config/`. Commit: `feat(store): add the mismatched reason and the manual queue state`.

## 3. Flag wrong movie matches in reads and checks

- [x] 3.1 Add `CompareMovieIDs` in `internal/refresher/wrongmatch.go`, reusing `parseIDs` and a `normalizeIDValue` helper split out of `parseID`. Set the reason or the `Keep` bit in reads and in the check of `checkHeld` through `storeItem`, and log the saved reasons in `Checked item`. Log `Wrong match` at debug level through `logWrongMatches` from the item loop of `readLibrary`. Skip a row in `recheck` that has only `ManualReasons` left. Add tests for each scenario of the library-scan and manual-actions deltas, including normalized IDs, other Plex schemes, malformed Plex IDs, duplicate Plex IDs, the fixed source order, one agreeing source, two IDs of one source, unmatched movies, a manual match that clears the reason, a refresh that keeps it, and the recheck skip of the refresh-runs delta. Add the feature and reason to `README.md` (Features, the reason list, and a note on the first full read), and update the `internal/refresher` and `internal/plex` rows of `AGENTS.md`. Verify with `go test ./internal/refresher/ ./cmd/autorefresh/`. Commit: `feat(refresher): flag movies whose path IDs disagree with Plex`.

## 4. Show the comparison in the live Plex view

- [x] 4.1 Add `externalIds` and, for matched movies, `mismatch` to `GET /api/item/live` through `refresher.CompareMovieIDs` with `d.GUID` and `d.Paths.Files`. Add handler tests for the dashboard-api scenarios, including an unmatched movie without `mismatch` and the source order, with `includeGuids=1` checked on the recorded Plex request. Add the `manual` state and the new reason filter to the handler tests of `GET /api/items`. Update the `internal/web` row of `AGENTS.md`. Verify with `go test ./internal/web/`. Commit: `feat(web): show the wrong match result in the live Plex view`.

## 5. Show wrong matches in the dashboard

- [x] 5.1 Add the `manual` state and the live fields to `api-types.ts`, the state text to `format.ts`, the reason option to `items.tsx`, the `pink` badge to `media.tsx`, and the warning banner without a button to `DetailBody` in `detail.tsx`, using Astryx components and tokens only. Add Vitest tests for the state text, the reason label in the badge and the table row, and the warning with and without a live view and for a non-`mismatched` result. Describe the badge, state, and warning in the Dashboard section of `README.md`. Verify with `mise run web-lint` and `mise run web-test`, and with `pnpm exec astryx doctor --built .` in `web/` after `mise run web-build`. Commit: `feat(web): show wrong movie matches in Items and the item detail`.

## 6. Full checks

- [x] 6.1 Run `mise run vendor`, `mise run test`, `mise run lint`, `mise run web-install`, `mise run web-lint`, `mise run web-test`, and `mise run test-webui` in order, then `pnpm exec astryx doctor --built .` in `web/`. All pass, and `go.mod` and `go.sum` have no change. Check that `openspec validate detect-wrong-movie-matches --strict` passes. No commit unless a check needs a fix, which then gets its own `fix(<scope>)` commit.

## Workflow follow-up

- Live acceptance (maintainer): after deploy, run "Full cache refresh, then run", then check a few flagged movies in Plex. Send no fix match without the maintainer's approval.
- Archive: sync the delta specs into `openspec/specs`, run `openspec validate --all --strict`, and archive the change.
