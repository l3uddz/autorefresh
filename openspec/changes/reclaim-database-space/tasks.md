# Tasks

## 1. Incremental auto-vacuum in the store

- [x] 1.1 Add the `retryBusy` helper and use it for `enableWAL` and for the pre-WAL `auto_vacuum = INCREMENTAL` of a new file in `Open`. Add `Store.ConvertAutoVacuum` with the `maxConvertBytes` cap and the truncating checkpoint, returning sizes as pages times page size. Call it from `cmd/autorefresh` after `store.Open` unless `--dry-run`, with the `Converted database`, `Database too large to convert`, and `Database conversion failed` logs.
  - Store tests: a new file, two handles creating a new file, converting an old file with its rows kept, a conversion blocked by a second handle's write transaction, the cap, and the closed-database case in `TestMethodsReportDatabaseFailure`.
  - `cmd/autorefresh` test: `--dry-run` leaves an old file at `auto_vacuum = 0`, and a normal run converts it.
  - Update the `internal/store` and `cmd/autorefresh` rows of `AGENTS.md`.
  - Verify with `go test ./internal/store/ ./cmd/autorefresh/`.
  - Commit: `perf(store): use incremental auto-vacuum for the database`.

## 2. Space upkeep at run start and statistics at stop

- [ ] 2.1 Add `Store.Reclaim` (one-fifth and 1 MiB threshold, the unconditional truncating checkpoint, and pages from the fall in `page_count`) and `Store.Optimize`, and add both to the `refresher.Store` interface. Call `upkeep` at the start of `execute` unless a dry run, with the `Reclaimed database space` debug line and warning-only failures. Call `Optimize` with a 5 s context before `closeStore` at a normal stop of `main` unless `--dry-run`.
  - Store tests: release above and below the threshold, a database without auto-vacuum, a busy checkpoint that a later `Reclaim` completes, `Optimize` normally and under a held write lock within its deadline, and the closed-database cases.
  - Refresher tests: no calls in a dry run, warning-only failures, and one call per manual scan from the job and not from `RunNow`.
  - `cmd/autorefresh` tests: a normal stop writes `sqlite_stat1`, and a `--dry-run` stop does not.
  - Add one sentence on reclaimed space to the database note of `README.md`.
  - Verify with `go test ./internal/store/ ./internal/refresher/ ./cmd/autorefresh/`.
  - Commit: `perf(store): reclaim free database pages at run start`.

## 3. Full checks

- [ ] 3.1 Run `mise run vendor`, `mise run test`, `mise run lint`, `mise run web-install`, `mise run web-lint`, `mise run web-test`, and `mise run test-webui` in order. All pass, and `go.mod` and `go.sum` have no change. Check that `openspec validate reclaim-database-space --strict` passes. No commit unless a check needs a fix, which then gets its own `fix(store)` commit.

## Workflow follow-up

- Live acceptance (maintainer): after deploy, check the log for `Converted database` once, and that `PRAGMA auto_vacuum` of `/config/autorefresh.db` reads `2`.
- Archive: sync the delta specs into `openspec/specs`, run `openspec validate --all --strict`, and archive the change.
