# Run "now" panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** While a run is busy, the server card shows the item in work or the
next item with a countdown, and the last finished item with its outcome.

**Architecture:** `refresher.Progress` reports full item details
(`ProgressItem`) at start and a new `ItemNext` before each pace wait.
`web.Status` keeps `current`, `next` + `nextAt`, and `last` (+ outcome and
finish time) on `RunProgress`, with proxy thumb URLs and reason names. The
frontend renders a `NowPanel` in `RunLive`.

**Tech Stack:** Go, zerolog, React, TypeScript, Astryx, TanStack Query,
Vitest.

**Spec:** `docs/superpowers/specs/2026-11-23-run-now-panel-design.md`
(mockup: `docs/superpowers/specs/2026-11-23-run-now-panel-mockups/now-panel.html`)

## Global Constraints

- Astryx components and tokens only, no own CSS files or raw `div` styling (`web/AGENTS.md`).
- Each production Go function: cognitive complexity ≤ 15 (`mise run lint`).
- The status never carries a raw Plex image path: thumbs go through `imageURL("thumb", srv, key, thumb)` (`internal/web/items.go:353`).
- `Snapshot` stays a deep copy: copy each item pointer too.
- The in-work badge says "Working". Between items: "Up next" and `in Ns`.
- Commit subjects: Conventional Commits, scope required, ≤ 72 chars, with the `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` trailer.

## Review Focus

1. A run whose last item finished but that has not ended (summary, settle): top row hidden, footer shows `last`. Test in Task 3.
2. A countdown past `nextAt` (a slow clock or a late event): shows `now`, never `in -2s`. Test in Task 3.
3. Item without a thumb, year, library, or reasons: no proxy URL, no empty `·` separators. Tests in Tasks 2 and 3.
4. `ItemDone` without a preceding `ItemStarted` (existing `TestStatusTracksRun` calls it this way): `last` gets the key and outcome only, and nothing panics. Test in Task 2.
5. A dry run and the first item of a batch: no `ItemNext`. Test in Task 1.

---

### Task 1: Refresher reports full items and the next item

**Files:**
- Modify: `internal/refresher/progress.go`, `internal/refresher/refresh.go`, `internal/refresher/jobs.go`
- Modify (compile only): `internal/web/status.go`
- Test: `internal/refresher/progress_test.go`, `internal/refresher/refresh_test.go`, `internal/refresher/jobs_test.go`, `internal/refresher/decision_test.go`

**Interfaces:**
- Produces, in `progress.go`:
  ```go
  // ProgressItem is an item as the live status shows it.
  type ProgressItem struct {
      RatingKey, Title, Type, Library, Thumb string // Thumb: the Plex image path
      Year    int
      Reasons store.Reasons
  }
  type Progress interface {
      RunStarted(id int64, start store.RunStart)
      LibraryRead(server, library string)
      Batch(server string, selected, due int)
      ItemNext(server string, item ProgressItem, at time.Time) // before the pace wait for item
      ItemStarted(server string, item ProgressItem)
      ItemDone(server, ratingKey, outcome string, counts Counts)
      RunFinished(server string, res store.RunResult, err error, recorded bool)
  }
  func progressItem(it store.Item, library string) ProgressItem
  ```

- [ ] **Step 1: Update `recordingProgress`** in `progress_test.go`: `ItemStarted` records `"ItemStarted <server> <key> <title>"` (unchanged text, so existing `wantProgress` calls stay valid), plus a `started []ProgressItem` slice. `ItemNext` records `"ItemNext <server> <key> <at RFC3339>"`. `itemCalls` keeps returning only `ItemStarted` and `ItemDone`; add `nextCalls()` for `ItemNext`.

- [ ] **Step 2: Write failing tests**
  - `TestRunReportsNextItemBeforePace` (refresh_test.go): two due items in library "Movies" (seed with `Year: 1999`, reasons `store.MissingSummary` or the existing reason constant), non-dry run. Assert `p.nextCalls() == ["ItemNext home <key2> <f.now + refreshDelay>"]`, the `ItemNext` comes after `ItemDone <key1>` and before `ItemStarted <key2>` in `p.calls`, and `p.started[0]` equals `ProgressItem{RatingKey: key1, Title: ..., Type: "movie", Library: "Movies", Year: 1999, Reasons: ...}`.
  - `TestDryRunReportsNoNextItem`: same seed, `DryRun = true` → `len(p.nextCalls()) == 0`.
  - `TestBulkReportsLibraryOfItem` (jobs_test.go): a bulk refresh of two cached items in "Movies" → `p.started[i].Library == "Movies"`, and one `ItemNext` for the second item.

- [ ] **Step 3: Run** `go test ./internal/refresher/ -run 'NextItem|ReportsLibrary'` — expect FAIL (compile errors).

- [ ] **Step 4: Implement**
  - `progress.go`: the types above; `noProgress.ItemNext`; `progressItem` (reads `it.Type`, `it.Year`, `it.Thumb`, `it.Reasons`).
  - `refresh.go`: `refreshBatch` calls `r.progress().ItemNext(server, progressItem(items[i], run.library(items[i].Section).Title), r.now().Add(r.opts.RefreshDelay))` only when `pace` will sleep (i > 0 and not dry run). Pass the decision through by splitting `pace` or by checking the same condition in one helper `paceNext(ctx, run, item, i)`; do not duplicate the condition. `runItem` calls `ItemStarted(server, progressItem(batch, run.library(batch.Section).Title))`.
  - `jobs.go`: `bulkAdmission` gets `shown ProgressItem`. `prepareBulk` reads `j.r.store.Libraries(context.Background(), server)` once into a section→title map and sets `shown: progressItem(row, titles[row.Section])` (for an uncached row: `ProgressItem{RatingKey: key, Title: row.Title}`). `bulkBatch` calls `ItemNext(..., a.shown, r.now().Add(RefreshDelay))` right before its `pace` call and `ItemStarted(server, a.shown)`.
  - `internal/web/status.go`: change the signatures only: `ItemStarted(server string, item refresher.ProgressItem)` sets `run.Current = item.Title`; `ItemNext` is a no-op for now. Update `status_test.go` calls to `st.ItemStarted("two", refresher.ProgressItem{RatingKey: "1", Title: "Alien"})`.

- [ ] **Step 5: Run** `go test ./internal/...` — expect PASS. Run `mise run lint` — expect no findings.

- [ ] **Step 6: Commit**
  ```bash
  git add internal/refresher internal/web/status.go internal/web/status_test.go
  git commit -m "feat(refresher): report item details and the next item to progress"
  ```

### Task 2: Status keeps current, next, and last items

**Files:**
- Modify: `internal/web/status.go`
- Test: `internal/web/status_test.go`

**Interfaces:**
- Consumes: `refresher.ProgressItem`, `Progress.ItemNext` (Task 1); `imageURL`, `reasonNames` (`internal/web/items.go`).
- Produces (JSON of `RunProgress`, which Task 3 reads):
  ```go
  // LiveItem is an item of a run as the dashboard shows it.
  type LiveItem struct {
      Key        string    `json:"key"`
      Title      string    `json:"title,omitempty"`
      Year       int       `json:"year,omitempty"`
      Type       string    `json:"type,omitempty"`
      Library    string    `json:"library,omitempty"`
      Reasons    []string  `json:"reasons,omitempty"`
      Thumb      string    `json:"thumb,omitempty"` // proxy URL
      Outcome    string    `json:"outcome,omitempty"`    // last only
      FinishedAt time.Time `json:"finishedAt,omitzero"`  // last only
  }
  // RunProgress: remove `Current string`; add
  Current *LiveItem `json:"current,omitempty"`
  Next    *LiveItem `json:"next,omitempty"`
  NextAt  time.Time `json:"nextAt,omitzero"`
  Last    *LiveItem `json:"last,omitempty"`
  ```

- [ ] **Step 1: Write failing tests** in `status_test.go`
  - `TestStatusShowsNextCurrentAndLast`: server "two" at index 1. `ItemNext("two", ProgressItem{RatingKey: "1", Title: "Alien", Year: 1979, Type: "movie", Library: "Movies", Thumb: "/library/metadata/1/thumb/170", Reasons: <summary reason>}, at)` → `Run.Next` has `Thumb == imageURL("thumb", 1, "1", "/library/metadata/1/thumb/170")`, `Reasons == reasonNames(...)`, `NextAt == at`, `Current == nil`. Then `ItemStarted` with the same item → `Next == nil`, `NextAt.IsZero()`, `Current.Title == "Alien"`. Then `ItemDone("two", "1", "refresh", ...)` with a fixed clock → `Current == nil`, `Last` = the item with `Outcome: "refresh"` and `FinishedAt` set.
  - `TestStatusLastWithoutStart`: `ItemDone` with no `ItemStarted` → `Last == &LiveItem{Key: "5", Outcome: "gone", FinishedAt: ...}`.
  - `TestStatusItemWithoutThumb`: empty `Thumb` → `LiveItem.Thumb == ""`; zero reasons → `Reasons == nil` (omitted in JSON).
  - `TestStatusSnapshotCopiesItems`: change `Snapshot().Servers[1].Run.Last.Title`, take a new snapshot, and it still has the old title.
  - Update `TestStatusTracksRun`: its `want` has no `Current`; assert `Last` of the final snapshot is `{Key: "6", Title: "Brazil", Outcome: "dry-run", ...}`; `RunFinished` clears `Current`, `Next`, `NextAt` on `Last` copy.

- [ ] **Step 2: Run** `go test ./internal/web/ -run Status` — expect FAIL.

- [ ] **Step 3: Implement** in `status.go`:
  - `Status` gets `now func() time.Time` (default `time.Now`; tests set it) for `FinishedAt`.
  - `liveItem(srv int, it refresher.ProgressItem) *LiveItem` uses `imageURL("thumb", srv, it.RatingKey, it.Thumb)` and `reasonNames` with nil for 0 reasons. Pass the server index from `updateRun` (extend the helper to hand the index, or look it up once in a new `updateRunAt(server, func(i int, run *RunProgress))`; keep one helper).
  - `ItemNext` sets `Next`, `NextAt`. `ItemStarted` sets `Current`, clears `Next`, `NextAt`. `ItemDone` moves `Current` (or `&LiveItem{Key: ratingKey}` when nil or when the keys differ) to `Last` with outcome and time, clears `Current`.
  - `Snapshot` copies `Current`, `Next`, `Last` pointees (and their `Reasons` slice) for both `Run` and `Last`.
  - `RunFinished` sets `last.Current, last.Next, last.NextAt` to zero.

- [ ] **Step 4: Run** `go test ./internal/web/` and `mise run lint` — expect PASS and no findings.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/web/status.go internal/web/status_test.go
  git commit -m "feat(web): keep the current, next, and last item of a run"
  ```

### Task 3: Dashboard now panel

**Files:**
- Create: `web/src/now.ts`, `web/src/now.test.ts`, `web/src/nowpanel.tsx`
- Modify: `web/src/api.ts` (`RunProgress`), `web/src/clock.ts`, `web/src/status.tsx` (`RunLive`)
- Modify: `AGENTS.md` (`web/` row: name `nowpanel.tsx` and `now.ts`; `internal/refresher/` row: `ItemNext` and `ProgressItem`; `internal/web/` row: `Status` keeps current, next, last), `README.md` if it describes the Status card

**Interfaces:**
- Consumes: the JSON of Task 2.
- Produces:
  ```ts
  // api.ts
  export interface LiveItem { key: string; title?: string; year?: number; type?: string; library?: string; reasons?: string[]; thumb?: string; outcome?: string; finishedAt?: string }
  // RunProgress: replace `current?: string` with
  current?: LiveItem; next?: LiveItem; nextAt?: string; last?: LiveItem;
  // now.ts
  export type OutcomeTone = 'green' | 'purple' | 'red' | 'gray';
  export function outcomeView(outcome: string | undefined): { text: string; tone: OutcomeTone };
  export function countdown(at: string | undefined, now: number): string; // "in 4s" | "now"
  export function itemDetail(item: LiveItem, withReasons: boolean): string; // "TV Shows · missing summary"
  // clock.ts
  export function useSecondClock(isActive: boolean): number; // 1 s ticks while active, own listener set
  ```

- [ ] **Step 1: Write failing tests** in `now.test.ts`
  - `outcomeView`: `refresh`, `accepted` → `{text: 'refreshed', tone: 'green'}`; `match`, `tmdb-match`, `title-match` → `matched`/`purple`; `refresh-failed`, `match-failed`, `tmdb-match-failed`, `title-match-failed`, `search-failed`, `failed` → `failed`/`red`; `skipped`, `stopped`, `changed`, `not-cached` → `skipped`/`gray`; `gone` → `gone`/`gray`; `dry-run` → `dry run`/`gray`; `undefined` or unknown `x` → `{text: 'x' or '', tone: 'gray'}`.
  - `countdown`: `nextAt` 4.2 s ahead → `in 5s` (ceil); 0 s or 3 s behind → `now`; `undefined` → `''`.
  - `itemDetail`: `{library: 'TV Shows', reasons: ['summary']}` → uses `label()` from `format.ts` the way the reason badges do → `'TV Shows · <label of summary>'`; no library and no reasons → `''`; `withReasons: false` drops reasons.

- [ ] **Step 2: Run** `pnpm exec vitest run src/now.test.ts` in `web/` — expect FAIL.

- [ ] **Step 3: Implement** `now.ts`, `useSecondClock` (an interval of 1000 ms that runs only while `isActive` and at least one listener exists; do not change `useNow`), and `NowPanel({ run, srv }: { run: RunProgress; srv: number })` in `nowpanel.tsx`:
  - Layout from the mockup: an inset container (muted background, border token, inner radius) with a top row — `Poster` `sm` (from `media.tsx`), title + year (`Text`), the `itemDetail` line with the library type icon (`FilmIcon`/`TvIcon`), and on the right a `Badge` ("Working", or "Up next" with the `countdown` under it) — and a footer after a divider: outcome icon in the tone colour, `Last` muted, title, year, `itemDetail(last, false)`, outcome text, and `relative`-style seconds on the right (`now`, `Ns ago`; use `useSecondClock`).
  - Top row shows `current`, else `next`; neither → top row hidden. No `last` → footer hidden. Neither → render nothing.
  - Reasons show only for `run.kind === 'run'`.
  - In `RunLive`: keep the reading-libraries text while `run.selected === 0`; otherwise render `<NowPanel>` instead of the `Now:` / "Waiting for the next item" text.

- [ ] **Step 4: Run** in `web/`: `pnpm exec vitest run`, `mise run web-lint`, `pnpm exec astryx doctor --built .` (after `mise run web-build`) — expect PASS and no findings.

- [ ] **Step 5: Commit**
  ```bash
  git add web/src AGENTS.md README.md
  git commit -m "feat(dashboard): show the now panel of a busy run"
  ```

### Task 4: Full checks

- [ ] Run the CI sequence from `AGENTS.md`: `mise run vendor`, `mise run test`, `mise run lint`, `mise run web-install`, `mise run web-lint`, `mise run web-test`, `mise run test-webui` — expect all PASS and `git status` clean apart from ignored files.
