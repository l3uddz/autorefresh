# Tasks

## 1. Refresh now fixes a mismatched movie

Commit: `feat(refresher): apply the automatic pick to a mismatched movie`

- [x] 1.1 Add `refreshSeeksMatch` beside `seeksMatch` in `internal/refresher/refresh.go`: an item that `seeksMatch`, or a movie with the reason `mismatched` that `ignore-reasons` does not list. Call it from `manualMatch`, and keep `seeksMatch` in `findMatch`.
- [x] 1.2 Replace `preCheckUnmatched` with `preCheck`, which returns the paths and a `matchState{unmatched bool; guid string}`. For a mismatched movie, require a GUID that is not empty, and require `CompareMovieIDs` with the live GUID, files, and external IDs to give `mismatched`. Otherwise log `Skipped fix match` with the reason `no longer mismatched` and send the plain refresh. Keep the current failure handling.
- [x] 1.3 In `manualMatch`, return no match for a mismatched movie whose pick has the GUID of the state, after the decision is recorded, and log `Skipped fix match` with the reason `already matched`.
- [x] 1.4 Make `keepMatch` check the state: still unmatched, or the same GUID and still `mismatched`. Keep the reasons `no longer unmatched` and `match state unknown`, and add `no longer mismatched`.
- [x] 1.5 Keep every function at a cognitive complexity of 15 or less, and update the doc comments of `RefreshItem`, `manualMatch`, `preCheck`, and `keepMatch`.
- [x] 1.6 Make the fake `checkedMetadata.ItemWithPaths` in `internal/refresher/manual_test.go` return `Files: a.item.Files`, as the Plex client does for a movie. Check `TestRefreshKeepsWrongMatch` and `TestCheckKeepsUndecidedWrongMatch` in `wrongmatch_test.go` against the new behavior. Add tests with the fake Plex server, one for each scenario of the `manual-actions` delta:
  - a pick applied, then refreshed and checked to Fixed;
  - a pick that is the current match;
  - no pick, because of a conflict;
  - a movie that is no longer mismatched at the first read;
  - `mismatched` ignored;
  - another GUID just before the match;
  - the same GUID just before the match, but no longer mismatched;
  - a mismatched movie without a GUID;
  - a failed read just before the match.

  Add a run test showing that a due movie with `mismatched` and `no-summary` still gets no match search.

## 2. Dashboard texts

Commit: `feat(web): name Refresh now as a fix for a mismatched movie`

- [ ] 2.1 Change the preview help text in `web/src/fixmatch.tsx`, the warning text in `web/src/wrongmatch.tsx`, the tooltip of Refresh now in `web/src/detail.tsx`, and the empty state of the decision panel in `web/src/history.tsx` to the texts of the `dashboard-ui` delta. Update `web/src/wrongmatch.test.tsx`, and add Vitest tests that assert the preview help text, the tooltip, and the empty state.

## 3. Documentation

Commit: `docs(readme): say that Refresh now fixes a mismatched movie`

- [ ] 3.1 Update the wrong match texts of `README.md`: Refresh now on a mismatched movie applies the automatic pick when it differs from the current match, and runs still never match such a movie. Update the `internal/refresher` row of `AGENTS.md` for the manual fix match of a mismatched movie.

## 4. Checks

- [ ] 4.1 Run the CI sequence of AGENTS.md: `mise run vendor`, `mise run test`, `mise run lint`, `mise run web-install`, `mise run web-lint`, `mise run web-test`, and `mise run test-webui`. Then run `pnpm exec astryx doctor --built .` in `web/`, and `openspec validate --all --strict`.
