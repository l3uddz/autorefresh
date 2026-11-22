# Maintenance dashboard design

Date: 2026-11-22
Status: approved by the maintainer on 2026-11-22

## Intent

Autorefresh runs unattended. Today the only way to see what it does is its
log, and the only way to fix an item that it cannot fix is the Plex web app.
The maintainer wants a web UI, served by autorefresh itself, to:

1. **Status:** see whether autorefresh works. Show live progress of the current
   run, recent runs, and recent fixes for each Plex server, and start a run by
   hand.
2. **Worklist:** see the items that autorefresh has not fixed, with their
   reasons, attempts, refreshes, next due time, and last action.
3. **Manual fixes:** refresh an item, or search for match candidates and apply
   one, including a re-match of an item that autorefresh matched wrongly.
   Tell autorefresh to stop trying an item that can never be fixed.
4. **Explain decisions:** for each auto fix match attempt, show which searches
   autorefresh made, which candidates it found, and why it picked one or
   picked none.

The UI must be visually appealing, responsive, and pleasant to use. It shows
poster art, item details, and the item path on the Plex server. It supports
several Plex servers, but most users have one, so the one-server case must
look natural and not show server pickers that do nothing.

The approved mockups are in the conversation of 2026-11-22. Copies are in
`docs/superpowers/specs/2026-11-22-maintenance-dashboard-mockups/` (see
"Mockups" below).

## Goals

- One binary and one container, as today. The UI is optional.
- A layout based on the approved mockups (direction C with parts of B):
  - **Items** page: a list on the left and a large detail view on the right,
    with tabs **Attention**, **Fixed**, and **Stopped**. The list header
    toggles between **List** mode and **Table** mode. Table mode is a sortable,
    filterable table with multi-select and bulk **Refresh** and **Stop
    trying**. A table row opens the same detail view.
  - **Status** page: a card for each server with live progress, a "recently
    fixed" list, and a "recent runs" list. With one server, the single card
    fills the width.
  - **Runs** page: the run history.
  - **Fix match** dialog: rich candidate cards on the left, and the timeline of
    the last auto fix match attempt on the right.
  - On a phone, the list is full width, and an item opens full screen.
- Visual style: **stock Astryx** (Meta's open-source React design system,
  <https://astryx.atmeta.com/>) with its Neutral theme in dark mode. Only the
  colour tokens change, to the dark palette of the Astryx website itself
  (maintainer choice; see "Theme tokens"). The font is the theme's Figtree,
  as on that website. No custom component styling. The mockups set the layout and content; Astryx sets
  the look. Posters with the year, coloured reason badges, and the item art
  in the detail view stay, built from Astryx components.
- Live status without page reloads.
- Every manual action acts on one item, or on a selected set of items in bulk.
- History drops off automatically after a set period. Deleting a history
  entry deletes everything that belongs to it through foreign keys with
  `ON DELETE CASCADE`.

## Non-goals

- No library-wide refresh or scan. The UI never asks Plex to refresh or scan
  a library.
- No editing of the configuration in the UI.
- No light theme in the first version (maintainer call: dark only for now).
- No user accounts or roles. One optional username and password.
- No bulk fix match. Each match needs a person to choose the candidate.

## Anti-goals

- The Plex token never reaches the browser, in any URL, response, or header.
- Turning on the UI must not change what autorefresh does on its schedule.
- An upgrade must not open a new port. Existing users get no listener until
  they configure one.

## Approaches

| Approach | Trade-off | Outcome |
| --- | --- | --- |
| Go `html/template` + htmx, no JS build | One language and no Node in the build. The approved UI is very interactive (list and table toggle, multi-select, fix match dialog with search, live progress, in-place detail). With templates this becomes a pile of partials and swap rules, and the polish is costly. | Rejected |
| SPA embedded in the Go binary | Best fit for the interactive UI. Clean JSON API that Go tests cover with `httptest`. Adds Node and a frontend build to mise, CI, and goreleaser, and frontend dependencies that the maintainer updates by hand. | **Chosen** |
| Separate frontend container | Users run and connect two services, and CORS appears, with no gain. | Rejected |

Framework: the maintainer chose **React**. The framework alone does not make
an app fast or beautiful, but React's component ecosystem makes the polish
cheaper. That ecosystem is the reason for the choice.

Component library: the maintainer chose **Astryx**, used stock. A throwaway
prototype (Vite, React 19, Astryx 0.7.17, Neutral theme with colour
overrides) confirmed that plain Vite builds it without a StyleX plugin, that
`defineTheme` colour overrides give the soft-dark palette, and that its
`Table` covers sorting, selection, and row highlight. Rejected alternative:
Radix primitives with Tailwind and shadcn/ui-style components, which need
much more of our own styling for the same result.

## Decided by the maintainer

- A React single-page app, embedded in the binary.
- The three jobs above, plus the "why it was not picked" explanation and the
  attempt timeline.
- Optional simple auth. Most users run behind a reverse proxy that handles
  auth, so auth is off unless configured.
- History drops off after a set period, with cascading deletes.
- "Stop trying" for single items, with a **Stopped** view and a way to undo.
- A manual run can be started from the UI.
- Stock Astryx, dark mode with the Astryx website's colours and Figtree,
  posters with year, multi-server support.
- Separate change, not part of this design: lower the default of
  `max-refreshes-per-run` from 250 to 100. It gets its own issue and PR.

## Constraints

- Repository rules in `AGENTS.md`: tests need no network, Plex, or TMDB;
  build URLs with `net/url`; log with zerolog fields; cognitive complexity of
  15 or less; update `README.md` for user-facing changes.
- The container runs as `65532:65532` from `scratch`. The UI must work
  without root and without a shell.
- The `ci` workflow's `build` job name must stay.
- SQLite is the only store. The UI runs in the same process as the scheduler,
  so there is one writer.

## Design

### Process and configuration

The UI runs in the autorefresh process, beside the scheduler. New top-level
config keys (names are my call; `KnownFields(true)` makes them permanent once
released, so they are fixed here):

```yaml
ui:
  listen: ":7380"          # absent ui section, or empty listen: no listener
  username: ""             # both set: HTTP Basic auth; both empty: no auth
  password: ""
history-retention: 720h    # default 30 days; how long history is kept
```

- No `ui` section, or an empty `listen`, means no listener (upgrade safety).
  There is no default port. The README and the Docker docs use `:7380`, and
  the image declares `EXPOSE 7380`. For a bare-metal install behind a local
  reverse proxy, the README recommends `127.0.0.1:7380`.
- Config validation fails if only one of `username` and `password` is set,
  if `username` contains `:` or a control character, or if `password`
  contains a control character.
- With no auth, autorefresh logs a warning at start, and the README says the
  port must only be reachable through an authenticating reverse proxy or a
  trusted network.
- Basic auth compares SHA-256 hashes of the values with
  `crypto/subtle.ConstantTimeCompare`.
- `history-retention` must be positive. Config validation rejects zero and
  negative values.
- `--once` or `--dry-run`: no listener. A dry run is a diagnostic mode, and
  `store.go` allows a second process (such as a dry run) to share the
  database, so a dry run must not compete for the UI port. (My call.)
- If the listener cannot bind, autorefresh exits at start with a clear error,
  like any other configuration error. (My call.) It binds before
  `checkTokens`, so a bind error does not wait for the token checks.
- Stop order: the HTTP server shuts down (`Shutdown`, which also ends SSE
  streams through their context), then the jobs and the scheduler stop, and
  only then the store closes.
- The docker image documents the port with `EXPOSE`.

### Storage changes

The current `item` table holds only incomplete items, and `ApplyRead`
deletes a row when its item completes. History must outlive the item row, so
history does not hang off `item`. It hangs off `library`, which survives
completion and is deleted when its library is pruned.

New and changed tables (one migration each, numbered after `5_actions.sql`):

- `library` gets `title`, `type`, `agent`, and `language`, written each time
  autorefresh lists the libraries of a server. The UI shows library titles
  from the cache, also while Plex is unreachable or before the first run.
- `item` gets:
  - `stopped_at INTEGER` (null: not stopped). `ApplyRead` keeps it on
    rewrite. `Due` and `CountIncomplete` skip stopped items. A stopped item
    that completes is deleted as today.
  - `last_outcome TEXT NOT NULL DEFAULT ''`: the outcome of the last
    attempt (`refresh`, `match`, `tmdb-match`, `refresh-failed`,
    `match-failed`, `tmdb-match-failed`, `search-failed`), and
    `last_match_outcome TEXT NOT NULL DEFAULT ''`: the outcome of the last
    automatic fix match evaluation (`matched`, `no-match`, `no-ids`,
    `failed`). A fix match that finds nothing is followed by a plain
    refresh, so both are needed for "Last action" ("refresh · no match").
    The table shows and sorts them without reading events.
  - `last_match_event_id INTEGER` (null: none): the ID of the request
    event of the last match that Plex accepted for this row. Rows from
    before this migration keep null, so their completion shows the match
    kind without a date or candidate.
  - `thumb TEXT NOT NULL DEFAULT ''` and `art TEXT NOT NULL DEFAULT ''`: the
    Plex image paths from the listing (`/library/metadata/<key>/thumb/<ts>`).
    Reads already receive them.
  - `path TEXT NOT NULL DEFAULT ''`: the folder of the item. Movies take the
    folder of the first file from the listing. Series take the first
    `Location` path when autorefresh calls `ItemPaths` (fix match, or the
    detail view), and the path is saved then. A series listing has no
    `Location`, so `ApplyRead` keeps a non-empty `path` when the read gives
    none. The path is for display and search only: fix match still gets all
    paths with `ItemPaths` (or the movie files of the read), because IDs can
    be in file names too.
- `event` (new): one row for each thing that happened to an item.
  - Columns: `id INTEGER PRIMARY KEY`, `server`, `section`, `rating_key`,
    `at`, `kind`, `source` (`auto` or `manual`), and a snapshot of `title`,
    `year`, `type`, `thumb`, `art`, and `path`, so the row still renders, and
    the Fixed tab can still act, after the item row is gone. Kind-specific
    columns: `action` (refresh, match, tmdb-match), `accepted` (bool),
    `outcome` (match decisions: `matched`, `no-match`, `failed`), `error`
    (short text), `match_guid`, `match_title`, `match_year`, `conflict` (the
    match rule that stopped a match), `path_year`, `tmdb_year`,
    `episode_count`, and `links` (the IDs whose search found or confirmed the
    entry, as in the `Sent request` log line), and for `completed` events
    `fixed_action`, `fixed_match`, and `fixed_match_event_id` (see
    "Completion").
  - Kinds: `request` (a refresh or match request, accepted or failed),
    `match-decision` (an auto fix match evaluation, with or without a
    match, or a search that failed), `completed` (the item left the cache
    because it became complete), `gone` (Plex answered 404 and the row was
    removed), `stopped`, `resumed`.
  - `FOREIGN KEY (server, section) REFERENCES library ON DELETE CASCADE`.
  - Indexes: `(server, rating_key, at)` for the timeline, `(kind, at)` for
    "recently fixed", `(at)` for retention, and `(server, section)` for the
    library cascade.
- `match_search` (new): one row for each Plex search in a `match-decision`
  event: `event_id`, `position`, `query` (for example `tmdb-48213`),
  `source` (`path`, `tmdb`, or `tie`, the extra TMDB search of a year
  confirmation), `from_id` (the path ID a TMDB search came from), `failed`
  (bool, also for a failed `tvdb` or `imdb` search that counts as empty). FK to `event` with cascade,
  and an index on `event_id`.
- `match_candidate` (new): one row for each candidate a search returned:
  `search_id`, `position`, `guid`, `name`, `year`, `summary`, `thumb` (the
  remote URL that Plex gave), `score` (if Plex gives it), and `verdict`
  (`picked`, or the reason it was not picked). FK to `match_search` with
  cascade, and an index on `search_id`.
- `run` (new): one row for each server run or bulk job: `id`, `server`,
  `kind` (`run` or `bulk-refresh`), `started_at`, `finished_at`, `source`
  (`schedule` or `manual`), `status` (`running`, `finished`, `failed`,
  `canceled`, `interrupted`), counts that the run summary log line already
  has (refreshed, matched, failed, completed, incomplete, due, selected), and
  `error` (for example an unavailable server). `run` has no library
  (a server can fail before it reads one), so `PruneServers` deletes the
  `run` rows of removed servers itself; the library query does not reach
  them. Removed by retention too. Indexes on `(started_at)` and
  `(server)`. At start, autorefresh marks rows still `running` from an
  earlier process as `interrupted`. Only the scheduled mode does this: a
  `--once` or `--dry-run` process can share the database with a running
  daemon, so it must not mark the daemon's runs.

Retention: at the start of each run, delete `event` and `run` rows older than
`history-retention`. The cascade removes their searches and candidates. The
`library` cascade removes the history of a pruned library. Dry runs skip
retention, record nothing, and prune no library or server, so a dry run
with another configuration cannot delete the daemon's cache or history.

#### Completion

"Recently fixed" and the Fixed tab show the `completed` events of items
that had at least one accepted request (automatic or manual), as the run
summary's `completed_after_request` counts them. Items that Plex completed
on its own are recorded but hidden behind a filter. The Fixed tab shows the
latest completion of each item once. `ApplyRead` writes the events in its
own transaction, for each row it deletes because the item became complete:

- The snapshot prefers the metadata of the completing read (title, year,
  thumb, art), because the cached row can still hold the title or empty
  poster from before a match. Path and art fall back to the cached row.
- The attribution is saved at completion from the row, which belongs to the
  current occurrence of the item: `fixed_action` (the row's `last_action`,
  shown as "the last accepted request before completion"), `fixed_match`
  (`last_match`), and `fixed_match_event_id`, the ID of the accepted match
  `request` event, which `RecordRequest` saved on the row as
  `last_match_event_id`. Event IDs are never reused (`AUTOINCREMENT`), so an
  earlier occurrence of the same rating key can never be credited. If that
  event has expired, the ID is stored as null and the match kind stays.
  Legacy rows have no event ID.

That also covers "matched by autorefresh on 12 Nov" in the detail view.

### Recording in the refresher

- Requests: the `request` event is written where the Plex call is made
  (`refreshItem`), with the intended action and its outcome (accepted, or
  failed with the error). It goes in the same transaction as
  `RecordAttempt`. Today `countFailure` records a failed attempt with an
  empty action, and the 404 branch of `itemFailed` deletes the row without
  `RecordAttempt`; the builder threads the intended action through, and a 404
  writes a `gone` event before the row is deleted. The existing attempt,
  backoff, and deletion rules do not change.
- Match decisions: `findMatch` gets a collector that records each search as
  it happens: path ID searches, and the TMDB lookups and second-pass
  searches, which today replace the first-pass results. The decision is
  written on every exit, not only from `resolveMatch`: `no-ids` (the paths
  have no ID, so nothing was searched; the UI says how to add one),
  `matched`, `no-match` (with the conflict), or `failed`
  (path lookup, search, TMDB, or episode lookup failed, with the searches
  made before the failure). A TVDB or IMDb search that Plex answers with an
  error counts as an empty search, as it does today: it is marked failed in
  the decision, and the outcome follows the other searches. The extra search of `tieTMDBCandidate` is
  recorded too. The verdict of each candidate uses the existing
  rules: `picked`, `several candidates`, `candidate without guid`,
  `different candidates`, `year differs`, and the confirmations by path
  year, TMDB year, or episode numbers.
- Completion: see "Completion" above.
- Dry runs record no events, as they record no attempts today. They also
  delete no item row that a read finds complete or gone, so the next
  scheduled run still records that completion or gone event.
- The log lines stay as they are.

### Live status

The refresher gets a small progress hook (an interface that the caller
passes in `Options`; nil means no hook). It reports per server: run started,
library read, the batch (`selected`: the items this run will try, after
`max-refreshes-per-run`, and `due`: all items due), item started (title),
item done (outcome), run finished. Progress shows `done / selected`, and the
card also shows `due` ("100 of 241 due this run"). The web package
keeps the latest state in memory and pushes changes to browsers with
server-sent events (`GET /api/events`). The state is also available as
`GET /api/status` for the first load. The SSE handler sends
`X-Accel-Buffering: no`, a keepalive comment every 25 s, and clears its
write deadline with `http.ResponseController`. The HTTP server sets
`ReadHeaderTimeout`, and JSON bodies are limited with `MaxBytesReader`.
The UI shows a "reconnecting" banner while the stream is down.

Each server card shows its health: `ok`, `unreachable`, or `unauthorized`
(Plex rejected the token), from the start check and the latest run, and the
TMDB token state when `tmdb-token` is set. Before the first read of a
server, the card and the lists show a "first scan" state instead of empty
lists. With several servers running at once, the top bar shows how many run
and their combined progress. With one server, "Run now" is a plain button,
and the server picker is hidden. With several, the picker scopes every
page.

### Manual run

`Run now` starts a run of all servers or of one server. It uses the existing
`runner` lock: if a run is busy, the API answers `409` and the UI shows
"a run is busy". Manual runs record `run.source = manual`.

- **Subset runs must not prune the other servers.** `Refresher.Run` builds
  the `PruneServers` list from the servers it runs (`refresher.go:175`).
  The refresher gets the servers to run separately from the full configured
  list, and `PruneServers` always gets the full configured list. A test runs
  one server and checks that the other server keeps its items, stopped
  state, events, and runs.

#### Background jobs

Manual runs and bulk refresh are background jobs:

- Admission is atomic: the handler takes the runner lock with `TryLock`
  before it answers. It answers `202` with a job ID (the `run` row ID), or
  `409` when busy. The scheduled start keeps its current behaviour (skip and
  warn).
- A job runs with a context that the process owns, not the request context,
  so closing the browser does not stop it.
- Correctness never depends on a clean shutdown. A power cut or a kill can
  stop the process at any point, so:
  - Each item's state change (the attempt, its event, and a stop or resume)
    is one SQLite transaction. A crash loses at most the record of the one
    request that was in flight, as today.
  - A `run` row is written `running` when the job starts. At start,
    autorefresh marks every row still `running` as `interrupted`. That is
    the only recovery step.
  - An interrupted bulk job is not resumed. Its items keep the state they
    reached, and the user can select the rest again.
- On SIGINT or SIGTERM, autorefresh still cancels the jobs and gives them a
  moment to stop at the next item, as scheduled runs do today, so a normal
  stop records `canceled` instead of `interrupted`. This is a courtesy, not
  something the design relies on.
- A bulk job reports, for each item, `accepted`, `failed` (with the error),
  or `skipped` (with the reason: gone, stopped, or changed since the job
  started; see "Concurrency"). The final result is in the job's `run` row
  and on the SSE stream, and the UI shows it in a toast and in the Runs
  page.

### Concurrency of manual actions and runs

Decided by the maintainer: manual actions stay available while a run is
busy. A run picks its batch once and then works through it slowly, with
`refresh-delay` between requests (`refreshDue`), so without a rule a run
could act on a stale copy of an item that the user just stopped or matched.

- One in-process lock for each item (`server` + `rating_key`), shared by the
  refresher and every manual action.
- The refresher takes the item lock before it sends a request and re-reads
  the row under the lock. It skips the item, without an attempt, if the row
  is gone, stopped, or has an attempt newer than the time the batch was
  loaded.
- Each manual action (refresh, apply match, stop, resume) takes the same lock
  and re-checks its preconditions under it.
- Bulk refresh takes the runner lock (one job at a time, paced) and the item
  lock for each item.

### Manual actions

All actions go through the refresher code, so they record attempts and
events exactly as automatic requests do, with `source = manual`.

- **Refresh** (one item): sends the refresh at once, under the item lock and
  outside the runner lock.
  It counts as an attempt and as a refresh, so the backoff grows and
  autorefresh does not refresh the item again right away.
- **Bulk refresh**: a background job (see "Background jobs") that takes the
  runner lock, so it never runs beside a scheduled run. It paces requests with `refresh-delay`. It
  does not count against `max-refreshes-per-run`. Progress goes through the
  same live status stream.
- **Fix match**:
  - Search: by title and optional year, or by an ID such as `tmdb-48213`,
    `tvdb-31207`, or `imdb-tt7712040`. On open, the dialog runs a title
    search (new: the automatic fix match never searches by title) and one
    search for each folder ID of the item (as the automatic fix match does).
  - Each candidate shows: poster, title, year, type, summary, Plex score (if
    present), and evidence chips: "matches folder `<id>`" when a folder-ID
    search returned the same GUID, year match or year difference against the
    item year, and "autorefresh found it via `<id>`" when the last
    `match-decision` event has it.
  - On open, the dialog also runs the TMDB step of the automatic fix match
    when `tmdb-token` is set: it translates `tvdb` and `imdb` folder IDs to
    TMDB IDs (reusing the `tmdb_id` cache) and searches those. Plex stops
    using TheTVDB on 2026-12-03, so without this step most `tvdb` folders
    find nothing.
  - `plex.SearchMatches` gets an optional year and decodes `type`,
    `summary`, `thumb`, and `score` (with a recorded response in
    `testdata/` to confirm the fields).
  - For series, a candidate tied to a TMDB ID (from a folder `tmdb` ID or the
    TMDB step) offers the episode-number check that autorefresh uses for TV
    year conflicts, on request, because it calls TMDB. Other candidates do
    not offer it.
  - Safety cues: the item's current match (from the live Plex GUID) is
    marked "currently matched", so the same wrong match is not applied
    again; folder and searched IDs link to their TMDB, IMDb, or TheTVDB pages
    (`pathID.link`); movies list their file names, not only the folder.
  - When there is no automatic attempt to show (the item is matched, was
    never tried, or is on the Fixed tab), the timeline pane shows a short
    empty state.
  - Apply: sends the match, records an accepted or failed `request` event,
    and then a refresh is not needed (Plex refreshes on match).
- **Stop trying / Resume**: sets or clears `item.stopped_at` and writes a
  `stopped` or `resumed` event. Bulk stop is allowed. A manual refresh or
  match of a stopped item keeps it stopped.
- **Cancel**: a running bulk job or manual run has a Cancel button
  (`POST /api/runs/cancel`). It stops at the next item.
- **After a request**: Plex works on a refresh or match in the background,
  so the item keeps its reasons until the next read. The item shows
  "Requested · <time>" until then. A **Check now** button re-reads that one
  item from Plex (one metadata request, never a library refresh) and
  updates its reasons, so the user can see the result without waiting for
  the next run.
- **Open in Plex**: a link to
  `https://app.plex.tv/desktop/#!/server/<machineIdentifier>/details?key=/library/metadata/<ratingKey>`.
  The machine identifier comes from `GET /` on the server, cached after the
  first successful answer, so a server that was down at start still gets the
  link later.

### Confirmations

Actions that change Plex or stop autorefresh's work ask first, with an
Astryx `AlertDialog` (`useImperativeAlertDialog`). The dialog names the item
or the number of items and says what happens. The action button shows a
spinner while the request runs, and a `Toast` reports the result.

| Action | Confirm | Dialog says |
| --- | --- | --- |
| Stop trying (one item) | Yes | Autorefresh stops refreshing "<title>" until you resume it. The item moves to Stopped. |
| Stop trying (bulk) | Yes | The same, for N items. |
| Refresh (bulk) | Yes | N refresh requests, paced by `refresh-delay` (about the total time). |
| Apply a match | Yes | "<item title> (<year>)" will be matched to "<candidate> (<year>)". Plex replaces the metadata of the item. |
| Refresh (one item) | No | Harmless and cheap. A toast reports it. |
| Resume | No | It only puts the item back in the queue. |
| Run now | No | It is refused when a run is busy, and it changes nothing that a scheduled run would not. |

Destructive-style action buttons (`actionVariant="destructive"`) are used
for Stop trying. Apply a match and bulk refresh use the primary variant.
(The confirm and no-confirm split is my call; the maintainer asked for
confirmations "for things like Stop trying".)

### Items without a cache row (Fixed tab)

A wrongly matched item usually becomes complete, so `ApplyRead` deletes its
`item` row and only its history remains. Decided by the maintainer:

- The Fixed tab offers **Fix match** and **Refresh**, not Stop trying.
- An action on such an item writes only an event (`source = manual`). It does
  not create an `item` row. If the item is still incomplete afterwards, the
  next incremental read adds it back as usual (`updatedAt` changed).
- Actions are possible while the item has history within
  `history-retention`. Images and paths come from the event snapshot.

### Item detail data

From the cache: title, year, type, reasons, attempts, refreshes, next due,
last action, path (if saved), stopped state, and the event timeline.

Fetched from Plex in a separate request after the cached detail shows
(`/api/item/live`, with a 5 s limit): summary, the current GUID, the number
of seasons and episodes for a series (`childCount`, `leafCount`), and the
path of a series if the cache has none (then saved). If Plex is unreachable
or answers 404, the cached detail and timeline stay, with a note.

### Images

- Item posters and art: `GET /api/items/thumb?srv=&key=`
  and `.../art`. The handler takes only a configured server name and a rating
  key that exists in the cache or the history, and looks up the stored image
  path. The client never supplies a path or URL.
- The stored path must be a relative Plex path of that item
  (`/library/metadata/<ratingKey>/thumb/<digits>` or `.../art/<digits>`).
  Reads store only such paths; any other value is stored as empty and the UI
  shows a placeholder. The handler validates it again before use.
- The fetch goes through the Plex client, with its token header and its
  same-origin redirect policy (`client.go`), through Plex's photo transcoder
  at a fixed set of sizes (`?size=sm|md|lg`).
- The response is app-owned: only `image/jpeg`, `image/png`, or
  `image/webp` (checked by sniffing), at most 10 MB, with only our headers
  (`Content-Type`, `Cache-Control: private, max-age=86400`,
  `X-Content-Type-Options: nosniff`, `Cross-Origin-Resource-Policy:
  same-origin`). No Plex header is passed through. The image URL carries a
  version taken from the stored path's timestamp (`?v=<ts>`), so a new
  poster after a refresh or match is not hidden by the browser cache.
- Candidate posters: the URLs that Plex's match search returns. Before they
  are stored or sent to the browser, autorefresh keeps only `https` URLs
  with no user info and no `X-Plex-Token` (or other token) query value, and
  drops the rest. The browser loads them directly with
  `referrerpolicy="no-referrer"`.
- Authentication covers everything the listener serves: the page, its
  assets, images, the SSE stream, and the JSON API.

### HTTP API

JSON under `/api/`. All mutating endpoints are `POST`, need
`Content-Type: application/json`, and go through Go's
`net/http.CrossOriginProtection`. It accepts `Sec-Fetch-Site: same-origin`
(sent by browsers to HTTPS and localhost), and falls back to comparing
`Origin` with `Host` only when `Sec-Fetch-Site` is absent. This blocks
cross-site requests with or without Basic auth. No `X-Forwarded-Host` trust.
The README says that a reverse proxy serving the UI over plain HTTP must pass
the original `Host` header. (My call.)

Server names can contain `/` (config allows any name without control
characters), and proxies can decode `%2F`. So no server name goes in a path
segment: the API identifies a server by its position in the configured list
(`srv=0`), or by name in a query parameter. Endpoints below use `{srv}` for
that key.

Endpoints (builder may refine names):

- `GET /api/status`, `GET /api/events` (SSE)
- `GET /api/servers` (name, libraries, machine identifier)
- `GET /api/items?tab=attention|fixed|stopped&server=&library=&reason=&type=&q=&sort=&cursor=`
- `GET /api/item?srv=&key=` (cached detail and timeline, at once)
- `GET /api/item/live?srv=&key=` (Plex enrichment, see "Item detail data")
- `GET /api/runs`, `POST /api/runs` (`{"srv": null}` for all),
  `POST /api/runs/cancel?id=`
- `POST /api/item/refresh?srv=&key=` (one item, synchronous, `200`)
- `POST /api/item/match?srv=&key=`, `GET /api/item/matches?srv=&key=&q=&year=`
- `POST /api/item/episode-check?srv=&key=&tmdb=` (see "Fix match")
- `POST /api/items/refresh` (bulk job, `202` or `409`), `/stop`, `/resume`
  (a list of items or a filter)

`q` searches titles, and paths where the cache has them.

#### Item list behaviour

- Table mode: a row opens the detail in a `Drawer` from the right, so the
  selection and the scroll position stay.
- List mode has the same filters as table mode (library, reason, type), in
  a filter menu above the list.
- The bulk bar follows the tab: Attention offers Refresh and Stop trying,
  Stopped offers Resume, Fixed offers no bulk action.
- Reasons that `ignore-reasons` lists are shown as muted badges.
- Runs page: started, duration, server, source, kind, status, items read,
  refreshed, matched, failed, fixed, due, error; a server filter; cursor
  pages.

#### Queue state and the item list

- **Attention** holds every cached item that is not stopped and has at
  least one reason that is not ignored. Each item shows one state, computed
  by the same function that `Due` uses (the builder extracts it, so the
  scheduler and the UI cannot disagree): `due`, `waiting` (until the end of
  its backoff wait), or `too new` (until `minimum-age` passes). Items whose
  reasons are all ignored are not in Attention; a filter shows them.
- **Next due** in the list and detail comes from that same function.
- The item list pages with a cursor over a stable server-side order: the
  chosen sort column, then `server`, then `rating_key`.
- **Select all** in table mode selects every item that matches the current
  filter, not only the loaded page. The bulk endpoints take either a list of
  items or the filter, and the confirmation dialog states the count.

### Frontend

- React with TypeScript, built by Vite into static files.
- Astryx (`@astryxdesign/core`, `@astryxdesign/theme-neutral`, and
  `@astryxdesign/cli` as a dev dependency), all pinned to the same exact
  version, because Astryx is in beta. Upgrades by hand with
  `astryx upgrade`. `astryx init` runs in `web/`, so its agent docs land in
  `web/`, not in the repository `AGENTS.md`.
- Theme: `defineTheme({extends: neutralTheme, tokens: {...}})` with the
  colour tokens in "Theme tokens", and `<Theme mode="dark">`. Figtree from
  `@astryxdesign/theme-neutral/fonts.css`, which ships the font files in the
  package. No request to a font CDN or any other CDN at run time.
- Pages start from Astryx templates (`astryx build`, `astryx template`), as
  the Astryx workflow requires:
  - Items: **Inbox Table** (a table that indexes a resizable reading pane)
    or **Incident Console** (a row queue with an inspector, a search filter
    bar, and a segmented status control). The builder picks the closer one.
    The list and table modes, tabs, and detail view are fitted into it.
  - Status: a dashboard template, cut down to server cards, recent fixes,
    and recent runs, without charts.
  - Fix match: `Dialog` with candidates as `Item`/`Card` rows and the
    timeline beside them.
  - Phone: `Drawer` or `BottomSheet` for the detail view.
- Theme tokens: the Astryx website (astryx.atmeta.com) uses its own site
  theme, which is not published as a package. Its colour tokens were read
  from the live site on 2026-11-22: all 93 colour tokens that the Neutral
  theme defines, each as a `[light, dark]` pair, including the categorical
  colours that badges use. They are in
  `2026-11-22-maintenance-dashboard-mockups/site-tokens.ts`, ready to pass to
  `defineTheme`. Key dark values: body `#111112`, surface and card
  `#1F1F22`, popover `#28292C`, text `#DFE2E5`, secondary text `#B0B5BB`.
  The accent is the site's near-white (dark) or near-black (light), not a
  colour, so primary buttons are light in dark mode, as on the site.
- Light mode later: every token has a light value, and the UI uses only
  components and tokens, so light mode is a change of `mode` (`"system"` or a
  toggle), with no other work. The prototype screenshot shows the same screen
  in both modes. Image overlays (poster year, art backdrop) use tokens too.
- Reason badges: Astryx `Badge` colour variants, which take the theme's
  categorical tokens: `unmatched` red, `no-poster` and `no-art` orange,
  `no-summary` blue, `episode-no-summary` and `episode-unmatched` purple.
- Spacing (maintainer request: not too compact): `Table` uses
  `density="spacious"`, lists use `density="balanced"`, controls (buttons,
  inputs, tabs, segmented controls) use `size="md"`, and page regions use the
  larger spacing steps. No `compact` density anywhere.
- Components: `Table` with `useTableSelection`, `useTableSortable`, and row
  virtualization for table mode; `List`/`Item` for list mode; `TabList`,
  `SegmentedControl`, `Badge` (reasons), `StatusDot`, `ProgressBar`,
  `MetadataList`, `Button`, `DropdownMenu` (Run now), `Toast` (action
  results), `Skeleton` (loading). The builder reads each with
  `astryx component <Name>`.
- No own CSS files, raw `div` styling, or hard-coded colours and sizes:
  components and tokens only, as the Astryx workflow requires.
- Templates use StyleX for layout, so Vite gets `@stylexjs/unplugin`
  (`stylex.vite()`), pointed only at our own source. The prototype did not
  use it, so stage 4 starts by proving that combination with Astryx 0.7.17.
  If it fails, the pages use Astryx layout components without the
  compiler.
- `siteTokens` is typed against Astryx's theme token type, and a Vitest
  test checks that every key exists in the Neutral theme, so an Astryx
  upgrade that renames a token fails the tests.
- Size: Figtree ships only the weights the theme uses, the build reports
  the size of `dist/`, and the Go server serves pre-compressed `.gz` assets
  when the browser accepts gzip.
- TanStack Query for API data, with SSE messages that update or invalidate
  queries.
- Hash-based routes (`#/items/...`) and relative asset and API URLs, so the UI
  works behind a reverse proxy at any sub-path without configuration.
- Single-server installs hide the server picker and server columns.

### Build, test, and CI

- Source in `web/`. Two embedded file sets in `internal/web`, chosen by a
  build tag, so generated output never mixes with the committed fallback:
  - Without the tag (`go test`, `go vet`, golangci-lint, plain `go build`):
    a committed `fallback/index.html` ("UI not built"). No Node needed.
  - With the `webui` tag: the generated `dist/` (ignored by git). If `dist/`
    is missing, the build fails at compile time, so a tagged build can never
    ship the fallback.
- Every packaging entry point builds the frontend from the locked
  dependencies (`pnpm install --frozen-lockfile`, then the Vite build) and
  builds Go with the `webui` tag: `mise run build`, and GoReleaser through a
  `before.hooks` step, which covers `snapshot`, `release`, and `publish`
  locally and in the CI `build` and `release` jobs. The Docker image copies
  that binary from `dist/`, so it gets the real UI.
- A packaging check: a Go test with the `webui` tag asserts that the
  embedded `index.html` is not the fallback. CI runs it after the frontend
  build.
- `mise.toml` adds `node` and `pnpm` (and the Astryx CLI comes from the
  pinned dev dependency), and tasks `web-install`, `web-build`,
  `web-lint` (TypeScript check and ESLint), and `web-test` (Vitest). `build`
  and `snapshot` depend on `web-build`.
- The CI `build` job runs the web install, lint, test, and build before
  `mise run snapshot`. The job name stays `build`.
- `AGENTS.md` and `README.md` document the new tasks, the `ui` config, the
  port, and the auth warning.

### Delivery order

Each stage is its own issue and PR into `main`. Each stage leaves `main`
working.

0. Separate: default `max-refreshes-per-run` 250 → 100.
1. Storage and recording: migrations, events, match decisions, runs,
   retention and `history-retention`, stopped flag (respected by `Due` and
   `CountIncomplete`), library metadata, thumb, art, and path columns, and
   the decoding of candidate `type`, `summary`, `thumb`, and `score` in
   `plex.SearchMatches` (the year parameter waits for stage 3). No UI yet.
2. HTTP server: config, auth, same-origin checks, read API, image proxy,
   status and SSE, the progress hook, placeholder page.
3. Write API: refresh, check now, bulk refresh, cancel, fix match search
   and apply, stop and resume, run now, item locks.
4. Frontend foundation: tooling, CI, theme, layout, Status page, Items list
   and table modes, detail view.
5. Frontend fix match dialog and timeline, Runs page, phone layout polish.

## Mockups

The approved mockups (HTML) are kept beside this document. Their posters,
titles, summaries, and paths are placeholders. They define the layout and
the content of each screen. The final look is stock Astryx, as in the
prototype screenshot `astryx-prototype.png`.

- `layout-directions.html`: directions A, B, and C.
- `layout-combined.html`: C with B's table mode and status cards.
- `look-and-feel.html`: palette, posters, detail view, fix match dialog. Its
  font and custom styling are superseded by stock Astryx.
- `astryx-prototype.png`: the Astryx prototype with the site tokens and the
  agreed spacing (list mode, table mode, status cards, and list mode in
  light mode).
- `site-tokens.ts`: the colour tokens of the Astryx website.

## Mockup elements and their data

| Element | Source |
| --- | --- |
| Item list poster with year | New `item.thumb` column, image proxy |
| Search by title or path | Cache; path only where saved |
| Reasons, tries, next due, last action | Cache and events |
| "match failed" last action | `request` event with `accepted = false`, or `match-decision` without a match |
| "matched by autorefresh on 12 Nov" | Last accepted match `request` event |
| Summary, seasons, episodes, backdrop | Plex, when the detail opens |
| "episode-no-summary · 4 episodes" count | **Dropped**: the number of affected episodes is not known without extra episode queries. The chip shows the reason only. |
| Candidate external IDs (`tmdb-48213 · imdb-…`) on every card | **Dropped**: Plex's match search returns a Plex GUID, not external IDs. A card shows "matches folder `<id>`" when a folder-ID search found the same GUID. |
| Candidate score | Plex match search `score`, if present in real responses (record a response to confirm). Hidden if absent. |
| "Open in Plex" | Machine identifier from Plex `GET /` |
| Server cards, progress, "now: <title>" | Progress hook and SSE |
| Recent runs, "unreachable" | New `run` table |
| Recently fixed with the action that fixed it | `completed` events and the preceding `request` event |
| Auto fix match timeline and verdicts | `match-decision` events, `match_search`, `match_candidate` |

## Left to the builder

- Exact endpoint names, JSON shapes, and package layout (expected:
  `internal/web` for the HTTP server and embedded files).
- Page size and cursor format of the item list.
- Image sizes for the photo transcoder.
- Component structure of the React app.

## Open questions

None.
