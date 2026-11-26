# Proposal

GitHub issue: #149

## Why

A full codebase review found security and resource-limit gaps in the web API, correctness and race bugs across the refresher, HTTP clients, store, and dashboard, and SQLite query bottlenecks that scan whole tables on hot paths. Fixing all of them together in one tracked change brings the service, specs, and dashboard up to a consistent reliability and performance baseline before further feature work.

## What Changes

- **Configuration and startup**: Reject oversized configuration files (> 1 MiB), invalid or empty server URL ports, non-ASCII whitespace or non-printable characters in Plex and TMDB tokens, and `@every` schedules shorter than 1 second. Bound startup token checks with a 15-second timeout, skip binding the UI listener if a shutdown signal already arrived, and stop new cron ticks and cancel active scheduled runs promptly on shutdown.
- **SQLite store and history**: Switch SQLite to WAL mode with `synchronous=NORMAL` (requiring local/POSIX storage for the database directory, not NFSv3). Add indexes for fixed-tab counts, per-server run listings, run pruning, and run events. Drop the redundant `library` join in fixed-tab counts, add a batched `KnownItems` lookup using snapshot columns, skip unnecessary search/candidate queries in item timelines, pass context consistently in `SetStopped`, reject zero timestamps in `SetStopped` and preserve `added_at` in `ApplyCheck`, clear stale match event pointers when a library is pruned, and mark restored runs as `events_linked = 0`.
- **Refresher and HTTP clients**: Deduplicate Plex GUIDs in same-title matching, cache positive TMDB lookups within a single preview evaluation, check live Plex match state before running automatic fix match on manual refresh, skip pace waits after skipped items, reject zero and strip leading zeros on numeric path IDs, attach underlying errors to bulk refresh failure logs, validate local episodes before TMDB calls and optimize `onlyIn`, classify Plex HTTP `429` as unavailable, preserve `context.DeadlineExceeded` and `context.Canceled` in Plex/TMDB body-read errors without leaking raw transport details, bound JSON response reads, increase Plex HTTP idle connections per host, stop paginating `EpisodeNumbers` on first invalid episode, and deduplicate shared redirect and body-draining helpers.
- **Dashboard API**: Require configured-server scoping on `GET /api/runs/items` (`404` for missing or unconfigured runs so removed servers cannot leak unredacted errors), cap concurrent SSE streams and bound/coalesce concurrent image proxy fetches, coalesce concurrent Plex identity lookups without caller-cancel backoff poisoning, extend write deadlines on `POST /api/item/match` and slow search/check routes, return `404` for unknown `POST /api/*` paths, return `503` on SSE shutdown races, cancel `baseCtx` on clean stop, fail closed in `basicAuth`, suppress no-op SSE notifications when no run is active, and log image proxy failures.
- **Dashboard UI and tooling**: Fix single-server `#/items` double-fetch and dropped early selections, lift bulk stop/resume mutations out of `BulkBand`, wrap lazy routes in an `ErrorBoundary`, fix empty-table rendering and missing retry on `ItemsPage` errors, guard Load more against concurrent fetches, harden SSE parsing/invalidation and bulk queue termination/generation/pagination, guard `NaN` timestamps, fix `FixMatchDialog` Clear search and year error placement, fix confirmation dialog double-invocation and hide races, fix Status page server lookup and pluralization, chain `useRowOpen` handlers, clear idle clock timers and evict retired-boot caches, scope `useStatus` with `select` and memoize routes/table props, add an `<h1>` on `ItemsPage`, add `eslint-plugin-react-hooks`, tighten API union types, and split the largest frontend modules.

### Surface Summary

- **Configuration keys**: No keys added, renamed, or removed. Stricter validation on existing `schedule`, `cache-refresh`, `plex[].url`, `plex[].token`, and `tmdb-token`.
- **API routes**: No routes added or removed. Behaviour changes on `GET /api/runs/items` (`404` for unknown or unconfigured server runs), `GET /api/events` (concurrency cap and shutdown `503`), `GET /api/items/thumb` and `GET /api/items/art` (bounded/coalesced fetches), `GET /api/servers` (coalesced identity lookup), `POST /api/item/match` and search/check routes (extended write deadline), and unknown `POST /api/*` paths (`404` before `415`).
- **Dashboard pages**: No pages added or removed. Fixes apply to Status, Items, Item detail, Fix match dialog, Runs, and app-level lazy route error handling.
- **Plex safety**: No new write request kind is sent to Plex, no library-wide refresh or scan is added, and no new external host is contacted.

### Non-Goals

- Adding new user-facing features, configuration keys, API endpoints, or dashboard pages.
- Supporting SQLite WAL mode over NFSv3 or SMB network mounts (documented as a local/POSIX storage requirement in `README.md`).
- Changing visual styling or replacing Astryx components (`Badge` for reason labels stays as specced in `dashboard-ui`).

## Capabilities

### New Capabilities
<!-- None -->

### Modified Capabilities
- `configuration`: Reject oversized config files, invalid URL ports, non-ASCII whitespace or non-printable token characters, and `@every` schedules shorter than 1 second.
- `service-operation`: Bound startup token checks, skip binding the UI listener when already signaled, and stop new cron ticks and cancel active scheduled runs promptly on shutdown.
- `history`: Use SQLite WAL mode with `synchronous=NORMAL`, clear stale match pointers on library removal, and mark restored runs as having unlinked events.
- `refresh-runs`: Classify Plex HTTP `429` as unavailable and skip `refresh-delay` waits after skipped items.
- `fix-match`: Normalize and reject zero numeric path IDs, deduplicate identical Plex GUIDs in same-title searches, and reuse positive TMDB lookups within one preview.
- `manual-actions`: Check live Plex match state before running automatic fix match during Refresh now.
- `dashboard-api`: Scope `GET /api/runs/items` to configured servers, cap SSE streams and image proxy concurrency, coalesce identity lookups safely, extend write deadlines for match/search routes, and return `404` for unknown `POST /api/*` paths.
- `dashboard-ui`: Fix single-server Items fetch/selection, bulk mutation lifecycle, Items error/loading states, lazy route error boundary, Fix match Clear search and year errors, and Status card pluralization/lookup.

## Impact

- **Go packages**: `cmd/autorefresh`, `internal/config`, `internal/neterr`, `internal/plex`, `internal/tmdb`, `internal/store` (plus migration `17_indexes.sql`), `internal/refresher`, `internal/web`.
- **Frontend**: `web/src/*`, `web/eslint.config.js`, `web/package.json` / `web/pnpm-lock.yaml` (adding `eslint-plugin-react-hooks`).
- **Docs**: `README.md` (WAL/local storage note for the database file, updated validation/shutdown behavior where documented) and `AGENTS.md` (updated package descriptions for any split frontend files and WAL/index changes).
