# autorefresh

Autorefresh finds Plex items with missing metadata, such as a missing summary
or poster, and asks Plex to refresh them. It runs on a schedule beside
[autoscan](https://github.com/l3uddz/autoscan).

The project is in early development.

## How it works

Autorefresh talks only to Plex. On each run, it reads the libraries of each
Plex server. It keeps the incomplete movies and series in a local SQLite
cache, and it refreshes the items that are due. It handles the servers one
after another.

### Libraries

Autorefresh checks each movie library with the agent `tv.plex.agents.movie`
and each show library with the agent `tv.plex.agents.series`. It skips
libraries with other agents and logs one warning for each of them. If a
server has a `libraries` list, autorefresh checks only those libraries.

### What counts as missing

| Item | Incomplete when |
| --- | --- |
| Movie | The movie is unmatched, or it has no summary, no poster, or no background art. |
| Series | The series is unmatched, or it has no summary, no poster, or no background art. |
| Series | Some matched regular episodes of the series have an empty summary. |
| Series | The series is matched, but some regular episodes are unmatched. |

An unmatched item has a `local://` guid. A regular episode is an episode
outside season 0, so specials never count. Autorefresh never refreshes a
season or a single episode. An incomplete episode causes a refresh of its
series.

Autorefresh skips items that Plex added less than `minimum-age` ago, because
Plex can still be working on them.

### Ignored reasons

Some reasons often cannot be fixed. For example, the metadata source of many
items has no background art. To stop refreshes for such a reason, add its
name to `ignore-reasons`. The names are the values of the `reasons` log
field:

| Name | Reason |
| --- | --- |
| `unmatched` | The movie or series is unmatched. |
| `no-summary` | The movie or series has no summary. |
| `no-poster` | The movie or series has no poster. |
| `no-art` | The movie or series has no background art. |
| `episode-no-summary` | Some matched regular episodes have an empty summary. |
| `episode-unmatched` | Some regular episodes are unmatched. |

Autorefresh does not refresh an item whose reasons are all ignored, and it
does not count the item in `incomplete_items`. The item stays in the cache,
so if you remove the name later, the item is due again at once. If you
ignore an episode reason, autorefresh does not send its episode request.

### Full and incremental reads

A full read lists every movie or series in a library. Autorefresh does a
full read of a library in these cases:

- The library has no full read yet.
- A `cache-refresh` time passed since the last full read of the library.

The full read happens in the first run at or after a `cache-refresh` time.
So choose a `cache-refresh` time at which `schedule` also starts a run. With
the defaults, the hourly run at 03:00 on Sunday does the full reads.

On other runs, autorefresh does an incremental read. It requests the items
that Plex added or updated since the last read minus `oversample`, and all
unmatched items. On every run, it also requests the series with incomplete
regular episodes. Autorefresh never lists all episodes.

If a read of a library fails, the cache of that library does not change.
The next run reads the same period again. If an item becomes complete,
autorefresh logs it with the number of refreshes and removes it from the
cache.

### Refreshes

If an incomplete item has no attempt in its wait, the item is due. An
attempt is a refresh or match request, successful or failed. A failed match
search also counts as an attempt, but it does not count toward
`max-refreshes-per-run`. A dry run records no attempts.

The wait backs off, because an item that many refreshes did not fix is
less likely to become complete. For an item with no refresh or one refresh,
the wait is `refresh-interval`. Each later refresh doubles the wait, up to
`max-refresh-interval`. A refresh is a refresh or match request that Plex
accepted, so failed requests do not make the wait longer. With the
defaults, the waits are:

| Refreshes | Wait |
| --- | --- |
| 0 or 1 | 12h |
| 2 | 24h |
| 3 | 48h |
| 4 | 96h |
| 5 or more | 168h (7 days) |

Autorefresh never gives up. It tries an incomplete item again after each
wait until the item is complete or gone from Plex. If you set
`max-refresh-interval` to `refresh-interval` or less, the wait never grows.

Each run refreshes the due items in this order:

1. Items with the fewest refreshes. A refresh is a refresh or match request
   that Plex accepted. Items that Plex never refreshed therefore go first.
2. Items with no attempt.
3. Items with the oldest last attempt.

If two items are equal, the item that Plex added first goes first. A failing
item therefore moves to the end of the items with the same number of
refreshes.

Autorefresh sends one refresh or match request at a time on each server. It
waits `refresh-delay` between two of these requests. A run sends at most
`max-refreshes-per-run` refresh and match requests on each server. If you
set it to 0, a run has no limit.

Runs do not overlap. If a run is still busy when the schedule starts the
next run, autorefresh skips that start and logs a warning. The first run on
a large server can take some hours.

If a Plex server does not answer, autorefresh logs an error and skips that
server for the rest of the run. If a refresh or match request fails,
autorefresh logs a warning. The failed request counts as an attempt.

### Fix match

A fix match links an unmatched item to a specific entry in the Plex metadata
database. Autorefresh tries a fix match only for an unmatched movie or series
that it refreshed at least once before. So the first attempt is always a
plain refresh.

1. Autorefresh reads IDs from the folder names and the file names of the
   item. An ID is in curly brackets, square brackets, or parentheses, for
   example `{tmdb-747059}`, `[imdb:tt19120100]`, or `(tvdb-463649)`.
   The sources are `imdb`, `tmdb`, and `tvdb`, also with an `id` suffix such
   as `imdbid`.
2. It searches Plex once for each ID. These searches are reads, and
   autorefresh does not wait `refresh-delay` between them.
3. If every ID with a result found exactly one candidate, and all of these
   candidates are the same entry, it can apply a match.
4. If the item and the candidate both have a year, the years must differ by
   1 or less.

If no match applies, autorefresh sends a plain refresh. If the IDs disagree,
it also logs the conflict. If a match search fails, autorefresh sends no
request for that item in this run. A match request counts as a refresh for
the wait and for `max-refreshes-per-run`.

### No library-wide refresh

Autorefresh refreshes one item at a time. It never sends a library-wide
refresh, which is the Plex "Refresh All Metadata" action on a library.

## Configuration

The configuration is a YAML file, by default `config.yml` in the default
configuration directory.

```yaml
schedule: "0 * * * *"         # cron expression for each run
minimum-age: 24h              # skip items added less than this long ago
refresh-interval: 12h         # wait at least this long between attempts on one item
max-refresh-interval: 168h    # the longest wait between attempts after the backoff
cache-refresh: "0 3 * * 0"    # cron expression for full reads of each library
oversample: 48h               # each incremental read also gets changes from this far back
refresh-delay: 5s             # wait between two refreshes on one server
max-refreshes-per-run: 250    # the most refresh and match requests in each run; 0 for no limit
ignore-reasons: []            # reasons that never cause a refresh, for example [no-art]

plex:
  - name: main                    # label for logs and the database
    url: https://plex.example.com
    token: XXXX
    libraries:                    # optional; if omitted, all movie and show libraries
      - Movies
      - TV
```

| Key | Required | Default |
| --- | --- | --- |
| `schedule` | no | `0 * * * *` |
| `minimum-age` | no | `24h` |
| `refresh-interval` | no | `12h` |
| `max-refresh-interval` | no | `168h` |
| `cache-refresh` | no | `0 3 * * 0` |
| `oversample` | no | `48h` |
| `refresh-delay` | no | `5s` |
| `max-refreshes-per-run` | no | `250` |
| `ignore-reasons` | no | none |
| `plex` | yes, at least one server | none |

`schedule` and `cache-refresh` are cron expressions with 5 fields, or
descriptors such as `@hourly`. They use the local time zone, which the `TZ`
environment variable sets. Choose a `cache-refresh` time at which
`schedule` also starts a run, because the full read happens in the first run
at or after that time.

Durations use Go units such as `h`, `m`, and `s`, so write 7 days as
`168h`. `refresh-interval` must be more than 0. `max-refresh-interval`,
`minimum-age`, `oversample`, and `refresh-delay` must be 0 or more.
`max-refreshes-per-run` must be 0 or more, and 0 means no limit. An unknown
name in `ignore-reasons` is an error. These keys apply to all servers.

Each entry in `plex` is one Plex server:

- `name` is required and must be unique. Logs and the database use it, so a
  new `url` keeps the state of the server.
- `url` is required and must use `http` or `https`.
- `token` is required. It is the Plex token of the server.
- `libraries` is optional. It selects libraries by their title.

With `http`, the token crosses the network in clear text. If the connection
leaves a trusted network, use `https`.

An unknown key is an error. If the configuration file is missing or empty,
autorefresh waits for it and checks again every 5 seconds. A container can
then start before `config.yml` exists.

## Command line

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--config` | `AUTOREFRESH_CONFIG` | `config.yml` in the default configuration directory | Configuration file path |
| `--database` | `AUTOREFRESH_DATABASE` | `autorefresh.db` in the default configuration directory | Database file path |
| `--dry-run` | `AUTOREFRESH_DRY_RUN` | off | Read Plex and update the cache, but send no refresh or match requests |
| `--once` | `AUTOREFRESH_ONCE` | off | Run one check at once and exit, instead of waiting for the schedule |
| `--log` | `AUTOREFRESH_LOG` | `activity.log` in the default configuration directory | Log file path |
| `-v`, `--verbosity` | `AUTOREFRESH_VERBOSITY` | `0` | Log level: `0` is info, `1` is debug, `2` or more is trace |
| `--version` | none | none | Print version information and quit |

If the directory of the binary contains `config.yml`, the default
configuration directory is that directory. Otherwise it is `autorefresh` in
the user configuration directory, for example `~/.config/autorefresh` on
Linux.

Without `--once`, the first run starts at the next `schedule` time. With
`--once`, autorefresh runs one check at once and exits. If that check fails
with a database error, the exit code is 1.

If autorefresh gets SIGINT or SIGTERM, it stops the schedule and cancels the
busy run. The run stops before its next request to Plex. Autorefresh then
closes the database and exits.

## Dry run

Use `--dry-run` to see what a run will do before it changes anything. A dry
run does these things:

- It reads Plex and updates the cache.
- It sends match searches, which are reads, to choose the action of each
  item.
- It logs one `Dry run` line at info level for each due item. The line has
  the server, the library, the title, the reasons, and the action, which is
  `refresh` or `match`.
- It applies `max-refreshes-per-run` to the logged items.

A dry run sends no refresh or match request and does not wait
`refresh-delay`. It records no attempts, so a later real run refreshes the
same due items.

```sh
autorefresh --once --dry-run
```

## Logging

Autorefresh writes each log line to stderr and to a log file. When the log
file reaches 5 MB, autorefresh starts a new file. It keeps 5 old files and
deletes files after 14 days.

| Level | Content |
| --- | --- |
| `fatal` | Problems that stop the program at the start, such as an invalid configuration or a database that does not open |
| `error` | Problems that stop a run or the checks of a server, such as a database error or a Plex server that does not answer |
| `warn` | Problems that autorefresh survives, such as a failed request, a match conflict, a skipped library, or a skipped scheduled start |
| `info` | The start, each request that autorefresh sends, each item that becomes complete, and one summary line for each server at the end of a run |
| `debug` | Each library read, items that `minimum-age` skips, and match search results |
| `trace` | Each HTTP request |

Autorefresh never logs the Plex token or the server URL.

When autorefresh finishes the refresh step of a server, it logs one
`Run summary` line for that server. A server that becomes unavailable during
the refresh step also gets this line. In these cases, a server gets no
summary line:

- The server becomes unavailable while autorefresh reads its libraries.
- A database error stops the run.
- You stop the run.

The line has these fields:

| Field | Content |
| --- | --- |
| `full_read_items` | The movies and series that the full reads of the run listed. If the run did no full read, the value is 0. |
| `incremental_read_items` | The movies and series that the incremental reads of the run listed. If the run did only full reads, the value is 0. |
| `incomplete_items` | The incomplete items in the cache after the run, without the items whose reasons are all ignored. An item that the run refreshed stays in this count until a later read finds it complete. |
| `due_items` | The incomplete items that were due for a request |
| `refresh_requests` | The refresh requests that the run sent, also failed ones |
| `match_requests` | The fix match requests that the run sent, also failed ones |
| `failed_requests` | The failed requests and match searches. A `404 Not Found` answer is not a failure. |
| `remaining_items` | The due items that the run did not reach, because of `max-refreshes-per-run` or because the server became unavailable. They stay due for later runs. |
| `completed_items` | The items in the cache that the reads of the run found complete. The run removes them from the cache. Each one also has an `Item complete` line. |
| `completed_after_request` | The items in `completed_items` that had at least one request from autorefresh that Plex accepted. Plex runs a refresh in the background, so the effect of a request shows in a later run. |
| `duration` | The time of the run of the server, with the reads, rounded to milliseconds, such as `11m3.814s` |

## Docker

CI publishes the image `l3uddz/autorefresh` to Docker Hub for
`linux/amd64`, `linux/arm64`, and `linux/arm/v7`.

The image sets these paths in the `/config` volume:

| Environment variable | Value |
| --- | --- |
| `AUTOREFRESH_CONFIG` | `/config/config.yml` |
| `AUTOREFRESH_DATABASE` | `/config/autorefresh.db` |
| `AUTOREFRESH_LOG` | `/config/activity.log` |

Put `config.yml` in the directory that you mount at `/config`. Set `TZ` to
choose the time zone of `schedule` and `cache-refresh`.

```sh
docker run -d --name autorefresh \
  -e TZ=Europe/London \
  -v /opt/autorefresh:/config \
  l3uddz/autorefresh:main
```

| Tag | Content |
| --- | --- |
| `latest` | The latest release |
| `<version>` | A release, for example `1.0.0` |
| `main` | The latest commit on the `main` branch |
| `<branch>` | The latest build of an open PR into `main` from a branch of this repository |

A PR from a fork, or a PR into another branch, publishes no image.

CI makes the `<branch>` tag from the branch name with these rules:

- Each character other than `A-Z`, `a-z`, `0-9`, `_`, `.` and `-` becomes
  `-`. For example, `fix/a` publishes `fix-a`.
- If the tag starts with `.` or `-`, it gets a `_` prefix. For example,
  `+fix` publishes `_-fix`.
- If the tag is longer than 128 characters, CI cuts it and adds an
  8-character hash of the branch name.
- If the tag is `latest` or starts with a digit, the build fails, because
  releases own these tags.
- If two branches map to the same tag, the build fails.

```sh
docker run --rm l3uddz/autorefresh:main --version
```

## Build from source

You need [mise](https://mise.jdx.dev). It installs the Go and goreleaser
versions from `mise.toml`.

```sh
mise install
mise run build
```

The binary is in `dist/autorefresh_<os>_<arch>/`. Run `mise tasks` to list
the other tasks.

## License

[MIT](LICENSE)
