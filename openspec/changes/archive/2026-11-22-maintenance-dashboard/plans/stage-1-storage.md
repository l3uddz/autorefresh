# Dashboard Stage 1: Storage and Recording Implementation Plan

**Goal:** Store everything the maintenance dashboard will show — item history, fix match decisions, runs, library metadata, images, and paths — and record it during normal runs, with no UI yet.

**Architecture:** New SQLite migrations add columns to `library` and `item` and new `event`, `match_search`, `match_candidate`, and `run` tables. History hangs off `library` (not `item`), so it outlives completed items and goes with a pruned library. The store gains write methods that each run in one transaction. The refresher calls them where it already records attempts, applies reads, and evaluates fix matches. Existing log lines do not change.

**Tech Stack:** Go (version from `go.mod`), `database/sql` with the existing SQLite driver (one connection: `SetMaxOpenConns(1)`), zerolog.

**Spec:** `../design.md` — sections "Storage changes", "Completion", "Recording in the refresher", "Process and configuration" (`history-retention` only), "Images" (path and URL validation only), and "Delivery order" stage 1. Read them before each task.

**Branch:** `feat/94-maintenance-dashboard` (parent: `main`). Issue: #94.

## Global Constraints

- Tests need no network, no Plex, and no TMDB token. Reuse the existing fixtures (`openTest`, `newFixture`, `newRefreshFixture`, `fakePlex`, `fakeTMDB`).
- Each production function has a cognitive complexity of 15 or less (`mise run lint`). Split into named steps when needed.
- Do not change any existing log line or field. New log lines are listed in their task.
- Every store write method that touches more than one row or table runs in one transaction.
- The store has one connection. Never run a query while another query's `*sql.Rows` is open: read all parent rows and close them before reading children.
- Event and run IDs are `INTEGER PRIMARY KEY AUTOINCREMENT`, so a deleted ID is never reused.
- Dry runs (`Options.DryRun`) write no event, no decision, and no run row, run no retention, and prune nothing (they log one `Dry run keeps cache of removed libraries` line per server, Task 9). Dry runs still update the item cache and the read times exactly as today, also deleting rows that a read finds complete or gone. Known limit, accepted on purpose: a completion or gone item that a dry run sees first is never recorded as an event, because the row is gone before a scheduled run reads it. Keeping those rows would also need the dry run to keep the read checkpoints and to leave them out of `Due` and every count; a diagnostic mode does not justify that. The README states the limit (Task 13).
- `event.source` is `auto` or `manual`. `run.source` is `schedule` or `manual`. They are different Go types.
- Times are stored as Unix seconds (`nullTime`, `timeFrom`). Lists of events and runs are ordered newest first by time, then by ID (`at DESC, id DESC`).
- `history-retention` defaults to `720h` and must be positive.
- Each task that adds a public store method adds it to `TestMethodsReportDatabaseFailure` (`internal/store/store_test.go`).
- Before each commit: `mise run vendor && mise run test && mise run lint` pass. Commit subjects use Conventional Commits with a scope (`store`, `refresher`, `config`, `plex`, `cli`, `readme`), at most 72 characters, and the body ends with `Refs #94` and the `Co-Authored-By` line.

## Review Focus

1. **An item that completes, comes back incomplete, and completes again, with both accepted matches in the same second.** The second `completed` event credits the second request's event ID. Test in Task 6.
2. **A dry run against a shared database with a different configuration.** It must not delete any library, server cache, or history, and must write no event. Tests in Tasks 6 and 9.
3. **An upgrade from schema version 5 with action history.** Data is kept, new columns take their defaults, the row is still due, and its completion writes an event with no match event ID. Tests in Tasks 2, 4, and 6.
4. **A fix match whose TMDB lookup fails after a successful path search.** The decision is `failed` with that search kept, and the item still gets its plain refresh as today. A Plex episode-number error instead stops the item before any request, as today. Tests in Task 11.
5. **Plex-supplied image values that are not a plain item image path or a safe remote URL.** They are stored as empty. Tests in Task 3.

---

### Task 1: `history-retention` config

**Files:**
- Modify: `internal/config/config.go` (Config struct, `defaults()`, validation)
- Test: `internal/config/config_test.go`
- Modify: `README.md` (configuration example, defaults table, validation rules)

**Interfaces:**
- Produces: `Config.HistoryRetention time.Duration`, yaml key `history-retention`.

- [ ] **Step 1: Write the failing tests**

```go
func TestDecodeDefaultsHistoryRetention(t *testing.T) // minimal valid config -> cfg.HistoryRetention == 720*time.Hour
func TestDecodeRejectsNonPositiveHistoryRetention(t *testing.T) // "0s" and "-1h" -> error containing "history-retention must be positive"
```

- [ ] **Step 2: Run** `go test ./internal/config -run HistoryRetention -v` — Expected: build failure (`HistoryRetention` undefined).
- [ ] **Step 3: Implement** the field, the default `720 * time.Hour`, and `if c.HistoryRetention <= 0 { return errors.New("history-retention must be positive") }` beside the `max-refreshes-per-run` check.
- [ ] **Step 4: Run** the tests — Expected: PASS.
- [ ] **Step 5: README** — add `history-retention: 720h    # how long the dashboard history is kept` to the example, `| history-retention | no | 720h |` to the defaults table, and "`history-retention` must be positive" beside the `max-refreshes-per-run` rule.
- [ ] **Step 6: Commit** — `feat(config): add history-retention`

---

### Task 2: Schema migrations

**Files:**
- Create: `internal/store/migrations/6_library_metadata.sql`, `7_item_dashboard.sql`, `8_history.sql`, `9_runs.sql`
- Test: `internal/store/store_test.go`

Schema (text columns `TEXT NOT NULL DEFAULT ''` and integer columns `INTEGER NOT NULL DEFAULT 0` unless stated):

- `6`: `library` adds `title`, `type`, `agent`, `language`.
- `7`: `item` adds `stopped_at INTEGER` (null), `thumb`, `art`, `path`, `last_outcome`, `last_match_outcome`, `last_match_event_id INTEGER` (null; no foreign key, because `item` is created before `event` and SQLite cannot add one later).
- `8`:
  - `event`: `id INTEGER PRIMARY KEY AUTOINCREMENT`, `server`, `section`, `rating_key`, `at INTEGER NOT NULL`, `kind`, `source`, `title`, `year`, `type`, `thumb`, `art`, `path`, `action`, `accepted`, `outcome`, `error`, `match_guid`, `match_title`, `match_year`, `conflict`, `path_year`, `tmdb_year`, `episode_count`, `links` (newline-joined), `fixed_action`, `fixed_match`, `fixed_match_event_id INTEGER REFERENCES event (id) ON DELETE SET NULL` (null); `FOREIGN KEY (server, section) REFERENCES library (server, section) ON DELETE CASCADE`. Indexes `event_item (server, rating_key, at)`, `event_kind (kind, at)`, `event_at (at)`, `event_library (server, section)`, `event_fixed_match (fixed_match_event_id)`.
  - `match_search`: `id INTEGER PRIMARY KEY AUTOINCREMENT`, `event_id INTEGER NOT NULL REFERENCES event (id) ON DELETE CASCADE`, `position`, `query`, `source`, `from_id`, `failed`. Index `match_search_event (event_id)`.
  - `match_candidate`: `id INTEGER PRIMARY KEY AUTOINCREMENT`, `search_id INTEGER NOT NULL REFERENCES match_search (id) ON DELETE CASCADE`, `position`, `guid`, `name`, `year`, `type`, `summary`, `thumb`, `score`, `verdict`. Index `match_candidate_search (search_id)`.
- `9`: `run`: `id INTEGER PRIMARY KEY AUTOINCREMENT`, `server`, `kind`, `source`, `status`, `started_at INTEGER NOT NULL`, `finished_at INTEGER` (null), `read_items`, `refreshed`, `matched`, `failed`, `completed`, `fixed`, `incomplete`, `due`, `selected`, `error`. Indexes `run_started (started_at)`, `run_server (server)`.

- [ ] **Step 1: Write the failing tests** (SQL only; no new Go types yet)

```go
// openAtVersion creates schema_migration, applies migrations 1..v with
// parseMigrations and applyMigration to a new file, and returns its path.
// openVersion1 stays as it is.
func openAtVersion(t *testing.T, v int) string

func TestMigrateAddsDashboardSchema(t *testing.T)
// openTest; PRAGMA index_list of event, match_search, match_candidate, run includes
// event_item, event_kind, event_at, event_library, event_fixed_match,
// match_search_event, match_candidate_search, run_started, run_server.

func TestMigrateFromVersion5KeepsActionHistory(t *testing.T)
// openAtVersion(t, 5); insert library home/9 and an item with refreshes 2, attempts 3,
// last_action 'match', last_match 'match'; Open the file;
// SELECT shows the old values unchanged, thumb = '', stopped_at IS NULL,
// last_outcome = '', last_match_event_id IS NULL.

func TestDeletingLibraryCascadesHistory(t *testing.T)
// library, item, event, match_search, match_candidate rows by SQL; DELETE FROM library;
// all five counts are 0.

func TestDeletingEventClearsFixedMatchReference(t *testing.T)
// event A, event B with fixed_match_event_id = A; DELETE A -> B.fixed_match_event_id IS NULL;
// then DELETE B (the highest ID) and insert C -> C.id > B.id (AUTOINCREMENT does not reuse it).
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'Migrate|Cascades|FixedMatchReference' -v` — Expected: FAIL.
- [ ] **Step 3: Write the four migration files.** `parseMigrations` picks them up by number.
- [ ] **Step 4: Run** the store package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(store): add the dashboard history schema`

---

### Task 3: Image validation and candidate details in `plex`

**Files:**
- Create: `internal/plex/image.go`, `internal/plex/image_test.go`
- Modify: `internal/plex/action.go` (`Candidate`, `SearchMatches` decoding)
- Modify: `internal/plex/testdata/matches_one.json` (add `"score": 98`)
- Modify: `internal/plex/action_test.go` (`TestSearchMatchesDecodesCandidates` expects the new fields)
- Modify: `internal/plex/library.go` (`Paths`, `ItemPaths`), `internal/plex/library_test.go`, `internal/plex/client_test.go` (call sites)
- Modify: `internal/refresher/refresher.go` (`Plex.ItemPaths` signature), `internal/refresher/fake_test.go` (`fakePlex.ItemPaths` returns `plex.Paths{Locations: f.paths[key]}`), `internal/refresher/refresh.go` (`itemPaths` returns `.All()`), `internal/refresher/read.go` (ignores the value as today)

**Interfaces:**
- Produces: `func ItemImagePath(ratingKey, p string) string` — returns `p` only if `ratingKey` matches `idPattern` (`client.go`) and `p` is exactly `/library/metadata/<ratingKey>/thumb/<digits>` or `/library/metadata/<ratingKey>/art/<digits>` (no query, fragment, or escape); else `""`.
- Changes: `type Paths struct{ Locations, Files []string }` with `func (p Paths) All() []string` (Locations when not empty, else Files: today's rule). `(c *Client) ItemPaths(ctx, ratingKey) (Paths, error)` fills both from the response. Match evaluation uses `All()`, so its behavior does not change; Task 10 saves only `Locations[0]`, so a media file can never become a series folder, whatever the response's `type`.
- Produces: `func SafeRemoteImage(raw string) string` — returns `raw` only if `raw` contains no `#` (a bare trailing `#` leaves `URL.Fragment` empty, as `config.go` already handles), `url.Parse` succeeds, the scheme is `https`, `Hostname()` is not empty, there is no user info, `url.ParseQuery(RawQuery)` succeeds, and no query key contains `token` (case-insensitive, so `X-Plex-Token` too); else `""`.
- Produces: `Candidate{GUID, Name string; Year int; Type, Summary, Thumb string; Score int}`. `SearchMatches` sets `Thumb` through `SafeRemoteImage`. `Match` keeps sending only `guid`, `name`, `year`.

- [ ] **Step 1: Write the failing tests** (table tests)

```go
func TestItemImagePath(t *testing.T)
// ("1001", "/library/metadata/1001/thumb/1700000050") and the art form -> same
// -> "" for: key "10a1"; "/library/metadata/1002/thumb/1"; "https://x/library/metadata/1001/thumb/1";
//    "//x/library/metadata/1001/thumb/1"; "/library/metadata/1001/thumb/1?x=1"; "...#f";
//    "/library/metadata/1001/thumb/abc"; "/library/metadata/1001%2Fthumb/1"; ""

func TestSafeRemoteImage(t *testing.T)
// "https://metadata.example.invalid/thumb.jpg" -> same
// -> "" for: "http://h/x.jpg"; "https://user:pw@h/x.jpg"; "https://:443/x.jpg";
//    "https://h/x.jpg?X-Plex-Token=abc"; "https://h/x.jpg?apitoken=abc"; "https://h/x.jpg?a=%zz";
//    "https://h/x.jpg#f"; "https://h/x.jpg#"; "/relative.jpg"; ""
// TestItemPathsReadsLocationsAndFiles keeps its cases, asserting All(), and also asserts Locations and Files:
//   item_show.json -> Locations == want, Files empty; item_movie.json -> Locations empty, Files == want (all three parts, in order)
```

Update `TestSearchMatchesDecodesCandidates` to expect `Candidate{GUID: "plex://movie/000000000000000000000001", Name: "Fixture Movie One", Year: 2020, Type: "movie", Summary: "A neutral summary.", Thumb: "https://metadata.example.invalid/thumb.jpg", Score: 98}`.

- [ ] **Step 2: Run** `go test ./internal/plex -v` — Expected: FAIL.
- [ ] **Step 3: Implement** with `net/url` only.
- [ ] **Step 4: Run** the plex package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(plex): decode candidate details and validate image values`

---

### Task 4: Item images, paths, stop flag, and library metadata in the store

**Files:**
- Modify: `internal/store/item.go` (`Item`, `itemColumns`, `scanItem`, `writeExisting`, `insertNewRows`, `Due`, `CountIncomplete`)
- Create: `internal/store/library.go` (`LibraryInfo`, `SaveLibraries`, `SetPath`)
- Test: `internal/store/item_test.go`, `internal/store/library_test.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces on `store.Item`: `Thumb, Art, Path string`; `StoppedAt time.Time` (zero: not stopped); `LastOutcome, LastMatchOutcome string`; `LastMatchEventID int64` (0: none).
- Produces: `type LibraryInfo struct{ Section, Title, Type, Agent, Language string }`; `func (s *Store) SaveLibraries(server string, libs []LibraryInfo) error` — upserts the four metadata columns, inserting missing rows with null read times and never changing read times.
- Produces: `func (s *Store) SetPath(server, ratingKey, path string) error` — sets `item.path` of an existing row; a missing row is not an error.
- `ApplyRead`: inserts and present-row updates write `thumb` and `art` from the read; `path` only when the read's value is not empty (`path = CASE WHEN ? = '' THEN path ELSE ? END`). A read never writes `stopped_at` or any `last_*` column.
- Produces: `Read.Folders map[string]string` (rating key -> series folder, staged by a full read's unseen-row check, Task 10). After the rows are written, `ApplyRead` runs `UPDATE item SET path = ? WHERE server = ? AND rating_key = ?` for each non-empty value in the same transaction, so it is saved only when the read commits, and a row the read deleted gets nothing.
- `Due` and `CountIncomplete` add `AND stopped_at IS NULL`.

- [ ] **Step 1: Write the failing tests**

```go
func TestApplyReadStoresImagesAndPath(t *testing.T)
func TestApplyReadKeepsSavedPathWhenReadHasNone(t *testing.T) // SetPath "/tv/Show", read with Path "" -> "/tv/Show"
func TestApplyReadReplacesImages(t *testing.T)
func TestApplyReadKeepsStoppedAt(t *testing.T)               // stopped_at set by SQL survives a re-read
func TestDueAndCountSkipStoppedItems(t *testing.T)            // two due rows, one stopped -> Due 1 row, total 1; CountIncomplete 1
func TestMigratedRowIsStillDue(t *testing.T)                  // openAtVersion(t, 5) row (Task 2) -> Due returns it with Thumb "", StoppedAt zero, LastOutcome ""
func TestSaveLibrariesUpsertsMetadataKeepsReadTimes(t *testing.T) // ApplyRead sets read times; SaveLibraries twice with a new title -> new title, agent, language; read times unchanged
func TestSetPathIgnoresMissingRow(t *testing.T)
func TestApplyReadWritesStagedFolders(t *testing.T)           // row kept by the read + Folders entry -> path set; Folders entry for a gone key -> no row, no error
```

- [ ] **Step 2: Run** `go test ./internal/store -v` — Expected: new tests FAIL.
- [ ] **Step 3: Implement.** Add `SaveLibraries` (with one `LibraryInfo`, so it cannot short-circuit) and `SetPath` to `TestMethodsReportDatabaseFailure`.
- [ ] **Step 4: Run** the store package — Expected: PASS, existing tests unchanged.
- [ ] **Step 5: Commit** — `feat(store): keep item images, paths, and library metadata`

---

### Task 5: Requests, gone items, and the timeline read

**Files:**
- Create: `internal/store/history.go`, `internal/store/history_test.go`

**Interfaces:**
- Produces:

```go
type Source string // SourceAuto = "auto", SourceManual = "manual"
type EventKind string // EventRequest = "request", EventMatchDecision = "match-decision", EventCompleted = "completed", EventGone = "gone"

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
	Match             *MatchTarget // match requests, accepted or failed
}

func (s *Store) RecordRequest(r Request) error
func (s *Store) RecordGone(server, ratingKey string, at time.Time, source Source) error

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
	Links                            []string // nil when none
	FixedAction, FixedMatch          Action
	FixedMatchEventID                int64 // 0: none
	Searches                         []MatchSearch // filled by Task 7
}

// MatchSearch and MatchCandidate are declared here because Event uses them.
type MatchCandidate struct {
	GUID, Name           string
	Year                 int
	Type, Summary, Thumb string
	Score                int
	Verdict              string
}

type MatchSearch struct {
	Query      string // for example "tmdb-48213"
	Source     string // "path", "tmdb", or "tie"
	FromID     string // the path ID a tmdb search came from
	Failed     bool
	Candidates []MatchCandidate
}

// Timeline returns the events of one item, ordered at DESC, id DESC.
func (s *Store) Timeline(server, ratingKey string) ([]Event, error)
```

- `RecordRequest`, in one transaction, on an existing row (a missing row writes nothing and is not an error):
  - the attempt update of today's `RecordAttempt` (always `last_attempt`, `attempts + 1`; accepted → `last_refresh`, `refreshes + 1`, `last_action`; accepted match → also `last_match`);
  - `last_outcome`: `string(Action)` if accepted, `string(Action) + "-failed"` if not, `"search-failed"` if `Action == ""`;
  - when `Action != ""`, a `request` event with the snapshot (`section`, `title`, `year`, `type`, `thumb`, `art`, `path` read from the row in the transaction) and the `Match` fields;
  - for an accepted match, `item.last_match_event_id` = the new event's ID.
- `RecordGone`: a `gone` event with the snapshot, then the row delete, in one transaction. A missing row writes nothing.
- `Timeline` decodes `links` with `strings.Split` only when the text is not empty.

- `RecordRequest` returns an error when `Source` is empty, before it writes anything.
- [ ] **Step 1: Write the failing tests** — only the history-specific ones, plus `TestRecordRequestRequiresSource` (empty `Source` -> error, row and Timeline unchanged). The attempt-counting assertions stay in `TestRecordAttempt*` until Task 10 converts those tests to `RecordRequest`, so there is one set of them.

```go
func TestRecordRequestWritesEventAndOutcome(t *testing.T)      // accepted refresh -> one event: request, auto, refresh, accepted, Title from row; LastOutcome "refresh"
func TestRecordRequestFailedMatchKeepsTarget(t *testing.T)     // match, not accepted, Error "boom", Match target -> event has MatchGUID and Error; LastOutcome "match-failed"; LastAction, LastMatch, Refreshes unchanged; attempts +1
func TestRecordRequestAcceptedMatchSetsMatchEventID(t *testing.T) // row LastMatchEventID == Timeline()[0].ID; event Links and PathYear kept
func TestRecordRequestSearchFailedWritesNoEvent(t *testing.T)  // Action "" -> no event; LastOutcome "search-failed"; attempts +1
func TestRecordRequestMissingRowWritesNothing(t *testing.T)
func TestRecordGoneWritesEventAndDeletesRow(t *testing.T)
func TestTimelineOrdersSameSecondByID(t *testing.T)           // two events at the same second -> the later ID first
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'RecordRequest|RecordGone|Timeline' -v` — Expected: FAIL.
- [ ] **Step 3: Implement.** Keep `RecordAttempt` and `Delete` (Task 10 removes them). The `RecordRequest` entry of `TestMethodsReportDatabaseFailure` passes `Source: SourceAuto`, so it reaches the closed database instead of the input check. Add the three new methods to `TestMethodsReportDatabaseFailure`.
- [ ] **Step 4: Run** the store package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(store): record requests and gone items as events`

---

### Task 6: Completion events, and no events in dry runs

**Files:**
- Modify: `internal/store/item.go` (`Read`, `ApplyRead`, `applyOldRows`, `writeExisting`, `deleteGone`)
- Modify: `internal/store/history.go` (`writeCompleted`, `writeGone`)
- Modify: `internal/refresher/read.go` (`readLibrary` sets `rd.DryRun = r.opts.DryRun`)
- Test: `internal/store/history_test.go`, `internal/refresher/history_test.go`

**Interfaces:**
- Produces: `Read.DryRun bool` — when true, `ApplyRead` writes no event; the rows and read times change exactly as today (see the known limit in Global Constraints).
- When not `DryRun`, `ApplyRead` writes in its transaction:
  - one `completed` event (source `auto`, `section = r.Section`) for each row it deletes because its final reasons are 0. Snapshot: title, year, thumb, art, and path from the read when the read lists the key (`readKey.present`) and the value is not empty, else from the row; for path, a non-empty `r.Folders[key]` (Task 4) comes before the row's path, because a full read can find a series folder and complete that series in the same read. Attribution: `fixed_action = last_action`, `fixed_match = last_match`, and `fixed_match_event_id = (SELECT id FROM event WHERE id = ?)` with the row's `last_match_event_id`, so an expired or deleted request (or 0) gives null instead of a foreign-key failure.
  - one `gone` event for each `r.Gone` row that exists, before it is deleted.
- `applyOldRows` collects the completed `(prev, readKey)` pairs; `writeCompleted(tx, r, prev, in)` writes one event.

- [ ] **Step 1: Write the failing tests**

```go
func TestApplyReadWritesCompletedEvent(t *testing.T)            // accepted refresh, then a read completes the row -> completed event, FixedAction refresh, FixedMatchEventID 0
func TestCompletedEventCreditsCurrentOccurrence(t *testing.T)
// occurrence 1: accepted match at second T, completes; occurrence 2 (new row, same key): accepted match at the same second T, completes
// -> the second completed event's FixedMatchEventID is the second request event's ID
func TestCompletedAfterPlainRefreshHasNoMatch(t *testing.T)     // occurrence 2 only refreshed -> FixedMatch "", FixedMatchEventID 0
func TestCompletedEventPrefersReadMetadata(t *testing.T)        // row Title "Old", Thumb ""; read Title "New", Thumb set -> event Title "New", Thumb set; Path from row
func TestMovedRowCompletesInNewSection(t *testing.T)            // as TestApplyReadMovedCompleteRowIsFixed -> event Section is the read's section
func TestLegacyRowCompletesWithoutMatchEvent(t *testing.T)      // openAtVersion(t, 5) row with last_match 'match' completes -> FixedMatch match, FixedMatchEventID 0
func TestCompletedEventUsesStagedFolder(t *testing.T)          // row with path "" completed by a full read whose Folders holds its key -> completed event path == the folder
func TestCompletesAfterMatchEventExpired(t *testing.T)          // accepted match, its request event deleted by SQL, then the row completes -> ApplyRead succeeds, FixedMatch match, FixedMatchEventID 0
func TestApplyReadWritesGoneEvents(t *testing.T)
func TestDryRunReadWritesNoEvents(t *testing.T)                 // Read.DryRun with a completed and a gone row -> rows change as today, Timeline empty
```

In `internal/refresher/history_test.go`:

```go
func TestReadRecordsCompletionOnlyWhenNotDryRun(t *testing.T)
// the same seeded row completed by the same listing, once with DryRun false and once with DryRun true
// -> one completed event in the first store, none in the second
```

- [ ] **Step 2: Run** `go test ./internal/store ./internal/refresher -run 'Completed|Completes|GoneEvents|DryRunRead|RecordsCompletion' -v` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the store package — Expected: PASS, including all existing `ApplyRead` tests.
- [ ] **Step 5: Commit** — `feat(store): record completions with the request that fixed them`

---

### Task 7: Match decisions

**Files:**
- Modify: `internal/store/history.go`, `internal/store/history_test.go`

**Interfaces:**
- Produces:

```go
type DecisionOutcome string // DecisionMatched = "matched", DecisionNoMatch = "no-match", DecisionNoIDs = "no-ids", DecisionFailed = "failed"

type MatchDecision struct {
	Server, RatingKey                string
	At                               time.Time
	Source                           Source
	Outcome                          DecisionOutcome
	Conflict, Error                  string
	PathYear, TMDBYear, EpisodeCount int
	Searches                         []MatchSearch // with verdicts already set
}

func (s *Store) RecordMatchDecision(d MatchDecision) error
```

- One transaction, on an existing row (a missing row writes nothing): a `match-decision` event (snapshot from the row, `outcome`, `conflict`, `error`, the three confirmation values), its searches and candidates with `position` in slice order, and `item.last_match_outcome = Outcome`.
- `Timeline` runs in one read-only transaction (as `Due` does). It reads all event rows and closes them, then reads the item's searches with one join (`match_search JOIN event ON event.id = match_search.event_id WHERE event.server = ? AND event.rating_key = ?`, ordered by `event_id, position`), closes them, then its candidates the same way, and attaches them. No dynamic `IN (...)` list.

- [ ] **Step 1: Write the failing tests**

```go
func TestRecordMatchDecisionRoundTrips(t *testing.T)
// searches: {path "tmdb-1", candidate verdict "picked"}, {tmdb "tmdb-2" FromID "tvdb-9", Failed true}
// -> Timeline()[0]: Kind match-decision, Outcome "matched", Searches equal the input; row LastMatchOutcome "matched"
func TestRecordMatchDecisionNoIDs(t *testing.T)            // no searches -> Outcome "no-ids", Searches nil
func TestTimelineWithSeveralDecisions(t *testing.T)       // two decisions and a request -> each decision has only its own searches (proves no nested-query deadlock and correct grouping)
```

- [ ] **Step 2: Run** — Expected: FAIL. **Step 3: Implement**; add `RecordMatchDecision` to `TestMethodsReportDatabaseFailure`. **Step 4: Run** the store package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(store): record fix match decisions`

---

### Task 8: Runs, retention, and pruning of run rows

**Files:**
- Create: `internal/store/run.go`, `internal/store/run_test.go`
- Modify: `internal/store/item.go` (`PruneServers`)

**Interfaces:**
- Produces:

```go
type RunSource string // RunSourceSchedule = "schedule", RunSourceManual = "manual"
type RunKind string   // RunKindRun = "run"
type RunStatus string // RunRunning = "running", RunFinished = "finished", RunFailed = "failed", RunCanceled = "canceled", RunInterrupted = "interrupted"

type RunStart struct {
	Server    string
	Kind      RunKind
	Source    RunSource
	StartedAt time.Time
}

type RunResult struct {
	FinishedAt                                          time.Time
	Status                                              RunStatus
	Error                                               string
	ReadItems, Refreshed, Matched, Failed               int
	Completed, Fixed, Incomplete, Due, Selected         int
}

type Run struct {
	ID         int64
	Server     string
	Kind       RunKind
	Source     RunSource
	Status     RunStatus
	StartedAt  time.Time
	FinishedAt time.Time // zero while running
	Error      string
	ReadItems, Refreshed, Matched, Failed       int
	Completed, Fixed, Incomplete, Due, Selected int
}

func (s *Store) StartRun(r RunStart) (int64, error)                   // status running
func (s *Store) FinishRun(id int64, r RunResult) error
func (s *Store) InterruptRuns(at time.Time) (int, error)              // every running row -> interrupted, finished_at = at
func (s *Store) PruneHistory(before time.Time) (events, runs int, err error) // event.at < before, run.started_at < before
func (s *Store) Runs(limit int) ([]Run, error)                        // started_at DESC, id DESC; limit <= 0: no limit
```

- `PruneHistory`, in one transaction, first clears `item.last_match_event_id` where it points to an event it deletes, then deletes the old events and runs.
- `PruneServers` also runs, in its transaction, `DELETE FROM run WHERE server NOT IN (...)` (every row when `servers` is empty). `PruneSections` does not touch `run`.

- [ ] **Step 1: Write the failing tests**

```go
func TestStartAndFinishRun(t *testing.T)                    // round trip of every field through Runs
func TestRunsOrderAndLimit(t *testing.T)                    // same started_at -> higher ID first; limit 1; limit 0 returns all
func TestInterruptRunsMarksOnlyRunning(t *testing.T)
func TestPruneHistoryDeletesOldRowsWithChildren(t *testing.T) // an old decision with a search and a candidate, a new event, an old and a new run -> old rows and children gone, counts (1, 1)
func TestCompleteAfterPruneHistory(t *testing.T)            // accepted match, PruneHistory removes its request, the row completes -> LastMatchEventID was cleared, ApplyRead succeeds, FixedMatchEventID 0
func TestPruneServersDeletesRunsOfRemovedServers(t *testing.T) // removed server with run rows and no library row -> its runs gone; kept server's runs stay; PruneServers(nil) deletes every run
```

- [ ] **Step 2: Run** — Expected: FAIL. **Step 3: Implement**; add the five methods to `TestMethodsReportDatabaseFailure`. **Step 4: Run** the store package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(store): record runs and prune old history`

---

### Task 9: Dry runs never prune

**Files:**
- Modify: `internal/refresher/refresher.go` (`Run`), `internal/refresher/read.go` (`pruneSections`, `readLibrary`)
- Test: `internal/refresher/prune_test.go`

**Interfaces:**
- In a dry run, `Run` does not call `PruneServers`, and `pruneSections` does not call `PruneSections`. `pruneSections` logs `Dry run keeps cache of removed libraries` at info level with field `server`, once per server and run, instead. No store method is added to list what would be pruned.
- [ ] **Step 1: Set `f.r.opts.DryRun = false` in all four existing tests of `internal/refresher/prune_test.go`** (also `TestRunCanceledBeforeStartDoesNotPrune`, which would otherwise pass vacuously), and add:

```go
func TestDryRunPrunesNothing(t *testing.T)
// seed a library of a removed server and an unselected library with history events;
// dry Run -> both libraries, their items, and their events remain; one "Dry run keeps cache of removed libraries" line per server
```

- [ ] **Step 2: Run** `go test ./internal/refresher -run 'Prune|DryRun' -v` — Expected: new tests FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `mise run test` — Expected: PASS.
- [ ] **Step 5: Commit** — `fix(refresher): keep the cache and history in dry runs`

---

### Task 10: Refresher records requests, gone items, images, paths, and libraries

**Files:**
- Modify: `internal/refresher/refresher.go` (`Store` interface: add `RecordRequest`, `RecordGone`, `SaveLibraries`, `SetPath`; remove `RecordAttempt`, `Delete`)
- Modify: `internal/refresher/refresh.go` (`refreshItem`, `itemFailed`, `countFailure`; `itemPaths` becomes a `*Refresher` method)
- Modify: `internal/refresher/read.go` (`readLibraries`, `storeItem`, `readLibrary`, `goneKeys`)
- Modify: `internal/store/item.go` (delete `RecordAttempt` and `Delete`)
- Port every remaining caller. The list below is a start, not the check: before deleting, `grep -rn 'RecordAttempt\|\.Delete(' internal cmd` must show only the definitions. Known callers: `internal/store/item_test.go` (`refresh` helper; every `TestDue*` test that calls `RecordAttempt`; `TestRecordAttempt*`, converted to `RecordRequest` as the one set of attempt-counting tests; `TestDelete`, converted to `RecordGone`; `TestApplyReadReturnsActionsOfFixedRows`; and the `Due` doc comment), `internal/store/store_test.go` (`TestMethodsReportDatabaseFailure` entries), `internal/store/tmdb_test.go` (`TestTMDBIDGoesWithItem`: use `RecordGone`), `internal/refresher/refresh_test.go` (`fixture.seed`), `internal/refresher/read_test.go` (`f.refreshed`, `TestCompleteItemLogsFixed`). Keep their assertions; timeline assertions in new tests account for events the seed writes.
- Test: `internal/refresher/history_test.go` (also `TestFullReadSavesFolderOfUnseenSeries`: an unseen series row answers with a Location -> path saved; the same with a later read failure -> path unchanged)

**Interfaces:**
- `refreshItem` → on success `RecordRequest{Action: action, Accepted: true, Source: store.SourceAuto, Match: target(fm)}`. `target(fm *fixMatch) *store.MatchTarget` copies `cand.GUID`, `cand.Name`, `cand.Year`, `links`, `pathYear`, `tmdbYear`, `episodeCount`; nil when `fm` is nil.
- `itemFailed(ctx, run, it, action, fm, err)`: a 404 calls `RecordGone` (not in dry run); other failures call `countFailure(ctx, run, it, action, fm, err)`, which records `RecordRequest{Action: action, Accepted: false, Source: store.SourceAuto, Error: err.Error(), Match: target(fm)}`. Every refresher call of `RecordRequest` sets `Source: store.SourceAuto`; `RecordRequest` returns an error for an empty `Source` (Task 5), so a missed one fails a test instead of storing an empty source. The attempt counting, stop rules, and log lines stay as today.
- `storeItem` sets `Thumb: plex.ItemImagePath(it.RatingKey, it.Thumb)`, `Art: plex.ItemImagePath(it.RatingKey, it.Art)`, and for movies `Path: folderOf(it.Files)`. `folderOf(files []string) string` returns the first file up to its last `/` or `\`, or `""`.
- `(r *Refresher) itemPaths(ctx, run, it)` keeps returning `[]string` (`p.All()`). After a successful `ItemPaths` of a series (`it.Type == plex.ShowLibrary`), when `len(p.Locations) > 0 && p.Locations[0] != ""`, it calls `SetPath` with `p.Locations[0]` (never a file from `p.Files`; dry runs too, because the path is cache metadata like `SaveLibraries`); a `SetPath` error returns wrapped with `errCache` so `itemFailed` treats it as a store error.
- `readLibraries` calls `SaveLibraries` with the selected libraries after `selectLibraries` (dry runs too: it is cache metadata, not history).
- `goneKeys(...) (gone []string, folders map[string]string, err error)`: for an unseen row that `ItemPaths` answers, it stages `p.Locations[0]` when not empty and `lib.Type == plex.ShowLibrary` (the same guard as `itemPaths`; the fake returns every path as a location); `readLibrary` puts it in `rd.Folders`, so `ApplyRead` saves it with the read (Task 4) and a failed read saves nothing.

- [ ] **Step 1: Write the failing tests**

```go
func TestRefreshRecordsAcceptedRequestEvent(t *testing.T)
func TestFailedRefreshRecordsFailedEvent(t *testing.T)    // LastOutcome "refresh-failed", event Error set
func TestFailedMatchRecordsItsTarget(t *testing.T)        // matchErrs -> event Action match, Accepted false, MatchGUID set; LastMatch unchanged
func TestNotFoundRecordsGoneEvent(t *testing.T)
func TestMatchRecordsTarget(t *testing.T)                 // event MatchGUID, Links; row LastMatchEventID set
func TestDryRunRecordsNoRequestEvents(t *testing.T)
func TestReadStoresImagesAndMovieFolder(t *testing.T)
func TestReadDropsForeignImagePaths(t *testing.T)        // Thumb "https://x/y.jpg" -> ""
func TestSeriesPathSavedAfterItemPaths(t *testing.T)
func TestEmptyItemPathsSavesNothing(t *testing.T)
func TestReadSavesLibraryMetadata(t *testing.T)
```

- [ ] **Step 2: Run** `go test ./internal/refresher -v` — Expected: new tests FAIL.
- [ ] **Step 3: Implement**, then delete `RecordAttempt` and `Delete` and port their callers.
- [ ] **Step 4: Run** `mise run test && mise run lint` — Expected: PASS, 0 issues; existing log-field tests unchanged.
- [ ] **Step 5: Commit** — `feat(refresher): record requests, images, paths, and libraries`

---

### Task 11: Refresher records every fix match decision

**Files:**
- Create: `internal/refresher/decision.go`, `internal/refresher/decision_test.go`
- Modify: `internal/refresher/refresh.go` (`findMatch`, `resolveMatch`, `searchIDs`, `recordTMDBFailure`), `internal/refresher/tmdb.go` (`tmdbResults`, `confirmWithTMDB`, `confirmationLookupFailed`, `tieTMDBCandidate`), `internal/refresher/episodes.go` (failure path), `internal/refresher/refresher.go` (`Store` gains `RecordMatchDecision`)

**Interfaces:**
- Produces (package-private):

```go
// decisionLog collects one fix match evaluation as it happens.
type decisionLog struct {
	searches []store.MatchSearch
	err      error // a failure that the code handled (a swallowed TMDB or confirmation failure)
}

func (d *decisionLog) add(id pathID, source, fromID string, cands []plex.Candidate, failed bool) // candidates copied with Verdict ""
func (d *decisionLog) fail(err error)                                                            // keeps the first error
```

- `findMatch` creates the `decisionLog` right after its guard (unmatched, not ignored, refreshed before), before `itemPaths`, and passes it down: `searchIDs` (source `path`), `tmdbResults` (source `tmdb`, `fromID` = the path ID's `title()`), and `tieTMDBCandidate` (source `tie`). Every `SearchMatches` call adds its result before any return, with `failed: true` for any error (also one that `searchFailureIsEmpty` accepts). A failure that `searchFailureIsEmpty` accepts does not set `d.err` and counts as an empty search, exactly as the match code treats it today: the outcome follows the other searches and the TMDB fallback (which this empty result can trigger). The search keeps `Failed` in every outcome, so the UI can tell it apart from an empty answer. First-pass searches stay in the log when the TMDB pass replaces `results`.
- `searchResult` gains `pos int`, the index of its search in the `decisionLog`, so `resolveMatch` sets verdicts on the logged copy of exactly the results it evaluated. A `tie` search is not in `mc.results`; `resolveMatch` also sets the verdicts of the `tie` searches in the `decisionLog` (rule 3 below).
- Handled failures: `recordTMDBFailure` and `confirmationLookupFailed` call `d.fail(err)` and keep returning exactly what they return today, so the item still gets its plain refresh. In `confirmEpisodeNumbers` this covers a TMDB `SeasonEpisodes` error (through `confirmationLookupFailed`: plain refresh as today).
- A Plex `EpisodeNumbers` error is not handled: it returns as today, so no request is sent and `countFailure` records the failed attempt (`Match search failed`). The decision is `failed` through rule 2 below. `TestEpisodeEvidenceLookupFailures` keeps its three cases unchanged and gains the decision assertions: Plex error -> `failed` and no request; TMDB error -> `failed` and the `Refresh 201` request; cancel -> no decision.
- `findMatch` records one decision on its way out, with `Source: store.SourceAuto`, never in a dry run. The classification, in this order:
  1. skip (record nothing) when the returned error is `errCache`, when `ctx.Err() != nil`, or when it is `plex.ErrNotFound` (a `gone` event follows);
  2. a returned error (for example a non-404 `ItemPaths` failure) or `d.err != nil` → `failed` with `Error` and the searches so far, all verdicts empty;
  3. `len(ids) == 0` after a successful path lookup and parse → `no-ids`, returned before `useTMDB`;
  4. a match → `matched`; otherwise → `no-match` with the conflict (`""` when no search found a candidate).
- `yearResolution` and `fixMatch` do not change, so the `Sent request` line keeps its fields. The decision takes `PathYear` (non-zero only when the path year confirmed the match), `TMDBYear` (when TMDB answered, for both `matched` and `no-match`), and `EpisodeCount` from it.
- Verdicts are set in `resolveMatch` only when `d.err == nil`, per logged candidate, by the first rule that applies:
  1. its search has more than one candidate → `several candidates` (any source, also `tie`);
  2. its GUID is empty → `candidate without guid` (any source);
  3. a `tie` search → `confirms` if its GUID equals the evaluated GUID, else `different candidate`. The evaluated GUID is the picked candidate's, or, on `year differs`, the one `chooseCandidate(mc.results)` returns (`chooseMatch` returns an empty candidate then);
  4. other searches, `matched` → `picked` if its GUID equals the picked GUID, else `different candidate`;
  5. other searches, `no-match`: `different candidates` → `different candidates`; `year differs` → `year differs`; `several candidates` or `candidate without guid` caused by another search → `not compared`.
- A `RecordMatchDecision` error returns wrapped with `errCache`.

- [ ] **Step 1: Write the failing tests** (reuse the scenarios in `refresh_test.go` and `tmdb_test.go`; assert on `Timeline()`)

```go
func TestDecisionMatched(t *testing.T)                     // one path search, one candidate -> matched, verdict picked
func TestDecisionNoIDs(t *testing.T)                       // no ID in the paths -> no-ids, no searches, and no TMDB call
func TestDecisionSeveralCandidates(t *testing.T)
func TestDecisionCandidateWithoutGUID(t *testing.T)
func TestDecisionDifferentCandidates(t *testing.T)
func TestDecisionYearDiffers(t *testing.T)                 // unconfirmed -> no-match, verdict year differs, TMDBYear kept when TMDB answered
func TestDecisionConfirmedByPathYear(t *testing.T)         // matched, PathYear set
func TestDecisionConfirmedByTMDBYear(t *testing.T)         // matched, TMDBYear set, tie search verdict confirms
func TestDecisionConfirmedByEpisodes(t *testing.T)         // matched, EpisodeCount set
func TestDecisionTieWithDifferentGUID(t *testing.T)        // tie search verdict different candidate, outcome no-match
func TestDecisionKeepsFirstPassWithTMDB(t *testing.T)      // searches: path (empty), then tmdb with FromID
func TestSwallowedTMDBFailureRecordsFailedAndRefreshes(t *testing.T) // path search ok, TMDB lookup fails -> decision failed with the path search; the plain refresh is still sent
func TestAcceptedEmptySearchFailureIsMarkedFailed(t *testing.T) // tvdb search error accepted as empty -> Failed true
func TestCanceledOrCacheErrorRecordsNoDecision(t *testing.T)
func TestPathLookupErrorRecordsFailed(t *testing.T)       // non-404 ItemPaths error -> failed, not no-ids, no searches
func TestAcceptedSearchFailureOutcome(t *testing.T)        // TMDB off: tvdb search answers 500 -> no-match, conflict "", that search Failed; with a second (imdb) search that matches -> matched, first search still Failed
func TestAcceptedSearchFailureThenTMDBMatch(t *testing.T)  // TMDB on: tvdb search answers 500, the TMDB pass matches -> matched, the tvdb search still Failed, the tmdb search picked
func TestMixedResultsVerdicts(t *testing.T)               // one search with two candidates, another with one valid -> "several candidates" x2 and "not compared"
func TestTMDBFallbackKeepsPositions(t *testing.T)         // verdicts land on the tmdb searches, the empty first-pass search keeps none
```

- [ ] **Step 2: Run** `go test ./internal/refresher -run 'Decision|Swallowed|AcceptedEmpty|CacheError' -v` — Expected: FAIL.
- [ ] **Step 3: Implement.** The log lines `Match conflict`, `Match search result`, `TMDB lookup failed`, and `Sent request` keep their fields.
- [ ] **Step 4: Run** `mise run test && mise run lint` — Expected: PASS, 0 issues.
- [ ] **Step 5: Commit** — `feat(refresher): record every fix match decision`

---

### Task 12: Run rows, retention, and interrupted runs

**Files:**
- Modify: `internal/refresher/refresher.go` (`Options.HistoryRetention`, `Store` gains `StartRun`, `FinishRun`, `PruneHistory`; `Run`, `runServer`, `serverRun.selected`)
- Modify: `internal/refresher/refresh.go` (`refreshDue` sets `run.selected`; `logSummary` returns the incomplete count)
- Modify: `cmd/autorefresh/main.go` (pass `HistoryRetention`; new `interruptRuns`)
- Test: `internal/refresher/run_test.go`, `cmd/autorefresh/main_test.go`

**Interfaces:**
- `Options.HistoryRetention time.Duration` (0: no retention).
- `Run`, not in a dry run: after `PruneServers`, when `HistoryRetention > 0`, `PruneHistory(now - HistoryRetention)`; a store error returns like the prune error does. When either count is above 0, log `Pruned history` at debug level with fields `events` and `runs`.
- `runServer`, not in a dry run:
  - `StartRun{Server, Kind: store.RunKindRun, Source: store.RunSourceSchedule, StartedAt: run.began}` first; an error returns at once as `server <name>: <err>`.
  - `FinishRun` on every later return, with `ReadItems = fullReadItems + incrementalReadItems`, `Refreshed = refreshes`, `Matched = matches`, `Failed = failures`, `Completed = len(completed)`, `Fixed = fixed`, `Incomplete` (from the summary; 0 without one), `Due = due`, `Selected = selected`.
  - Status: `canceled` under the existing condition (`ctx.Err() != nil && (err == nil || errors.Is(err, ctxErr))`); otherwise `failed` with `Error = err.Error()` when `err != nil`; otherwise `finished`.
  - A `FinishRun` error is returned only when there is no earlier error; the canceled path still returns nil as today.
- `cmd/autorefresh`: `func interruptRuns(st *store.Store, opts options, now time.Time) error` returns nil at once when `opts.Once || opts.DryRun`; otherwise it calls `InterruptRuns` and logs `Marked interrupted runs` at info level with field `runs` when above 0. `run` calls it right after the store opens with one unnested `if`; on error it closes the store and returns `fatal(err, "Failed marking interrupted runs")`, because `main` exits without logging and `run` logs each error it returns.
- Keep `Refresher.Run` and `runServer` under the complexity limit with named helpers: `pruneBeforeRun(now)` (server prune and retention) and `finishRun(ctx, run, id, err) error` (status, counters, and the error precedence above).

- [ ] **Step 1: Write the failing tests**

```go
func TestRunWritesFinishedRunRow(t *testing.T)            // Runs(0)[0]: source schedule, status finished, Refreshed 1, Selected 1, Due 1
func TestCompletionWithoutRequestCountsCompletedOnly(t *testing.T) // Completed 1, Fixed 0
func TestUnavailableServerWritesFailedRunRow(t *testing.T) // Error contains "unavailable"
func TestCanceledRunWritesCanceledRow(t *testing.T)       // cancel after the run starts
func TestRunPrunesOldHistory(t *testing.T)
func TestDryRunWritesNoRunRowAndKeepsHistory(t *testing.T)
func TestScheduledStartMarksInterruptedRuns(t *testing.T) // running row of server "test" -> interrupted in scheduled mode; unchanged with --once and with --dry-run
```

- [ ] **Step 2: Run** — Expected: FAIL. **Step 3: Implement.** **Step 4: Run** `mise run test && mise run lint` — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(refresher): record runs and prune old history`

---

### Task 13: Documentation

**Files:**
- Modify: `README.md`: a short "History" section under "How it works" (what is recorded, `history-retention`, cascades with a removed library); the "Dry run" section and the `--dry-run` flag row (a dry run records no history and prunes nothing, and logs `Dry run keeps cache of removed libraries`; an item that completes or disappears while only a dry run reads it gets no history entry); the "Cache cleanup" section (a removed library also loses its history).
- Modify: `AGENTS.md`: the `internal/store/` and `internal/refresher/` rows of the path table.

- [ ] **Step 1: Write the sections** in the README's existing style.
- [ ] **Step 2: Run** `mise run vendor && mise run test && mise run lint` — Expected: PASS; `go.mod`/`go.sum` unchanged.
- [ ] **Step 3: Commit** — `docs(readme): describe the recorded history`

---

## Delivery

After Task 13: push `feat/94-maintenance-dashboard`, open a PR into `main` titled `feat(store): record dashboard history, decisions, and runs`, with the problem, the change, the checks run, `feat/94-maintenance-dashboard → main`, and `Refs #94` (not `Closes`: the issue stays open for stages 2–5). Follow `AGENTS.md` for CodeRabbit and auto-merge.
