# Agent guidance for autorefresh

This file gives repository rules for coding agents. It refines the owner's
global agent policy. If a rule here conflicts with an explicit user
instruction, follow the user instruction.

## What autorefresh does

Autorefresh is a Go service. It runs beside autoscan
(<https://github.com/l3uddz/autoscan>). On a schedule, it finds Plex items
with missing metadata, such as a missing summary or poster. It then asks Plex
to refresh the metadata of those items.

The project is in early development. The application design is not final.
When the design adds packages or rules, update this file.

| Path | Content |
| --- | --- |
| `cmd/autorefresh/` | CLI entry point. It holds the flags, the default paths, logging, and the wait for the configuration file (rejecting files over 1 MiB). The flags are `--config`, `--database`, `--dry-run`, `--once`, `--log`, `-v`, and `--version`. `run` wires the internal packages together, and tests call it with a temporary config and database. Outside `--dry-run`, it converts an older database to incremental auto-vacuum once after opening it (`database.go`), and runs `PRAGMA optimize` within 5 s (`finishStore`) before it closes the store at the end of `--once` and after `serveUntilStopped`; only an exit on a start error, or on a signal noticed before the dashboard binds, closes the store without it. It runs the refresher once with `--once`. Otherwise it checks the token of each Plex server and the TMDB token once (bounded by a 15 s timeout). Then it runs the refresher once at once and then on the `schedule` cron, with one run at a time. With the UI, the scheduler gives `web.Status` its next time when it starts and at each tick, also a tick that a busy run skips. SIGINT and SIGTERM cancel the run and stop the schedule. With `ui.listen` set, and without `--once` and `--dry-run`, `ui.go` binds the dashboard listener before the token checks, so a bind error stops the start before any Plex request. The `web.Status` then gets the token checks and the run progress, and the web server gets the store, the Plex clients, the queue rules, the secrets that its API redacts from stored error texts, and the credentials (the Plex tokens, the TMDB token, and the UI password) that its poster check uses. `uiCredentials` builds the credentials once, and `uiSecrets` adds the Plex hosts and path prefixes to them. The schedule has its own context. After a signal, `run` cancels the schedule promptly so no new ticks fire and an active scheduled run starts stopping, stops the UI (5 s timeout), waits for the scheduled run, closes Jobs to cancel and drain manual jobs, then drains item actions before closing the store. Tests replace `listenFunc` to bind a free port. |
| `internal/config/` | Configuration types, defaults, loading, and validation |
| `internal/plex/` | Plex API client: list libraries and items, get item paths and episode numbers, refresh, search matches, and apply a match. Movie listings and every item read send `includeGuids=1`, so `Item` and `Details` carry the external IDs (`ExternalIDs`, such as `tmdb://100`); a `Guid` value of another shape gives none. It also reads the server identity and the details of an item, and gets the thumb or art of an item through the photo transcoder, at most 10 MB. `PathPrefix` gives the path of the server URL, which the web server redacts. `ProviderSeries` asks the Plex metadata provider (`metadata.provider.plex.tv`) about the `plex://show/` GUID of a match candidate, with the server token in the header, and returns its TMDB ID and season and episode counts. The dashboard and the title fallback of the refresher call it. It is the only request to another host than the server. `Refresh` and `Match` (`write.go`) return a `plex.Outcome` with the error: `accepted` for a 2xx answer, `rejected` for 400, 401, 403, 404, or 405 (or a request that was never built), and `unknown` for everything else. They follow no redirect, so a 3xx answer is an error that is neither `ErrNotFound` nor `ErrUnavailable`; reads keep `neterr.SameOrigin`. The write client is a copy of the client on each call, without its own timeout when the deadline of the context ends first. |
| `internal/tmdb/` | TMDB API client: find the TMDB ID of a TVDB or IMDb ID, read release years and the premiere year of a series (`PremiereYear`: the year of the air date of its season 1, never a later season's), regular season episode numbers, and alternate TV episode groups, and check the token. It sends the token only in a header. |
| `internal/store/` | SQLite cache (WAL mode with `synchronous=NORMAL`, requiring local POSIX storage, and incremental auto-vacuum, which `Open` sets on a new file before WAL and `ConvertAutoVacuum` sets once on an older one up to 256 MiB; `Reclaim` releases the free pages of a database with incremental auto-vacuum when they are at least a fifth of the pages and 1 MiB, then tries a truncating WAL checkpoint, which a reader of another process can block, and `Optimize` runs `PRAGMA optimize`; the store logs nothing about them, its callers do) of incomplete items with their attempts and the last requests that Plex accepted, the read times of each library, and the TMDB IDs of items. It also keeps the history: events of requests, fix match decisions with their searches, candidates, and `MatchEvidence` (the TMDB ID of the year check, why TMDB was not asked, the IDs that TMDB has no single entry for, the confirming alternate TMDB `EpisodeGroup`, and the season whose episodes differ, with the episode numbers that only Plex or only TMDB has), completions, and gone items, where a gone event that a request found keeps the action of that request and a removal of a read or a check keeps none (the `action` column of `event`), and the runs of each server, with the number of libraries that each run read fully and incrementally. Each event that a run writes links to that run and keeps its item history when the run is removed. It removes events and runs older than the retention, and the cache of unused servers and libraries. Foreign keys with `ON DELETE CASCADE` remove the rows of an item or a library with it. A connection waits for the locks of another process and keeps temporary data in memory. For the dashboard, it lists the items of the `attention`, `fixed`, and `stopped` tabs with filters, sorts, and cursors, counts each tab, and reads item details, timelines, libraries, and pages of runs. `DueRules` decide the queue state for both `Due` and the lists. The methods that the web server calls take the request context first. `ApplyRead` neither adds nor changes the row of an item with a completed or gone event, or a request that Plex accepted, after `Read.Mark`, the `EventMark` (newest event ID and prune generations) of `ReadStart` that the refresher takes before it lists the library, because a check or a manual action settled or changed the item during the read, so the listing can be older. A later read that lists the item as incomplete adds it again, at the latest the next full read. Event IDs give this order; the event times have whole seconds, and a manual request records the time when it started. Migration 18 adds a `write_attempt` row for each Plex write, `read_hold` rows for items that a read or a check could not apply, and the prune generations of `prune_fence`. `BeginAttempt` saves an attempt before its write (with the admission of an automatic send, which returns `ErrHeld`), `DropAttempt` deletes an unsent one, `PruneAttempts` deletes the old ones, and `RecordRequest`, `RecordGone`, and `RecordManual` delete a rejected attempt or save its outcome. `ReadStart` returns the read times, the hold flag, and the mark in one snapshot. `ApplyRead` returns `ErrReadPruned` when a pruning ran since that mark, also skips and holds the keys with an attempt live at or after `Read.At` (`readguard.go`), and a full read deletes the holds that it reconciled and returns the held keys of `Read.Present` as `Omitted`. `ApplyCheck` and `ApplyCheckGone` take the `Observation` of the check (`Observe`) and yield with a hold (`ErrCheckYielded`); `AddHold` saves a hold. `DueRules` count a live attempt or a hold as `waiting`, and `Held` answers it for one item. The store clock (`SetClock`) is read only by `BeginAttempt` and the check saves. The store records stop and resume events, reads cached items or history snapshots through `KnownItem` and batched `KnownItems`, updates item metadata through `ApplyCheck`, reads run bulk items with their server via `RunItemsWithServer`, and saves bulk item results in the same transaction as the terminal run. |
| `internal/refresher/` | The run of each server. It removes the cache of unused servers and libraries, reads the libraries, and refreshes the due items, with the pacing (only after an item that sent a request to Plex), the limit for each run, and the dry run. It records the history of each run and removes old history, but a dry run records and removes none. After the servers of a scheduled run, a Run now scan, or a `--once` run end, so after every removal of the run, `upkeep` calls `Reclaim` and `Optimize` once, outside a dry run and unless the run context ended, and only logs their failures; a bulk refresh does no upkeep. It runs outside the run lock: `Run` (`--once`) calls it after `scan`, `Jobs.Scheduled` after it gives the lock back, and a Run now job through `launchThen`, so the upkeep never keeps the run lock busy and never makes a Run now answer that a run is busy; a Run now during it waits for the one database connection while the upkeep holds it (at most 2 s lock wait for each upkeep statement, plus its work). It logs the summary of each server. It also holds the rules for missing metadata, the wrong match check of a movie (`wrongmatch.go`: `CompareMovieIDs` compares the path IDs with the Plex external IDs by source in the order `tmdb`, `imdb`, `tvdb` with the normalization of `normalizeIDValue`; `mismatched` when no compared source agrees, and `undecided`, which keeps the stored bit through `store.Item.Keep`, when nothing can be compared; reads and checks set the reason, `recheck` and `Due` skip a row with only `store.ManualReasons`, and no request is sent for it), path ID and path year parsing, the fix match choice, the TMDB fallback, the title fallback of a series (`title.go`: a title and year search whose one same-title candidate the provider TMDB ID and the TMDB episode numbers confirm; when no same-title candidate's Plex year agrees, `pickByTMDBYear` checks at most `maxTitleYearChecks` same-title candidates with another known Plex year, whose provider TMDB ID must have a TMDB premiere year that equals the item year and the folder year, and the one candidate that passes then takes the same folder TMDB ID and episode checks), and match confirmation by year or episode numbers (allowing missing episodes at >=90% coverage across local regular seasons with zero extras, and falling back to TVDB-aligned TMDB episode groups within a bounded request cap and time budget). `compareEpisodes` also returns the seasons that its deciding comparison read (`SeasonEpisodes`), whether a differing season stopped it before the later local seasons, and the episode groups whose seasons it read, which the episode check of the dashboard answers. `Options.Progress` gets the live state of each run: its start, each library read, the batch, the next item before each pace wait (`ItemNext`), each item (`ItemStarted` with a `ProgressItem`: title, year, type, library, thumb, and reasons), each finished item, and its end. A nil `Progress` reports nothing. `Jobs` shares one run lock across scheduled scans, manual scans, and bulk refreshes. Item locks protect each send and its recording. A scheduled run rechecks the row after taking the item lock. The refresher also handles manual refresh, match search and application, episode checks, and stop and resume. A manual refresh (`RefreshItem`) handles an item as a run does: for a cached item that `seeksMatch`, it checks live match state via `ItemWithPaths` first (skipping `autoMatch` if Plex already matched the item and reusing the paths when still unmatched), runs `autoMatch` (also before its first refresh), and records its decision; a pick is applied and followed up by `settleMatch`, unless Plex no longer has the item unmatched or cannot tell just before the match (`keepMatch`), and a search that runs out of `manualSearchTimeout` falls back to the plain refresh, and a plain refresh of a cached item is followed by `followUpCheck`. The `decisionLog` of a fix match also records the TMDB steps that did not run or found nothing, the folder year, and the episode comparison. `autoMatch` is the one entry point of the automatic fix match decision: a run records and applies its decision, and `PreviewMatch` returns it for any known item without writing to the store. `settleMatch` follows up every accepted match, manual or of a run: a refresh, then checks that move a complete item to Fixed. A run records these steps, and counts a completion, as its own. An unavailable server ends the follow-up, and in a run also the run of that server. Every Plex write goes through `sendWrite` (`write.go`): it saves a write attempt (`store.BeginAttempt`, automatic for a run, with the sections whose read the run applied) and sends the write within one deadline, the server timeout (`Server.Timeout`); a write that it did not send returns `ErrNotSent`, and is neither recorded nor counted. A run reads a library with a hold in full, sends automatic writes only to the items of the libraries whose read it applied (`readSections`), skips a held item (cause `held`) and a held follow-up refresh (`Skipped follow-up refresh`), and logs `Held item not in listing`. The checks after a request get the attempts of its action and, after a match, the GUID of the candidate (`followUp`): a check saves nothing while Plex shows another GUID, a check that the store yields (`store.ErrCheckYielded`) ends the checks with a hold, and checks after a match that end without its GUID save a hold (`addHold`). |
| `internal/web/` | The HTTP server of the dashboard: optional Basic auth, cross-origin protection, security headers, the dashboard page, and a JSON API under `/api/`. `assets.go` serves `index.html` and `assets/`, with the `.gz` copy when the request accepts gzip. With the `webui` build tag, `assets_webui.go` embeds the Vite output in `internal/web/dist/`, which git ignores, and the build fails if it is missing. Without the tag, `assets_fallback.go` embeds the committed `fallback/index.html`, which says that the UI is not built. The API serves the live status and its server-sent event stream (capped at `defaultMaxSSEStreams`), the servers (coalescing concurrent Plex identity lookups on `baseCtx`), the item lists, the item details and a live Plex view (with the external IDs and, for a matched movie, the result of `refresher.CompareMovieIDs` as `mismatch`), the automatic fix match preview of an item, the metadata provider view of a series candidate, the posters and art through a bounded, coalescing proxy, the runs, and the details and bulk items of a run scoped to configured servers. The API delegates writes to the refresher and Jobs. `POST /api/item/refresh` answers the item detail after the refresh, with the fix match that it applied and whether a check saved the reasons of the item. It leaves out a detail that it fails to read, because Plex accepted the request, and its answer gets a longer write deadline (3 m), as do match (2 m) and search/preview/episode-check routes (90 s). `POST /api/item/episode-check` answers the result with the compared seasons, each with its sorted Plex and TMDB episode numbers. Known `POST` routes require JSON content type; unknown `/api/` paths return 404. Bodyless routes require an empty body, and other POST routes accept one JSON object up to 1 MiB. The live view fills an empty item path. `Status` keeps the live status and is the `refresher.Progress` of the service. For each run, it keeps the item in work (`current`), the next item with the end of the pace wait (`next`, `nextAt`), and the last finished item with its outcome and finish time (`last`), each a `NowItem` with a poster proxy URL. Each snapshot carries the `boot` ID of the process and `seq`, the count of its changes, so the frontend can keep the newer snapshot. The API replaces the configured secrets in stored error texts with `<redacted>`. It drops a candidate thumb whose URL holds a configured credential, also percent-decoded, but not one that holds only a host or a path prefix. On the routes that take query parameters, a query string that does not parse gets a 400. The handlers share the one SQLite connection with the refresher, so a handler never holds a transaction or open rows across a Plex request or a response write. `Stop` closes handler admission, so later requests get a 503, ends the event streams, shuts the server down within the deadline of its context (forcing a close on timeout), and cancels `baseCtx`. |
| `web/` | The dashboard frontend: React, TypeScript, Vite, TanStack Query, and Astryx with its Neutral theme in dark mode, recoloured with the tokens of `src/site-tokens.ts`. The Astryx packages are pinned to one exact version. `web/AGENTS.md` gives the Astryx rules: components and tokens only, no own CSS files or raw `div` styling. Routes live in the hash (`route.ts`), with an optional `?srv=` server scope that the top bar picker sets and every link keeps, and every URL is relative, so the page works at any proxy sub-path. `api-types.ts` holds the API DTO types, and `api.ts` re-exports them along with the queries, selective `useStatus`, and the event stream with its reconnect. GET and stream snapshots use `seq` to order updates within a process. A new `boot` replaces the old process, evicts retired-boot `candidate`/`auto-match` caches, and ignores later snapshots from retired processes. `bulk.ts` builds the bulk requests and sends bulk stop and resume, `queue-format.ts` formats bulk queue summaries and outcomes, and `queue.ts` runs a bulk refresh of several servers as one job per server, each after the previous one ends; it lives outside the components, so it survives page changes. Both skip a server whose bulk request answers 400 `no items`. A start reserves the queue before its first POST, so a second start fails. When the status no longer knows a job (a restart, a missed event, or a later last run), or is unavailable, the queue reads its end from `GET /api/runs` and ends it as unknown after a bounded number of polls, also when the runs are unavailable. `timeline.ts` derives the Fixed and removed details and the Refresh now result from the item timeline, because the item detail has no completion fields, and gives the texts of timeline events and match decisions. `history.tsx` renders the item timeline and the last automatic fix match decision from them. `app.tsx` wraps lazy routes in an error boundary with a Reload recovery action. `status.tsx`, `items.tsx` (with `itemtable.tsx` and `bulkband.tsx`), `detail.tsx`, and `runspage.tsx` (`#/runs`, cursor pages under the `['runs', 'pages', scope]` key) are the pages. `rundetail.tsx` is the run drawer of `#/runs/<id>`, which reads `GET /api/runs/detail` under the `['runs', 'detail', id]` key, and `runGroups` in `runs.ts` groups the run's events, or the items of a bulk refresh, by outcome. A click on a run count that can filter (`RunCount.shows`, a predicate: the requests of one kind, one group, or for Failed also an entry with a failed fix match decision) shows only the listed items that it selects (`filterGroups`), and a second click shows every item (`nextCountFilter`). A request counts by `RunEntry.sent`, which a removal with the action of the request that found it gone gets too, under Removed. The history does not list every counted request, such as a removal of a run from before the gone events kept their action, so the listed items can be fewer than the count. `rowopen.ts` opens a table row on click, Enter, or Space for the Items and Runs tables, `listlink.tsx` (`LinkListItem`) wraps Astryx `ListItem` so clicking or middle-clicking anywhere on a linked list row opens its target while keeping native anchor semantics and text-selection safety, and `media.tsx` holds the poster, art, remote candidate poster, and reason badges. `wrongmatch.tsx` is the warning of `DetailBody` for a live `mismatch` result of `mismatched`, with one line for each compared source and no button. `nowpanel.tsx` is the now panel of a busy server card: the item in work or the next item with its countdown, and the last finished item. `now.ts` gives its outcome, countdown, and age texts. The `Countdown` and `Ago` of the panel read `useSecondClock` in `clock.ts`, which ticks each second and is never earlier than the mount of its component; each takes its time as key, so a new status event starts it from the time of the event. `fixmatch.tsx` (with `searchform.tsx` and `candidatecard.tsx`) is the Fix match dialog of `ItemDetail`. Without a last automatic decision, it runs the automatic fix match preview of `GET /api/item/auto-match` on request and marks the preview's pick on the candidates. `previewQuery` keeps the preview in the cache while the page is open, and the panel shows the newer of the preview and the last decision. Each series candidate with a `plex://` GUID reads `GET /api/candidate` once (`candidateQuery`), for its counts and the TMDB ID that Compare episodes uses when no search ties one. `episodecheck.tsx` holds Compare episodes: its result opens the episode numbers of each compared season, which `seasonView` in `match.ts` marks as in both, only in TMDB, or only in Plex. `match.ts` groups its candidates by Plex GUID with the searches that found them, ties a candidate to a TMDB ID only when a search of that ID found it alone and no search of that ID found another candidate, and checks a typed search like the API. Its searches use keys outside the `item` prefix and never refetch by themselves, because each one calls Plex. The dialog freezes the item and the candidate when it asks for confirmation. `errors.ts` gives the toast text of a failed item action. `useNotify` in `notify.tsx` shows every action toast with an icon of its `Outcome`: success, notice, or error, defaulting `isAutoHide: true` so every outcome (including errors) auto-closes with Astryx's stock 5 s duration unless overridden. The live status also refreshes the `runs` queries when a run starts. Table mode windows its rows with `useTableRowsVirtualization`, which admits only columns without `renderCell`, so `table.ts` builds plain text rows, and a flat row plugin opens a row. Keep the table data identity stable (memoized): Astryx re-anchors the scroll on each new data array. Vitest tests sit beside the modules. `pnpm build` writes `internal/web/dist/`. |
| `internal/neterr/` | Shared Plex and TMDB transport error classification (`Reason`), same-origin redirect check (`SameOrigin`), and bounded response body draining (`DrainAndClose`) |
| `docker/Dockerfile` | Container image, built from the goreleaser output in `dist/`. It runs as the non-root user `65532:65532`, which owns `/config`. It declares `EXPOSE 7380` for the dashboard, but nothing listens until `ui.listen` is set. |
| `mise.toml` | Tool versions and development tasks |
| `.golangci.yml` | golangci-lint configuration: the formatter, the linters, and the complexity limit that `mise run lint` checks |
| `.goreleaser.yml` | Release build configuration. A `before` hook runs `mise run web-build`, and the build uses the `webui` tag, so every release embeds the dashboard. |
| `openspec/` | OpenSpec root with the `spec-driven` schema. `specs/<capability>/spec.md` describes the current behaviour of each capability: `configuration`, `service-operation`, `library-scan`, `refresh-runs`, `fix-match`, `history`, `manual-actions`, `dashboard-api`, and `dashboard-ui`. `changes/<name>/` holds a planned change, and `changes/archive/` holds finished ones. The archive starts with the five designs and plans of the retired superpowers workflow, which describe the design as it was then. `config.yaml` holds the schema, the project context, the rules of each artifact, and the guidance for apply and archive. |

## Code changes

### Code quality

Apply SOLID, KISS, DRY, and YAGNI to each code change.

- Give each package and function a clear responsibility. Keep interfaces small
  and define them where callers use them.
- Prefer direct control flow and names that explain intent.
- If existing code implements the same rule, reuse it. If two rules are
  separate, keep their code separate. If the code looks similar, keep it
  separate too.
- Add abstractions, configuration, and dependencies only for current needs.
- Before delivery, review the diff for duplicated rules, unnecessary
  abstraction, and unused code. Fix the problems that the change introduces.
- Keep each production function at a cognitive complexity of 15 or less.
  `mise run lint` enforces it.
- Preserve behavior outside the requested scope. Keep input validation and
  error handling that prevents lost work.

### Repository conventions

- Tests must not need network access, a running Plex server, or a TMDB
  token. Use `net/http/httptest` and put recorded Plex and TMDB responses in
  the `testdata/` directory of the package that reads them.
- Remove server addresses, tokens, and other private values from recorded
  responses before you commit them.
- Build URLs with `net/url`: `url.URL`, `URL.JoinPath`, `url.Values`, and
  `url.PathEscape`. Do not join strings to make a URL. `JoinPath` does not
  escape its segments and removes `..`, so escape each segment and check
  each ID that comes from outside.
- Log with the zerolog global logger, `github.com/rs/zerolog/log`. `main`
  configures it in `cmd/autorefresh/logging.go`. Put values in fields, for
  example `log.Info().Str("item", title).Msg("Refreshed item")`, not in the
  message text.
- If you change user-facing behavior or configuration, update `README.md`.
- If you change the behaviour that a spec in `openspec/specs/` describes, plan the change as an OpenSpec change, or update that spec in the same branch. The specs follow the code, not the archived designs.
- The Go version comes from `go.mod`. Do not lower it. Keep the `go` version
  in `mise.toml` on the same minor release.

## Checks

You need [mise](https://mise.jdx.dev). Run `mise install` once to install
the Go, goreleaser, golangci-lint, Node.js, and pnpm versions from
`mise.toml`.

The required `build` job of the `ci` workflow runs the following commands in
order before packaging. Run the same commands before you push.

```sh
mise run vendor
mise run test
mise run lint
mise run web-install
mise run web-lint
mise run web-test
mise run test-webui
```

The `web-*` tasks run in `web/`. `web-install` runs
`pnpm install --frozen-lockfile`. `web-lint` runs the TypeScript check and
ESLint, and `web-test` runs Vitest. `test-webui` runs `web-build`, which
builds the frontend into `internal/web/dist/`, and then the `internal/web`
tests with the `webui` tag. They check that the binary embeds and serves
the built frontend, not the fallback page. A plain `go test` or `go build` needs
no Node.js. If you change `web/`, also run `pnpm exec astryx doctor --built .`
in `web/`.

`mise run vendor` runs `go mod tidy` and then creates `vendor/`. Git ignores
`vendor/`. Do not commit it. If `go mod tidy` changes `go.mod` or `go.sum`,
commit those changes with the change that caused them. If `go mod tidy`
changes the committed files, CI fails.

`mise run lint` runs golangci-lint with the configuration in
`.golangci.yml`. A lint failure fails the `build` job.

- `golangci-lint fmt --diff` fails if a file needs `gofmt`. Then run
  `mise run format`, which rewrites the files in the `gofmt` format.
- `golangci-lint run` runs the analyzers of `go vet`, every staticcheck
  check, including the style checks (`ST*`) and unused code (`U1000`), and
  `gocognit`.
- `gocognit` fails a production function with a cognitive complexity
  above 15. Tests are exempt. Split a function that grows above the limit
  into named steps.
- Fix each finding. Do not hide one with a `//nolint` comment. Disable a
  check only in `.golangci.yml`, with the reason in a comment, and only
  after the maintainer agrees.

`mise run build` builds the frontend and makes a local binary with the
`webui` tag in `dist/`.
`mise run snapshot` makes the release binaries in `dist/` with goreleaser.

## CI side effects

The `ci` workflow in `.github/workflows/ci.yml` runs on pushes to
`main`, on `v*` tags, and on PRs into `main`. A push to another branch, such
as `fix/<issue>-<slug>`, runs CI only after you open a PR into `main`.

Pushes to `main` and PRs run the `build` job, which only reads the
repository. A `v*` tag runs the `release` job instead. It has the same
steps through a YAML anchor, and it alone gets write access to publish the
GitHub release. Keep the name `build`, because the ruleset requires it.

- If a run is not from a fork, CI publishes a public Docker image tag,
  `l3uddz/autorefresh:<branch>`. For a PR, the image contains the PR merge
  commit. `.github/scripts/docker-branch-tag.sh` makes the tag from the
  branch name. Each character that a Docker tag does not allow, such as `/`,
  becomes a hyphen. A tag longer than 128 characters is cut and gets a hash
  suffix.
- Releases own the tag `latest` and the tags that start with a digit. If a
  branch name maps to one of these tags, the build fails. Do not give a
  branch such a name.
- If two branches on origin map to the same tag, such as `fix/a` and
  `fix-a`, the build and the cleanup both fail.
- The workflows pin each action to a commit SHA, with the version in a
  comment. No bot updates them. To update an action, take the SHA of the new
  tag from `gh api repos/<owner>/<repo>/commits/<tag>` and change the SHA and
  the comment together.
- A new run cancels an older run for the same ref. Only one ref publishes
  each branch tag: `main` for pushes, or the PR for another branch. An older
  commit then cannot replace the image of a newer commit.
- If you delete a branch, the `docker-cleanup` workflow removes its Docker
  tag. It uses the same script as the build.
- If someone pushes a `v*` tag, CI publishes a GitHub release and the Docker
  tags `<version>` and `latest`. Do not create or push tags unless the user
  asks for a release.
- The release notes list the commit subjects since the previous tag. The
  `changelog` section of `.goreleaser.yml` groups them by Conventional Commit
  type: breaking changes, features, bug fixes, performance, and other
  changes. It leaves out `docs` and `test` commits and merge commits. It
  reads only the subject line. "Commits and pull requests" gives the rules
  that keep the release notes correct.

## Delivery

- The default branch is `main`. It is the only integration branch.
- Start each change on a short-lived branch from `main`. Name it
  `<type>/<issue>-<slug>`, for example `fix/31-missing-sections`. Use the
  commit type that fits the change, such as `feat`, `fix`, `docs`, `build`,
  or `ci`.
- Track work in GitHub issues on this repository. If no issue exists, create
  one before you start, or ask the user. Do not invent an issue number.
- The repository allows only merge commits. It deletes the source branch after
  a merge. It allows auto-merge. It has no PR template.
- The `main protection` ruleset protects `main`. It requires the `build`
  check and allows only merge commits. It blocks deletion and force pushes.
- The ruleset requires one approving review. An open "changes requested"
  review also blocks the merge. GitHub does not let the PR author approve
  their own PR.
- CodeRabbit reviews every PR and can request changes. It approves only after
  all of its threads are resolved, and its approval counts as the required
  review. `.coderabbit.yaml` holds its configuration. Its title check fails a
  PR title that does not use the Conventional Commits format.
- If you fix a CodeRabbit finding, reply in its thread. Do not post
  `@coderabbitai approve` or `@coderabbitai resolve` to get an approval.
- Contributors do not approve, merge, or enable auto-merge for a PR into
  `main`. The maintainer, `l3uddz`, merges it after CodeRabbit approves and
  the `build` check passes.
- One exception applies only to the maintainer's own PRs. If `l3uddz` opened
  the PR, an agent that works for the maintainer enables auto-merge with a
  merge commit. GitHub then merges the PR after the ruleset is satisfied:
  one approving review, no open "changes requested" review, and a passing
  `build` check. The ruleset accepts an approval from any reviewer. Today
  `l3uddz` is the only account with write access and cannot approve their
  own PR, so the approval comes from CodeRabbit. If another account gets
  write access, its approval alone satisfies the ruleset, even before
  CodeRabbit approves. The agent still
  never approves a PR and never bypasses the ruleset.
- In a PR into `main`, use `Closes #<id>` only for issues that the PR
  delivers.

## Commits and pull requests

The repository merges with merge commits, so every commit on a branch stays
in the history of `main`. Each commit subject also becomes one line of the
release notes. Write each subject for a user who reads the release notes.

### Commit messages

Use the Conventional Commits format for every commit subject:

```text
<type>(<scope>)[!]: <summary>
```

- The type is one of `feat`, `fix`, `docs`, `refactor`, `perf`, `test`,
  `build`, `ci`, `chore`, `style`, or `revert`. Choose it from the change,
  not from the branch name. `feat` is new behavior, `fix` is a bug fix, and
  `docs` is documentation only.
- The scope is required and lowercase. It names the affected area, for
  example `config`, `plex`, `store`, `refresher`, `cli`, `docker`,
  `release`, `readme`, or `agents`.
- The summary is a short imperative phrase with no trailing period, for
  example `handle missing library sections`. The whole subject has at most
  72 characters.
- If a commit makes a breaking change, put `!` before the colon, for
  example `feat(config)!: rename the plex section`. Also explain the impact
  and the migration in a `BREAKING CHANGE:` footer. The release notes read
  only the subject. Without `!`, the commit is not listed under breaking
  changes.
- Put the reason for the change in the body, after a blank line. Reference
  only real issues. Keep closing keywords such as `Closes #<id>` out of
  commit messages.
- Make one change in each commit. A follow-up fix for a review finding is
  its own `fix` or `docs` commit, and it appears in the release notes too.
- Check each new commit subject before you push. After you push a branch,
  do not rewrite its commits and do not force-push it.

### Merge commits

Keep the default merge commit title. On GitHub, it is `Merge pull request
#<id> from <branch>`. For a local merge, it is the `Merge branch '<branch>'`
subject that git writes. Do not write a custom merge commit title, also not
one in the Conventional Commits format. The release notes leave out only the
commits whose subject starts with `Merge`.

### Pull requests

- The PR title uses the same format as a commit subject. The CodeRabbit
  title check fails any other title.
- Write the description for a reader who did not see your work:
  - the problem
  - the change and the resulting behavior
  - the checks that you ran and their results
  - the source branch and the target branch
  - `Closes #<id>` for each issue that the PR delivers
- Keep local checks, live checks on a real Plex server, and acceptance that
  is still open apart. Do not claim a check that did not run.
- Do not put tokens, server addresses, or other private values in a commit,
  a PR description, or a comment.
- If the scope of a PR changes, rewrite its title and description.
