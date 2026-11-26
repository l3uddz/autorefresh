# TMDB fallback and store foreign keys: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce foreign keys in the SQLite store. Give an unmatched item
a fix match search with the TMDB ID of its `tvdb` or `imdb` path ID.

**Architecture:** Part 1 turns on SQLite foreign keys and rebuilds `item`
with a cascading key to `library`. Part 2 adds a small `internal/tmdb`
client and a per-item `tmdb_id` cache table that cascades from `item`. It
also adds a second pass to `findMatch`. If no normal search found a
candidate, the second pass runs.

**Tech Stack:** Go (version from `go.mod`), `modernc.org/sqlite`, zerolog,
`net/http/httptest`, mise tasks.

**Spec:** `docs/superpowers/specs/2026-11-20-tmdb-tvdb-fallback-design.md`

## Global Constraints

- Tests need no network and no Plex server. Use `httptest` and put recorded
  responses in the `testdata/` directory of the package that reads them.
- Log with `github.com/rs/zerolog/log`, values in fields.
- The TMDB token never appears in logs, errors, or URLs. It goes in an
  `Authorization: Bearer` header.
- Without `tmdb-token`, autorefresh sends no TMDB request. The search error
  rule of Task 4 applies without a token too.
- A TMDB error never wraps `plex.ErrUnavailable` or `plex.ErrNotFound`.
- Before each push: `mise run vendor`, `mise run test`, `mise run lint`.
- Commit subjects use `<type>(<scope>): <summary>`, at most 72 characters,
  with the `Co-Authored-By` trailer. No closing keywords in commits.
- Task 1 ships as PR `feat/68-store-foreign-keys` into `main` (issue #68).
  Tasks 2 to 6 ship as PR `feat/69-tmdb-fallback` into `main` (issue #69),
  branched from `main` after the first PR merges.

## Review Focus

1. A second read of a library must keep the refresh history of its items.
   An upsert of `library` that deletes the row, such as
   `INSERT OR REPLACE`, cascades and wipes the items. Test in Task 1.
2. An item that moves to a library with no `library` row yet must move,
   not fail on the foreign key. Test in Task 1.
3. A TMDB failure must never delete an item or stop the server, also for a
   TMDB 404 or 401. Test in Task 5.
4. A store error in the TMDB cache must stop the run like any store error,
   and must not count as a failed match search. Test in Task 5.
5. A `tvdb` ID in a movie library and a path with a `tmdb` ID must send no
   TMDB request. Test in Task 5.

---

### Task 1: Foreign keys in the store

Branch: `feat/68-store-foreign-keys` from `origin/main`. Commit the spec
and this plan first as `docs(design): add the TMDB fallback design and plan`.

**Files:**
- Create: `internal/store/migrations/2_foreign_keys.sql`
- Modify: `internal/store/store.go` (`Open`)
- Modify: `internal/store/item.go` (`ApplyRead`, `prune`)
- Test: `internal/store/store_test.go`, `internal/store/item_test.go`

**Interfaces:**
- Produces: `item(server, section)` references `library(server, section)`
  `ON DELETE CASCADE`, and foreign keys are on for the connection. Task 2
  relies on both.

- [ ] **Step 1: Write the failing tests**

In `store_test.go`:

```go
func TestOpenEnablesForeignKeys(t *testing.T)
// var on int; s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); want 1.

func TestMigrationKeepsItemsWithoutLibrary(t *testing.T)
// Create a file with sql.Open("sqlite", file), run the text of
// migrations/1_init.sql, create schema_migration and insert version 1.
// Insert an item row ("home", "9", "101", refreshes 3) with no library row.
// Open(file). Want: Unseen("home", "9", nil) has 101 with Refreshes 3;
// Library("home", "9") has zero LastRead and LastFullRead;
// `PRAGMA foreign_key_check` returns no rows.

func TestItemNeedsLibrary(t *testing.T)
// After Open, a direct INSERT INTO item for a section with no library row
// returns an error.
```

In `item_test.go`:

```go
func TestApplyReadKeepsHistoryOnSecondRead(t *testing.T)
// ApplyRead a full read with item 101, RecordAttempt(…, true) twice,
// ApplyRead an incremental read of the same library with 101 still
// incomplete. Want Refreshes 2.

func TestApplyReadMovesItemToNewLibrary(t *testing.T)
// Item 101 in section "1". ApplyRead a first read of section "2" (no
// library row yet) that lists 101. Want 101 in section "2" with its
// refreshes, and no error.
```

The cache cleanup of PR #61 needs a review here, because foreign keys make
two of its parts redundant. Its item deletes become the cascade, and its
count query no longer needs to find item rows without a `library` row. The
existing prune tests in `internal/store` and
`internal/refresher/prune_test.go` keep the behavior and the `Items`
counts. Add one test for the migrated case:

```go
func TestPruneRemovesMigratedLibrary(t *testing.T)
// The setup of TestMigrationKeepsItemsWithoutLibrary, then
// PruneSections("home", nil). Want []Pruned{{"home", "9", 1}} and no
// item rows of "home".
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `go test ./internal/store/ -run 'ForeignKeys|WithoutLibrary|NeedsLibrary|SecondRead|NewLibrary|MigratedLibrary'`
Expected: `TestOpenEnablesForeignKeys` and `TestItemNeedsLibrary` fail.
`TestMigrationKeepsItemsWithoutLibrary` fails on `foreign_key_check`. The
other three can pass already and guard the change.

- [ ] **Step 3: Implement**

- `Open`: open `file + "?_pragma=foreign_keys(1)"`, so each new connection
  turns on foreign keys.
- `2_foreign_keys.sql` does these steps in this order:
  1. Insert a `library (server, section)` row for each distinct
     `(server, section)` of `item` without one.
  2. Create `item_new` with the columns and primary key of `item`. Add
     `FOREIGN KEY (server, section) REFERENCES library (server, section) ON DELETE CASCADE`.
  3. Copy all rows by column name.
  4. `DROP TABLE item`, then `ALTER TABLE item_new RENAME TO item`.
  5. `CREATE INDEX item_library ON item (server, section)`.
- `ApplyRead`: call `writeReadTimes` before the item writes. Keep its
  `INSERT … ON CONFLICT DO UPDATE` form.
- `prune`: delete only the `library` rows. The cascade deletes the items.
  Replace the `UNION ALL` count query with `library LEFT JOIN item` on
  `(server, section)` and `COUNT(item.rating_key)`, grouped by server and
  section and still filtered by `?1`. Each item now has a `library` row,
  so the join finds every library with items. Update the comments of
  `PruneServers`, `PruneSections`, and `prune`.

- [ ] **Step 4: Run all checks**

Run: `mise run vendor && mise run test && mise run lint`
Expected: all packages `ok`, lint prints no file.

- [ ] **Step 5: Commit, push, and open the PR**

```bash
git add internal/store/
git commit -m "feat(store): enforce foreign keys between items and libraries"
```

PR title: `feat(store): enforce foreign keys between items and libraries`.
Body: problem, change, checks, `feat/68-store-foreign-keys` into `main`,
`Closes #68`. Enable auto-merge with a merge commit.

---

### Task 2: TMDB ID cache in the store

Branch: `feat/69-tmdb-fallback` from `origin/main` after Task 1 merged.

**Files:**
- Create: `internal/store/migrations/3_tmdb_id.sql`
- Create: `internal/store/tmdb.go`
- Test: `internal/store/tmdb_test.go`

**Interfaces:**
- Consumes: foreign keys from Task 1.
- Produces:
  - `func (s *Store) TMDBID(server, ratingKey, source, externalID string) (tmdbID string, ok bool, err error)`
  - `func (s *Store) SaveTMDBID(server, ratingKey, source, externalID, tmdbID string) error`
    (upsert on the primary key)
  - `source` is `"tvdb"` or `"imdb"`.

- [ ] **Step 1: Write the failing tests**

```go
func TestTMDBIDRoundTrip(t *testing.T)
// Seed item 101 with ApplyRead. SaveTMDBID("home","101","tvdb","5","77").
// TMDBID → "77", true. TMDBID with source "imdb" → "", false.

func TestTMDBIDGoesWithItem(t *testing.T)
// Three subtests, each saves an ID for 101 and then removes 101:
// Delete("home","101"); an ApplyRead that finds 101 complete;
// PruneSections("home", nil). Each: TMDBID → ok false.

func TestSaveTMDBIDNeedsItem(t *testing.T)
// SaveTMDBID for a rating key with no item row returns an error.
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/store/ -run TMDBID`
Expected: build failure, `TMDBID` undefined.

- [ ] **Step 3: Implement**

`3_tmdb_id.sql`: table `tmdb_id (server, rating_key, source, external_id, tmdb_id)`,
all `TEXT NOT NULL`, primary key `(server, rating_key, source, external_id)`,
`FOREIGN KEY (server, rating_key) REFERENCES item (server, rating_key) ON DELETE CASCADE`.
Then the two methods in `tmdb.go`.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/store/`
Expected: `ok`.

- [ ] **Step 5: Commit**

`feat(store): cache the TMDB IDs of items`

---

### Task 3: TMDB client

**Files:**
- Create: `internal/tmdb/client.go`
- Create: `internal/tmdb/testdata/find_tv.json`, `find_movie.json`, `find_empty.json`, `find_two_tv.json`
- Test: `internal/tmdb/client_test.go`

**Interfaces:**
- Produces:
  - `func New(token string, timeout time.Duration) *Client`
  - `func (c *Client) Find(ctx context.Context, source, id string, show bool) (tmdbID string, ok bool, err error)`.
    `source` `"tvdb"` sends `external_source=tvdb_id`, `"imdb"` sends
    `imdb_id`. `show` reads `tv_results`, otherwise `movie_results`. `ok`
    is true only for exactly one entry. Its integer `id` becomes the string.
  - `func (c *Client) Check(ctx context.Context) error`, a
    `GET /3/authentication`.
  - `var ErrUnauthorized = errors.New("tmdb token rejected")` for 401.
  - The base URL is the unexported field `baseURL`, default
    `https://api.themoviedb.org`. Tests set it to the `httptest` server.

The testdata files follow the documented `/3/find` answer shape
(`movie_results`, `tv_results`, … arrays of objects with `id`). Write them
by hand with made-up titles, because the tests cannot call TMDB.

- [ ] **Step 1: Write the failing tests**

```go
func TestFindSeries(t *testing.T)
// Server checks path "/3/find/463649", query external_source=tvdb_id,
// header "Authorization: Bearer secret", and no "secret" in the URL.
// Serves find_tv.json. Find(ctx,"tvdb","463649",true) → "1399", true, nil.

func TestFindMovieByIMDb(t *testing.T)
// external_source=imdb_id, find_movie.json, show false → its id, true.

func TestFindNothing(t *testing.T)
// find_empty.json → "", false, nil. find_two_tv.json → "", false, nil.
// find_tv.json with show false → "", false, nil.

func TestFindErrors(t *testing.T)
// 401 → errors.Is(err, ErrUnauthorized). 404, 429, 500, bad JSON, and a
// closed server → err != nil. For each: !errors.Is(err, plex.ErrUnavailable),
// !errors.Is(err, plex.ErrNotFound), and err.Error() has no "secret".

func TestCheck(t *testing.T)
// 200 on /3/authentication → nil. 401 → ErrUnauthorized.
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/tmdb/`
Expected: build failure.

- [ ] **Step 3: Implement** the client with `http.Client{Timeout: timeout}`.
Errors name the method and path, never the full URL with the host, and
never the token.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/tmdb/`
Expected: `ok`.

- [ ] **Step 5: Commit**

`feat(tmdb): add a client that finds TMDB IDs by TVDB or IMDb ID`

---

### Task 4: Plex search errors of TVDB and IMDb IDs

**Files:**
- Modify: `internal/refresher/refresh.go` (`findMatch`)
- Modify: `internal/refresher/fake_test.go` (`SearchMatches`)
- Test: `internal/refresher/refresh_test.go` (`TestFixMatchReadErrors`)

**Interfaces:**
- Produces: in `findMatch`, a `SearchMatches` error for a `tvdb` or `imdb`
  path ID can become an empty result. The condition is
  `stopError(ctx, err) == nil && !errors.Is(err, plex.ErrNotFound)`. It logs a debug line
  `Match search failed` with the `id` field and the error.

- [ ] **Step 1: Change the fake and the tests**

- `fakePlex.SearchMatches` reads `searchErrs[ratingKey+" "+title]` first,
  then `searchErrs[ratingKey]`.
- In `TestFixMatchReadErrors`, the row `search 500` keeps its meaning only
  for a `tmdb` ID: give that case the path `/tv/Show (2020) {tmdb-5}` and
  the candidate key `201 tmdb-5` (add a `paths` field to the table, empty
  means the current `{tvdb-5}` path).
- Add rows: `tvdb search 500` → sends `Refresh 201`, `Refresh 202`, row
  refreshed. `tvdb search not found` → item deleted, as today.
  `tvdb search unavailable` → stops the server, as today.
- Add `TestFixMatchIMDbSearchErrorUsesOtherID`: paths `{imdb-tt1}` and
  `{tmdb-2}`, `searchErrs["201 imdb-tt1"] = serverErr`, a candidate for
  `201 tmdb-2`. Want `Match 201 <guid>`.

- [ ] **Step 2: Run and see the new rows fail**

Run: `go test ./internal/refresher/ -run 'FixMatch'`
Expected: `tvdb search 500` and the IMDb test fail.

- [ ] **Step 3: Implement** the rule in the search loop of `findMatch`.

- [ ] **Step 4: Run the refresher tests**

Run: `go test ./internal/refresher/`
Expected: `ok`.

- [ ] **Step 5: Commit**

`fix(refresher): treat failed TVDB and IMDb searches as no candidate`

---

### Task 5: TMDB fallback in fix match

**Files:**
- Modify: `internal/refresher/refresher.go` (interfaces, `Options`)
- Modify: `internal/refresher/refresh.go` (`findMatch`, `matchSearchFailed`)
- Create: `internal/refresher/tmdb.go` (the second pass)
- Modify: `internal/refresher/fake_test.go` (a `fakeTMDB`)
- Test: `internal/refresher/tmdb_test.go`

**Interfaces:**
- Consumes: Task 2 store methods, Task 3 `Find` signature.
- Produces:
  - `type TMDB interface { Find(ctx context.Context, source, id string, show bool) (string, bool, error) }`
  - `Options.TMDB TMDB`. nil means no fallback.
  - `Store` gains `TMDBID` and `SaveTMDBID` with the Task 2 signatures.
  - `var errTMDB = errors.New("tmdb lookup")` and
    `var errCache = errors.New("tmdb cache")`. The second pass wraps TMDB
    errors with `errTMDB` and store errors with `errCache`.
  - `matchSearchFailed` returns an `errCache` error at once, as a store
    error. For an `errTMDB` error, it logs `TMDB lookup failed` instead of
    `Match search failed`. All else stays.

`fakeTMDB` holds `ids map[string]string` by `"<source> <id> <show>"`,
`errs map[string]error` by the same key, and records calls as
`"Find <source> <id> <show>"`.

The second pass runs after the normal searches. All of these conditions
must be true: `r.opts.TMDB != nil`, no result has a candidate, and no path
ID has the source `tmdb`. The pass reads the `imdb` path IDs and, for a
show (`it.Type == "show"`), the `tvdb` path IDs, in path order. For each,
it reads the cache. Without a cached ID, it calls `Find` and saves a found
ID. Search Plex once for each
different TMDB ID with `pathID{"tmdb", id}.title()`. Pass those results to
`chooseMatch`. A failed Plex search in this pass returns its error as
today. The debug line `Match search result` of a translated search has the
field `from`, for example `tvdb-463649`. A dry run also writes the cache.

- [ ] **Step 1: Write the failing tests**

All tests use a seeded unmatched show 201 with 1 refresh (or movie 101),
`newRefreshFixture` with `f.r.opts.TMDB` set to a `fakeTMDB`.

```go
func TestTMDBFallbackMatchesSeries(t *testing.T)
// Path {tvdb-5}, no candidate for "201 tvdb-5", TMDB "tvdb 5 true" → "77",
// candidate for "201 tmdb-77". Want sends "Match 201 <guid>", the cache
// holds 77, and a second run with the item due again calls no Find.

func TestTMDBFallbackMatchesMovieByIMDb(t *testing.T)
// Movie with file path {imdb-tt1}, TMDB "imdb tt1 false" → "9". Want Match.

func TestTMDBFallbackSkipped(t *testing.T)
// Table, each wants no Find call and the current behavior:
// no TMDB option; a normal search found a candidate; the path has a tmdb ID;
// a tvdb ID in a movie library.

func TestTMDBFallbackFindsNothing(t *testing.T)
// TMDB has no entry. Want "Refresh 201", no cache row.

func TestTMDBFallbackConflict(t *testing.T)
// {tvdb-5} → 77 and {imdb-tt1} → 78, both find different candidates.
// Want "Refresh 201" and a "Match conflict" warning.

func TestTMDBFailureSkipsItem(t *testing.T)
// Table of TMDB errors: a 500-like error, tmdb.ErrUnauthorized, and an
// error whose text says 404. Each: 201 gets no request, the row stays with
// Refreshes 1 and LastAttempt t0, the next item 202 still gets its refresh,
// a "TMDB lookup failed" warning, failed_requests 1, Run returns nil.

func TestTMDBCacheErrorStopsRun(t *testing.T)
// A Store wrapper whose TMDBID returns an error. Want Run to return that
// error and no "TMDB lookup failed" warning.
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/refresher/ -run TMDB`
Expected: build failure.

- [ ] **Step 3: Implement** the interfaces, the second pass in `tmdb.go`,
and the `matchSearchFailed` changes.

- [ ] **Step 4: Run the refresher tests**

Run: `go test ./internal/refresher/`
Expected: `ok`.

- [ ] **Step 5: Commit**

`feat(refresher): search Plex with TMDB IDs when other IDs find nothing`

---

### Task 6: Configuration, wiring, startup check, and README

**Files:**
- Modify: `internal/config/config.go` (`TMDBToken string \`yaml:"tmdb-token"\``)
- Modify: `cmd/autorefresh/main.go`, `cmd/autorefresh/check.go`
- Modify: `README.md`
- Test: `internal/config/config_test.go`, `cmd/autorefresh/check_test.go`

**Interfaces:**
- Consumes: `tmdb.New`, `tmdb.Client.Check`, `tmdb.ErrUnauthorized`,
  `refresher.Options.TMDB`.
- Produces: `func checkTMDB(ctx context.Context, client interface{ Check(context.Context) error })`.

- [ ] **Step 1: Write the failing tests**

- Configuration: `tmdb-token: abc` decodes to `TMDBToken "abc"`. The default is
  `""`.
- `checkTMDB`: nil → info `TMDB accepted the token`. `tmdb.ErrUnauthorized`
  → error `TMDB rejected the token`. Another error → warn
  `Checking TMDB failed`. Follow the style of the existing
  `checkServer` tests.

- [ ] **Step 2: Run and see them fail**

Run: `go test ./internal/config/ ./cmd/autorefresh/`
Expected: build failure.

- [ ] **Step 3: Implement**

- `main`: if `cfg.TMDBToken != ""`, build `tmdb.New(cfg.TMDBToken, config.DefaultTimeout)`
  and pass it as `Options.TMDB`. Otherwise leave it nil. Never assign a
  typed nil pointer to the interface.
- Schedule mode: if the client exists, call `checkTMDB` next to
  `checkServers`.
- `README.md`: change "Autorefresh talks only to Plex". The new text says
  that with a `tmdb-token`, it also calls TMDB. Add `tmdb-token` with a
  comment to the example configuration. Add the fallback, the search error rule, and the
  TMDB failure rule to the "Fix match" section. Note in `failed_requests`
  that failed TMDB lookups count.

- [ ] **Step 4: Run all checks**

Run: `mise run vendor && mise run test && mise run lint`
Expected: all `ok`, lint prints no file.

- [ ] **Step 5: Commit, push, and open the PR**

`feat(config): add the tmdb-token key and the TMDB startup check`

PR title: `feat(refresher): fall back to TMDB IDs for TVDB and IMDb IDs`.
The body gives the problem, the change, the checks, the branches
(`feat/69-tmdb-fallback` into `main`), and `Closes #69`. It says that the
testdata is hand-written from the documented response shape. It also says
that no live TMDB or Plex check ran. Enable auto-merge with a merge commit.
