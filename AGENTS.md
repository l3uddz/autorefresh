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
| `cmd/autorefresh/` | CLI entry point. It holds the flags, the default paths, logging, and the wait for the configuration file. The flags are `--config`, `--database`, `--dry-run`, `--once`, `--log`, `-v`, and `--version`. `run` wires the internal packages together, and tests call it with a temporary config and database. It runs the refresher once with `--once`. Otherwise it checks the token of each Plex server and the TMDB token once. Then it runs the refresher once at once and then on the `schedule` cron, with one run at a time. SIGINT and SIGTERM cancel the run and stop the schedule. With `ui.listen` set, and without `--once` and `--dry-run`, `ui.go` binds the dashboard listener before the token checks, so a bind error stops the start before any Plex request. The `web.Status` then gets the token checks and the run progress, and the web server gets the store, the Plex clients, the queue rules, the secrets that its API redacts from stored error texts, and the credentials (the Plex tokens, the TMDB token, and the UI password) that its poster check uses. `uiCredentials` builds the credentials once, and `uiSecrets` adds the Plex hosts and path prefixes to them. The schedule has its own context. After a signal, `run` stops the UI (5 s timeout), then cancels the schedule and waits for the busy run, then closes the store. Tests replace `listenFunc` to bind a free port. |
| `internal/config/` | Configuration types, defaults, loading, and validation |
| `internal/plex/` | Plex API client: list libraries and items, get item paths and episode numbers, refresh, search matches, and apply a match. It also reads the server identity and the details of an item, and gets the thumb or art of an item through the photo transcoder, at most 10 MB. `PathPrefix` gives the path of the server URL, which the web server redacts. |
| `internal/tmdb/` | TMDB API client: find the TMDB ID of a TVDB or IMDb ID, read release years and episode numbers, and check the token. It sends the token only in a header. |
| `internal/store/` | SQLite cache of incomplete items with their attempts and the last requests that Plex accepted, the read times of each library, and the TMDB IDs of items. It also keeps the history: events of requests, fix match decisions with their searches and candidates, completions, and gone items, and the runs of each server. It removes events and runs older than the retention, and the cache of unused servers and libraries. Foreign keys with `ON DELETE CASCADE` remove the rows of an item or a library with it. A connection waits for the locks of another process and keeps temporary data in memory. For the dashboard, it lists the items of the `attention`, `fixed`, and `stopped` tabs with filters, sorts, and cursors, counts each tab, and reads item details, timelines, libraries, and pages of runs. `DueRules` decide the queue state for both `Due` and the lists. The methods that the web server calls take the request context first. |
| `internal/refresher/` | The run of each server. It removes the cache of unused servers and libraries, reads the libraries, and refreshes the due items, with the pacing, the limit for each run, and the dry run. It records the history of each run and removes old history, but a dry run records and removes none. It logs the summary of each server. It also holds the rules for missing metadata, path ID and path year parsing, the fix match choice, the TMDB fallback, and match confirmation by year or episode numbers. `Options.Progress` gets the live state of each run: its start, each library read, the batch, each item, and its end. A nil `Progress` reports nothing. |
| `internal/web/` | The HTTP server of the dashboard: optional Basic auth, cross-origin protection, security headers, a placeholder page, and a JSON API under `/api/`. The API serves the live status and its server-sent event stream, the servers, the item lists, the item details and a live Plex view, the posters and art through a proxy, and the runs. It only reads, except that the live view fills an empty item path. `Status` keeps the live status and is the `refresher.Progress` of the service. The API replaces the configured secrets in stored error texts with `<redacted>`. It drops a candidate thumb whose URL holds a configured credential, also percent-decoded, but not one that holds only a host or a path prefix. On the routes that take query parameters, a query string that does not parse gets a 400. The handlers share the one SQLite connection with the refresher, so a handler never holds a transaction or open rows across a Plex request or a response write. `Stop` closes handler admission, so later requests get a 503, then ends the event streams and shuts the server down within the deadline of its context. If that fails, it forces a close. |
| `docker/Dockerfile` | Container image, built from the goreleaser output in `dist/`. It runs as the non-root user `65532:65532`, which owns `/config`. It declares `EXPOSE 7380` for the dashboard, but nothing listens until `ui.listen` is set. |
| `mise.toml` | Tool versions and development tasks |
| `.golangci.yml` | golangci-lint configuration: the formatter, the linters, and the complexity limit that `mise run lint` checks |
| `.goreleaser.yml` | Release build configuration |

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
- The Go version comes from `go.mod`. Do not lower it. Keep the `go` version
  in `mise.toml` on the same minor release.

## Checks

You need [mise](https://mise.jdx.dev). Run `mise install` once to install
the Go, goreleaser, and golangci-lint versions from `mise.toml`.

The required `build` job of the `ci` workflow runs the following commands in
order before packaging. Run the same commands before you push.

```sh
mise run vendor
mise run test
mise run lint
```

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

`mise run build` makes a local binary in `dist/`.
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
