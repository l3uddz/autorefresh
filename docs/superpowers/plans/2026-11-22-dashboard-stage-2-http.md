# Dashboard Stage 2: HTTP Server and Read API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve an optional, authenticated HTTP listener from the autorefresh process with a placeholder page, live status over SSE, the read-only JSON API, and the poster proxy. No write action and no frontend yet.

**Architecture:** A new `internal/web` package holds the HTTP server, the middleware (security headers, cross-origin protection, Basic auth), an in-memory `Status` that the refresher feeds through a new progress hook, and the read handlers. The handlers read the SQLite store through new list queries and call Plex through three new read-only client methods. `cmd/autorefresh` binds the listener before the token checks and stops it before the scheduler and the store.

**Tech Stack:** Go (version from `go.mod`; Go 1.25+ `http.CrossOriginProtection`, Go 1.24+ `omitzero`), `net/http`, `embed`, `database/sql` with the existing SQLite driver (one connection), zerolog.

**Spec:** `docs/superpowers/specs/2026-11-22-maintenance-dashboard-design.md` — sections "Process and configuration", "Live status", "Images", "HTTP API" (with "Item list behaviour" and "Queue state and the item list"), "Item detail data", "Items without a cache row", "Manual action → Open in Plex" (machine identifier only), and "Delivery order" stage 2. Read them before each task.

**Branch:** `feat/94-dashboard-http-server` (parent: `main`). Issue: #94. The branch was created from `origin/main` and tracks it, so push with `git push -u origin feat/94-dashboard-http-server`.

## Global Constraints

- Tests need no network, no Plex, and no TMDB token. Web tests use a real store (`store.Open` in `t.TempDir()`) and a fake `web.Plex`. Plex client tests use `httptest` and recorded responses in `internal/plex/testdata/`.
- Each production function has a cognitive complexity of 15 or less (`mise run lint`). Split into named steps when needed.
- Do not change any existing log line or field. New log lines are listed in their task.
- **One SQLite connection is now shared with HTTP handlers.** No handler holds a transaction or an open `*sql.Rows` across a Plex request or a response write: read, close, then call Plex or write. Never run a query while another query's rows are open. The SSE handler never touches the store. Progress hook calls never block on a subscriber.
- Each task that adds a public store method adds it to `TestMethodsReportDatabaseFailure` (`internal/store/store_test.go`).
- Build URLs with `net/url`. Every URL the server emits (image URLs in JSON, links in the placeholder page) is relative with no leading slash (`api/items/thumb?...`), so the UI works behind a proxy at any sub-path.
- The Plex token never reaches the browser. No Plex header, body, or error text is passed to a response. Handlers answer Plex failures with fixed messages.
- A server is identified by `srv`, its 0-based position in the configured list, as a query parameter. No server name is ever a path segment.
- JSON keys are camelCase. Times are RFC 3339 (`time.Time`); a zero time is left out with `omitzero`. Every error response is `{"error": "<fixed message>"}` with `Content-Type: application/json`. A store error is logged (`log.Error().Err(err).Str("path", r.URL.Path).Msg("API request failed")`) and answered `500 {"error":"internal error"}`.
- Accepted limit: the Fixed tab and "recently fixed" count a completion as fixed when its `fixed_action` is not empty. Rows from before migration 5 that had refreshes but no recorded action complete with an empty `fixed_action`, so they show only with the `all=1` filter. The run summary's `completed_after_request` (which uses `Refreshes > 0`) can count them; the difference ends once those legacy rows are gone.
- Before each commit: `mise run vendor && mise run test && mise run lint` pass. Commit subjects use Conventional Commits with a scope (`config`, `store`, `plex`, `refresher`, `web`, `cli`, `docker`, `readme`, `agents`), at most 72 characters, and the body ends with `Refs #94` and the `Co-Authored-By` line.

### Deferred to later stages (do not add here; an unused helper fails `U1000`)

- Stage 3: the `Content-Type: application/json` check and `MaxBytesReader` for POST bodies (stage 2 has no POST route), item locks, manual runs and their `run.source = manual`, Cancel, Check now, job results on the SSE stream.
- Stage 4: the `webui` build tag, the generated `dist/`, pre-compressed `.gz` assets, the packaging test, Node tooling.

## Review Focus

1. **A stored image path that is not this item's thumb or art (another item's path, a remote URL, or junk).** The proxy answers 404 and sends no Plex request. Test in Task 10.
2. **Plex answers an image request with HTML, an unknown type, or more than 10 MB.** The answer is 502 with our JSON error, and no Plex byte or header reaches the browser. Tests in Tasks 4 and 10.
3. **A forged, truncated, or foreign cursor, and a `srv` that is negative, out of range, or not a number.** The answer is 400, never 500. Tests in Tasks 8 and 9.
4. **A browser tab that keeps an SSE stream open, or never reads it, during a busy run and at shutdown.** Progress calls never block, and `Stop` returns before its deadline. Tests in Task 7.
5. **An item that exists only in history (it completed, so its row is gone).** Detail, timeline, and poster work from the newest event snapshot, and the live view creates no row. Tests in Tasks 3, 9, and 10.

---

### Task 1: `ui` config

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.UI UI` (yaml `ui`), `type UI struct { Listen, Username, Password string }` (yaml `listen`, `username`, `password`). An absent section is the zero `UI`: no listener.

- [ ] **Step 1: Write the failing tests**

```go
func TestDecodeWithoutUIHasNoListener(t *testing.T) // minimal config -> cfg.UI == config.UI{}
func TestDecodeUI(t *testing.T) // ui: {listen: ":7380", username: admin, password: "p:w"} -> the three values
func TestDecodeRejectsInvalidUI(t *testing.T)
// table, each error contains the message and never the password value "s3cret-value":
//   listen "7380"            -> "ui.listen must be host:port, such as :7380"
//   listen "host:http"       -> same
//   listen ":70000"          -> same
//   username only            -> "ui.username and ui.password must both be set or both be empty"
//   password "s3cret-value" only -> same
//   username "a:b"           -> "ui.username must not contain ':'"
//   username "a\x01"         -> "ui.username must not contain control characters"
//   password "s3cret-value\x7f" (with a username) -> "ui.password must not contain control characters"
//   ui: {port: 1}            -> error (unknown key)
```

- [ ] **Step 2: Run** `go test ./internal/config -run UI -v` — Expected: build failure (`UI` undefined).
- [ ] **Step 3: Implement** the types and `func (u UI) validate() error`, called from `Config.validate` before `validateServers`. Listen: `net.SplitHostPort`, then the port must parse with `strconv.ParseUint(port, 10, 16)`. Use the existing `hasControl`.
- [ ] **Step 4: Run** the config package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(config): add the ui section`

---

### Task 2: Queue state shared with `Due`, and the Attention and Stopped lists

**Files:**
- Create: `internal/store/list.go`, `internal/store/list_test.go`
- Modify: `internal/store/item.go` (`Due` uses the shared predicates), `internal/store/store_test.go`

**Interfaces:**
- Produces (Task 3 extends `ListItems` to `TabFixed`; Tasks 8–10 consume):

```go
type DueRules struct {
	Now                                             time.Time
	MinimumAge, RefreshInterval, MaxRefreshInterval time.Duration
	Ignored                                         Reasons
}
type QueueState string // "due", "waiting", "too-new", "ignored", "stopped": StateDue, StateWaiting, StateTooNew, StateIgnored, StateStopped
type Tab string        // "attention", "fixed", "stopped": TabAttention, TabFixed, TabStopped
type SortKey string    // "title", "year", "added", "due", "tries", "last-attempt", "stopped", "completed": SortTitle ... SortCompleted
type Cursor struct {
	Num               int64  // the sort value of a numeric key
	Text              string // the sort value of SortTitle
	Server, RatingKey string
}
type ListQuery struct {
	Tab                   Tab
	Servers               []string // the servers to include; empty: every server
	Section, Type, Search string   // "": any
	Reason                Reasons  // 0: any; not used by TabFixed
	WithIgnored           bool     // TabAttention: also rows whose reasons are all ignored
	WithUnrequested       bool     // TabFixed: also completions without an accepted request
	Sort                  SortKey  // "": the tab default
	Desc                  bool     // used only with a Sort
	After                 *Cursor
	Limit                 int      // must be > 0
	Rules                 DueRules
}
type ListItem struct {
	Server, Section, RatingKey, Library string // Library: the cached title, "" if unknown
	Type, Title                         string
	Year                                int
	Thumb, Art, Path                    string
	Cached                              bool // an item row exists
	Reasons                             Reasons
	State                               QueueState
	NextDue                             time.Time // zero unless State is waiting or too-new
	AddedAt, LastAttempt, StoppedAt     time.Time
	Attempts, Refreshes                 int
	LastAction                          Action
	LastOutcome, LastMatchOutcome       string
	CompletedAt                         time.Time // TabFixed
	FixedAction, FixedMatch             Action    // TabFixed
}
var ErrInvalidQuery = errors.New("invalid list query")
func (s *Store) ListItems(q ListQuery) (items []ListItem, next *Cursor, err error)
```

Rules:

- **One definition of the queue state.** `DueRules` produces three SQL expressions with their arguments, and both `Due` and `ListItems` use them. Do not reimplement them in Go:
  - too new: `added_at > ?` with `Now.Add(-MinimumAge).Unix()`
  - waiting: `last_attempt IS NOT NULL AND last_attempt > (? - MIN(?, ? << MIN(MAX(refreshes - 1, 0), ?))) / 1000` with `Now.UnixMilli()` and `backoff`'s `maxWait, interval, doublings`
  - next due, in Unix ms: `MAX(added_at * 1000 + ?, COALESCE(last_attempt * 1000 + MIN(?, ? << MIN(MAX(refreshes - 1, 0), ?)), 0))` with `MinimumAge.Milliseconds()` and the same backoff values
- `Due` keeps its signature, order, and results. Its WHERE becomes `... AND stopped_at IS NULL AND reasons & ~? != 0 AND NOT (<too new>) AND NOT (<waiting>)`. Add an unexported `func (q DueQuery) rules() DueRules`.
- State: `CASE WHEN stopped_at IS NOT NULL THEN 'stopped' WHEN reasons & ~? = 0 THEN 'ignored' WHEN <too new> THEN 'too-new' WHEN <waiting> THEN 'waiting' ELSE 'due' END`.
- Attention: `stopped_at IS NULL`, plus `reasons & ~ignored != 0` unless `WithIgnored`. Stopped: `stopped_at IS NOT NULL`. Filters: `server IN (...)`, `section = ?`, `type = ?`, `reasons & ? != 0`, and Search as `(title LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\')` with `%`, `_`, and `\` escaped in the term. Library title from `LEFT JOIN library`.
- Sort expressions (all non-null): title `item.title COLLATE NOCASE` (Cursor.Text), year, added `added_at`, due (next due ms), tries `attempts`, last-attempt `COALESCE(last_attempt, 0)`, stopped `COALESCE(stopped_at, 0)`. Defaults: Attention `due` ascending, Stopped `stopped` descending. `completed` and any unknown key on these tabs, `Limit <= 0`, or an unknown tab give an error wrapping `ErrInvalidQuery`. Until Task 3, `TabFixed` gives `ErrInvalidQuery` too.
- Keyset paging: `ORDER BY <sort> <dir>, item.server <dir>, item.rating_key <dir>` and, with `After`, `(<sort>, item.server, item.rating_key) > (?, ?, ?)` (`<` when descending). Read `Limit+1` rows, return `Limit`, and set `next` from the last returned row only when more exist.
- `NextDue` is set only for `waiting` and `too-new` rows (`time.UnixMilli`).

- [ ] **Step 1: Write the failing tests** (set `stopped_at` with SQL; stage 3 adds the API)

```go
func TestDueUsesSharedQueueState(t *testing.T)
// The agreement gate. Rows: never tried and old; added exactly at the cutoff second (due);
// added one second after it (too-new); last attempt exactly at the end of its wait (due);
// one second inside it (waiting); refreshes 3 inside a doubled wait (waiting); stopped;
// all reasons ignored. Same rules for both calls. The keys with State == StateDue from
// ListItems(TabAttention, WithIgnored: true, Limit: 100) equal the keys of Due (as sets);
// the stopped row is not listed; the ignored row has StateIgnored.
func TestListNextDue(t *testing.T)
// waiting row: NextDue == last attempt + min(max, interval<<2) for refreshes 3;
// too-new row: NextDue == AddedAt + MinimumAge; due row: NextDue.IsZero().
func TestListAttentionAndStoppedTabs(t *testing.T)
// stopped rows only in TabStopped, ordered stopped_at DESC by default; ignored rows only with WithIgnored.
func TestListFilters(t *testing.T)
// Servers, Section, Type, Reason, Search on title, Search on path; Search "100%" matches
// "100% Pure" and not "1000 Pure"; Search "a_b" matches "a_b" and not "axb"; library title joined.
func TestListPagesWithoutGapsOrRepeats(t *testing.T)
// 7 rows with duplicate titles and years; Limit 3; for title asc, title desc, due asc:
// the pages joined equal one Limit-100 call, and the last page has next == nil.
func TestListRejectsInvalidQuery(t *testing.T)
// Sort "completed" on TabAttention, Sort "bogus", Limit 0, Tab "x", TabFixed (until Task 3)
// -> errors.Is(err, ErrInvalidQuery).
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'Due|List' -v` — Expected: build failure.
- [ ] **Step 3: Implement** `list.go` and the `Due` change.
- [ ] **Step 4: Run** the store package — Expected: PASS, with every existing `Due` test unchanged.
- [ ] **Step 5:** Add `ListItems` to `TestMethodsReportDatabaseFailure`.
- [ ] **Step 6: Commit** — `feat(store): list queued and stopped items`

---

### Task 3: Fixed list, counts, item detail, libraries, and run pages

**Files:**
- Modify: `internal/store/list.go`, `internal/store/library.go`, `internal/store/run.go`, their tests, `internal/store/store_test.go`, `internal/refresher/run_test.go` (call sites of `Runs`)

**Interfaces:**
- Consumes: Task 2's `ListQuery`, `ListItem`, `Cursor`, `DueRules`.
- Produces:

```go
type ListCounts struct{ Attention, Fixed, Stopped int }
// CountItems counts each tab under the filters of q (Servers, Section, Type, Search;
// Reason for the queue tabs; WithIgnored and WithUnrequested for their tab). It ignores
// Tab, Sort, Desc, After, and Limit, and reads in one read-only transaction.
func (s *Store) CountItems(q ListQuery) (ListCounts, error)
// ItemDetail returns the item row as ListItems shows it (Cached true), or else the
// snapshot of the newest event of the item (Cached false; no State). found is false
// when neither exists.
func (s *Store) ItemDetail(server, ratingKey string, rules DueRules) (item ListItem, found bool, err error)
type LibraryEntry struct {
	LibraryInfo // Section, Title, Type, Agent, Language
	Library     // LastFullRead, LastRead
}
func (s *Store) Libraries(server string) ([]LibraryEntry, error) // ordered by title, then section
type RunCursor struct {
	StartedAt time.Time
	ID        int64
}
type RunQuery struct {
	Servers []string   // empty: every server
	Before  *RunCursor // only runs older than this, in the order below
	Limit   int        // <= 0: all
}
func (s *Store) Runs(q RunQuery) ([]Run, error) // replaces Runs(limit int); started_at DESC, id DESC
```

Rules:

- `TabFixed` rows come from `event`: for each `(server, rating_key)`, only its newest `completed` event (`id = (SELECT MAX(id) ...)`), then `fixed_action != ''` unless `WithUnrequested`. Fields: the snapshot columns, `CompletedAt` (`at`), `FixedAction`, `FixedMatch`, the joined library title, and `Cached` (`EXISTS` an item row). Sorts: `title`, `year`, `completed` (default, descending). Search uses the event's title and path. `Reason` is not used.
- Keyset paging works as in Task 2, with `event.server, event.rating_key` as the tiebreakers.

- [ ] **Step 1: Write the failing tests**

```go
func TestListFixedShowsNewestCompletionOnce(t *testing.T)
// an item fixed after an accepted request, back in the cache, and fixed again ->
// one row with the newer CompletedAt and FixedAction; an item that completed without
// a request is listed only with WithUnrequested; Cached is true while the item is cached again.
func TestCountItemsMatchesLists(t *testing.T)
// with a filter, each count equals len(ListItems) of that tab with Limit 100.
func TestItemDetailFromRowOrHistory(t *testing.T)
// cached row -> Cached true, State set; after its completion -> Cached false with the
// completed event's title, year, thumb, art, path, and section; unknown key -> found false.
func TestLibrariesListsMetadataAndReadTimes(t *testing.T)
func TestRunsFiltersAndPages(t *testing.T)
// three runs of two servers; Servers filter; Before the second run returns only the
// third; Limit 1 returns the newest.
```

- [ ] **Step 2: Run** `go test ./internal/store -run 'Fixed|Count|ItemDetail|Libraries|Runs' -v` — Expected: FAIL.
- [ ] **Step 3: Implement**, and change each existing `Runs(n)` call to `Runs(store.RunQuery{Limit: n})`.
- [ ] **Step 4: Run** `go test ./internal/store ./internal/refresher` — Expected: PASS.
- [ ] **Step 5:** Add `CountItems`, `ItemDetail`, and `Libraries` to `TestMethodsReportDatabaseFailure`, and update its `Runs` call.
- [ ] **Step 6: Commit** — `feat(store): list fixed items, item details, and run pages`

---

### Task 4: Plex identity, item details, and photos

Verified on 2026-11-22 with read-only requests to a real server: `GET /` has `MediaContainer.machineIdentifier` (40 letters and digits); a show's metadata has integer `childCount` and `leafCount` and a `Location` list; `GET /photo/:/transcode?url=<relative item image path>&width=240&height=360&minSize=1&upscale=1` with the token header answers `200 image/jpeg`, and an empty `url` answers `400 text/html`. Keep the fixtures in that shape, with neutral values.

**Files:**
- Modify: `internal/plex/client.go` (extract `do`), `internal/plex/library.go`, `internal/plex/image.go`, their tests
- Create: `internal/plex/testdata/server_root.json`
- Modify: `internal/plex/testdata/item_show.json` (add `"childCount": 2, "leafCount": 12`)

**Interfaces:**
- Produces:

```go
func ValidRatingKey(key string) bool // the existing idPattern rule, for callers outside plex
func (c *Client) Identity(ctx context.Context) (string, error) // GET / -> machineIdentifier
type Details struct {
	GUID, Summary         string
	ChildCount, LeafCount int // seasons and episodes of a show; 0 otherwise
	Paths                 Paths
}
func (c *Client) ItemDetails(ctx context.Context, ratingKey string) (Details, error)
const MaxPhotoBytes = 10 << 20
// Photo returns the image at imagePath, scaled by the photo transcoder. imagePath
// must be the thumb or art path of ratingKey (ItemImagePath); otherwise it sends nothing.
func (c *Client) Photo(ctx context.Context, ratingKey, imagePath string, width, height int) ([]byte, error)
```

Rules:

- `do(ctx, method, endpoint, query, accept)` sends the request with the token header, traces it, checks `statusError`, and returns the response with its body open. `send` becomes `do` plus JSON decoding. Draining and closing stay as today.
- `Identity`: the identifier must match `^[A-Za-z0-9-]{1,128}$`, else an error (`identity: invalid machine identifier`). Errors are prefixed `identity:`.
- `ItemPaths` and `ItemDetails` share one unexported metadata read. `ItemPaths` keeps its error texts (`item paths: ...`). `ItemDetails` uses `item details: ...`.
- `Photo`: `Accept: image/*`; query `url`, `width`, `height`, `minSize=1`, `upscale=1`; endpoint segments `photo`, `:`, `transcode`. It reads at most `MaxPhotoBytes+1` bytes; more is an error (`photo: larger than 10 MB`). A body read error wraps `ErrUnavailable` without its text, as `bodyError` does.

- [ ] **Step 1: Write the failing tests**

```go
func TestIdentity(t *testing.T) // server_root.json -> "0000000000000000000000000000000000000001"; request path "/", token header set
func TestIdentityRejectsInvalidIdentifier(t *testing.T) // "" and "a/b" -> error
func TestItemDetails(t *testing.T) // item_show.json -> GUID "local://1004", ChildCount 2, LeafCount 12, Locations as ItemPaths gives
func TestPhotoUsesTranscoder(t *testing.T)
// key "1004", path "/library/metadata/1004/thumb/1700000300", 240x360 -> request path
// "/photo/:/transcode", query url/width/height/minSize/upscale as above, Accept "image/*",
// token header; returns the served bytes. With a base URL path "/plex" the path is "/plex/photo/:/transcode".
func TestPhotoRejectsForeignPath(t *testing.T) // path of key 999 with key 1004 -> error, no request recorded
func TestPhotoLimitsSize(t *testing.T) // MaxPhotoBytes+1 bytes -> error; MaxPhotoBytes bytes -> ok
func TestPhotoNotFound(t *testing.T) // 404 -> errors.Is(err, ErrNotFound)
```

- [ ] **Step 2: Run** `go test ./internal/plex -run 'Identity|ItemDetails|Photo' -v` — Expected: build failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the plex package — Expected: PASS, with every existing test unchanged.
- [ ] **Step 5: Commit** — `feat(plex): read the server identity, item details, and photos`

---

### Task 5: Progress hook in the refresher

**Files:**
- Create: `internal/refresher/progress.go`, `internal/refresher/progress_test.go`
- Modify: `internal/refresher/refresher.go`, `read.go`, `refresh.go`; `internal/store/history.go` (export the outcome)

**Interfaces:**
- Produces:

```go
// Progress receives the live state of each server run. Servers run in parallel, so an
// implementation must be safe for concurrent use, and it must not block.
type Progress interface {
	RunStarted(server string, at time.Time)
	LibraryRead(server, library string) // the library title, after its read was applied
	Batch(server string, selected, due int)
	ItemStarted(server, ratingKey, title string)
	ItemDone(server, ratingKey, outcome string)
	RunFinished(server string, res store.RunResult, err error)
}
const (
	OutcomeGone   = "gone"    // Plex answered 404
	OutcomeDryRun = "dry-run" // a dry run sent nothing
)
// Options.Progress Progress // nil: no progress
// store: func (r Request) Outcome() string // replaces requestOutcome: "refresh", "match",
// "tmdb-match", "<action>-failed", or "search-failed"
```

Rules:

- A nil `Options.Progress` means no calls (an unexported no-op implementation).
- `RunStarted` after `StartRun` in `runServer` (also in a dry run, with `run.began`). `RunFinished` once in `finishRun` for each started run, with the `RunResult` (built also in a dry run, which does not store it) and the run error before the server name is added: nil for a finished or canceled run.
- `LibraryRead` after each applied read. `Batch` after `Due` in `refreshDue`, also with 0 selected.
- `ItemStarted` in `refreshDue` after the sleep and before `refreshItem`.
- `ItemDone` exactly once for each item that `refreshItem` handles. The outcome is: an accepted request gives `req.Outcome()`; the dry-run path gives `OutcomeDryRun`; a 404 gives `OutcomeGone` (also in a dry run); any other failure gives `store.Request{Action: action}.Outcome()`, also when the failure stops the server. There is no call when `refreshItem` returns `errCache`, a store error, or a context error before a request was sent.

- [ ] **Step 1: Write the failing tests** (a recording fake with a mutex; set `f.r.opts.Progress` after building the fixture)

```go
func TestProgressReportsRunSteps(t *testing.T)
// one library, two due items: the first refresh accepted, the second fails with a plain
// error -> RunStarted, LibraryRead("<title>"), Batch(2, 2), ItemStarted(k1), ItemDone(k1, "refresh"),
// ItemStarted(k2), ItemDone(k2, "refresh-failed"), RunFinished(status finished, err nil).
func TestProgressReportsGoneAndDryRun(t *testing.T) // 404 -> "gone"; dry run -> "dry-run"
func TestProgressReportsMatchOutcome(t *testing.T) // accepted fix match -> "match"
func TestProgressReportsUnavailableServer(t *testing.T)
// Libraries fails with plex.ErrUnavailable -> RunStarted, then RunFinished with status
// failed and errors.Is(err, plex.ErrUnavailable); no LibraryRead.
```

- [ ] **Step 2: Run** `go test ./internal/refresher -run Progress -v` — Expected: build failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/refresher ./internal/store` — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(refresher): report run progress to a hook`

---

### Task 6: Web server foundation

**Files:**
- Create: `internal/web/server.go`, `internal/web/auth.go`, `internal/web/respond.go`, `internal/web/assets.go`, `internal/web/fallback/index.html`, `internal/web/server_test.go`, `internal/web/auth_test.go`

**Interfaces:**
- Produces (later tasks add fields to `Options` and routes to the mux):

```go
type Options struct {
	Username, Password string // both empty: no auth
}
func New(opts Options) *Server
func (s *Server) Handler() http.Handler
// Start serves ln in a goroutine and logs a Serve error other than http.ErrServerClosed
// (log.Error().Err(err).Msg("UI server failed")).
func (s *Server) Start(ln net.Listener)
// Stop cancels the SSE context (Task 7), calls Shutdown(ctx), calls Close if Shutdown
// fails, and waits for Serve to return. It returns the Shutdown error.
func (s *Server) Stop(ctx context.Context) error
// respond.go: writeJSON(w, status, v), writeError(w, status, msg)
```

Rules:

- Middleware, outermost first: security headers (`X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY` on every response), then `http.NewCrossOriginProtection()` (no trusted origins, no bypass), then Basic auth, then the mux. The cross-origin check wraps the whole mux, so a cross-site POST gets 403 before routing.
- Basic auth (when `Username` is set): compare `sha256.Sum256` of the given and the configured username and password with `subtle.ConstantTimeCompare`; compute both comparisons before deciding. A missing or wrong value answers 401 with `WWW-Authenticate: Basic realm="autorefresh", charset="UTF-8"` and `{"error":"unauthorized"}`.
- `http.Server`: `ReadHeaderTimeout: 10 * time.Second`, `ReadTimeout: 30 * time.Second`, `WriteTimeout: time.Minute`, `IdleTimeout: 2 * time.Minute`.
- Routes: `GET /{$}` serves the embedded `fallback/index.html` (`text/html; charset=utf-8`, `Cache-Control: no-cache`). `/api/` (catch-all) answers `404 {"error":"not found"}`. The page says that the dashboard UI is not built into this binary and links to `api/status` (relative).

- [ ] **Step 1: Write the failing tests**

```go
func TestPlaceholderPage(t *testing.T) // GET / -> 200, text/html, body has "not built" and href="api/status", no href="/
func TestUnknownAPIPathIsJSON404(t *testing.T)
func TestSecurityHeaders(t *testing.T) // on GET / and on a 404
func TestBasicAuth(t *testing.T)
// table on "/" and "/api/x": no header, wrong password, wrong username -> 401 with
// WWW-Authenticate; right values -> not 401.
func TestNoAuthWhenUnset(t *testing.T)
func TestCrossOriginPostIsRejected(t *testing.T)
// POST / with Sec-Fetch-Site cross-site -> 403; POST / without Sec-Fetch-Site, with
// Origin https://evil.example and Host ui.local -> 403; POST / with Sec-Fetch-Site
// same-origin -> 405 (it passed the check and reached the GET-only mux).
func TestStartAndStop(t *testing.T)
// listen 127.0.0.1:0, Start, GET / -> 200; Stop(ctx with 2 s) -> nil; a new GET fails.
```

- [ ] **Step 2: Run** `go test ./internal/web -v` — Expected: build failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the web package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(web): add the HTTP server with auth and a placeholder page`

---

### Task 7: Live status and SSE

**Files:**
- Create: `internal/web/status.go`, `internal/web/events.go`, `internal/web/status_test.go`, `internal/web/events_test.go`
- Modify: `internal/web/server.go`

**Interfaces:**
- Consumes: Task 5's `refresher.Progress` and outcomes.
- Produces:

```go
func NewStatus(servers []string, tmdb bool) *Status // servers in configured order
var _ refresher.Progress = (*Status)(nil)
func (s *Status) ServerChecked(server string, err error) // the start check of one server
func (s *Status) TMDBChecked(err error)                  // the start check of TMDB
func (s *Status) Snapshot() StatusView
type Health string // "unknown", "ok", "unreachable", "unauthorized"
type StatusView struct {
	Servers []ServerStatus `json:"servers"`
	TMDB    string         `json:"tmdb"` // "off", "unknown", "ok", "unreachable", "unauthorized"
}
type ServerStatus struct {
	Srv    int          `json:"srv"`
	Name   string       `json:"name"`
	Health Health       `json:"health"`
	Run    *RunProgress `json:"run"` // null while idle
}
type RunProgress struct {
	StartedAt                   time.Time `json:"startedAt"`
	LibrariesRead               int       `json:"librariesRead"`
	Library                     string    `json:"library,omitempty"` // the last library read
	Selected, Due, Done         int       // json: selected, due, done
	Refreshed, Matched, Failed  int       // json: refreshed, matched, failed
	Current                     string    `json:"current,omitempty"` // the title in work
}
// Options.Status *Status; routes GET /api/status and GET /api/events
```

Rules:

- Health: `ServerChecked(nil)` gives ok, an error wrapping `plex.ErrUnauthorized` gives unauthorized, any other error gives unreachable. `RunFinished` with an error wrapping `plex.ErrUnauthorized` gives unauthorized, with `plex.ErrUnavailable` unreachable, with no error and status `finished` ok; anything else keeps the health. `RunFinished` sets `Run` to nil.
- TMDB: `"off"` when `tmdb` is false, `"unknown"` until `TMDBChecked`; nil gives ok, `tmdb.ErrUnauthorized` unauthorized, any other error unreachable.
- `ItemDone`: `Done++`, `Current = ""`; outcome `refresh` adds to `Refreshed`, `match` and `tmdb-match` to `Matched`, a `-failed` suffix to `Failed`. Unknown server names are ignored.
- Each change notifies every subscriber with a non-blocking send on its own channel with a buffer of 1. `Snapshot` copies under the mutex.
- `GET /api/events`: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`; `http.NewResponseController(w).SetWriteDeadline(time.Time{})`. Subscribe first, then send the snapshot as `event: status\ndata: <json>\n\n` and flush. Then loop: return when the SSE context (owned by `Server`, canceled first by `Stop`) or the request ends; send the snapshot on each notification; send `: keepalive\n\n` every 25 s (a `Server` field that tests shorten). Return on a write error.

- [ ] **Step 1: Write the failing tests**

```go
func TestStatusTracksRun(t *testing.T) // the Task 5 call sequence -> Snapshot fields; after RunFinished, Run == nil and Health ok
func TestStatusHealth(t *testing.T) // table for ServerChecked and RunFinished as in the rules
func TestStatusTMDB(t *testing.T) // off; unknown; ok; unauthorized; unreachable
func TestStatusNotifyNeverBlocks(t *testing.T) // a subscriber that never reads; 10000 ItemDone calls return; one signal pending
func TestStatusEndpoint(t *testing.T) // GET /api/status decodes to the snapshot
func TestEventsStreamSnapshots(t *testing.T)
// GET /api/events: headers as above; the first event has "run":null; after RunStarted the
// next event has a run.
func TestEventsKeepalive(t *testing.T) // keepalive 20 ms -> a ": keepalive" line
func TestStopEndsOpenStreams(t *testing.T)
// Start on 127.0.0.1:0, open a stream, Stop(ctx with 2 s) returns nil before the deadline,
// and the stream reader gets EOF.
```

- [ ] **Step 2: Run** `go test ./internal/web -run 'Status|Events|Stop' -v` — Expected: build failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the web package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(web): stream live run status`

---

### Task 8: Servers and item list API

**Files:**
- Create: `internal/web/servers.go`, `internal/web/items.go`, `internal/web/params.go`, `internal/web/servers_test.go`, `internal/web/items_test.go`
- Modify: `internal/web/server.go`

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces:

```go
type Plex interface { // the part of plex.Client that the web server uses (Tasks 9 and 10 add theirs)
	Identity(ctx context.Context) (string, error)
}
type Store interface { // the part of store.Store that the web server uses (Tasks 9 and 10 add theirs)
	ListItems(q store.ListQuery) ([]store.ListItem, *store.Cursor, error)
	CountItems(q store.ListQuery) (store.ListCounts, error)
	Libraries(server string) ([]store.LibraryEntry, error)
}
type PlexServer struct {
	Name string
	Plex Plex
}
// Options adds: Servers []PlexServer (srv is the index); Store Store;
// Rules store.DueRules (Now is set for each request from Server.now)
// params.go: srvParam(r) (int, error); keyParam(r) (string, error) with plex.ValidRatingKey;
// itemJSON and toItemJSON(srv int, it store.ListItem) (Task 9 reuses them)
```

`GET /api/servers` answers:

```json
{"servers":[{"srv":0,"name":"Home","machineIdentifier":"…","libraries":[{"section":"1","title":"Movies","type":"movie","lastRead":"…","lastFullRead":"…"}]}],
 "ignoreReasons":["no-art"]}
```

- The machine identifier is cached per server after the first success. Missing identifiers are looked up in parallel, each with a 3 s timeout. A failure leaves the key out and logs at debug (`log.Debug().Str("server", name).Err(err).Msg("Reading Plex identity failed")`); the next request tries again.

`GET /api/items` parameters:

| Param | Values | Default | Invalid gives 400 with |
| --- | --- | --- | --- |
| `tab` | `attention`, `fixed`, `stopped` | `attention` | `invalid tab` |
| `srv` | configured index | every configured server | `invalid srv` |
| `library` | section ID; needs `srv` | any | `library needs srv` |
| `reason` | one reason name | any | `invalid reason` |
| `type` | `movie`, `show` | any | `invalid type` |
| `q` | trimmed, at most 200 bytes | none | `invalid q` |
| `sort` | a store sort key, `-` prefix for descending | tab default | `invalid sort` |
| `all` | `1` (`WithIgnored` or `WithUnrequested`) | off | `invalid all` |
| `limit` | 1–500 | 100 | `invalid limit` |
| `cursor` | opaque | first page | `invalid cursor` |

- The store always gets `Servers`: the one chosen server, or all configured names, so rows of a removed server never show.
- The cursor is `base64.RawURLEncoding` of JSON `{"tab","sort","n","t","s","k"}` (`sort` is the raw parameter, with `-`). A decode error, or a tab or sort that differs from the request, gives `invalid cursor`.
- The answer is `{"items":[...],"next":"<cursor>","counts":{"attention":0,"fixed":0,"stopped":0}}`; `next` is left out on the last page. Counts use the same filters.
- `itemJSON` fields: `srv`, `server`, `section`, `library`, `key`, `type`, `title`, `year`, `path`, `thumb`, `art`, `cached`, `reasons` (names), `state`, `nextDue`, `addedAt`, `lastAttempt`, `stoppedAt`, `attempts`, `refreshes`, `lastAction`, `lastOutcome`, `lastMatchOutcome`, `completedAt`, `fixedAction`, `fixedMatch`. Empty strings, zero numbers, and zero times are left out, except `srv`, `cached`, `attempts`, and `refreshes`.
- `thumb` and `art` are `api/items/thumb?key=<key>&srv=<srv>&v=<digits after the last "/">` (and `api/items/art?...`), built with `url.URL{Path: ..., RawQuery: url.Values{...}.Encode()}`, and only when `plex.ItemImagePath(key, path)` accepts the stored path.

- [ ] **Step 1: Write the failing tests**

```go
func TestServersEndpoint(t *testing.T)
// two servers with libraries and ignore-reasons; Identity ok for srv 0, error for srv 1 ->
// srv 0 has machineIdentifier, srv 1 has none; a second request calls Identity only for srv 1.
func TestItemsAttention(t *testing.T)
// srv, reason names, state, counts; thumb == "api/items/thumb?key=101&srv=0&v=1700000000".
func TestItemsPages(t *testing.T) // limit 2, follow next, no repeats, no next on the last page
func TestItemsFixedTab(t *testing.T) // completedAt and fixedAction; all=1 adds the unrequested completion
func TestItemsHidesRemovedServers(t *testing.T) // rows of a server that is not configured are not listed or counted
func TestItemsRejectsInvalidParams(t *testing.T)
// each invalid row of the table above, plus srv -1, srv 9, srv "a", a cursor of another
// sort, and a truncated cursor -> 400 with the JSON error.
```

- [ ] **Step 2: Run** `go test ./internal/web -run 'Servers|Items' -v` — Expected: build failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the web package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(web): serve the server and item list API`

---

### Task 9: Item detail, live view, and runs API

**Files:**
- Create: `internal/web/item.go`, `internal/web/runs.go`, `internal/web/item_test.go`, `internal/web/runs_test.go`
- Modify: `internal/web/server.go` (interfaces)

**Interfaces:**
- Consumes: Tasks 3, 4, and 8.
- Produces: `Plex` adds `ItemDetails(ctx context.Context, ratingKey string) (plex.Details, error)`. `Store` adds `ItemDetail`, `Timeline`, `Runs`, and `SetPath`.

`GET /api/item?srv=&key=`:

- It needs `srv` and a valid `key` (400 `invalid srv` / `invalid key`). An unknown item gives `404 {"error":"not found"}`. The answer is `{"item": itemJSON, "timeline": [eventJSON...]}`.
- `eventJSON`: `id`, `at`, `kind`, `source`, `action`, `accepted`, `outcome`, `error`, `match` (`{"guid","title","year"}` when `MatchGUID` is set), `conflict`, `pathYear`, `tmdbYear`, `episodeCount`, `links`, `fixedAction`, `fixedMatch`, `fixedMatchEventId`, `searches` (`query`, `source`, `fromId`, `failed`, `candidates`: `guid`, `name`, `year`, `type`, `summary`, `thumb`, `score`, `verdict`). Pass each candidate thumb through `plex.SafeRemoteImage` again before sending.

`GET /api/item/live?srv=&key=`:

- The item must be known (`ItemDetail` found), else 404. Then `ItemDetails` with a 5 s timeout (a `Server` field that tests shorten).
- Errors: `plex.ErrNotFound` gives `404 {"error":"not found in Plex"}`, `plex.ErrUnauthorized` gives `502 {"error":"Plex rejected the token"}`, any other error `502 {"error":"Plex unavailable"}`.
- The answer is `{"summary","guid","seasons","episodes","locations","files","path"}`. `seasons` and `episodes` are set only for shows. `path` is the cached path. For a show with no cached path it is the first location; if the item is cached, `SetPath` saves it. An uncached item never gets a row.

`GET /api/runs?srv=&cursor=&limit=`:

- `limit` is 1–500, default 50. The cursor is `base64.RawURLEncoding` of `{"a": <started_at unix>, "i": <id>}`. The store always gets `Servers` (the chosen one, or all configured).
- The answer is `{"runs":[runJSON...],"next":"..."}`. `runJSON` has `id`, `srv`, `server`, `kind`, `source`, `status`, `startedAt`, `finishedAt`, `error`, and the counts `readItems`, `refreshed`, `matched`, `failed`, `completed`, `fixed`, `incomplete`, `due`, `selected`.

- [ ] **Step 1: Write the failing tests**

```go
func TestItemFromRowAndHistory(t *testing.T)
// cached item -> cached true and its timeline; after completion -> cached false, title from
// the snapshot, and the completed event in the timeline; unknown key -> 404; key "1/2" -> 400.
func TestItemTimelineDropsUnsafeCandidateThumbs(t *testing.T) // a stored candidate thumb "http://h/x.jpg" -> "thumb" absent
func TestItemLive(t *testing.T)
// show without a path -> path is the first location and the store row now has it; movie keeps
// its cached path; an uncached item gets the path in the answer and still has no row.
func TestItemLiveErrors(t *testing.T)
// fake errors ErrNotFound -> 404, ErrUnauthorized -> 502 "Plex rejected the token", other -> 502
// "Plex unavailable"; the body never holds the fake error's text.
func TestItemLiveTimesOut(t *testing.T) // fake blocks until ctx ends; timeout 50 ms -> 502
func TestRunsEndpoint(t *testing.T) // srv filter, cursor pages, runs of an unconfigured server left out, invalid cursor -> 400
```

- [ ] **Step 2: Run** `go test ./internal/web -run 'Item|Runs' -v` — Expected: build failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the web package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(web): serve item details, the live view, and runs`

---

### Task 10: Image proxy

**Files:**
- Create: `internal/web/images.go`, `internal/web/images_test.go`
- Modify: `internal/web/server.go`

**Interfaces:**
- Consumes: Task 4's `Photo` and `MaxPhotoBytes`, and Task 3's `ItemDetail`.
- Produces: `Plex` adds `Photo(ctx context.Context, ratingKey, imagePath string, width, height int) ([]byte, error)`. Routes `GET /api/items/thumb` and `GET /api/items/art` with `srv`, `key`, `size` (`sm`, `md`, `lg`; default `md`), and `v` (ignored; it only busts the browser cache).

Sizes (width × height): thumb `sm` 120×180, `md` 240×360, `lg` 480×720; art `sm` 480×270, `md` 960×540, `lg` 1920×1080. Any other `size` gives 400 `invalid size`.

Flow:

1. Parse `srv` and `key`.
2. Read `ItemDetail`. An unknown item gives 404.
3. If `plex.ItemImagePath(key, stored)` rejects the stored path, answer 404 with no Plex request.
4. Call `Photo` with a 30 s timeout. `plex.ErrNotFound` gives 404; any other error gives `502 {"error":"image unavailable"}`.
5. Sniff with `http.DetectContentType`. Only `image/jpeg`, `image/png`, and `image/webp` pass; anything else gives the same 502.
6. Write the bytes with only our headers: `Content-Type` (sniffed), `Content-Length`, `Cache-Control: private, max-age=86400`, `Cross-Origin-Resource-Policy: same-origin`, and the global security headers.

- [ ] **Step 1: Write the failing tests**

```go
func TestThumbServesImage(t *testing.T)
// a 1x1 PNG from image/png -> 200, Content-Type image/png, Cache-Control, CORP, nosniff,
// the same bytes; the fake got key, stored path, 240, 360.
func TestImageSizes(t *testing.T) // thumb sm -> 120x180; art lg -> 1920x1080; size xl -> 400
func TestImageRejectsForeignPath(t *testing.T)
// stored thumb set by SQL to "/library/metadata/999/thumb/1", then "https://h/x.jpg" -> 404, Photo never called
func TestImageRejectsNonImage(t *testing.T) // Photo returns "<html>…" -> 502 JSON; the body has no HTML
func TestImageNotFound(t *testing.T) // unknown key -> 404 without a call; Photo ErrNotFound -> 404
func TestImageFromHistory(t *testing.T) // completed item (row gone) -> served from the event snapshot path
func TestImageNeedsAuth(t *testing.T) // with a username set, no credentials -> 401 and no Photo call
```

- [ ] **Step 2: Run** `go test ./internal/web -run Image -v` — Expected: build failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the web package — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(web): proxy item posters and art`

---

### Task 11: Wire the UI into the process, and document it

**Files:**
- Create: `cmd/autorefresh/ui.go`, `cmd/autorefresh/ui_test.go`
- Modify: `cmd/autorefresh/main.go`, `cmd/autorefresh/check.go`, `cmd/autorefresh/check_test.go`, `cmd/autorefresh/main_test.go` (helper for extra config lines)
- Modify: `docker/Dockerfile`, `README.md`, `AGENTS.md`

**Interfaces:**
- Consumes: Tasks 1 and 5–10.
- Produces:

```go
var listenFunc = net.Listen // tests replace it, like sleepFunc and readFileFunc
type healthRecorder interface {
	ServerChecked(server string, err error)
	TMDBChecked(err error)
}
func checkTokens(ctx context.Context, servers []refresher.Server, tmdbClient interface{ Check(context.Context) error }, rec healthRecorder) // rec may be nil
```

Behaviour:

- The UI runs only when `cfg.UI.Listen != ""` and neither `--once` nor `--dry-run` is set. With `ui.listen` set in those modes, log `log.Info().Msg("UI is off with --once or --dry-run")` once and bind nothing.
- Build `web.NewStatus` before the refresher, and pass it as `Options.Progress` only when the UI runs.
- Keep each server's `*plex.Client` beside its `refresher.Server`, and pass it as `web.PlexServer.Plex`. `web.Options` gets the store, the status, `cfg.UI.Username` and `cfg.UI.Password`, and `Rules: store.DueRules{MinimumAge, RefreshInterval, MaxRefreshInterval, Ignored: cfg.Ignored}` from the config.
- After the `--once` branch and before `checkTokens`, bind with `listenFunc("tcp", cfg.UI.Listen)`. On error, close the store and `return fatal(err, "Failed starting UI listener")`. No Plex request is sent before the bind.
- Start the server, then log `log.Info().Str("listen", ln.Addr().String()).Bool("auth", cfg.UI.Username != "").Msg("UI listening")`. Without auth, also log `log.Warn().Msg("UI has no authentication, so allow access only through an authenticating reverse proxy or a trusted network")`.
- Pass the status as `rec` to `checkTokens`, or nil. Report a check only if ctx has not ended.
- Stop order, on every exit after the bind (also a cancel during `checkTokens`):
  1. Wait for ctx to end.
  2. `Stop` the UI with a 5 s timeout, and log its error at warn (`"Stopping UI failed"`).
  3. Wait for `serve` to return (run it in a goroutine).
  4. Close the store.

  Without a UI, the behaviour is as today.
- Keep `run` at a complexity of 15 or less: put the UI setup and stop in `ui.go`.
- Dockerfile: `EXPOSE 7380`, with a comment that nothing listens until `ui.listen` is set.
- README: a "Dashboard" section.
  - The `ui` keys and their validation rules. There is no default port; `:7380` is the documented choice.
  - A Docker example with `ui.listen: ":7380"` and `-p 7380:7380`. For bare metal behind a local proxy, `127.0.0.1:7380`.
  - The no-auth warning, and that a reverse proxy serving plain HTTP must pass the original `Host` header.
  - No listener with `--once` or `--dry-run`.
  - The page is a placeholder until the frontend ships; the JSON API is under `api/`.
  - Add `ui` to the configuration example.
- AGENTS.md:
  - Add the `internal/web/` row.
  - Update the `cmd/autorefresh/`, `internal/store/`, `internal/refresher/`, `internal/plex/`, and `docker/Dockerfile` rows: the UI listener and stop order, the list queries, the progress hook, identity, details and photos, and `EXPOSE`.

- [ ] **Step 1: Write the failing tests**

```go
func TestRunBindFailureStopsBeforePlex(t *testing.T)
// a listener holds 127.0.0.1:port; ui.listen is that address; run returns an error, the log
// has "Failed starting UI listener", runTarget saw 0 requests, the database opens.
func TestRunOnceIgnoresUI(t *testing.T) // occupied port, opts.Once -> nil, log has "UI is off with --once or --dry-run"
func TestRunDryRunIgnoresUI(t *testing.T) // occupied port, scheduled dry run, cancel after the first run -> nil, no bind error
func TestRunServesUIAndStops(t *testing.T)
// listenFunc wraps net.Listen("tcp", "127.0.0.1:0") and records the listener; run in a
// goroutine; poll GET http://<addr>/api/status until 200 with server "test" health "ok";
// the log has "UI listening" and the no-auth warning; cancel -> run returns nil within 10 s;
// the database opens.
func TestCheckTokensRecordsHealth(t *testing.T) // a recording rec gets each server's result and TMDB's; existing calls pass nil
```

- [ ] **Step 2: Run** `go test ./cmd/autorefresh -run 'UI|Health' -v` — Expected: FAIL.
- [ ] **Step 3: Implement**, then the Dockerfile, README, and AGENTS.md changes.
- [ ] **Step 4: Run** `mise run vendor && mise run test && mise run lint` — Expected: PASS.
- [ ] **Step 5: Commit** — `feat(cli): serve the dashboard API beside the scheduler` (Dockerfile, README, and AGENTS.md changes as separate `docker`, `readme`, and `agents` commits)

---

## Delivery

1. Run the full checks once more on the branch tip.
2. Live check, read-only (record the results in the PR). Do this only with a temporary config and database outside the repository.
   - Config: `ui.listen: "127.0.0.1:7380"`, a username and password, the real server, and `minimum-age: 87600h`, so nothing is due and no refresh request is sent. Reading libraries is allowed.
   - Request `api/status`, `api/servers`, `api/items`, one `api/item`, one `api/item/live`, one `api/items/thumb`, and `api/events` (with and without credentials).
   - Stop the process and confirm that it exits cleanly.
   - Put no address, token, or machine identifier in the PR.
3. Push with `git push -u origin feat/94-dashboard-http-server`. Open a PR into `main` titled `feat(web): add the dashboard HTTP server and read API`.
   - Describe the problem, the change, the checks run (local, live), the deferred items, the accepted limit, and the source and target branches.
   - Use `Refs #94`, not `Closes`, because stages 3–5 remain.
4. Reviews: Codex `gpt-6.2-sol` until it is ready, then Gemini `gemini-4-neon`, then CodeRabbit. Enable auto-merge (merge commit) only after all three approve.
