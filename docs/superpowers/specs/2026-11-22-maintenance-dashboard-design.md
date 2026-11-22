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

- No `ui` section means no listener (upgrade safety). Config validation fails
  if only one of `username` and `password` is set.
- With no auth, autorefresh logs a warning at start, and the README says the
  port must only be reachable through an authenticating reverse proxy or a
  trusted network.
- Basic auth compares with `crypto/subtle`.
- `--once` or `--dry-run`: no listener. A dry run is a diagnostic mode, and
  `store.go` allows a second process (such as a dry run) to share the
  database, so a dry run must not compete for the UI port. (My call.)
- If the listener cannot bind, autorefresh exits at start with a clear error,
  like any other configuration error. (My call.)
- The docker image documents the port with `EXPOSE`.

### Storage changes

The current `item` table holds only incomplete items, and `ApplyRead`
deletes a row when its item completes. History must outlive the item row, so
history does not hang off `item`. It hangs off `library`, which survives
completion and is deleted when its library is pruned.

New and changed tables (one migration each, numbered after `5_actions.sql`):

- `item` gets:
  - `stopped_at INTEGER` (null: not stopped). `ApplyRead` keeps it on
    rewrite. `Due` and `CountIncomplete` skip stopped items. A stopped item
    that completes is deleted as today.
  - `thumb TEXT NOT NULL DEFAULT ''` and `art TEXT NOT NULL DEFAULT ''`: the
    Plex image paths from the listing (`/library/metadata/<key>/thumb/<ts>`).
    Reads already receive them.
  - `path TEXT NOT NULL DEFAULT ''`: the folder of the item. Movies take the
    folder of the first file from the listing. Series take the first
    `Location` path when autorefresh calls `ItemPaths` (fix match, or the
    detail view), and the path is saved then.
- `event` (new): one row for each thing that happened to an item.
  - Columns: `id INTEGER PRIMARY KEY`, `server`, `section`, `rating_key`,
    `at`, `kind`, `source` (`auto` or `manual`), and a snapshot of `title`,
    `year`, `type`, and `thumb`, so the row still renders after the item row
    is gone. Kind-specific columns: `action` (refresh, match, tmdb-match),
    `accepted` (bool), `error` (short text), `match_guid`, `match_title`,
    `match_year`, `conflict` (the match rule that stopped a match).
  - Kinds: `request` (a refresh or match request, accepted or failed),
    `match-decision` (an auto fix match evaluation, with or without a
    match), `completed` (the item left the cache because it became
    complete), `stopped`, `resumed`.
  - `FOREIGN KEY (server, section) REFERENCES library ON DELETE CASCADE`.
  - Index on `(server, rating_key, at)` and on `(kind, at)`.
- `match_search` (new): one row for each Plex search in a `match-decision`
  event: `event_id`, `position`, `query` (for example `tmdb-48213`),
  `source` (`path` or `tmdb`), `failed` (bool). FK to `event` with cascade.
- `match_candidate` (new): one row for each candidate a search returned:
  `search_id`, `position`, `guid`, `name`, `year`, `summary`, `thumb` (the
  remote URL that Plex gave), `score` (if Plex gives it), and `verdict`
  (`picked`, or the reason it was not picked). FK to `match_search` with
  cascade.
- `run` (new): one row for each server run: `id`, `server`, `started_at`,
  `finished_at`, `source` (`schedule` or `manual`), counts that the run
  summary log line already has (refreshed, matched, failed, completed,
  incomplete), and `error` (for example an unavailable server). Removed by
  retention and by `PruneServers`.

Retention: at the start of each run, delete `event` and `run` rows older than
`history-retention`. The cascade removes their searches and candidates. The
`library` cascade removes the history of a pruned library.

"Recently fixed" is the `completed` events. The match that fixed an item is
the last accepted match `request` event before its `completed` event. That
also covers "matched by autorefresh on 12 Nov" in the detail view.

### Recording in the refresher

- Each `store.RecordAttempt` call (today in `refreshItem` and
  `countFailure`) also writes a `request` event, in the same transaction.
- `resolveMatch` writes a `match-decision` event with its searches and
  candidates, both when it finds a match and when it does not. The verdict of
  each candidate uses the existing rules: `picked`, `several candidates`,
  `candidate without guid`, `different candidates`, `year differs`, and the
  confirmations by path year, TMDB year, or episode numbers. The TMDB second
  pass records its searches with `source = tmdb`.
- `ApplyRead` writes a `completed` event for each item it deletes because the
  item became complete (the `fixed` list it already returns). Items that are
  gone from Plex get no `completed` event.
- Dry runs record no events, as they record no attempts today.
- The log lines stay as they are.

### Live status

The refresher gets a small progress hook (an interface that the caller
passes in `Options`; nil means no hook). It reports per server: run started,
library read, item started (title), item done, run finished. The web package
keeps the latest state in memory and pushes changes to browsers with
server-sent events (`GET /api/events`). The state is also available as
`GET /api/status` for the first load.

### Manual run

`Run now` starts a run of all servers or of one server. It uses the existing
`runner` lock: if a run is busy, the API answers `409` and the UI shows
"a run is busy". The refresher gets a way to run a subset of servers.
Manual runs record `run.source = manual`.

### Manual actions

All actions go through the refresher code, so they record attempts and
events exactly as automatic requests do, with `source = manual`.

- **Refresh** (one item): sends the refresh at once, outside the run lock.
  It counts as an attempt and as a refresh, so the backoff grows and
  autorefresh does not refresh the item again right away.
- **Bulk refresh**: a background job that takes the run lock, so it never
  runs beside a scheduled run. It paces requests with `refresh-delay`. It
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
  - For series, a candidate can also show the episode-number check that
    autorefresh uses for TV year conflicts (on request, because it calls
    TMDB).
  - Apply: sends the match, records an accepted or failed `request` event,
    and then a refresh is not needed (Plex refreshes on match).
- **Stop trying / Resume**: sets or clears `item.stopped_at` and writes a
  `stopped` or `resumed` event. Bulk stop is allowed.
- **Open in Plex**: a link to
  `https://app.plex.tv/desktop/#!/server/<machineIdentifier>/details?key=/library/metadata/<ratingKey>`.
  The machine identifier comes from `GET /` on the server, once per process.

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

Fetched from Plex when the detail opens (one request): summary, art, the
number of seasons and episodes for a series (`childCount`, `leafCount`), and
the path of a series if the cache has none (then saved).

### Images

- Item posters and art: `GET /api/servers/{server}/items/{ratingKey}/thumb`
  and `.../art`. The handler takes only a configured server name and a rating
  key that exists in the cache or the history, looks up the stored image
  path, and fetches it from Plex with the token through Plex's photo
  transcoder at a fixed set of sizes (`?size=sm|md|lg`). The client never
  supplies a path or URL. Responses get `Cache-Control` so the browser caches
  them.
- Candidate posters: the remote URLs that Plex's match search returns (no
  token). The browser loads them directly.

### HTTP API

JSON under `/api/`. All mutating endpoints are `POST`, need
`Content-Type: application/json`, and go through Go's
`net/http.CrossOriginProtection`. It accepts `Sec-Fetch-Site: same-origin`
(sent by browsers to HTTPS and localhost), and falls back to comparing
`Origin` with `Host` only when `Sec-Fetch-Site` is absent. This blocks
cross-site requests with or without Basic auth. No `X-Forwarded-Host` trust.
The README says that a reverse proxy serving the UI over plain HTTP must pass
the original `Host` header. (My call.)

Endpoints (builder may refine names):

- `GET /api/status`, `GET /api/events` (SSE)
- `GET /api/servers` (name, libraries, machine identifier)
- `GET /api/items?tab=attention|fixed|stopped&server=&library=&reason=&type=&q=&sort=&cursor=`
- `GET /api/items/{server}/{ratingKey}` (detail and timeline)
- `GET /api/runs`
- `POST /api/runs` (`{"server": ""}` for all)
- `POST /api/items/refresh`, `/stop`, `/resume` (list of items)
- `GET /api/items/{server}/{ratingKey}/matches?q=&year=`
- `POST /api/items/{server}/{ratingKey}/match`

`q` searches titles, and paths where the cache has them.

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
  (`stylex.vite()`), pointed only at our own source.
- TanStack Query for API data, with SSE messages that update or invalidate
  queries.
- Hash-based routes (`#/items/...`) and relative asset and API URLs, so the UI
  works behind a reverse proxy at any sub-path without configuration.
- Single-server installs hide the server picker and server columns.

### Build, test, and CI

- Source in `web/`. Build output in `internal/web/dist/`, embedded with
  `go:embed`. A committed placeholder `internal/web/dist/index.html` ("UI not
  built") keeps `go test`, `go vet`, and golangci-lint working without Node.
  The real build overwrites it, and `.gitignore` ignores everything else in
  that folder.
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
   retention, stopped flag (respected by `Due` and `CountIncomplete`),
   thumb, art, and path columns. No UI yet.
2. HTTP server: config, auth, same-origin checks, read API, image proxy,
   status and SSE, the progress hook, placeholder page.
3. Write API: refresh, bulk refresh, fix match search and apply, stop and
   resume, run now, dry-run refusal.
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
- The default port number.
- Page size and cursor format of the item list.
- Image sizes for the photo transcoder.
- Component structure of the React app.

## Open questions

None.
