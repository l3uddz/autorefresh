# Stage 5 dashboard match workflow and run history

Issue #102 delivers Stage 5 of #94. The parent branch is `main`. Integrate the
Stage 4 dashboard from PR #101 through `main` before editing the frontend.
Use the approved maintenance dashboard design and its mockups for layout,
match evidence, confirmation, and phone behavior.

## 1. Match search and evidence

- Extend the existing API types in `web/src/api.ts` for candidate searches and
  the match decision evidence returned in the item timeline. Check property
  names against `internal/web/actions.go` and `internal/web/item.go`.
- In a match dialog opened from `ItemDetail`, load the opening search with
  `GET api/item/matches?srv=N&key=K`. Keep that evidence while a separate
  typed title, title and year, or external ID search is submitted. Abort
  stale reads on item changes or dialog close. Use the existing byte-length
  validation and validate an optional positive four-digit-or-shorter year.
- Group candidates by Plex GUID. Preserve the searches that found each one.
  Render poster, title, year, type, summary, score, folder IDs, file paths,
  year comparison, previous automatic evidence, and the current live GUID
  when available. Link IDs through their API-provided URLs or fixed provider
  origins. Treat every Plex field as text or an image source.
- Expose episode comparison only for a show candidate with evidence of a
  TMDB ID. Send the bodyless `POST api/item/episode-check?srv=N&key=K&tmdb=ID`
  and display the result without applying a match.
- Use the existing confirmation and toast helpers. Freeze the item and
  candidate at confirmation and submit `{guid,name,year}` to
  `POST api/item/match?srv=N&key=K`. Disable applying the known current match,
  prevent duplicate submissions, and reconcile the item and lists when
  the request settles. Plex applies its own refresh on acceptance.

Test evidence grouping, current-match detection, search input boundaries,
and candidate-to-TMDB association with Vitest. In a fixture browser, exercise
the opening and typed searches, canceled confirmation, episode comparison,
one accepted match, and a failed apply.

## 2. Item timeline

- Extend `web/src/timeline.ts` with presentation helpers, and use a shared
  component for the full timeline in item detail and the automatic decision
  panel in the match dialog.
- Show timestamps, source, action, accepted or failed outcome, completion or
  removal, and the recorded search/candidate evidence and decision reason.
  Use the API's redacted error text. Preserve newest-first order and the
  existing lifecycle rules. Use an empty state when there is no automatic
  match decision to explain.

Test manual and scheduled entries, removed and completed items, an empty
decision panel, and a decision with multiple searches and candidates.

## 3. Runs page

- Add `#/runs` to `web/src/route.ts` and the app navigation. Retain the optional
  global `srv` scope and hide the server column on single-server installs.
- Read `GET api/runs` with TanStack Query infinite paging. Pass the cursor
  from the previous response and reset pages when scope changes. Keep the
  existing `['runs', …]` invalidation convention.
- Render start, duration, server, source, kind, status, items read,
  refreshed, matched, failed, fixed, due, and error. Display a live or
  unfinished run without inventing a completion time. Provide distinct
  loading, error, empty, and load-more states.

Test route round trips and invalid scope handling, then use the fixture to
verify cursor paging, server changes, and live invalidation.

## 4. Phone layouts and verification

- Keep Astryx components and theme tokens. Verify the Items reading pane,
  item detail, match evidence, and Runs table at phone and desktop widths.
  Keep action buttons accessible and preserve scope and selection when a
  drawer or dialog closes.
- Run `mise run vendor`, `test`, `lint`, `web-install`, `web-lint`,
  `web-test`, and `test-webui`. Run Astryx doctor in `web/`.
- Run a Browserless fixture under a proxy sub-path. Verify scoped navigation,
  the match workflow, the timeline, paginated runs, and phone overflow.
  Keep isolated fixture results separate from live Plex acceptance.
- Have Opus implement the frontend and Gemini review it before opening the
  integration PR for CodeRabbit. Record Stage 5 limitations and keep
  issue #94 open if any required dashboard acceptance remains outstanding.
