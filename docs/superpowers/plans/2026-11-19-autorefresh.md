# Autorefresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build autorefresh: a scheduled service that finds Plex movies and series with missing metadata and refreshes or fix-matches them one item at a time.

**Architecture:** One binary. `cmd/autorefresh` wires four packages under `internal/`: `config` (YAML), `plex` (HTTP client), `store` (SQLite cache), and `refresher` (the rules). The refresher reads each library into the cache, then sends paced refresh and match requests for the due items.

**Tech Stack:** Go 1.27, `modernc.org/sqlite`, `github.com/robfig/cron/v3`, `go.yaml.in/yaml/v3`, zerolog, kong, mise.

**Spec:** `docs/superpowers/specs/2026-11-19-autorefresh-design.md` (issue #7, PR #8, merge commit `e2d689e`). The spec is the source of truth. Read it with this plan.

Plan issue: #9.

## Global Constraints

Each task includes these requirements. The values come from the spec and from `AGENTS.md`.

- Autorefresh must never send a library-wide refresh (`/library/sections/<id>/refresh`). No code builds that path.
- Refresh only movies and series, one item at a time, with `PUT /library/metadata/<ratingKey>/refresh`. Never refresh seasons or episodes.
- A token or server address must never appear in a log line or an error message.
- Every Plex request sends the token in the `X-Plex-Token` header and `Accept: application/json`.
- Check only libraries of type `movie` with agent `tv.plex.agents.movie` and of type `show` with agent `tv.plex.agents.series`.
- A regular episode is outside season 0. Specials never count.
- Use `modernc.org/sqlite` (no cgo) with one open connection. Embed the migrations as SQL files.
- Use `github.com/robfig/cron/v3` for the schedule.
- Configuration defaults: `schedule` `0 * * * *`, `minimum-age` `24h`, `refresh-interval` `12h`, `cache-refresh` `0 3 * * 0` (a cron expression), `oversample` `48h`, `refresh-delay` `5s`, `max-refreshes-per-run` no limit. `plex` needs at least one server.
- Each server needs `name`, `url`, and `token`. Each `name` is unique. `url` accepts `http` and `https`.
- New flags: `--config` (`AUTOREFRESH_CONFIG`), `--database` (`AUTOREFRESH_DATABASE`), `--dry-run` (`AUTOREFRESH_DRY_RUN`), `--once` (`AUTOREFRESH_ONCE`). Keep `--version`, `--log`, and `-v`.
- Default files are `config.yml` and `autorefresh.db` in the default configuration directory.
- Log with the zerolog global logger `github.com/rs/zerolog/log`. Put values in fields, not in the message text. The levels follow the spec's Logging table.
- Tests need no network and no Plex server. Use `net/http/httptest`. Put recorded Plex responses in the `testdata/` directory of the package that reads them.
- Do not lower the Go version in `go.mod`. Commit the `go.mod` and `go.sum` changes with the task that first imports a module.
- Before each push, run `mise run vendor`, `mise run test`, and `mise run lint`. Do not commit `vendor/`.
- Use Conventional Commits with a lowercase scope for commit subjects and PR titles.

## Review Focus

These inputs are likely to break autorefresh for a user, and the spec does not test them. Each line has a test in the named task.

1. A rating key that is not a decimal number, such as `../sections/1`. The client must refuse it and send no request, because the URL join collapses `..` into a section refresh. Test: Task 2.
2. A library read that fails part of the way. Autorefresh must not change the read times, must not delete unseen rows, and must keep the last episode reasons. Test: Task 5.
3. A series that is complete at item level but has an episode reason. An item read must not delete its row. Test: Task 3 and Task 5.
4. An `oversample` shorter than `minimum-age`. A young incomplete item must stay in the cache. After `minimum-age`, it must become due. Test: Task 3.
5. A failed match search. Autorefresh must send no match and no plain refresh, must record the attempt, and must not count it toward `max-refreshes-per-run`. A dry run must write no attempt or refresh state. Test: Task 6.

## Builder decisions

The spec leaves these points to the builder, or it is silent on them. Each decision uses the plain wording of the spec first and the autoscan pattern second. The owner approves them with this plan.

The owner chose three rows on 2026-11-19, in answers to separate questions: "Missing or empty configuration file", "Dry run", and the 401 part of "Plex status codes".

| Point | Decision |
| --- | --- |
| Page size for listings | 500 items. |
| HTTP timeout | 30 seconds for each request, as in autoscan. |
| Listing order | `sort=addedAt` (ascending). New items then land on the last page, and earlier pages do not move during a read. The recording probe makes sure that Plex accepts the parameter. |
| `excludeElements` | Series listings: `Media,Role,Director,Writer,Genre,Country`. Movie listings: `Role,Director,Writer,Genre,Country`, because they need `Media` for the file paths. |
| Plex status codes | 2xx is success. 404 is `plex.ErrNotFound`. 401, 502, 503, 504, a transport error, and a timeout are `plex.ErrUnavailable`. Every other status, also 500, is a plain error. Autoscan maps 404 and 500 to "unavailable". Autorefresh does not, because a 404 deletes a row and Plex returns 500 for a bad filter. |
| "Plex does not answer" | Any `plex.ErrUnavailable`. Autorefresh logs an error and skips the server for the rest of the run. |
| Other read failure | Autorefresh logs a warning, keeps the read state of that library, and continues with the next library. The refresh step still uses the cached rows. |
| Time of a read | The start time of the read. Times are stored as Unix seconds, so the due order compares numbers, not text. |
| Minimum age | The store keeps young incomplete items. The due query skips them. |
| Which rows can be due | Only rows of libraries that the current run checks. Rows of removed libraries stay unused. |
| Movie paths for a fix match | The file paths from the listings of the current run. If no listing of the run returned the movie, autorefresh requests the single item. |
| Dry run | Autorefresh reads Plex, updates the cache, and sends match searches (reads) to choose the logged action. It sends no refresh or match request, writes no attempt or refresh state, does not wait `refresh-delay`, and applies `max-refreshes-per-run` to the logged items. |
| Missing or empty configuration file | Autorefresh waits for it and checks again every 5 seconds, as autoscan's `waitForConfig` does. A container can then start before `config.yml` exists. |
| `max-refreshes-per-run` | If it is set, it must be 1 or more. |
| `minimum-age`, `oversample`, `refresh-delay` | 0 or more. `refresh-interval` must be more than 0. |
| Schedule | `schedule` and `cache-refresh` use `cron.ParseStandard` (5 fields and descriptors such as `@hourly`), in the local time zone (`TZ`). Without `--once`, the first run waits for the schedule. |
| `cache-refresh` default | `0 3 * * 0`, weekly at 03:00 on Sunday. It replaces the `168h` of the design. |
| Shutdown | On SIGINT or SIGTERM, autorefresh stops the schedule and cancels the run. The run stops before its next request. Then the process exits. |
| Warnings that repeat | A skipped library with another agent and a configured library title that Plex does not have each log one warning for each process. |
| YAML library | `go.yaml.in/yaml/v3` with `KnownFields(true)`. An unknown key is an error, as in autoscan. A test pins duration decoding. |
| Docker | The image sets `AUTOREFRESH_CONFIG=/config/config.yml` and `AUTOREFRESH_DATABASE=/config/autorefresh.db`. The scratch image has no `HOME`, so the default directory is `/app`, which is outside the `/config` volume. |

Not ported from autoscan: the `Scan` method (`library/sections/<id>/refresh`), the `migrate` package as a separate package, and text time columns.

## Recording test data

Only the main session records. A subagent cannot receive the secrets. Record all Plex fixtures in one session at the start of Task 2, so the owner gets the prompts once.

Rules:

- Send only `GET` requests. Never send `PUT`. The refresh and match handlers in the tests are written by hand, because Plex returns an empty 200 body for both.
- Keep the URL, the token, and the raw responses outside the repository. Use a `rec/` directory in the session scratchpad.
- Never print a response body, a URL, or curl's stderr. Curl names the host in its errors. Write bodies and stderr to files and print only exit codes, counts, and rating keys. Do not use `curl -v` or `set -x`.
- After the recording, delete the secret files and the raw responses.

Procedure:

1. Before the prompt, write the recording commands as one script in the scratchpad, `rec/record.sh`. It takes the two secret file paths as arguments. Read it once more and make sure that every request uses `--request GET`.
2. Call `request_secret` with `delivery: "file"` twice: label `Plex server URL`, then label `Plex token`. Ask for one value on each card.
3. The first command copies both secret files into `rec/` with `umask 077`. A secret file can be gone after its first read, and the copies serve the later commands.
4. Write a header file: `printf 'X-Plex-Token: %s\nAccept: application/json\n' "$(cat rec/token)" > rec/headers`. `printf` is a shell builtin, so the token is not in the process list.
5. Use one helper for every request: `curl -sS --fail --request GET --max-time 60 -H @rec/headers -o "rec/<name>.json" "$(cat rec/url)<path-and-query>" 2>"rec/<name>.err"; echo "exit=$?"`.
6. Probe the request forms on one movie library. Print only numbers and rating keys.
   - Filter encoding: get `totalSize` with `X-Plex-Container-Size=0` for no filter, for raw `addedAt>>=<unix time 30 days ago>`, and for percent-encoded `addedAt%3E%3E=<same time>`. If the count of a form is lower than the unfiltered count, that form filters. If both forms filter with the same count, use the percent-encoded form, because `url.Values.Encode` makes it. If only one form filters, use that form. If neither filters, stop and tell the owner.
   - Order: get one item with `sort=addedAt` and one with `sort=addedAt:desc`. If the `addedAt` of `sort=addedAt` is lower, that form is ascending. Otherwise try `sort=addedAt:asc`.
   - Paging: get one item with `X-Plex-Container-Start=0` and one with `X-Plex-Container-Start=1`, both with `X-Plex-Container-Size=1` in the query. If each response has one item and the rating keys differ, paging in the query works. Otherwise try the same names as headers.
   - Language: search `title=tmdb-747059` with the movie agent once with `language=<section language>` and once without it. Both must give the same number of candidates.

   Record the results in the Task 2 PR description. The client and the tests use the forms that won.
7. Record these responses. Each request uses the exact query that the client builds: the winning forms, `sort`, `excludeElements`, the paging parameters, and the match search `language`. A size of 1 to 3 keeps each file small.

   | File | Request |
   | --- | --- |
   | `sections.json` | `GET /library/sections` |
   | `movies_matched.json` | type 1, `unmatched=0`, size 2 |
   | `movies_unmatched.json` | type 1, `unmatched=1`, size 1 |
   | `shows_unmatched.json` | type 2, `unmatched=1`, size 1 |
   | `shows_episode_summary.json` | the spec's first episode request, size 2 |
   | `item_movie.json` | `GET /library/metadata/<key>` of a movie |
   | `item_show.json` | `GET /library/metadata/<key>` of a series |
   | `matches_one.json` | match search with `title=tmdb-747059` and the movie agent, on an unmatched movie |
   | `matches_none.json` | match search with `title=imdb-tt13167288`, on the same movie |

   The two IDs in the match searches are already public in the spec. Find the rating keys with `jq` and print only the keys.
8. Clean each file into `internal/plex/testdata/` with a `jq` program in the scratchpad. Use an allow list of keys, not a block list. Keep the JSON types and keep absent keys absent: `ratingKey` stays a string, a missing `thumb`, `art`, or `updatedAt` stays absent, and an empty `summary` stays `""`.

   | Object | Kept keys | Neutral values |
   | --- | --- | --- |
   | `MediaContainer` | `size`, `offset`, `totalSize`, `Directory`, `Metadata`, `SearchResult` | counts unchanged |
   | `Directory` (section) | `key`, `type`, `title`, `agent`, `language` | `key` `1`, `2`, `3`, and titles `Movies`, `TV Shows`, `Home Videos` |
   | `Metadata` | `ratingKey`, `key`, `guid`, `type`, `title`, `year`, `summary`, `thumb`, `art`, `addedAt`, `updatedAt`, `Media[].Part[].file`, `Location[].path` | See the list below the table. |
   | `SearchResult` | `guid`, `name`, `year`, `type`, `summary`, `thumb` | `name` `Movie One`, `thumb` `https://metadata.example.invalid/thumb.jpg` |

   Neutral values for `Metadata`:

   - Rating keys start at `1001`. Use the same new key in `key`, `thumb`, `art`, and `local://` guids.
   - Titles become `Movie One` and `Series One`.
   - A summary that is not empty becomes `A neutral summary.`
   - A `plex://` guid becomes `plex://movie/000000000000000000000001` or `plex://show/000000000000000000000001`.
   - Times start at `1700000000`.
   - File paths become `/media/movies/Movie One (2020) {tmdb-100001}/Movie One (2020).mkv`.
   - Series paths become `/media/tv/Series One (2021) {tvdb-200001}`.

   The server can have no library with another agent. Then copy the movie section into a third entry. Set its `agent` to `tv.plex.agents.none`, its `type` to `movie`, and its `title` to `Home Videos`.
9. Run the leak check. It prints counts only, and each count must be 0.
   - `grep -rFc -f rec/token internal/plex/testdata`
   - the host of the URL: write it to `rec/host` with `sed`, then `grep -rFc -f rec/host internal/plex/testdata`
   - `grep -rEc 'machineIdentifier|uuid|librarySectionUUID' internal/plex/testdata`
   - the original titles, file names, and paths: extract every `title`, `file`, and `path` value from `rec/*.json` into `rec/private`, then `grep -rFc -f rec/private internal/plex/testdata`
10. Run `rm -rf rec`. If the two secret files still exist, delete them.

If a later task needs a new fixture, it repeats this procedure for that fixture only.

---

## Delivery for each task

Each task is one GitHub issue, one branch, and one PR into `main`. The tasks run in order, because CI runs only on PRs into `main`. Stacked task branches get no `build` check.

1. After the owner approves this plan, create the seven issues in the "Planned issues" table. Then each task uses its real issue number.
2. Start each branch from the latest `origin/main`, after the PR of the previous task merged: `git switch -c feat/<issue>-<slug> origin/main`.
3. Use TDD in each task: write the failing test, see it fail, write the code, see it pass.
4. Before each push, run the checks:

   ```sh
   mise run vendor
   mise run test
   mise run lint
   git diff --exit-code -- go.mod go.sum
   ```

   Expected: every test passes, `lint` lists no file, and the `git diff` shows no change after the commit.
5. Open the PR with the planned title. Write the problem, the change, the check results, the source and parent branches, and `Closes #<issue>`.
6. Address each CodeRabbit finding. Reply in its thread with `@coderabbitai`, the fixing commit SHA, and the change. Never post `@coderabbitai approve` or `@coderabbitai resolve`.
7. When all threads are resolved, enable auto-merge, which is the owner's standing rule for this repository: `gh pr merge <n> --auto --merge --delete-branch`. Never approve the PR yourself.
8. After the merge, read the PR state and the merge commit. Make sure that the issue is closed.

## Planned issues

| Task | Issue title | Branch | PR title | Needs |
| --- | --- | --- | --- | --- |
| 1 | Add the configuration package | `feat/<issue>-config` | `feat(config): load and validate the configuration` | none |
| 2 | Add the Plex API client | `feat/<issue>-plex-client` | `feat(plex): add the Plex API client` | none |
| 3 | Add the SQLite cache | `feat/<issue>-store` | `feat(store): add the SQLite cache of incomplete items` | none |
| 4 | Add the missing-metadata and fix match rules | `feat/<issue>-refresher-rules` | `feat(refresher): add missing-metadata and fix match rules` | 2, 3 |
| 5 | Read Plex libraries into the cache | `feat/<issue>-library-reads` | `feat(refresher): read libraries into the cache` | 4 |
| 6 | Refresh and match the due items | `feat/<issue>-refresh-run` | `feat(refresher): refresh and match due items` | 5 |
| 7 | Run autorefresh on a schedule | `feat/<issue>-schedule` | `feat(cli): run autorefresh on a schedule` | 1, 6 |

Each issue body has four parts. Problem tells what is missing. Scope lists the files and the behavior of the task, with a link to its heading in this plan. Out of scope names the later tasks. The last part refers to the spec and to #9. Tasks 1, 2, and 3 do not depend on each other. They still merge one at a time, because each changes `go.mod`.

Each task that adds a package also adds its row to the path table in `AGENTS.md`.

---

### Task 1: Configuration

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `go.mod`, `go.sum` (add `go.yaml.in/yaml/v3` and `github.com/robfig/cron/v3`)
- Modify: `AGENTS.md` (path table row for `internal/config/`)

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
package config

type Config struct {
	Schedule           string        `yaml:"schedule"`
	MinimumAge         time.Duration `yaml:"minimum-age"`
	RefreshInterval    time.Duration `yaml:"refresh-interval"`
	CacheRefresh       string        `yaml:"cache-refresh"` // cron expression
	Oversample         time.Duration `yaml:"oversample"`
	RefreshDelay       time.Duration `yaml:"refresh-delay"`
	MaxRefreshesPerRun *int          `yaml:"max-refreshes-per-run"` // nil: no limit
	Plex               []Server      `yaml:"plex"`
}

type Server struct {
	Name      string   `yaml:"name"`
	URL       string   `yaml:"url"`
	Token     string   `yaml:"token"`
	Libraries []string `yaml:"libraries"` // empty: all supported libraries
}

// Decode reads YAML from r over the defaults, rejects unknown keys, and
// validates the result.
func Decode(r io.Reader) (Config, error)
```

- [ ] Step 1: Write the failing tests in `internal/config/config_test.go`.
  - `TestDecodeAppliesDefaults`: the file has only one server (`name: main`, `url: http://plex.example.invalid:32400`, `token: example-token`). Expected values:
    - `Schedule == "0 * * * *"`
    - `MinimumAge == 24*time.Hour`, `RefreshInterval == 12*time.Hour`, `CacheRefresh == "0 3 * * 0"`
    - `Oversample == 48*time.Hour`, `RefreshDelay == 5*time.Second`
    - `MaxRefreshesPerRun == nil`
  - `TestDecodeReadsAllKeys`: the YAML example from the spec, with neutral values, gives every key, `*MaxRefreshesPerRun == 100`, and `Libraries == []string{"Movies", "TV"}`.
  - `TestDecodeRejectsUnknownKey`: `refresh-dely: 5s` returns an error.
  - `TestDecodeRejectsInvalidConfig`, a table test. Each of these cases returns an error:
    - no `plex` entry, two servers with the same `name`, an empty `name`
    - an empty `url`, `url: ftp://plex.example.invalid`, a `url` without a host, an empty `token`
    - `schedule: every hour`, `refresh-interval: 0s`, `cache-refresh: 168h`
    - `minimum-age: -1h`, `oversample: -1h`, `refresh-delay: -1s`
    - `max-refreshes-per-run: 0`, an empty library title
  - `TestDecodeErrorsOmitSecrets`: a server with `url: ftp://secret-host.example.invalid` and `token: secret-token` returns an error that contains the server name and contains neither `secret-host` nor `secret-token`.
- [ ] Step 2: Run `go test ./internal/config/...`. Expected: FAIL, because `Decode` is not defined.
- [ ] Step 3: Implement `Decode` in `internal/config/config.go`. Use `yaml.NewDecoder(r)` with `KnownFields(true)` and `cron.ParseStandard` for `schedule` and `cache-refresh`. Name the server by its `name` in each error, never by its URL or token.
- [ ] Step 4: Run `go test ./internal/config/...`. Expected: PASS.
- [ ] Step 5: Add the `internal/config/` row to `AGENTS.md`. Run the checks from "Delivery for each task". Commit with `feat(config): load and validate the configuration`.

### Task 2: Plex client

**Files:**
- Create: `internal/plex/client.go` (`New`, request sending, status mapping, trace log, rating key check)
- Create: `internal/plex/library.go` (`Libraries`, `List`, `ItemPaths`)
- Create: `internal/plex/action.go` (`Refresh`, `SearchMatches`, `Match`)
- Test: `internal/plex/client_test.go`, `internal/plex/library_test.go`, `internal/plex/action_test.go`
- Create: `internal/plex/testdata/*.json` (see "Recording test data")
- Modify: `AGENTS.md` (path table row for `internal/plex/`)

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
package plex

var (
	ErrUnavailable = errors.New("plex server unavailable") // 401, 502, 503, 504, transport error, timeout
	ErrNotFound    = errors.New("plex item not found")     // 404
)

const (
	TypeMovie = 1
	TypeShow  = 2
)

// name is the server name from the configuration. It labels trace lines.
func New(name, baseURL, token string) *Client

type Library struct {
	ID, Title, Type, Agent, Language string // Type: "movie" or "show"
}

type Item struct {
	RatingKey string
	GUID      string // "local://<ratingKey>" when unmatched
	Title     string
	Year      int    // 0 when absent
	Summary   string
	Thumb     string // "" when absent
	Art       string // "" when absent
	AddedAt   time.Time
	Files     []string // movie listings: Media[].Part[].file
}

// Filter is one Plex filter, sent as Field+Op+Value, for example
// {Field: "addedAt", Op: ">>=", Value: "1795025872"} or
// {Field: "episode.summary", Op: "=", Value: ""}.
type Filter struct{ Field, Op, Value string }

type Candidate struct {
	GUID, Name string
	Year       int // 0 when absent
}

func (c *Client) Libraries(ctx context.Context) ([]Library, error)
// List reads every page of one item type in a section.
func (c *Client) List(ctx context.Context, section string, itemType int, filters ...Filter) ([]Item, error)
// ItemPaths returns Location[].path of a series, or Media[].Part[].file of a movie.
func (c *Client) ItemPaths(ctx context.Context, ratingKey string) ([]string, error)
func (c *Client) Refresh(ctx context.Context, ratingKey string) error
func (c *Client) SearchMatches(ctx context.Context, ratingKey, title, agent, language string) ([]Candidate, error)
func (c *Client) Match(ctx context.Context, ratingKey string, cand Candidate) error
```

Requests:

| Method | Request |
| --- | --- |
| `Libraries` | `GET /library/sections` |
| `List` | `GET /library/sections/<section>/all?type=<n>&sort=addedAt&excludeElements=<list>&X-Plex-Container-Start=<s>&X-Plex-Container-Size=500&<filters>` |
| `ItemPaths` | `GET /library/metadata/<ratingKey>` |
| `Refresh` | `PUT /library/metadata/<ratingKey>/refresh` |
| `SearchMatches` | `GET /library/metadata/<ratingKey>/matches?manual=1&title=<title>&agent=<agent>&language=<language>` |
| `Match` | `PUT /library/metadata/<ratingKey>/match?guid=<guid>&name=<name>&year=<year>`. If the year is 0, leave out `year`. |

- [ ] Step 1: Record and clean the fixtures as "Recording test data" describes. Expected: the probe selects a form for each point, and every leak check count is 0.
- [ ] Step 2: Write the failing tests. Each test uses an `httptest.Server`.
  - `TestRequestsSendTokenHeaderAndAcceptJSON`: every method sends `X-Plex-Token` and `Accept: application/json`, and no URL has a `X-Plex-Token` query parameter.
  - `TestLibrariesDecodesSections`: `sections.json` gives three libraries with the IDs, types, agents, and languages of the fixture.
  - `TestListDecodesRecordedShapes`: `movies_matched.json`, `movies_unmatched.json`, `shows_unmatched.json`, and `shows_episode_summary.json` give these values:
    - string rating keys, and `AddedAt` from Unix seconds
    - `Files` from `Media[].Part[].file`
    - `Thumb == ""` and `Art == ""` where the fixture has no key
    - `Summary == ""` for an empty summary
  - `TestListReadsEveryPage`: a handler with `totalSize` 1,001 and pages of 500, 500, and 1 items gives 1,001 items and requests `X-Plex-Container-Start` 0, 500, and 1000.
  - `TestListStopsOnEmptyPage`: a page with 0 items before `totalSize` ends the read with an error, not a loop.
  - `TestListEncodesFilters`: `List(ctx, "2", TypeShow, Filter{"addedAt", ">>=", "100"}, Filter{"episode.summary", "=", ""})` sends the filter, order, and paging forms that the recording probe selected. Assert the exact `r.URL.RawQuery`. Also assert `excludeElements=Media,Role,Director,Writer,Genre,Country` for type 2 and `excludeElements=Role,Director,Writer,Genre,Country` for type 1.
  - `TestItemPathsReadsLocationsAndFiles`: `item_show.json` gives the `Location` path. `item_movie.json` gives the file path.
  - `TestRefreshSendsPutToItem`: `Refresh(ctx, "1001")` sends `PUT /library/metadata/1001/refresh`.
  - `TestSearchMatchesDecodesCandidates`: `matches_one.json` gives one candidate with its guid, name, and year. `matches_none.json` gives none. The query has `manual=1`, `title`, `agent`, and `language`.
  - `TestMatchSendsGuidNameYear`: `Match` sends `PUT /library/metadata/1001/match` with `guid`, `name`, and `year`. With `Year == 0`, the query has no `year`.
  - `TestRejectsInvalidRatingKey` (Review Focus 1): call `Refresh`, `Match`, `ItemPaths`, and `SearchMatches` with each of `""`, `"../sections/1"`, `"1/refresh"`, and `"12a"`. Each call returns an error, and the server gets no request.
  - `TestStatusMapping`, a table test: 401 and 503 give `errors.Is(err, ErrUnavailable)`. 404 gives `ErrNotFound`. 500 gives an error that is neither. A closed server gives `ErrUnavailable`.
  - `TestErrorsAndTraceOmitSecrets`: set the global logger to trace level with a buffer. After a failed request and a successful request, the error text and the buffer contain neither the token nor the host of the server URL. The buffer has one trace line for each request with the method, the path, the status, and the time.
- [ ] Step 3: Run `go test ./internal/plex/...`. Expected: FAIL, because the package is empty.
- [ ] Step 4: Implement the client. Before you build a URL, make sure that the rating key matches `^[0-9]+$`. If it does not, return an error. Remove the URL from transport errors, as autoscan's `withoutURL` does. Build the filter query in the form that the probe selected. Do not add a method for a section refresh.
- [ ] Step 5: Run `go test ./internal/plex/...`. Expected: PASS.
- [ ] Step 6: Add the `internal/plex/` row to `AGENTS.md`. Run the checks. Commit with `feat(plex): add the Plex API client`. Put the probe results in the PR description.

### Task 3: Store

**Files:**
- Create: `internal/store/store.go` (`Open`, `Close`, migrations)
- Create: `internal/store/reasons.go`
- Create: `internal/store/item.go` (item and library queries)
- Create: `internal/store/migrations/1_init.sql`
- Test: `internal/store/store_test.go`, `internal/store/item_test.go`, `internal/store/reasons_test.go`
- Modify: `go.mod`, `go.sum` (add `modernc.org/sqlite`)
- Modify: `AGENTS.md` (path table row for `internal/store/`)

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
package store

type Reasons uint16

const (
	Unmatched        Reasons = 1 << iota // local:// guid
	NoSummary
	NoPoster
	NoArt
	EpisodeNoSummary // regular episodes with an empty summary
	EpisodeUnmatched // matched series with unmatched regular episodes
)

const ItemReasons = Unmatched | NoSummary | NoPoster | NoArt

// String gives "unmatched,no-summary,no-poster,no-art,episode-no-summary,episode-unmatched"
// in this order, for the set flags only. 0 gives "".
func (r Reasons) String() string

type Library struct{ LastFullRead, LastRead time.Time } // zero: never

type Item struct {
	Server, Section, RatingKey string
	Type                       string // "movie" or "show"
	Title                      string
	Year                       int
	AddedAt                    time.Time
	Reasons                    Reasons
	LastRefresh, LastAttempt   time.Time // zero: never
	Refreshes                  int
}

func Open(path string) (*Store, error) // one connection, runs migrations
func (s *Store) Close() error

func (s *Store) Library(server, section string) (Library, error)
// SaveRead sets LastRead to at. If full, it also sets LastFullRead.
func (s *Store) SaveRead(server, section string, at time.Time, full bool) error

// SaveItems sets the bits in mask of each item's row to item.Reasons&mask
// and keeps the other bits. It writes title, year, and addedAt. It inserts
// a row only when the result is not 0. It deletes each row whose result is
// 0 and returns these rows as they were before the delete.
func (s *Store) SaveItems(items []Item, mask Reasons) (fixed []Item, err error)

// ReplaceReason sets reason on the rows of items and clears it on every
// other row of the section. It deletes rows that have no reason left and
// returns them.
func (s *Store) ReplaceReason(server, section string, reason Reasons, items []Item) (fixed []Item, err error)

// DeleteUnseen deletes the rows of the section whose rating key is not in
// seen, and returns them.
func (s *Store) DeleteUnseen(server, section string, seen []string) ([]Item, error)

// Due returns the rows of the sections that are incomplete, added at or
// before now-minimumAge, and with no attempt after now-refreshInterval.
// Order: no attempt first, then the oldest LastAttempt, then the oldest AddedAt.
func (s *Store) Due(server string, sections []string, now time.Time, minimumAge, refreshInterval time.Duration) ([]Item, error)

func (s *Store) CountIncomplete(server string, sections []string) (int, error)
// RecordAttempt sets LastAttempt. If refreshed, it also sets LastRefresh
// and adds 1 to Refreshes.
func (s *Store) RecordAttempt(server, ratingKey string, at time.Time, refreshed bool) error
func (s *Store) Delete(server, ratingKey string) error
```

Schema (`1_init.sql`). Times are Unix seconds, and `NULL` means never.

```sql
CREATE TABLE library (
    server TEXT NOT NULL,
    section TEXT NOT NULL,
    last_full_read INTEGER,
    last_read INTEGER,
    PRIMARY KEY (server, section)
);

CREATE TABLE item (
    server TEXT NOT NULL,
    section TEXT NOT NULL,
    rating_key TEXT NOT NULL,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    year INTEGER NOT NULL,
    added_at INTEGER NOT NULL,
    reasons INTEGER NOT NULL,
    last_refresh INTEGER,
    last_attempt INTEGER,
    refreshes INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (server, rating_key)
);
```

The migration runner is a small part of `store.go`: a `schema_migration (version INTEGER PRIMARY KEY)` table, the embedded `migrations/*.sql` files in version order, and one transaction for each file. It follows autoscan's `migrate` package without the component column, because only one package has migrations.

- [ ] Step 1: Write the failing tests. Each test opens a database in `t.TempDir()`.
  - `TestOpenMigratesOnce`: `Open` twice on the same file succeeds, and `schema_migration` has one row.
  - `TestReasonsString`: `Unmatched|NoArt` gives `"unmatched,no-art"`. `0` gives `""`.
  - `TestSaveItemsStoresOnlyIncomplete`: of two items, one with reasons and one with `0`, only the first gets a row.
  - `TestSaveItemsReturnsFixedItem`: a row with `Refreshes == 3` and `NoSummary`, saved again with `0`, is deleted, and `fixed` has one item with `Refreshes == 3`. After this, the same item saved with `NoSummary` has `Refreshes == 0` and a zero `LastAttempt`.
  - `TestSaveItemsKeepsEpisodeReason` (Review Focus 3): a row with `NoArt|EpisodeNoSummary`, saved with mask `ItemReasons` and reasons `0`, keeps `EpisodeNoSummary`, and `fixed` is empty.
  - `TestReplaceReasonClearsAbsentSeries`: rows A and B have `EpisodeUnmatched`. `ReplaceReason` with only B and C keeps B, adds C, and deletes A. `fixed` has A. A row with another reason keeps that reason.
  - `TestDeleteUnseen`: in a section with rows A, B, and C, `seen` `[A, C]` deletes B only. A row in another section stays.
  - `TestDueOrder`: rows with no attempt come first, then the oldest `LastAttempt`. Equal values go by the oldest `AddedAt`.
  - `TestDueSkipsRecentAttemptsAndOtherScopes`: a row with an attempt 1 hour before `now` and `refreshInterval` 12h is not due. A row of another server or of a section that is not in `sections` is not due.
  - `TestDueSkipsYoungItemUntilOldEnough` (Review Focus 4): with `minimumAge` 24h, an item added 1 hour before `now` is stored but not due. With `now` 24 hours later, it is due.
  - `TestDueComparesTimesAcrossZones`: times saved in two different `time.Location` values sort by instant.
  - `TestRecordAttempt`: `refreshed == true` sets `LastRefresh` and `Refreshes == 1`. `refreshed == false` sets only `LastAttempt`.
  - `TestSaveReadIncrementalKeepsFullRead`: a full read at t1 and an incremental read at t2 give `LastFullRead == t1` and `LastRead == t2`.
  - `TestDelete`: the row is gone, and a delete of a missing row is not an error.
- [ ] Step 2: Run `go test ./internal/store/...`. Expected: FAIL, because the package is empty.
- [ ] Step 3: Implement the store. Call `db.SetMaxOpenConns(1)` in `Open`. Run each multi-row change in one transaction. Close rows before the next statement, because the pool has one connection.
- [ ] Step 4: Run `go test ./internal/store/...`. Expected: PASS.
- [ ] Step 5: Add the `internal/store/` row to `AGENTS.md`. Run the checks. Commit with `feat(store): add the SQLite cache of incomplete items`.

### Task 4: Refresher rules

**Files:**
- Create: `internal/refresher/missing.go`, `internal/refresher/ids.go`, `internal/refresher/match.go`
- Test: `internal/refresher/missing_test.go`, `internal/refresher/ids_test.go`, `internal/refresher/match_test.go`
- Modify: `AGENTS.md` (path table row for `internal/refresher/`)

**Interfaces:**
- Consumes: `plex.Item`, `plex.Candidate` (Task 2). `store.Reasons` and its flags (Task 3).
- Produces (unexported, used by Tasks 5 and 6):

```go
// itemReasons returns the item-level reasons of a movie or series.
func itemReasons(it plex.Item) store.Reasons

type pathID struct{ Source, Value string } // Source: "imdb", "tmdb", or "tvdb"
// title gives the match search title, for example "tmdb-747059".
func (id pathID) title() string
// parseIDs reads the IDs in all paths, without duplicates, in the order found.
func parseIDs(paths []string) []pathID

type searchResult struct {
	id         pathID
	candidates []plex.Candidate
}

// chooseMatch applies fix match steps 4 to 7 of the spec. ok is true only
// for a match to apply. conflict is "" or a short reason for the log.
func chooseMatch(itemYear int, results []searchResult) (cand plex.Candidate, ok bool, conflict string)
```

- [ ] Step 1: Write the failing tests.
  - `TestItemReasons`, a table test: a `local://1001` guid gives `Unmatched`. An empty summary gives `NoSummary`. An empty thumb gives `NoPoster`. An empty art gives `NoArt`. A complete `plex://` item gives `0`. An unmatched series with no summary, thumb, or art gives all four flags.
  - `TestParseIDs`, a table test with the expected `[]pathID`:
    - `/m/Film (2020) {tmdb-747059}/Film.mkv` gives `tmdb 747059`
    - `[imdb:tt19120100]` gives `imdb tt19120100`
    - `(tvdb-463649)` gives `tvdb 463649`
    - `{imdbid-tt31289994}` gives `imdb tt31289994`
    - `{tvdb=1}` and `{TMDB_2}` give `tvdb 1` and `tmdb 2`
    - `/tv/Show (2020)` gives nothing, because a year has no source
    - `{imdb-12345}` and `{tmdb-tt1}` give nothing, because the value type is wrong
    - `/m/Film tmdb-1 (2020)/f.mkv` gives nothing, because the ID is outside brackets
    - `{tmdb-1 imdb-tt2}` gives nothing, because a group holds one ID
    - the same ID in the folder and the file name gives it once
  - `TestChooseMatch`, a table test:
    - one ID with one candidate gives `ok`
    - two IDs that find the same guid give `ok`
    - an ID with no candidates next to an ID with one candidate gives `ok`
    - no IDs, or only empty results, give no `ok` and `conflict == ""`
    - one ID with two candidates gives a conflict
    - two IDs with different guids give a conflict
    - item year 2020 with candidate year 2022 gives a conflict
    - year 2020 with 2021 gives `ok`
    - year 0 on either side gives `ok`
- [ ] Step 2: Run `go test ./internal/refresher/...`. Expected: FAIL, because the functions are not defined.
- [ ] Step 3: Implement the three files. `parseIDs` finds each `{...}`, `[...]`, and `(...)` group. All of the text in the group must be one ID: the source `imdb`, `tmdb`, or `tvdb` in any case, an optional `id` suffix, a separator of non-alphanumeric characters, and a value. Other groups give no ID. An IMDb value is `tt` and digits. TMDb and TVDB values are digits.
- [ ] Step 4: Run `go test ./internal/refresher/...`. Expected: PASS.
- [ ] Step 5: Add the `internal/refresher/` row to `AGENTS.md`. Run the checks. Commit with `feat(refresher): add missing-metadata and fix match rules`.

### Task 5: Library reads

**Files:**
- Create: `internal/refresher/refresher.go` (`Refresher`, `New`, `Run`, interfaces)
- Create: `internal/refresher/read.go` (library selection, full and incremental reads, episode requests)
- Test: `internal/refresher/read_test.go`, `internal/refresher/fake_test.go` (a fake Plex that records calls)

**Interfaces:**
- Consumes: the `plex.Client` methods and types (Task 2). The `store.Store` methods (Task 3). `itemReasons` (Task 4).
- Produces:

```go
package refresher

// Plex is the part of plex.Client that the refresher uses.
type Plex interface {
	Libraries(ctx context.Context) ([]plex.Library, error)
	List(ctx context.Context, section string, itemType int, filters ...plex.Filter) ([]plex.Item, error)
	ItemPaths(ctx context.Context, ratingKey string) ([]string, error)
	Refresh(ctx context.Context, ratingKey string) error
	SearchMatches(ctx context.Context, ratingKey, title, agent, language string) ([]plex.Candidate, error)
	Match(ctx context.Context, ratingKey string, cand plex.Candidate) error
}

// Store is the part of store.Store that the refresher uses: Library,
// SaveRead, SaveItems, ReplaceReason, DeleteUnseen, Due, CountIncomplete,
// RecordAttempt, and Delete, with the Task 3 signatures.
type Store interface { /* the nine methods */ }

type Server struct {
	Name      string
	Plex      Plex
	Libraries []string // titles; empty means all supported libraries
}

// Schedule is the part of cron.Schedule that the refresher uses.
type Schedule interface{ Next(time.Time) time.Time }

type Options struct {
	MinimumAge, RefreshInterval, Oversample, RefreshDelay time.Duration
	CacheRefresh       Schedule
	MaxRefreshesPerRun int // 0: no limit
	DryRun             bool
}

func New(servers []Server, st Store, opts Options) *Refresher
// Run checks each server in order. It returns an error only for a store error.
func (r *Refresher) Run(ctx context.Context) error
```

`Refresher` has the unexported fields `now func() time.Time` and `sleep func(context.Context, time.Duration) error`. The tests replace them.

Reads for each selected library, with `now` at the start of the read:

| Read | Requests |
| --- | --- |
| Full (no full read yet, or `CacheRefresh.Next(LastFullRead)` is not after the start time) | `List(section, type)` with no filters |
| Incremental, with `T = LastRead - Oversample` in Unix seconds | `updatedAt>>=T`, `addedAt>>=T`, and `unmatched=1`. Autorefresh joins the results by rating key. |
| Episode requests (each `show` library, every run), with `A = now - MinimumAge` | `unmatched=0`, `episode.unmatched=0`, `episode.summary=`, `episode.addedAt<<=A`, `season.index>>=1`, which gives `EpisodeNoSummary` |
| | `unmatched=0`, `episode.unmatched=1`, `episode.addedAt<<=A`, `season.index>>=1`, which gives `EpisodeUnmatched` |

After a full or incremental read, autorefresh calls `SaveItems` with mask `ItemReasons`. After a full read, it also calls `DeleteUnseen`. Each episode request ends with `ReplaceReason`. Only after all requests of a library succeed does autorefresh call `SaveRead(server, section, start, full)`. It keeps the movie `Files` of the run in a map by rating key for Task 6.

- [ ] Step 1: Write the failing tests. Use the fake Plex and a real store in `t.TempDir()`.
  - `TestRunSelectsSupportedLibraries`: Plex has three libraries. They are a movie library with the movie agent, a show library with the series agent, and a movie library with `tv.plex.agents.none`. Only the first two are read. Two runs log the warning for the third library once.
  - `TestRunUsesConfiguredLibraries`: with `Libraries: ["Movies"]`, only that title is read. A configured title that Plex does not have logs one warning.
  - `TestFirstRunDoesFullRead`: `List` gets no filters. The incomplete items get rows, and the complete items do not. `LastFullRead` and `LastRead` equal the start time.
  - `TestFullReadDeletesUnseenRows`: a row whose item is not in the next full read is deleted.
  - Tests that use `CacheRefresh` parse `0 3 * * 0` and set t0 to Saturday 2026-11-21 12:00 in `time.Local`.
  - `TestIncrementalReadFilters`: with a full read at t0 and a run at t0+1h, `List` gets the three filters with `T = t0 - 48h` in Unix seconds, and `LastFullRead` stays t0.
  - `TestFullReadAfterCacheRefresh`: the full read is at t0. A run on Sunday 2026-11-22 at 02:59 does an incremental read. A run at 03:00 does a full read.
  - `TestEpisodeRequestFilters`: a show library gets the two requests with the exact filters in the table above. A series in a result gets the reason, and an absent series loses it.
  - `TestFailedReadKeepsState` (Review Focus 2): a `List` error on a full read changes no read time and deletes no row. A failed episode request keeps the earlier `EpisodeNoSummary` rows. The next library is still read.
  - `TestUnavailableSkipsServer`: `ErrUnavailable` from `Libraries` or from `List` stops that server and logs an error. The next server is still read.
  - `TestCompleteItemLogsFixed`: a stored item that a read returns complete loses its row, and an info line has the title and the refresh count.
  - `TestCompleteItemKeepsEpisodeReason` (Review Focus 3): a series with an `EpisodeNoSummary` row that a full read returns complete keeps its row.
  - `TestYoungItemLogsDebug`: an incomplete item added after `now - MinimumAge` is stored, and a debug line has its title.
- [ ] Step 2: Run `go test ./internal/refresher/...`. Expected: FAIL.
- [ ] Step 3: Implement `refresher.go` and `read.go`. Log each library read at debug with the kind of read, the item count, the incomplete count, and the time it took.
- [ ] Step 4: Run `go test ./internal/refresher/...`. Expected: PASS.
- [ ] Step 5: Run the checks. Commit with `feat(refresher): read libraries into the cache`.

### Task 6: Refresh run

**Files:**
- Create: `internal/refresher/refresh.go` (due queue, pacing, limit, fix match, dry run, summary)
- Modify: `internal/refresher/refresher.go` (call the refresh step after the reads of each server)
- Test: `internal/refresher/refresh_test.go`

**Interfaces:**
- Consumes: `Store.Due`, `Store.RecordAttempt`, `Store.Delete`, and `Store.CountIncomplete` (Task 3). `parseIDs`, `chooseMatch`, and `pathID.title` (Task 4). The selected libraries and the movie file map of the run (Task 5).
- Produces: the complete `Run` behavior. The interface does not change.

The step for each due item, in `Due` order:

1. If the limit is set and the run sent `MaxRefreshesPerRun` refresh or match requests on this server, stop.
2. Choose the action. If the item has `Unmatched` and `Refreshes >= 1`, the action is a fix match. Otherwise it is a plain refresh.
3. For a fix match, get the paths. A movie uses the file map of the run. If the map has no entry for it, it uses `ItemPaths`. A series uses `ItemPaths`. Search once for each ID with the agent and the language of the section. Then call `chooseMatch`. A conflict logs a warning and uses a plain refresh.
4. If `ItemPaths` or a search fails, send nothing for this item. This does not count toward the limit. The error decides the next step:
   - `ErrUnavailable`: log an error and stop the server.
   - `ErrNotFound`: `Delete` the row.
   - Another error: log a warning and `RecordAttempt(refreshed=false)`. In a dry run, record nothing.
5. In a dry run, log the item at info with the server, the library, the title, the reasons, and the action, and continue.
6. If this is not the first request on the server in this run, call `sleep(RefreshDelay)`.
7. Send `Match` or `Refresh`. On success: `RecordAttempt(refreshed=true)` and an info line. On `ErrNotFound`: `Delete`. On `ErrUnavailable`: log an error and stop the server. On another error: log a warning and `RecordAttempt(refreshed=false)`.

At the end of each server, log one info summary line with: items read, incomplete items (`CountIncomplete`), due items, refreshes sent, matches sent, failures, items that became complete, and the time of the run.

- [ ] Step 1: Write the failing tests. Use the fake Plex, a real store, a fixed `now`, and a `sleep` that records its calls.
  - `TestRefreshOrderAndDelay`: three due items are refreshed in `Due` order, and `sleep` gets `RefreshDelay` twice.
  - `TestMaxRefreshesPerRun`: with a limit of 2 and three due items, the run sends 2 refreshes. The next run, `RefreshInterval` later, sends the third first.
  - `TestRefreshSuccessRecordsRefresh`: `Refreshes == 1`, and `LastRefresh` and `LastAttempt` equal `now`.
  - `TestRefreshFailureRecordsAttemptOnly`: a 500 error sets `LastAttempt`, keeps `Refreshes == 0`, and logs a warning. The item is not due again until `RefreshInterval` later.
  - `TestRefreshNotFoundDeletesRow`.
  - `TestRefreshUnavailableStopsServer`: after `ErrUnavailable` on the first item, the server sends no more requests, and the next server still refreshes.
  - `TestUnmatchedFirstAttemptIsPlainRefresh`: an unmatched item with `Refreshes == 0` gets `Refresh` and no `SearchMatches`.
  - `TestFixMatchMovie`: an unmatched movie with `Refreshes == 1` and the listing file `/m/Film (2020) {tmdb-1}/Film.mkv` gets `SearchMatches(key, "tmdb-1", "tv.plex.agents.movie", <section language>)`, then `Match` with the candidate. `Refreshes` becomes 2, and the summary counts one match.
  - `TestFixMatchMovieWithoutListingUsesItemPaths`.
  - `TestFixMatchSeriesUsesItemPaths`: the series path comes from `ItemPaths`, and the agent is `tv.plex.agents.series`.
  - `TestFixMatchConflictSendsPlainRefresh`: two IDs with different guids log a warning and send `Refresh`.
  - `TestFixMatchWithoutIDsSendsPlainRefresh`.
  - `TestFixMatchReadErrors`, a table test for `ItemPaths` and `SearchMatches`. `ErrUnavailable` stops the server. `ErrNotFound` deletes the row. A 500 error records a failed attempt. No case sends `Match` or `Refresh`.
  - `TestFailedSearchSendsNothing` (Review Focus 5): the limit is 1, and `SearchMatches` fails on the first item. That item gets no `Match` or `Refresh`. Its `LastAttempt` is set, and `Refreshes` does not change. The second due item still gets its refresh.
  - `TestEpisodeReasonGetsPlainRefresh`: a matched series with `EpisodeUnmatched` and `Refreshes == 3` gets `Refresh` and no `SearchMatches`.
  - `TestDryRunSendsAndRecordsNothing` (Review Focus 5): the fake gets no `Refresh` or `Match` call, and `sleep` gets no call. `LastAttempt` stays zero. The log has one info line for each due item with its action. A failed `SearchMatches` writes no state either. The read state is saved.
  - `TestSummaryLine`: the info line has the counts of a run with one refresh, one match, one failure, and one fixed item.
- [ ] Step 2: Run `go test ./internal/refresher/...`. Expected: FAIL.
- [ ] Step 3: Implement `refresh.go` and call it from `Run`. Log each match search result at debug with the ID and the number of candidates.
- [ ] Step 4: Run `go test ./internal/refresher/...`. Expected: PASS.
- [ ] Step 5: Run the checks. Commit with `feat(refresher): refresh and match due items`.

### Task 7: Command line, schedule, Docker, and README

**Files:**
- Modify: `cmd/autorefresh/main.go` (flags, wiring, `--once`, schedule, signals)
- Create: `cmd/autorefresh/config.go` (`applyDefaultPaths`, `waitForConfig`)
- Create: `cmd/autorefresh/schedule.go` (`runner`)
- Test: `cmd/autorefresh/config_test.go`, `cmd/autorefresh/schedule_test.go`
- Modify: `docker/Dockerfile`, `README.md`, `AGENTS.md`

**Interfaces:**
- Consumes: `config.Decode` (Task 1), `plex.New` (Task 2), `store.Open` (Task 3), `refresher.New` and `Run` (Tasks 5 and 6).
- Produces:

```go
// applyDefaultPaths sets each empty path to a file in the default
// configuration directory. It resolves that directory only if a path is empty.
func applyDefaultPaths(config, database, logFile *string)

// waitForConfig is autoscan's function: it opens path and waits while the
// file is missing or empty.
func waitForConfig(path string, interval time.Duration) (*os.File, error)

// runner starts one run at a time.
type runner struct {
	mu  sync.Mutex
	run func(context.Context) error
}

// start runs r.run. If a run is busy, it logs a warning and returns.
func (r *runner) start(ctx context.Context)
```

Wiring in `main`:

1. Parse the flags. Run `applyDefaultPaths`. Start the logger.
2. Wait for the configuration and decode it. Open the store. Each failure is a fatal error.
3. Make one `plex.New(name, url, token)` for each server, and `refresher.New` with the options. `CacheRefresh` is the result of `cron.ParseStandard(cfg.CacheRefresh)`. If `cfg.MaxRefreshesPerRun` is nil, pass 0. Otherwise pass its value.
4. Log `Initialised` at info with the version, the server names, the schedule, and `dry_run`.
5. With `--once`, run once and exit. If `Run` returns an error, exit with code 1.
6. Otherwise, add `runner.start` to `cron.New()` with the schedule. Start the schedule. On SIGINT or SIGTERM, cancel the context, wait for `cron.Stop()`, and close the store.

- [ ] Step 1: Write the failing tests.
  - `TestApplyDefaultPathsKeepsSetPaths`: with all three paths set, no directory is created, and the paths do not change.
  - `TestApplyDefaultPathsFillsEmptyPaths`: empty paths become `config.yml`, `autorefresh.db`, and `activity.log` in the stubbed default directory.
  - `TestWaitForConfigWaitsForContent`: port autoscan's test. An empty file that gets content after a short time opens.
  - `TestRunnerSkipsOverlappingStart`: while one `start` blocks in `run`, a second `start` returns at once, does not call `run`, and logs a warning.
  - `TestRunnerLogsRunError`: an error from `run` gives an error line.
- [ ] Step 2: Run `go test ./cmd/autorefresh/...`. Expected: FAIL.
- [ ] Step 3: Implement the wiring. Add `github.com/robfig/cron/v3` to the imports of `main` (the module is already in `go.mod` from Task 1).
- [ ] Step 4: Run `go test ./cmd/autorefresh/...`. Expected: PASS.
- [ ] Step 5: Add `AUTOREFRESH_CONFIG="/config/config.yml"` and `AUTOREFRESH_DATABASE="/config/autorefresh.db"` to the `ENV` block of `docker/Dockerfile`.
- [ ] Step 6: Update `README.md`:
  - Remove the sentence that the binary only starts its logger.
  - Add "How it works". Describe these points:
    - what counts as missing
    - full and incremental reads
    - the refresh interval, with no limit on attempts
    - the fix match from path IDs, and the pacing
    - autorefresh never sends a library-wide refresh
  - Add "Configuration": the spec's example with neutral values, the key table with defaults, and the rules for `name`, `url`, `token`, and `libraries`. Add a note that `http` sends the token in clear text. If the connection leaves a trusted network, tell users to use `https`.
  - Add the four new flags to the flag table, and add a "Dry run" section.
  - In the Docker section, list the three environment variables that the image sets.
- [ ] Step 7: Update the `cmd/autorefresh/` row in the `AGENTS.md` path table.
- [ ] Step 8: Run the checks and `mise run build`. Write a scratch configuration with one server at `http://127.0.0.1:9` and the token `example-token`. Run `dist/autorefresh_linux_amd64/autorefresh --once --dry-run --config <scratch file> --database <scratch db> --log <scratch log>`. Expected: the run logs the server failure at error level and exits. Neither the console nor the log file contains `example-token` or `127.0.0.1`.
- [ ] Step 9: Commit with `feat(cli): run autorefresh on a schedule`.

## Acceptance on the owner's server

These steps happen after Task 7 merges. Each step needs the owner's agreement before it starts.

1. Dry run: `autorefresh --once --dry-run` with the owner's configuration. It reads Plex and sends match searches, and it sends no refresh or match. Compare the due items in the log with the spec's counts.
2. First real run with `max-refreshes-per-run: 5`, then a check that the five items changed in Plex.
3. The owner removes the limit and runs the service on the schedule.

Then close the acceptance with a comment on #9 or on a new acceptance issue that the owner names.
