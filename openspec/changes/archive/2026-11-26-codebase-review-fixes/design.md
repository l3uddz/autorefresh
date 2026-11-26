# Design

## Context

See `proposal.md` for motivation and scope, and the delta specs under `specs/` for the updated behavior contracts. All changes land in one PR for GitHub issue #149, split into focused Conventional Commits by area, while keeping every production Go function at a cognitive complexity of 15 or less (`gocognit`) and all dashboard UI work strictly on Astryx components and tokens (`web/AGENTS.md`).

## Goals / Non-Goals

**Goals:**
- Close every verified security, concurrency, correctness, and SQLite/UI performance gap from the codebase review.
- Reduce wasted Plex and TMDB API calls and lock contention without adding new external hosts or write request kinds.
- Keep all existing tests passing and add targeted unit tests (no network, no live Plex/TMDB) for every fix.

**Non-Goals:**
- Splitting the SQLite handle into two `*sql.DB` pools in this change unless benchmarks show single-handle contention after WAL + indexing (WAL + `synchronous=NORMAL` + the 4 new indexes + batch `KnownItems` already eliminate the fsync and full-scan bottlenecks while keeping `Store` simple).
- Adding new configuration flags to toggle WAL mode or concurrency caps.

## Decisions

### 1. SQLite WAL mode, indexes, and batch reads (`internal/store`)
- **WAL + `synchronous=NORMAL`**: Add `_pragma=journal_mode(WAL)` and `_pragma=synchronous(NORMAL)` in `dataSourceName`, and verify `journal_mode` is `wal` via `checkPragmaString` at open time.
  - *Alternative considered*: Keep rollback journal (`DELETE` + `synchronous=FULL`). Rejected because every write transaction does multiple fsyncs and blocks cross-process dry-run readers.
- **Schema migration `17_indexes.sql`**:
  - `CREATE INDEX event_completed_item ON event (server, rating_key, at) WHERE kind = 'completed';` (paired with dropping `LEFT JOIN library` in `countQuery` for `TabFixed`).
  - `DROP INDEX run_server; CREATE INDEX run_server_started ON run (server, started_at);`
  - `CREATE INDEX item_last_match_event ON item (last_match_event_id) WHERE last_match_event_id IS NOT NULL;`
  - `CREATE INDEX run_prune ON run (COALESCE(finished_at, started_at)) WHERE status != 'running';`
  - `DROP INDEX event_run; CREATE INDEX event_run ON event (run_id, at) WHERE run_id IS NOT NULL;`
- **Batch `KnownItems` & snapshot scanner**: Extract a shared 9-column snapshot query/scanner from `list.go` and add `KnownItems(ctx, server, keys)` that resolves up to 5,000 keys in one `ReadOnly: true` transaction instead of 5,000 sequential transactions in `Jobs.prepareBulk`.
- **Statement preparation in write loops**: Prepare repeated `INSERT`/`UPDATE` statements once per transaction inside `ApplyRead`, `FinishJob`, and `insertSearch` to reduce `modernc.org/sqlite` compilation overhead.
- **Data integrity guards**:
  - Pass `ctx` into `readItemContext`/`insertEventContext` inside `SetStopped`, and reject `at.IsZero()` before opening the transaction.
  - Preserve `prev.AddedAt` in `ApplyCheck` when `it.AddedAt.IsZero()`.
  - Clear `item.last_match_event_id` for events in `(server, section)` inside `prune()` before `DELETE FROM library`.
  - Pass `events_linked = 0` in `restoreRun` because retention's `ON DELETE SET NULL` already unlinked the deleted run's events.

### 2. Web API scoping, coalescing, caps, and deadlines (`internal/web`)
- **`GET /api/runs/items` scoping**: Read the run's server alongside its items (or verify via a single read-only query in `store.RunItemsForServers`) and return `404 {"error":"no such run"}` if the run is absent or its server is not in `s.srvIndex`. This closes the unredacted-error leak for removed servers.
- **Singleflight `readIdentity`**: Track an in-flight channel per server under `idMu`. Launch the Plex `Identity` lookup with `context.WithTimeout(s.baseCtx, identityTimeout)` so one caller disconnecting neither aborts the lookup nor stamps `identityTried[srv]`. Stamp `identityTried[srv]` only when the upstream call fails. Concurrent callers `select` on `<-inflight.done` or `<-ctx.Done()`.
- **Bounded SSE & image proxy**:
  - Cap concurrent `/api/events` streams (e.g., `maxSSEStreams = 32`) using an atomic/mutex counter on `Server`; answer `503 {"error":"too many event streams"}` when full.
  - Bound concurrent image fetches in `serveImage` with a semaphore (e.g., 16 slots, respecting `r.Context().Done()`) and coalesce identical concurrent `(srv, ratingKey, imagePath, width, height)` requests via a small in-flight map so bursts share one 10 MiB buffer and one Plex transcoder call.
- **Route registration & write deadlines**:
  - Register `/api/` 404 fallback directly on `s.mux` and apply `requireJSON` only to known `POST` routes so unknown `POST /api/*` returns `404`, not `415`.
  - Add `extendWriteDeadline(w, d)` and apply it to `serveRefresh` (3m), `serveMatch` (2m), and `serveMatches`/`serveAutoMatch`/`serveEpisodeCheck` (90s).
- **Shutdown & status hygiene**:
  - `defer s.cancelBase()` in `Server.Stop` so `baseCtx` is always canceled on clean stop.
  - Return `503 {"error":"shutting down"}` if `s.sseCtx.Err() != nil` before headers are written in `serveEvents`, and pre-check `s.sseCtx.Err()` in the SSE loop before writing.
  - Change `Status.updateServer` to accept `func(*ServerStatus) bool` so `updateRun` notifies only when `srv.Run != nil`.
  - Fail closed in `basicAuth` (`username == "" && password == ""`).
  - Log `serveImage` upstream/sniff failures and guard `r.Context().Err()` consistently across `serveImage`, `refreshedDetail`, `writeActionError`, and `writeTMDBError`.

### 3. Refresher, HTTP clients, CLI & config (`internal/refresher`, `internal/plex`, `internal/tmdb`, `internal/neterr`, `internal/config`, `cmd/autorefresh`)
- **Plex & TMDB load impact**:
  - *Manual refresh (`RefreshItem`)*: Read `Plex.ItemWithPaths(ctx, key)` once before `autoMatch`. If Plex already reports the item as matched, skip `autoMatch` (saving 1–20 Plex/TMDB/Provider requests and up to 45s) and send 1 plain refresh. If still unmatched, pass the already-fetched paths into `autoMatch` (saving 1 duplicate `/library/metadata/{key}` read) and keep `keepMatch` before applying a picked match.
  - *Preview (`PreviewMatch`)*: Cache positive `pathID -> tmdbID` resolutions in a per-evaluation map on `decisionLog`/`matchContext`, eliminating duplicate `TMDB.Find` calls during title fallback.
  - *Same-title matching (`sameTitle`)*: Deduplicate non-empty Plex GUIDs (matching `staleYears`) so duplicate rows for one series do not trigger `conflictSeveral` or waste retries.
  - *Episode checks*: Stop `walkMetadataPages` early in `plex.EpisodeNumbers` as soon as an invalid episode is seen; validate local season episodes before calling TMDB seasons in automatic `confirmEpisodeNumbers`; replace quadratic `onlyIn` with a set map.
  - *Pacing (`refreshBatch`)*: Track `needPace` (matching `bulkBatch`) so `refresh-delay` waits only after an item that sent a request to Plex, never after a skipped item.
  - *HTTP transport & rate limiting*: Clone `http.DefaultTransport` in `plex.New` with `MaxIdleConnsPerHost: 16` to eliminate TLS/TCP churn during dashboard poster bursts. Classify HTTP `429` as `plex.ErrUnavailable` so a rate-limiting reverse proxy stops the server's run immediately rather than failing every remaining item.
- **Shared HTTP helpers & error classification**:
  - Move `SameOrigin` and `DrainAndClose` into `internal/neterr` and reuse them from `internal/plex` and `internal/tmdb`.
  - Add `context.Canceled` -> `"request canceled"` in `neterr.Reason`.
  - Pass `ctx` into `plex.bodyError` to preserve `context.DeadlineExceeded` and wrap `context.Canceled` with `ErrUnavailable`; add `"photo: "` prefix on `Photo` read errors.
  - Route `tmdb.doGet` body-read network/context errors through `formatTransportError`, use `neterr.Reason(err)` in `traceTransport`, and wrap JSON response decoders in `io.LimitReader` (32 MiB Plex, 8 MiB TMDB).
- **Path IDs & config validation**:
  - Strip leading zeros and reject `"0"` for numeric `tmdb`/`tvdb` IDs in `parseID` and `EpisodeCheck`.
  - Validate URL ports (`1..65535`, no trailing `:`), reject non-printable-ASCII runes (`r <= 0x20 || r >= 0x7f`) in Plex/TMDB tokens, reject `@every` durations `< time.Second`, and reject config files `> 1 MiB` before `os.ReadFile`.
- **Startup & shutdown (`cmd/autorefresh`)**:
  - Bound `checkTokens` with a 15-second timeout (`context.WithTimeout(ctx, checkTimeout)`).
  - Return early before `startUI` if `ctx.Err() != nil`.
  - On `<-ctx.Done()` in `serveUntilStopped`, call `cancelSched()` and stop the cron from firing new ticks right away, then stop the UI (5s graceful timeout), wait for `served`, close `Jobs`, drain item actions, and close the store.
  - Build `newLogger` via `zerolog.New(multi).With().Timestamp().Logger()` instead of deriving from global `log.Logger`.
  - Log both `err` and `saveErr` in `finishBulk`, fix `checkHeld` to use `prev.Section`/`prev.Type`, and deduplicate `names` in `Jobs.selectServers`.

### 4. Dashboard UI & tooling (`web/`)
- **Component & hook architecture (Astryx-only)**:
  - Add an `ErrorBoundary` component using Astryx `EmptyState`/`Alert` + `Button` ("Reload") around lazy-loaded routes in `app.tsx`.
  - Split `items.tsx` into `items.tsx`, `itemtable.tsx`, and `bulkband.tsx`; split `api.ts` types into `api-types.ts`; split `fixmatch.tsx` into `fixmatch.tsx`, `candidatecard.tsx`, and `searchform.tsx`; split `queue.ts` pure helpers into `queue-format.ts`. Keep all imports clean and verify with `pnpm exec astryx doctor --built .`.
  - Lift the bulk stop/resume `useMutation` and confirmation handling to `ItemsPage` (with a synchronous busy guard in `confirm.ts` and Clear selection disabled while pending) so unmounting `BulkBand` cannot abort or duplicate in-flight mutations.
  - Keep `scope.srv` stable on single-server `#/items` until a library filter is explicitly chosen, using `inventory[0].srv` instead of hardcoded `0`.
  - Render `<Heading level={1}>Items</Heading>` on `ItemsPage`, hide `<ItemTable>` when `rows.length === 0 && page.error`, add Try again on the error banner, and guard Load more with `page.isFetching` + `{ cancelRefetch: false }`.
- **Render & lifecycle efficiency**:
  - Add scoped selectors (`useServerCount`, `useStatusField`) with TanStack Query `select` in `app.tsx`, `runspage.tsx`, and `fixmatch.tsx`.
  - Memoize `parseRoute(hash)` in `useRoute()`, memoize `columns`/`plugins`/`sort`/`keys` in `ItemTable` and `RunTable`, move `useNow()` into a small `<RelativeTime />` cell in `RunTable`, and memoize `runGroups`/`groupCandidates`.
  - Harden `useLiveStatus` (`try/catch` around `JSON.parse`, `else if` to avoid double `['runs']` invalidation, stale-stream guards and timer cleanup, and evicting retired-boot `['candidate']`/`['auto-match']` queries on boot switch).
  - Harden `queue.ts` (`try/finally` around `drive`, `queueGeneration` check, page cap & repeated-cursor guard in `storedRun`).
  - Guard `Number.isFinite` in `format.ts` and `now.ts`; chain `onClick`/`onKeyDown` in `rowopen.ts`; clear idle 30s timer in `clock.ts`; fix `SearchForm` Clear search & per-field error status; fix `StatusPage` `.find` and `"1 item needs attention"`.
  - Add `eslint-plugin-react-hooks` to `web/package.json` and `web/eslint.config.js`, and tighten `api-types.ts` string-literal unions.

## Risks / Trade-offs

- **SQLite WAL on network mounts** → WAL requires shared memory (`-shm`) and does not work on NFSv3 or some SMB setups. *Mitigation*: Document clearly in `README.md` that the database file requires local/POSIX storage (standard Docker named volumes or local bind mounts).
- **Shutdown ordering test updates** → Existing `TestServeUntilStoppedDrainsInOrder` asserts the old `ui-stop -> scheduled-return` order. *Mitigation*: Update the test and `openspec/specs/service-operation/spec.md` together to verify that new cron ticks stop immediately and active runs cancel promptly while UI, runs, jobs, actions, and DB still drain cleanly.
- **Restored run `events_linked = 0` test update** → `TestFinishRunRestoresRemovedRow` currently asserts `EventsLinked: true`. *Mitigation*: Update the test and `openspec/specs/history/spec.md` together so the UI no longer falsely claims a restored run had zero changes.
- **Single PR size** → Touching backend, store, refresher, API, and frontend in one PR creates a larger diff for CodeRabbit review. *Mitigation*: Organize into clean, atomic Conventional Commits by area, keep cognitive complexity ≤ 15 in every Go function, and run the full CI check suite plus `astryx doctor --built .` and `openspec validate --all --strict`.

## Migration Plan

1. Schema migration `17_indexes.sql` runs automatically on startup inside a transaction, creating the new indexes and replacing `run_server` and `event_run`. It is additive/index-only and requires no table rebuild or data backfill.
2. SQLite switches to WAL mode automatically on `store.Open`. On clean `store.Close()`, SQLite checkpoints the WAL cleanly.
3. Rollback: Rolling back to a binary prior to migration 17 is blocked by the existing forward-version schema guard in `internal/store/store.go` (expected behavior for every schema migration).
