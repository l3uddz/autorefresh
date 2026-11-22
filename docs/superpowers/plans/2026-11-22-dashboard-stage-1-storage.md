# Dashboard Stage 1: Storage and Recording Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store everything the maintenance dashboard will show — item history, fix match decisions, runs, library metadata, images, and paths — and record it during normal runs, with no UI yet.

**Architecture:** New SQLite migrations add columns to `library` and `item` and new `event`, `match_search`, `match_candidate`, and `run` tables. History hangs off `library` (not `item`), so it outlives completed items and goes with a pruned library. The store gains write methods that each run in one transaction. The refresher calls them where it already records attempts, applies reads, and evaluates fix matches. Log lines do not change.

**Tech Stack:** Go (version from `go.mod`), `database/sql` with the existing SQLite driver, zerolog, `net/http/httptest` fakes already in the tests.

**Spec:** `docs/superpowers/specs/2026-11-22-maintenance-dashboard-design.md` — sections "Storage changes", "Completion", "Recording in the refresher", "Process and configuration" (`history-retention` only), "Images" (path and URL validation only), and "Delivery order" stage 1. Read them before each task.

**Branch:** `feat/94-maintenance-dashboard` (parent: `main`). Issue: #94.

## Global Constraints

- Tests need no network, no Plex, and no TMDB token. Reuse the existing fixtures (`openTest`, `newFixture`, `newRefreshFixture`, `fakePlex`, `fakeTMDB`).
- Each production function has a cognitive complexity of 15 or less (`mise run lint`).
- Log with zerolog fields. Do not change any existing log line or field.
- Every store write method that touches more than one row or table runs in one transaction.
- Dry runs (`Options.DryRun`) write no event, no decision, no run row, and run no retention.
- Times are stored as Unix seconds, like the existing columns (`nullTime`, `timeFrom`).
- `history-retention` defaults to `720h` and must be positive.
- Before each commit: `mise run vendor && mise run test && mise run lint` pass. Commit subjects use Conventional Commits with a scope (`store`, `refresher`, `config`, `plex`, `readme`), at most 72 characters, and end the body with `Refs #94` and the `Co-Authored-By` line.

## Review Focus

1. **An item that completes, comes back incomplete, and completes again.** Its second `completed` event must credit only the second occurrence's match, never the first (`fixed_match_event_id` from `last_match_at`). Test in Task 6.
2. **A series read after its path was saved.** Series listings have no `Location`; the read must keep the saved `path`. Test in Task 4.
3. **An upgrade from the current schema with existing rows.** Old rows get empty or null new columns, `Due` still returns them, and their completion writes an event with an empty `fixed_match_event_id`. Test in Task 2.
4. **A fix match that fails part-way** (path lookup ok, first search ok, TMDB lookup fails). The decision must still be written with outcome `failed` and the searches made so far. Test in Task 10.
5. **A Plex-supplied image value that is not a plain item image path** (an absolute URL, `//host/x`, another item's path, a path with a query). It must be stored as empty. Test in Task 3.

---

### Task 1: `history-retention` config

**Files:**
- Modify: `internal/config/config.go` (Config struct, `defaults()`, validation)
- Test: `internal/config/config_test.go`
- Modify: `README.md` (configuration example, defaults table, validation list)

**Interfaces:**
- Produces: `Config.HistoryRetention time.Duration` with yaml key `history-retention`.

- [ ] **Step 1: Write the failing tests**

```go
func TestDecodeDefaultsHistoryRetention(t *testing.T) // decode a minimal valid config; cfg.HistoryRetention == 720*time.Hour
func TestDecodeRejectsNonPositiveHistoryRetention(t *testing.T) // "history-retention: 0s" and "-1h" each return an error containing "history-retention must be positive"
```

- [ ] **Step 2: Run** `go test ./internal/config -run HistoryRetention -v` — Expected: FAIL (unknown field / no default).
- [ ] **Step 3: Implement** the field, default `720 * time.Hour`, and the check `if c.HistoryRetention <= 0 { return errors.New("history-retention must be positive") }` next to the `max-refreshes-per-run` check.
- [ ] **Step 4: Run** the tests — Expected: PASS.
- [ ] **Step 5: README** — add `history-retention: 720h    # how long the dashboard history is kept` to the example, a row `| history-retention | no | 720h |` to the defaults table, and the rule "`history-retention` must be positive" next to the `max-refreshes-per-run` rule.
- [ ] **Step 6: Commit** — `feat(config): add history-retention`

---

### Task 2: Schema migrations

**Files:**
- Create: `internal/store/migrations/6_library_metadata.sql`, `7_item_dashboard.sql`, `8_history.sql`, `9_runs.sql`
- Test: `internal/store/store_test.go`

Columns, tables, keys, and indexes are exactly those in the spec's "Storage changes", with these names:

- `6`: `ALTER TABLE library ADD COLUMN title|type|agent|language TEXT NOT NULL DEFAULT ''`.
- `7`: `item` adds `stopped_at INTEGER`, `thumb`, `art`, `path`, `last_outcome`, `last_match_outcome` (`TEXT NOT NULL DEFAULT ''`), `last_match_at INTEGER`.
- `8`: `event` (`id INTEGER PRIMARY KEY`, `server`, `section`, `rating_key`, `at INTEGER NOT NULL`, `kind`, `source`, `title`, `year INTEGER`, `type`, `thumb`, `art`, `path`, `action`, `accepted INTEGER NOT NULL DEFAULT 0`, `outcome`, `error`, `match_guid`, `match_title`, `match_year INTEGER`, `conflict`, `path_year INTEGER`, `tmdb_year INTEGER`, `episode_count INTEGER`, `links` (newline-joined), `fixed_action`, `fixed_match`, `fixed_match_event_id INTEGER`; text columns `NOT NULL DEFAULT ''`, integer columns `NOT NULL DEFAULT 0` except `fixed_match_event_id`, which is nullable), `FOREIGN KEY (server, section) REFERENCES library (server, section) ON DELETE CASCADE`, indexes `event_item (server, rating_key, at)`, `event_kind (kind, at)`, `event_at (at)`, `event_library (server, section)`. `match_search` (`id`, `event_id` FK cascade, `position`, `query`, `source`, `from_id`, `failed`), index `match_search_event (event_id)`. `match_candidate` (`id`, `search_id` FK cascade, `position`, `guid`, `name`, `year`, `type`, `summary`, `thumb`, `score`, `verdict`), index `match_candidate_search (search_id)`.
- `9`: `run` (`id INTEGER PRIMARY KEY`, `server`, `kind`, `source`, `status`, `started_at INTEGER NOT NULL`, `finished_at INTEGER`, `read_items`, `refreshed`, `matched`, `failed`, `fixed`, `incomplete`, `due`, `selected` (`INTEGER NOT NULL DEFAULT 0`), `error TEXT NOT NULL DEFAULT ''`), indexes `run_started (started_at)`, `run_server (server)`.

- [ ] **Step 1: Write the failing tests**

```go
func TestMigrateAddsDashboardSchema(t *testing.T)
// openTest; PRAGMA index_list on event, match_search, match_candidate, run contains
// event_item, event_kind, event_at, event_library, match_search_event, match_candidate_search, run_started, run_server.

func TestDeletingLibraryCascadesHistory(t *testing.T)
// insert library L, item, event on L, match_search on the event, match_candidate on the search;
// DELETE FROM library WHERE ...; counts of item, event, match_search, match_candidate are all 0.

func TestMigrateKeepsExistingRows(t *testing.T)
// openVersion1 (existing helper), add a row as today's code would, reopen with Open;
// the row reads back through Due with Thumb == "", StoppedAt zero, LastOutcome == "".
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'Migrate|Cascades' -v` — Expected: FAIL.
- [ ] **Step 3: Write the four migration files.** `parseMigrations` already picks them up by number.
- [ ] **Step 4: Run** the tests — Expected: PASS. Run the whole store package too.
- [ ] **Step 5: Commit** — `feat(store): add the dashboard history schema`

---

### Task 3: Image path and candidate URL validation in `plex`, and richer candidates

**Files:**
- Create: `internal/plex/image.go`
- Test: `internal/plex/image_test.go`
- Modify: `internal/plex/action.go` (`Candidate`, `SearchMatches` decoding)
- Modify: `internal/plex/testdata/matches_one.json` (add `"score": 98` and keep `summary`, `thumb`, `type`)
- Test: `internal/plex/action_test.go`

**Interfaces:**
- Produces: `func ItemImagePath(ratingKey, p string) string` — returns `p` if it is exactly `/library/metadata/<ratingKey>/thumb/<digits>` or `/library/metadata/<ratingKey>/art/<digits>`, else `""`.
- Produces: `func SafeRemoteImage(raw string) string` — returns `raw` if it parses as an absolute `https` URL with a host, no user info, and no query key equal (case-insensitive) to `X-Plex-Token` or containing `token`; else `""`.
- Produces: `Candidate{GUID, Name string; Year int; Type, Summary, Thumb string; Score int}`. `Thumb` already passed through `SafeRemoteImage`.

- [ ] **Step 1: Write the failing tests** (table tests)

```go
func TestItemImagePath(t *testing.T)
// ("1001", "/library/metadata/1001/thumb/1700000050") -> same
// ("1001", "/library/metadata/1001/art/1700000050") -> same
// ("1001", "/library/metadata/1002/thumb/1") -> ""
// ("1001", "https://x/library/metadata/1001/thumb/1") -> ""
// ("1001", "//x/library/metadata/1001/thumb/1") -> ""
// ("1001", "/library/metadata/1001/thumb/1?x=1") -> ""
// ("1001", "/library/metadata/1001/thumb/abc") -> ""
// ("1001", "") -> ""

func TestSafeRemoteImage(t *testing.T)
// "https://metadata.example.invalid/thumb.jpg" -> same
// "http://metadata.example.invalid/thumb.jpg" -> ""
// "https://user:pw@h/x.jpg" -> ""
// "https://h/x.jpg?X-Plex-Token=abc" -> ""
// "https://h/x.jpg?apitoken=abc" -> ""
// "/relative.jpg" -> "", "" -> ""

func TestSearchMatchesDecodesCandidateDetails(t *testing.T)
// matches_one.json -> Candidate{GUID:"plex://movie/000000000000000000000001", Name:"Fixture Movie One",
//   Year:2020, Type:"movie", Summary:"A neutral summary.", Thumb:"https://metadata.example.invalid/thumb.jpg", Score:98}
```

- [ ] **Step 2: Run** `go test ./internal/plex -run 'ImagePath|RemoteImage|DecodesCandidate' -v` — Expected: FAIL.
- [ ] **Step 3: Implement** both functions with `net/url` (no string URL building) and extend the `SearchResult` decode struct and the `Candidate` fields. Existing `Match` keeps sending only `guid`, `name`, `year`.
- [ ] **Step 4: Run** the plex package tests — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(plex): decode candidate details and validate image values`

---

### Task 4: Item images, paths, stop flag, and library metadata in the store

**Files:**
- Modify: `internal/store/item.go` (`Item`, `itemColumns`, `scanItem`, `writeExisting`, `insertNewRows`, `Due`, `CountIncomplete`)
- Create: `internal/store/library.go` (`LibraryInfo`, `SaveLibraries`, `SetPath`)
- Test: `internal/store/item_test.go`, `internal/store/library_test.go`

**Interfaces:**
- Produces on `store.Item`: `Thumb, Art, Path string`, `StoppedAt time.Time` (zero: not stopped), `LastOutcome, LastMatchOutcome string`, `LastMatchAt time.Time`.
- Produces: `type LibraryInfo struct{ Section, Title, Type, Agent, Language string }` and `func (s *Store) SaveLibraries(server string, libs []LibraryInfo) error` — upserts each library row's metadata, inserting missing rows with null read times.
- Produces: `func (s *Store) SetPath(server, ratingKey, path string) error` — sets `item.path` if the row exists; a missing row is not an error.
- `ApplyRead`: inserts and present-row updates write `thumb` and `art` from the read; `path` is written only when the read's value is not empty (`path = CASE WHEN ? = '' THEN path ELSE ? END`). `stopped_at`, `last_*` columns are never touched by a read.

- [ ] **Step 1: Write the failing tests**

```go
func TestApplyReadStoresImagesAndPath(t *testing.T) // read item with Thumb, Art, Path -> rows() has them
func TestApplyReadKeepsSavedPathWhenReadHasNone(t *testing.T) // SetPath("s","k","/tv/Show"), read again with Path "" -> Path == "/tv/Show"
func TestApplyReadReplacesImages(t *testing.T) // second read with new Thumb -> new value
func TestApplyReadKeepsStoppedAt(t *testing.T) // set stopped_at via SQL, re-read -> StoppedAt unchanged
func TestDueAndCountSkipStoppedItems(t *testing.T) // two due rows, one stopped -> Due returns 1 row, total 1; CountIncomplete == 1
func TestSaveLibrariesUpsertsMetadata(t *testing.T) // save, save again with new title -> one row with new title, agent, language; read times still null
func TestSetPathIgnoresMissingRow(t *testing.T)
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'Images|SavedPath|StoppedAt|Stopped|SaveLibraries|SetPath' -v` — Expected: FAIL.
- [ ] **Step 3: Implement.** `Due` and `CountIncomplete` add `AND stopped_at IS NULL`. `scanItem` reads the new columns (`nullTime`/`timeFrom` for the two times).
- [ ] **Step 4: Run** the whole store package — Expected: PASS (existing tests unchanged).
- [ ] **Step 5: Commit** — `feat(store): keep item images, paths, and library metadata`

---

### Task 5: Requests and gone items

**Files:**
- Create: `internal/store/history.go` (types shared by Tasks 5–8, `snapshot` helper, `RecordRequest`, `RecordGone`)
- Modify: `internal/store/item.go` (remove `RecordAttempt` and `Delete` after their callers move in Task 9)
- Test: `internal/store/history_test.go`

**Interfaces:**
- Produces:

```go
type Source string // SourceAuto = "auto", SourceManual = "manual"
type EventKind string // EventRequest = "request", EventMatchDecision = "match-decision", EventCompleted = "completed", EventGone = "gone", EventStopped = "stopped", EventResumed = "resumed"

// MatchTarget is the candidate of a match request and how it was confirmed.
type MatchTarget struct {
	GUID, Title                      string
	Year                             int
	Links                            []string
	PathYear, TMDBYear, EpisodeCount int
}

// Request is one attempt of an item. Action is "" for a fix match search
// that failed before any request was sent.
type Request struct {
	Server, RatingKey string
	At                time.Time
	Source            Source
	Action            Action
	Accepted          bool
	Error             string
	Match             *MatchTarget // match requests only
}

func (s *Store) RecordRequest(r Request) error
func (s *Store) RecordGone(server, ratingKey string, at time.Time, source Source) error
```

- `RecordRequest`, in one transaction: the attempt update of today's `RecordAttempt` (accepted → `last_refresh`, `refreshes+1`, `last_action`; accepted match → also `last_match` and `last_match_at = At`; always `last_attempt`, `attempts+1`), `last_outcome` (`string(Action)` if accepted, `string(Action)+"-failed"` if not, `"search-failed"` if `Action == ""`), and, when `Action != ""`, a `request` event with the item's snapshot. A missing row writes nothing and is not an error (today's behaviour).
- `RecordGone`, in one transaction: a `gone` event with the snapshot, then the row delete. A missing row writes nothing.
- The snapshot (`section`, `title`, `year`, `type`, `thumb`, `art`, `path`) is read from the `item` row inside the transaction.

- [ ] **Step 1: Write the failing tests** — port `TestRecordAttempt` and `TestRecordAttemptKeepsActions` to `RecordRequest` with the same assertions, then add:

```go
func TestRecordRequestWritesEventAndOutcome(t *testing.T)
// accepted refresh -> one request event: Kind request, Source auto, Action refresh, Accepted true, Title from row; LastOutcome "refresh"
func TestRecordRequestFailedMatch(t *testing.T)
// Action match, Accepted false, Error "boom" -> event Accepted false, Error "boom"; LastOutcome "match-failed"; Refreshes unchanged; attempts +1
func TestRecordRequestAcceptedMatchSetsLastMatchAt(t *testing.T)
// Match target with Links ["https://www.themoviedb.org/movie/1"], PathYear 2019 -> event MatchGUID, MatchTitle, MatchYear, Links, PathYear; row LastMatchAt == At
func TestRecordRequestSearchFailedWritesNoEvent(t *testing.T) // Action "" -> no event; LastOutcome "search-failed"; attempts +1
func TestRecordGoneWritesEventAndDeletesRow(t *testing.T)
```

Events are read back with `Timeline` from Task 6's Interfaces; until then the test reads `event` with SQL through a test helper `events(t, s) []map[string]any`.

- [ ] **Step 2: Run** `go test ./internal/store -run 'RecordRequest|RecordGone' -v` — Expected: FAIL.
- [ ] **Step 3: Implement** in `history.go`. Keep `RecordAttempt` and `Delete` until Task 9 removes their callers.
- [ ] **Step 4: Run** the store package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(store): record requests and gone items as events`

---

### Task 6: Completion events and the timeline read

**Files:**
- Modify: `internal/store/item.go` (`applyOldRows`/`writeExisting` pass the read metadata; `deleteGone`)
- Modify: `internal/store/history.go` (`writeCompleted`, `Event`, `Timeline`)
- Test: `internal/store/history_test.go`

**Interfaces:**
- Produces:

```go
type Event struct {
	ID                               int64
	Server, Section, RatingKey       string
	At                               time.Time
	Kind                             EventKind
	Source                           Source
	Title                            string
	Year                             int
	Type, Thumb, Art, Path           string
	Action                           Action
	Accepted                         bool
	Outcome, Error                   string
	MatchGUID, MatchTitle            string
	MatchYear                        int
	Conflict                         string
	PathYear, TMDBYear, EpisodeCount int
	Links                            []string
	FixedAction, FixedMatch          Action
	FixedMatchEventID                int64 // 0: unknown
	Searches                         []MatchSearch // match-decision events only, written by Task 7
}

// Defined here because Event uses them; Task 7 writes them.
type MatchCandidate struct {
	GUID, Name           string
	Year                 int
	Type, Summary, Thumb string
	Score                int
	Verdict              string // "picked", or the reason it was not picked
}

type MatchSearch struct {
	Query      string // for example "tmdb-48213"
	Source     string // "path", "tmdb", or "tie"
	FromID     string // the path ID a tmdb search came from, for example "tvdb-31207"
	Failed     bool
	Candidates []MatchCandidate
}

// Timeline returns the events of one item, newest first, with the searches
// and candidates of its match decisions.
func (s *Store) Timeline(server, ratingKey string) ([]Event, error)
```

- `ApplyRead` writes, in its transaction, one `completed` event for each row it deletes because the final reasons are 0. Snapshot: title, year, thumb, art from the completing read when the read lists the key (`readKey.present`), else from the row; path and art fall back to the row when the read's value is empty. Attribution from the row: `fixed_action = last_action`, `fixed_match = last_match`, `fixed_match_event_id` = the `id` of the accepted `request` event of the same server and rating key with `at = last_match_at` and `action = last_match` (null when `last_match_at` is null or no such event exists). Source is `auto`.
- `deleteGone` writes a `gone` event for each `r.Gone` row that exists, before the delete.

- [ ] **Step 1: Write the failing tests**

```go
func TestApplyReadWritesCompletedEvent(t *testing.T)
// row with refresh request accepted, then a read completes it -> one completed event, FixedAction refresh, snapshot Title from the read
func TestCompletedEventCreditsCurrentOccurrenceOnly(t *testing.T)
// occurrence 1: accepted match at t1, completes; occurrence 2: new row same key, accepted match at t2, completes
// -> second completed event FixedMatchEventID == id of the t2 request event, not t1
func TestCompletedEventPrefersReadMetadata(t *testing.T)
// row Title "Old", Thumb ""; completing read Title "New", Thumb "/library/metadata/k/thumb/2" -> event Title "New", Thumb set; Path from row
func TestLegacyRowCompletesWithoutMatchEvent(t *testing.T)
// row with last_match set by SQL and last_match_at NULL -> FixedMatch set, FixedMatchEventID 0
func TestApplyReadWritesGoneEvents(t *testing.T)
func TestTimelineNewestFirst(t *testing.T)
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'Completed|Completes|GoneEvents|Timeline' -v` — Expected: FAIL.
- [ ] **Step 3: Implement.** Keep each function under the complexity limit: `applyOldRows` collects `(prev, readKey)` pairs for completed rows; a separate `writeCompleted(tx, r, prev, in)` writes each event.
- [ ] **Step 4: Run** the store package — Expected: PASS, including every existing `ApplyRead` test.
- [ ] **Step 5: Commit** — `feat(store): record completions with the match that fixed them`

---

### Task 7: Match decisions

**Files:**
- Modify: `internal/store/history.go`
- Test: `internal/store/history_test.go`

**Interfaces:**
- Produces:

```go
type DecisionOutcome string // "matched", "no-match", "no-ids", "failed"

// MatchSearch and MatchCandidate come from Task 6.
type MatchDecision struct {
	Server, RatingKey                string
	At                               time.Time
	Source                           Source
	Outcome                          DecisionOutcome
	Conflict, Error                  string
	PathYear, TMDBYear, EpisodeCount int
	Searches                         []MatchSearch
}

func (s *Store) RecordMatchDecision(d MatchDecision) error
```

- One transaction: a `match-decision` event (snapshot from the row, `outcome`, `conflict`, `error`, confirmation years), its searches and candidates with `position` in slice order, and `item.last_match_outcome = Outcome`. A missing row writes nothing.
- `Timeline` fills `Event.Searches` for decision events in `position` order.

- [ ] **Step 1: Write the failing tests**

```go
func TestRecordMatchDecisionStoresSearchesInOrder(t *testing.T)
// two searches (path tmdb-1 with 1 candidate Verdict "picked"; tmdb from tvdb-2, Failed true) ->
// Timeline()[0].Searches equals the input; row LastMatchOutcome "matched"
func TestRecordMatchDecisionNoIDs(t *testing.T) // Outcome no-ids, no searches -> event Outcome "no-ids", Searches empty
func TestRetentionCascadeRemovesDecisionChildren(t *testing.T) // delete the event row -> match_search and match_candidate counts 0
```

- [ ] **Step 2: Run** — Expected: FAIL. **Step 3: Implement.** **Step 4: Run** the store package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(store): record fix match decisions`

---

### Task 8: Runs, retention, and pruning

**Files:**
- Create: `internal/store/run.go`
- Modify: `internal/store/item.go` (`PruneServers` also deletes `run` rows of removed servers in its transaction)
- Test: `internal/store/run_test.go`

**Interfaces:**
- Produces:

```go
type RunKind string   // RunKindRun = "run", RunKindBulkRefresh = "bulk-refresh"
type RunStatus string // "running", "finished", "failed", "canceled", "interrupted"

type RunStart struct {
	Server string
	Kind   RunKind
	Source Source // SourceAuto for scheduled runs
	At     time.Time
}

type RunResult struct {
	At                                     time.Time
	Status                                 RunStatus
	Error                                  string
	ReadItems, Refreshed, Matched, Failed  int
	Fixed, Incomplete, Due, Selected       int
}

type Run struct {
	ID int64
	RunStart
	FinishedAt time.Time // zero while running
	RunResult
}

func (s *Store) StartRun(r RunStart) (int64, error)              // status running
func (s *Store) FinishRun(id int64, r RunResult) error
func (s *Store) InterruptRuns(at time.Time) (int, error)         // running -> interrupted, finished_at = at
func (s *Store) PruneHistory(before time.Time) (events, runs int, err error) // deletes rows with at/started_at < before
func (s *Store) Runs(limit int) ([]Run, error)                   // newest first
```

- [ ] **Step 1: Write the failing tests**

```go
func TestStartAndFinishRun(t *testing.T)
func TestInterruptRunsMarksOnlyRunning(t *testing.T)
func TestPruneHistoryDeletesOldRowsWithChildren(t *testing.T) // an old decision with searches and a new event -> old gone with children, new kept; counts returned
func TestPruneServersDeletesRunsOfRemovedServers(t *testing.T)
```

- [ ] **Step 2: Run** — Expected: FAIL. **Step 3: Implement.** **Step 4: Run** the store package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(store): record runs and prune old history`

---

### Task 9: Refresher records requests, gone items, images, paths, and libraries

**Files:**
- Modify: `internal/refresher/refresher.go` (`Store` interface: add `RecordRequest`, `RecordGone`, `RecordMatchDecision`, `SaveLibraries`, `SetPath`, `StartRun`, `FinishRun`, `PruneHistory`; remove `RecordAttempt`, `Delete`)
- Modify: `internal/refresher/refresh.go` (`refreshItem`, `itemFailed`, `countFailure`, `itemPaths`)
- Modify: `internal/refresher/read.go` (`readLibraries`, `storeItem`)
- Modify: `internal/store/item.go` (delete `RecordAttempt` and `Delete`)
- Test: `internal/refresher/refresh_test.go`, `internal/refresher/read_test.go`, and the test seed helper `fixture.seed` (use `RecordRequest`)

**Interfaces:**
- Consumes: Tasks 3–5.
- Behaviour:
  - `refreshItem` → `RecordRequest{Action: action, Accepted: true, Source: SourceAuto, Match: target(fm)}` on success. `target(fm *fixMatch) *store.MatchTarget` copies `cand.GUID`, `cand.Name`, `cand.Year`, `links`, `pathYear`, `tmdbYear`, `episodeCount`; nil for a plain refresh.
  - `itemFailed(…, action, err)`: a 404 calls `RecordGone` (not in dry run); other failures call `countFailure(ctx, run, it, action, err)`, which records `RecordRequest{Action: action, Accepted: false, Error: err.Error()}`. The attempt and stop rules stay exactly as today.
  - `storeItem` sets `Thumb: plex.ItemImagePath(it.RatingKey, it.Thumb)`, `Art: plex.ItemImagePath(it.RatingKey, it.Art)`, and for movies `Path: folderOf(it.Files)`. `folderOf(files []string) string` returns the part of the first file before its last `/` or `\`, or `""`.
  - `itemPaths` calls `SetPath(server, key, paths[0])` after a successful `ItemPaths` of a series (not in dry run).
  - `readLibraries` calls `SaveLibraries` with the selected libraries after `selectLibraries` and before `pruneSections`.

- [ ] **Step 1: Write the failing tests**

```go
func TestRefreshRecordsAcceptedRequestEvent(t *testing.T)   // Timeline has one request event, Accepted, Action refresh, Source auto
func TestFailedRefreshRecordsFailedEvent(t *testing.T)     // refreshErrs -> event Accepted false, Error set, row LastOutcome "refresh-failed"
func TestNotFoundRecordsGoneEvent(t *testing.T)            // refreshErrs 404 -> row gone, gone event
func TestMatchRecordsTarget(t *testing.T)                  // existing unmatched fixture -> request event MatchGUID, Links
func TestDryRunRecordsNoEvents(t *testing.T)
func TestReadStoresImagesAndMovieFolder(t *testing.T)      // movies_unmatched-like item -> row Thumb/Art set, Path is the folder
func TestReadDropsForeignImagePaths(t *testing.T)          // Thumb "https://x/y.jpg" -> row Thumb ""
func TestSeriesPathSavedAfterItemPaths(t *testing.T)
func TestReadSavesLibraryMetadata(t *testing.T)
```

- [ ] **Step 2: Run** `go test ./internal/refresher -v` — Expected: new tests FAIL.
- [ ] **Step 3: Implement**, then remove `RecordAttempt` and `Delete` from the store and its tests (their assertions moved to Task 5's tests).
- [ ] **Step 4: Run** `mise run test` — Expected: PASS. Existing log-field tests still pass unchanged.
- [ ] **Step 5: Commit** — `feat(refresher): record requests, images, paths, and libraries`

---

### Task 10: Refresher records every fix match decision

**Files:**
- Create: `internal/refresher/decision.go` (`decisionLog`, verdicts, `recordDecision`)
- Modify: `internal/refresher/refresh.go` (`findMatch`, `resolveMatch`, `searchIDs`), `internal/refresher/tmdb.go` (`tmdbResults`, `tieTMDBCandidate`, `confirmYear` paths that search)
- Test: `internal/refresher/decision_test.go`

**Interfaces:**
- Consumes: Task 7.
- Produces (package-private):

```go
// decisionLog collects the searches of one fix match evaluation as they happen.
type decisionLog struct{ searches []store.MatchSearch }

func (d *decisionLog) add(id pathID, source, fromID string, cands []plex.Candidate, failed bool)

// verdicts sets each candidate's Verdict: "picked" for the GUID of picked;
// otherwise conflict, or "year differs" for a candidate whose year differs
// from itemYear by more than 1 (yearDiffers).
func verdicts(searches []store.MatchSearch, picked string, itemYear int, conflict string)
```

- `findMatch` creates a `decisionLog` after `parseIDs` and passes it to `searchIDs`, `tmdbResults` (source `tmdb`, `fromID` = the path ID), and `tieTMDBCandidate` (source `tie`). First-pass searches are kept when the TMDB pass replaces `results`.
- A decision is recorded on every exit of `findMatch` after the guard at its top (unmatched, not ignored, refreshed before), and never in a dry run:
  - no IDs → `no-ids`, no searches;
  - an error from `ItemPaths`, a search, TMDB, or episode lookup → `failed` with `Error` and the searches so far (the error still returns as today);
  - a match → `matched`, with `PathYear`, `TMDBYear`, `EpisodeCount`;
  - otherwise → `no-match` with the conflict (`""` when no search found a candidate).
- A failed `tvdb`/`imdb` search that `searchFailureIsEmpty` accepts is added with `Failed: true` and no candidates.
- `recordDecision` writes `store.MatchDecision` with `Source: store.SourceAuto`.

- [ ] **Step 1: Write the failing tests**

```go
func TestDecisionMatchedWithSearches(t *testing.T)        // one path search, one candidate -> outcome matched, verdict picked
func TestDecisionNoIDs(t *testing.T)                      // unmatched item with no ID in its path -> outcome no-ids
func TestDecisionDifferentCandidates(t *testing.T)        // two IDs find different GUIDs -> no-match, conflict "different candidates", both verdicts "different candidates"
func TestDecisionYearDiffers(t *testing.T)                // year gap not confirmed -> no-match "year differs", candidate verdict "year differs"
func TestDecisionKeepsFirstPassWithTMDB(t *testing.T)     // first pass empty, TMDB pass finds -> searches: path (empty) then tmdb with FromID
func TestDecisionFailedKeepsSearchesSoFar(t *testing.T)   // path search ok, TMDB lookup fails -> outcome failed, Error set, first search kept
func TestDecisionRecordsTieSearch(t *testing.T)           // year confirmation via tieTMDBCandidate -> a search with Source "tie"
func TestDecisionAcceptedEmptySearchFailure(t *testing.T) // tvdb search error accepted as empty -> Failed true
```

Reuse the scenarios of `refresh_test.go` and `tmdb_test.go` that already drive these paths; assert on `Timeline()`.

- [ ] **Step 2: Run** `go test ./internal/refresher -run Decision -v` — Expected: FAIL.
- [ ] **Step 3: Implement.** Split `findMatch` into named steps if the complexity limit needs it; the log lines (`Match conflict`, `Match search result`, `Sent request`) keep their fields.
- [ ] **Step 4: Run** `mise run test && mise run lint` — Expected: PASS, 0 issues.
- [ ] **Step 5: Commit** — `feat(refresher): record every fix match decision`

---

### Task 11: Run rows, retention, and interrupted runs

**Files:**
- Modify: `internal/refresher/refresher.go` (`Options.HistoryRetention`, `Run`, `runServer`)
- Modify: `internal/refresher/refresh.go` (`logSummary` returns the incomplete count for the run row)
- Modify: `cmd/autorefresh/main.go` (pass `HistoryRetention`; call `InterruptRuns` once at start in scheduled mode only)
- Test: `internal/refresher/run_test.go`, `cmd/autorefresh/main_test.go`

**Interfaces:**
- Consumes: Task 8, Task 1.
- Behaviour:
  - `Options.HistoryRetention time.Duration` (0: no retention, for tests).
  - `Run` (not dry run): after `PruneServers`, calls `PruneHistory(now - HistoryRetention)` when `HistoryRetention > 0`, and logs at debug level with fields `events` and `runs` when either is above 0.
  - `runServer` (not dry run): `StartRun{Server, Kind: RunKindRun, Source: SourceAuto, At: run.began}` at its start, and `FinishRun` on every return with: `ReadItems = fullReadItems + incrementalReadItems`, `Refreshed = refreshes`, `Matched = matches`, `Failed = failures`, `Fixed = fixed`, `Incomplete` (from the summary, 0 when no summary), `Due = due`, `Selected = len(batch)` (add `selected int` to `serverRun`, set in `refreshDue`), and `Status`: `canceled` when ctx ended, `failed` with `Error` when the server was unavailable or a store error stopped it, else `finished`.
  - `main.run`: when neither `--once` nor `--dry-run` is set, `st.InterruptRuns(time.Now())` right after the store opens, logging the count at info level as `Marked interrupted runs` with field `runs` when above 0.

- [ ] **Step 1: Write the failing tests**

```go
func TestRunWritesFinishedRunRow(t *testing.T)            // one server, one due item -> Runs(10)[0]: Status finished, Refreshed 1, Selected 1, Due 1
func TestUnavailableServerWritesFailedRunRow(t *testing.T) // librariesErr -> Status failed, Error contains "unavailable"
func TestCanceledRunWritesCanceledRow(t *testing.T)
func TestRunPrunesOldHistory(t *testing.T)                // event older than retention is gone after Run
func TestDryRunWritesNoRunRowAndKeepsHistory(t *testing.T)
func TestScheduledStartMarksInterruptedRuns(t *testing.T) // main: running row before start -> interrupted; --once leaves it running
```

- [ ] **Step 2: Run** — Expected: FAIL. **Step 3: Implement.** **Step 4: Run** `mise run test && mise run lint` — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(refresher): record runs and prune old history`

---

### Task 12: Documentation

**Files:**
- Modify: `README.md` (a short "History" section under "How it works": what is recorded, `history-retention`, dry runs record nothing)
- Modify: `AGENTS.md` (the `internal/store/` and `internal/refresher/` rows of the path table: history, decisions, runs, retention)

- [ ] **Step 1: Write the sections** in the README's existing style (short sentences, the same terms as the log fields).
- [ ] **Step 2: Run** `mise run vendor && mise run test && mise run lint` — Expected: PASS, `go.mod`/`go.sum` unchanged.
- [ ] **Step 3: Commit** — `docs(readme): describe the recorded history`

---

## Delivery

After Task 12: push `feat/94-maintenance-dashboard`, open a PR into `main` titled `feat(store): record dashboard history, decisions, and runs`, with the problem, the change, the checks run, `feat/94-maintenance-dashboard → main`, and `Refs #94` (not `Closes`: the issue stays open for stages 2–5). Follow `AGENTS.md` for CodeRabbit and auto-merge.
