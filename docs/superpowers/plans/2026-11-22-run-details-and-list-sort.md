# Run details, list sort, match check, and next run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the items of each run in a drawer, sort the Items list view,
mark a manually matched item fixed at once, and show the next scheduled run.

**Architecture:** Four independent deliveries, each on its own branch from
`main` with its own PR. Events get a nullable run ID that the refresher
sets during a run. A new API route returns a run with its events. The
frontend adds a run drawer, a list sort control, and a next-run line. The
refresher checks an item after Plex accepts a manual match.

**Tech Stack:** Go, SQLite, React, TypeScript, TanStack Query, Astryx.

**Spec:** `docs/superpowers/specs/2026-11-22-run-details-and-list-sort-design.md`

## Global Constraints

- Follow `AGENTS.md` and `web/AGENTS.md`: Astryx components and tokens only,
  no own CSS, no raw `div`; URLs with `net/url`; zerolog fields; cognitive
  complexity 15 or less; no `//nolint`.
- Tests need no network: `httptest`, fakes, and a temporary SQLite store.
- Before each push, run in order: `mise run vendor`, `mise run test`,
  `mise run lint`, `mise run web-install`, `mise run web-lint`,
  `mise run web-test`, `mise run test-webui`. After a `web/` change also run
  `pnpm exec astryx doctor --built .` in `web/`.
- Conventional Commit subjects with a scope, at most 72 characters, and the
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` trailer.
- Update `README.md` when user-facing behavior changes.
- Branches, all from `origin/main`:
  `feat/106-check-after-match`, `feat/107-next-run`,
  `feat/105-items-list-sort`, `feat/104-run-details` (holds the spec and
  this plan).

## Review Focus

- A run whose row retention removed while it still ran, and that
  `FinishRun` restored: its events must still be written, not fail on the
  foreign key.
- A run from before the upgrade: the drawer says that its items were not
  recorded, and does not show an empty "nothing happened" state.
- A match check that keeps failing or that Plex answers with 404: the match
  request still answers 202, and the item shows the recorded match.
- A `nextRun` that the page clock passed before the scheduler published the
  next time: the page shows "starting", not a negative time. A scheduled
  start that a busy run skips does not run, so the scheduler publishes the
  following time at once, and the page shows that time.
- A deep link to `#/runs/<id>` of a removed or unknown run: the drawer shows
  "This run no longer exists", and closing returns to `#/runs`.

---

### Task 1 (#106): Check the item after an accepted manual match

**Files:**
- Modify: `internal/refresher/manual.go` (`manualRequest`, `CheckItem`)
- Test: `internal/refresher/manual_test.go`
- Modify: `README.md` (Fix match section)

**Interfaces:**
- Produces: `var matchCheckDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}`
- Produces: `func (r *Refresher) checkHeld(ctx context.Context, item manualItem) (complete bool, err error)`:
  the body of `CheckItem` after admission. `CheckItem` calls it.

- [ ] **Step 1: Write the failing tests** in `manual_test.go`:
  - `TestApplyMatchChecksUntilComplete`: the fake Plex `Item` answers an
    unmatched item without summary on the first call and a complete item on
    the second. `ApplyMatch` returns nil. The recorded sleeps are
    `[1s, 2s]`. The row is gone from the attention list, and the item
    timeline has a `completed` event with `FixedMatch == store.ActionMatch`.
  - `TestApplyMatchCheckFailureKeepsMatch`: `Item` always returns an
    error. `ApplyMatch` returns nil after three sleeps, and the timeline
    still has the accepted match request.
  - `TestApplyMatchFailedSkipsCheck`: `matchErrs` makes the match fail.
    There is no `Item` call and no sleep.
- [ ] **Step 2:** Run `go test ./internal/refresher -run TestApplyMatch`.
  Expected: FAIL.
- [ ] **Step 3: Implement.** In `manualRequest`, after an accepted
  `ActionMatch`, loop over `matchCheckDelays`: `r.sleep(ctx, d)`, then
  `checkHeld`. Stop at `complete`, at a gone item, or when ctx ends. Log a
  failed try with `log.Warn()` and continue. Return the match result only.
- [ ] **Step 4:** Run `go test ./internal/refresher`. Expected: PASS.
- [ ] **Step 5:** Update the README Fix match text. Run the checks. Commit
  `feat(refresher): check an item after a manual match`.

### Task 2 (#107): Carry and show the next scheduled run

**Files:**
- Modify: `internal/web/status.go` (`NewStatus`, `StatusView`, snapshot)
- Modify: `internal/web/server.go:129`, `cmd/autorefresh/main.go:137`
- Test: `internal/web/status_test.go`
- Modify: `web/src/api.ts` (`Status.nextRun?: string`), `web/src/status.tsx`,
  `web/src/runspage.tsx`, `web/src/format.ts` (+ `format.test.ts`)
- Modify: `README.md`

**Interfaces:**
- Produces: `func NewStatus(servers []string, tmdb bool, next func(time.Time) time.Time) *Status`;
  `StatusView.NextRun time.Time` with tag `json:"nextRun,omitzero"`.
- Produces: `export function nextRunText(nextRun: string | undefined, now: number): string | undefined`
  in `format.ts`: `undefined` without a time, `'starting'` for a time at or
  before now, otherwise `in 46m · 23:00` (reuse the relative and time
  formatters of `format.ts`).
- Produces: `export function NextRun()` in `status.tsx`, used by both pages.

- [ ] **Step 1: Write the failing tests:** `TestStatusNextRun` (a fixed
  `next` func gives `NextRun` in the snapshot; nil gives a zero time that
  the JSON omits); `nextRunText` cases in `format.test.ts`: none, past,
  45m ahead.
- [ ] **Step 2:** Run `go test ./internal/web -run TestStatusNextRun` and
  `pnpm exec vitest run src/format.test.ts`. Expected: FAIL.
- [ ] **Step 3: Implement.** `main.go` passes `cfg.RunSchedule.Next`. The
  snapshot computes `next(now)` with the clock that `Status` uses.
  `NextRun` renders a supporting `Text` with a clock icon, beside the TMDB
  state on Status and under the Runs heading; `useNow` drives the count.
- [ ] **Step 4:** Run the tests. Expected: PASS.
- [ ] **Step 5:** Update the README Status section. Run the checks. Commit
  `feat(dashboard): show the next scheduled run`.

### Task 3 (#105): Sort control in the Items list view

**Files:**
- Modify: `web/src/table.ts` (+ `table.test.ts`), `web/src/items.tsx`
- Modify: `README.md`

**Interfaces:**
- Produces in `table.ts`:
  `export const sortKeys: Record<TabName, readonly { key: string; label: string }[]>`
  with attention `title/Item, tries/Tries, last-attempt/Last action, due/Next due`,
  stopped `title/Item, tries/Tries, stopped/Stopped`,
  fixed `title/Item, completed/Completed`;
  `export function parseSort(sort: string): { key: string; desc: boolean }`
  and `export function formatSort(key: string, desc: boolean): string`
  (`''` for an empty key, `-key` when desc).
- `ItemTable` sets `sortable` from `sortKeys[tab]`, so the two modes share
  one list.

- [ ] **Step 1: Write the failing tests** in `table.test.ts`:
  `parseSort('-tries')` is `{ key: 'tries', desc: true }`; `parseSort('')`
  is `{ key: '', desc: false }`; `formatSort('due', false)` is `'due'`;
  `formatSort('', true)` is `''`; every sortable column key of the table
  columns is in `sortKeys`.
- [ ] **Step 2:** Run `pnpm exec vitest run src/table.test.ts`. Expected: FAIL.
- [ ] **Step 3: Implement.** In list mode, `ItemsHeader` shows a
  `Selector` "Sort" (first option "Default order") and, with a key, a
  direction `Button` (arrow icon, label "Ascending"/"Descending"). Both set
  the same `sorts[tab]` state that the table uses.
- [ ] **Step 4:** Run `mise run web-lint`, `mise run web-test`, and
  `pnpm exec astryx doctor --built .` after `mise run web-build`.
  Expected: PASS.
- [ ] **Step 5:** Update the README Items section. Commit
  `feat(dashboard): sort the items list view`.

### Task 4 (#104): Link events to their run

**Files:**
- Create: `internal/store/migrations/12_event_run.sql` (`11_run_reads.sql` of #107 holds version 11) and `13_run_events_linked.sql`
- Modify: `internal/store/history.go`, `internal/store/item.go`
  (`Read.RunID`), `internal/store/run.go`
- Modify: `internal/refresher/refresher.go` (store interface),
  `refresh.go`, `read.go`, `decision.go`
- Test: `internal/store/history_test.go`, `internal/store/run_test.go`,
  `internal/refresher/history_test.go`

**Interfaces:**
- Migration: `ALTER TABLE event ADD COLUMN run_id INTEGER REFERENCES run (id) ON DELETE SET NULL;`
- Migration: `ALTER TABLE run ADD COLUMN events_linked INTEGER NOT NULL DEFAULT 1;` and `UPDATE run SET events_linked = 0;`, so older runs say that they linked no events.
  `CREATE INDEX event_run ON event (run_id);`
- Produces: `RunID int64` (0: none) on `store.Request`, `store.MatchDecision`,
  `store.Read`, and `store.Event`.
  `RecordGone(server, ratingKey string, at time.Time, source Source, runID int64) error`.
- `insertEvent` writes `(SELECT id FROM run WHERE id = ?)`, as it does for
  `fixed_match_event_id`, so a missing run row stores NULL.
- Produces: `func (s *Store) Run(ctx context.Context, id int64) (Run, bool, error)`
  and `func (s *Store) RunEvents(ctx context.Context, runID int64) ([]Event, error)`,
  ordered `at ASC, id ASC`, without searches.

- [ ] **Step 1: Write the failing tests:** `TestRunEventsOfRun` (events of
  two runs and one without a run; `RunEvents` returns only the run's, in
  order); `TestEventRunMissingRow` (a `RunID` without a run row stores
  NULL and the write succeeds); `TestPruneRunKeepsEvents` (pruning the run
  keeps its events with run ID 0); `TestRunByID` (found and not found);
  in the refresher, `TestRunLinksEvents`: a run that refreshes one item and
  completes another writes request and completed events with the run's ID.
- [ ] **Step 2:** Run `go test ./internal/store ./internal/refresher`.
  Expected: FAIL.
- [ ] **Step 3: Implement** the migration, the fields, and the queries.
  `serverRun.id` flows into each `Request`, `MatchDecision`, `Read`, and
  `RecordGone` of `refresh.go`, `read.go`, and `decision.go`. Manual item
  actions keep 0.
- [ ] **Step 4:** Run `go test ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(store): link item events to their run`.

### Task 5 (#104): Run detail API

**Files:**
- Modify: `internal/web/runs.go`, `internal/web/server.go` (route)
- Test: `internal/web/runs_test.go`

**Interfaces:**
- Consumes: `Store.Run`, `Store.RunEvents`, `Store.RunItems`.
- Produces: `GET /api/runs/detail?id=<run>` answering
  `{ "run": runJSON, "events": [runEventJSON], "items": [runItemJSON] }`.
  `runEventJSON` embeds `eventJSON` and adds `key`, `title`, `year`,
  `type`, and `thumb` (from `imageURL("thumb", srv, key, e.Thumb)`).
  An unknown ID, or a run of a server that is not configured, answers 404
  `no such run`. A bad ID answers 400 through `runID`.

- [ ] **Step 1: Write the failing tests:** detail of a run with events
  (fields, order, redacted error); a bulk job with items; 404 for an
  unknown ID and for an unconfigured server; 400 for `id=x`.
- [ ] **Step 2:** Run `go test ./internal/web -run TestRunDetail`.
  Expected: FAIL.
- [ ] **Step 3: Implement** `serveRunDetail` with the store reads first
  and the JSON write after, so no rows stay open across the write.
- [ ] **Step 4:** Run `go test ./internal/web`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(web): serve the details of a run`.

### Task 6 (#104): Run drawer

**Files:**
- Modify: `web/src/route.ts` (+ `route.test.ts`), `web/src/api.ts`,
  `web/src/runspage.tsx`, `web/src/status.tsx` (`RunRow` link)
- Create: `web/src/rundetail.tsx`, `web/src/runs.test.ts` cases in the
  existing `runs.ts` if grouping lives there
- Modify: `README.md`, `AGENTS.md` (`web/` row: the run drawer and route)

**Interfaces:**
- Route: `{ page: 'runs'; run?: number }`, hash `#/runs/<id>` with the
  `?srv=` query; `parseRoute('#/runs/12?srv=1')` is
  `{ page: 'runs', run: 12, srv: 1 }`.
- Types: `RunDetail { run: RunRecord; events: RunEvent[]; items: RunItem[] }`,
  `RunEvent extends TimelineEvent { key: string; title?: string; year?: number; type?: string; thumb?: string }`.
- Produces in `runs.ts`: `export function runGroups(detail: RunDetail): { id: string; title: string; events: RunEvent[] }[]`,
  groups in this order and with these titles: `Refreshed`, `Matched`,
  `Failed`, `Fixed`, `Completed by Plex`, `Removed`; match decisions join
  the group of the request of the same item, or `Failed` when no request
  followed. Empty groups are left out.
- Query key `['runs', 'detail', id]`, so the live status refreshes it.

- [ ] **Step 1: Write the failing tests:** route parse and hash round trip
  for `#/runs/12` with and without `srv`; `runGroups` for a request
  accepted (refresh and match), a failed request, a search failure, a
  completed event with and without `fixedAction`, and a gone event.
- [ ] **Step 2:** Run `pnpm exec vitest run`. Expected: FAIL.
- [ ] **Step 3: Implement** `RunDrawer` in `rundetail.tsx`: Astryx
  `Drawer` (`width={600}`, `padding={0}`, label "Run details") as in
  `items.tsx`. The header shows the kind, status dot, start time, duration,
  server with several servers, and error. The counts follow. Then one
  section per group with `List` rows: `Poster` size `sm`, title and year,
  `eventView(...).detail`, and a link to `itemHash('attention' or 'fixed',
  …)`. A bulk job lists `items` grouped by outcome. Empty states: "Item
  details were not recorded for this run" for a run from before runs
  linked their events (`eventsLinked` false), also with zero counts, and
  with "Their history expired." for a newer run with no events whose
  outcome counts (refreshed, matched, failed, completed; `selected` for a
  bulk job) are not all zero. The read and due counts do not count. "No
  item changed in this run" otherwise, and "This run no longer exists" on
  404. `noEntries` in `runs.ts` holds these rules. Run table rows open with click, Enter, and
  Space (the row plugin of `items.tsx`, moved to a shared module), and list
  rows link to `#/runs/<id>`.
- [ ] **Step 4:** Run `mise run web-lint`, `mise run web-test`,
  `mise run test-webui`, and `pnpm exec astryx doctor --built .`.
  Expected: PASS.
- [ ] **Step 5:** Update README and AGENTS.md. Commit
  `feat(dashboard): show the items of a run`.
