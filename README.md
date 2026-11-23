# autorefresh

Autorefresh finds Plex items with missing metadata, such as a missing summary
or poster, and asks Plex to refresh them. It runs on a schedule beside
[autoscan](https://github.com/l3uddz/autoscan).

The project is in early development.

## How it works

Autorefresh talks to Plex. With a `tmdb-token`, it also asks TMDB for TMDB
IDs during a fix match. On each run, it reads the libraries of each Plex
server. It keeps the incomplete movies and series in a local SQLite
cache, and it refreshes the items that are due. It checks all servers at
the same time, so a slow server does not delay the others.

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
so if you remove the name later, the item counts again at once. It is due
again when its `minimum-age` and its wait after the last attempt passed.

If you ignore an episode reason, autorefresh does not send its episode
request. The cached series keep that reason and their refresh history.

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
The next run reads the same period again. Autorefresh reads a listing page
by page. Each page starts at the last item of the previous page. If that
item is not at its place, items moved in the part of the listing that
autorefresh already read, and the next page could skip an item, so the read
fails. If one item leaves that part and another item enters it, the last
item can stay at its place, and the read does not see the change. Items
that Plex adds at the end of the listing do not make the read fail. If an
item becomes complete, autorefresh logs it with the number of refreshes and
removes it from the cache.

A read saves its result when it ends. If **Refresh now**, a match, or the
check after one of them completes an item or finds it removed while the
read runs, the read does not add the item back to the cache. If Plex still
lists the item as incomplete, a later read that lists it adds it again, at
the latest the next full read.

### Cache cleanup

At the start of each run, autorefresh removes the cache of each server that
is not in `plex`. A server keeps its cache under its `name`, so a renamed
server also loses its old cache.

After autorefresh lists the libraries of a server, it removes the cache of
each library of that server that the run does not check. Such a library is
not in the `libraries` list, has an unsupported agent, or is gone from Plex.
If the listing fails, or if Plex lists no library at all, autorefresh keeps
the cache of that server, because the server can still be starting. A
failed listing also counts as an unavailable server for the run.

The cache of a library is its incomplete items, its read times, and its
history (see [History](#history)). Autorefresh logs one `Removed cache of
library` line for each removed library. If you check the library again
later, autorefresh does a new full read of it, and its items start with no
refreshes and no history. A removed server also loses its runs.

### Refreshes

If an incomplete item has no attempt in its wait, the item is due. An
attempt is a refresh or match request, successful or failed. A failed match
search also counts as an attempt. A dry run records no attempts.

The wait backs off, because an item that many refreshes did not fix is
less likely to become complete. For an item with no refresh or one refresh,
the wait is `refresh-interval`. Each later refresh doubles the wait, up to
`max-refresh-interval`. A refresh is a refresh or match request that Plex
accepted, so failed requests do not make the wait longer. With the
defaults, the waits are:

| Refreshes | Wait |
| --- | --- |
| 0 or 1 | 24h |
| 2 | 48h |
| 3 | 96h |
| 4 or more | 168h (7 days) |

Autorefresh never gives up. It tries an incomplete item again after each
wait until the item is complete or gone from Plex. If you set
`max-refresh-interval` to `refresh-interval` or less, the wait never grows.

When Plex accepts the refresh that moves an item to the longest wait,
autorefresh logs an `Item reached the longest wait` warning. The line has
the server, the library, the title, the reasons, the number of refreshes,
and the wait. Each item reaches the longest wait once, so it gets one
warning, and `max-refreshes-per-run` limits these warnings in each run. If
the wait never grows, autorefresh logs no such warning.

Each run refreshes the due items in this order:

1. Items with the fewest attempts. An attempt is a refresh or match
   request, successful or failed, or a failed match search. Items that
   autorefresh never tried therefore go first.
2. Items with no attempt.
3. Items with the oldest last attempt.

If two items are equal, the item that Plex added first goes first. A failed
attempt counts in this order, but not in the wait. An item that keeps
failing therefore moves behind the other due items, one attempt at a time,
and it cannot take the first place in every run. For example, an item that
fails with 1 refresh takes the first place for 4 runs before an item with 5
refreshes gets it.

Autorefresh handles one due item at a time on each server, and each item
gets at most one refresh or match request. A match that Plex accepts also
gets the refresh that follows it (see Fix match). It waits `refresh-delay` between
two items. A run handles at most `max-refreshes-per-run` due items on each
server. An item whose match search fails also counts, although it gets no
request. If you set `max-refreshes-per-run` to 0, a run has no limit.

Runs do not overlap. If a run is still busy when the schedule starts the
next run, autorefresh skips that start and logs a warning. The first run on
a large server can take some hours.

If a Plex server does not answer within its `timeout`, autorefresh logs an
error and skips that server for the rest of the run. The other servers
continue. If a refresh or
match request fails, autorefresh logs a warning. The failed request counts
as an attempt. A database error stops the run on all servers. After a run
with a database error or an unavailable server, autorefresh also logs one
`Run failed` error that names each server with the problem.

### Fix match

A fix match links an unmatched item to a specific entry in the Plex metadata
database. A run tries a fix match only for an unmatched movie or series
that it refreshed at least once before. So the first attempt of a run is
always a plain refresh. **Refresh now** in the dashboard tries the fix
match also before the first refresh. If `ignore-reasons` holds
`unmatched`, autorefresh never tries a fix match.

1. Autorefresh reads IDs from the folder names and the file names of the
   item. An ID is in curly brackets, square brackets, or parentheses, for
   example `{tmdb-747059}`, `[imdb:tt19120100]`, or `(tvdb-463649)`.
   The sources are `imdb`, `tmdb`, and `tvdb`, also with an `id` suffix such
   as `imdbid`. A bare IMDb ID such as `{tt0115097}` is an `imdb` ID too.
2. It searches Plex once for each ID. These searches are reads, and
   autorefresh does not wait `refresh-delay` between them.
3. If every ID with a result found exactly one candidate, and all of these
   candidates are the same entry, it can apply a match.
4. If the item and the candidate both have a year, the years must differ by
   1 or less. If only the year disagrees:
   - If the series folder, or the movie folder and file name, have one
     unambiguous release year in parentheses, such as `(2018)`, that differs
     from the item's year in Plex (for example when Plex parsed a bare year
     from a title), autorefresh checks the candidate against that path year.
   - If that does not confirm the match (either because there is no
     unambiguous path year or because the path year also disagrees with the
     candidate) and a `tmdb-token` is set, autorefresh resolves the item to
     a single TMDB ID, checks that Plex resolves that `tmdb-<id>` to the
     same candidate GUID, and compares TMDB's canonical year (the earliest
     regular season air date for a series, ignoring specials, or the
     earliest release date for a movie) against the item's year or its
     unambiguous path year.
   - If TMDB's year cannot confirm a series match, autorefresh reads the
     episode numbers Plex found in the files. Every episode number in each
     local regular season must exist in that season in TMDB (no extra or
     duplicate episode numbers), and at least 90% of TMDB's episodes across
     the local regular seasons must be present locally. At least one regular
     episode must match. Specials and seasons absent from the library do not
     affect the check.
     This can confirm a match even when the folder year disagrees with TMDB
     or a few episodes are missing (for example 9 of 10, or 39 of 41 across
     two seasons). Any extra episode number in Plex, or too many missing
     episodes (below 90% across the local seasons), still prevents
     confirmation by default TMDB seasons.
   - If TMDB's default seasons do not confirm valid local episode numbers,
     autorefresh checks TMDB's alternate episode groups for the series that
     align with TheTVDB's ordering formats, in priority order: **Original
     air date**, **DVD**, **Absolute**, and **TV** (skipping Digital,
     Story arc, and Production groups, groups with fewer episodes than Plex
     has locally, and checking at most 10 groups). It stops at the first
     group whose seasons confirm the local episodes under the same 90% and
     zero-extra rule. If no group confirms, autorefresh keeps the diff from
     TMDB's default seasons.

The decision of a year conflict keeps the years that autorefresh compared:
the year of the item, the year of its folder, if any, and the TMDB year, if
TMDB was asked, also when a later step failed. Only a search whose
candidate year differs from the item gets the verdict "year differs"; a
search that returned the same candidate with an agreeing year gets "year
agrees", and one without a year "not compared". A decision that failed
after a year conflict keeps the conflict. It also keeps the TMDB ID that it checked and, for a
series, the local season that kept episode confirmation from passing (the first
season with extra or invalid numbers, or the first season missing episodes when
coverage is below 90%),
with the episode count of Plex and of TMDB and the episode numbers that
only one of them has. The Fix match dialog shows them.

If no match applies, autorefresh sends a plain refresh. A series can first
get the [title fallback](#title-fallback). If the IDs disagree,
it also logs a `Match conflict` warning. The warning has the reason, the
year of the item, `tmdb_year` if TMDB was checked, and each candidate with
its year and the web page of the ID that found it. If a match search of an
ID fails, autorefresh sends no request for that item in this run. A failed
request of the title fallback still gives the item a plain refresh. A match request
counts as a refresh for the wait.

After Plex accepts a match, the run follows it up as a manual **Match** in
the dashboard does. It requests a refresh of the item, which counts as one
more attempt and one more refresh for the wait. Then it checks the item
after waits of 1, 2, and 4 seconds, while Plex loads the new metadata. A
check that finds the item complete moves it to the Fixed tab at once and
counts it in the summary of the run. A check that finds it still
incomplete updates its metadata, and the next run reads it again. The
follow-up refresh, and a completion or a removal that a check finds,
belong to the run, so they show in its details. If the server is
unavailable for the follow-up refresh or a check, the follow-up stops
and the run of that server ends, as it does after any request that fails
that way.

The `Sent request` line of a match, and its `Dry run` line, has these
fields, so you can check each match in the log:

| Field | Content |
| --- | --- |
| `action` | `match`, `tmdb-match` if the TMDB fallback below found the candidate, or `title-match` if the title fallback below found it |
| `item_year` | The year of the item in Plex, or 0 if it has none |
| `path_year` | Set when an unambiguous `(YYYY)` year in the path confirmed a year gap |
| `tmdb_year` | TMDB's canonical year, if available during confirmation |
| `episode_count` | Number of regular episodes compared when episode numbers confirmed the match |
| `candidate` | The title of the entry that the match applies |
| `candidate_year` | The year of that entry, or 0 if it has none |
| `links` | The web page of each ID whose search found or confirmed the entry. A `tmdb` ID links to TMDB, an `imdb` ID to IMDb, and a `tvdb` ID to TheTVDB. |

Plex stops using TheTVDB on December 3, 2026, and can then answer a search
for a `tvdb` ID with an error. So a failed search for a `tvdb` or an `imdb`
ID counts as a search without a candidate. Two errors keep their meaning:

- A `404 Not Found` answer means that the item is gone from Plex, and
  autorefresh removes it from the cache.
- An error that makes the server unavailable stops the server for the run.

#### TMDB fallback

If you set `tmdb-token`, a fix match gets a second pass. All of these
conditions must be true for the second pass:

- No search of the first pass found a candidate.
- The paths hold no `tmdb` ID. Plex already searched for such an ID.

The second pass asks TMDB for the TMDB ID of each `imdb` ID. For a series,
it also asks for the TMDB ID of each `tvdb` ID. TMDB has no movies for TVDB
IDs, so a movie gets no lookup for a `tvdb` ID. Autorefresh then searches
Plex once for each TMDB ID, for example `tmdb-1396`, with the same rules for
the candidates.

- If TMDB has no single entry for an ID (none, or several), autorefresh
  skips that ID. If no ID gives a match, the item gets a plain refresh, or
  a series first gets the [title fallback](#title-fallback).
  Autorefresh does not remember that TMDB had no single entry, because
  TMDB can change, so it asks again at the next attempt of the item.
- Autorefresh keeps each TMDB ID that it finds in the cache of the item, so
  it asks TMDB for an ID only once. The cache removes these IDs with the
  item.
- If a TMDB request fails, autorefresh logs a `TMDB lookup failed` warning
  and the item gets a plain refresh, as without a `tmdb-token`. The rest of
  the run on that server asks TMDB nothing more, so a rejected token sends
  one failing request in each run, not one for each item. The next run asks
  TMDB again.

The debug line of each second pass search has a `from` field with the ID
that TMDB translated, for example `tvdb-81189`.

The decision of the fix match records what the TMDB steps did not do, and
the Fix match dialog shows it:

- each ID that TMDB has no single entry for (none, or several), for
  example `tvdb-357542`, in the second pass or in the year check
- why TMDB was not asked: the paths hold a `tmdb` ID, they hold no ID that
  TMDB translates, such as a movie with only a `tvdb` ID, no `tmdb-token` is
  set, or an earlier TMDB failure in the same run turned TMDB off. The first
  reason that applies, in this order, is recorded.

#### Title fallback

A folder name can hold a wrong ID, such as an IMDb ID with a missing digit,
or no ID at all. Then no ID search finds the series. If you set
`tmdb-token`, a series gets one more pass. All of these conditions must be
true:

- The item is a series. A movie has no episodes to confirm a title, so a
  movie never gets this pass.
- The item has a year in Plex.
- The series folders do not hold different years, such as `(2012)` and
  `(2020)`, which can mean that one item holds two series.
- No search of the passes above found a candidate, or the paths hold no ID.
- No TMDB failure in the same run turned TMDB off.

Autorefresh then searches Plex with the title and the year of the item.
Plex returns many series for a title search, so the search alone proves
nothing. Autorefresh applies a match only if all of these are true:

1. Exactly one candidate has the same title as the item, ignoring case,
   spaces, and punctuation, and a year that differs by 1 or less from the
   year of the item and from the year of the series folder, such as
   `(2012)`, if it has one. Symbols such as `+` count, and a title without
   letters or digits matches nothing. Other candidates get the verdict
   "other title or year".
2. The Plex metadata provider gives one TMDB ID for that candidate. If it
   gives several different ones, nothing confirms the candidate. If the
   folder names give TMDB IDs, each of them must be that ID: each `tmdb`
   ID, and the TMDB ID that TMDB gives for each `imdb` ID and each `tvdb`
   ID. An ID that TMDB has several entries for names no single series.
   Otherwise the conflict is `folder id differs`.
3. Every episode number in each local regular season exists in that season
   in TMDB, with no extra or duplicate numbers, and at least 90% of TMDB's
   episodes across the local regular seasons are present locally, as in the
   year check above. Otherwise the conflict is `not confirmed`, and the
   decision keeps the first season with extra or invalid numbers, or the
   first season missing episodes when coverage is too low.

The match request has the action `title-match`. If the provider does not
know the candidate (`404 Not Found`), or gives it no TMDB ID, the conflict
is `not confirmed`. A failed title search,
provider request, episode read, or TMDB request is a failed decision: the
item gets a plain refresh, and the run goes on. Only a `404 Not Found`
answer of the title search or of the episode read keeps its meaning: the
item is gone.

### History

Autorefresh keeps a history in the database. The API of the
[dashboard](#dashboard) shows it. The history has these records:

- An event for each refresh or match request, with the time, the action, and
  the result. A failed request is an event too, with its error. Each event keeps the title, year, path, and images of the item, so it stays
  readable after the item leaves the cache.
- For each fix match, one decision event. It has each Plex search, each
  candidate that a search returned, and the verdict on each candidate. A
  match search that fails before autorefresh sends a request is a decision
  event with the outcome `failed`.
- An event when an item becomes complete. If a request that Plex accepted
  came before, the event names the last action and the last match that fixed
  the item.
- An event when an item is gone from Plex.
- One run record for each server in each check, with its status, its times,
  and its counts. The status is `running`, `finished`, `failed`, `canceled`,
  or `interrupted`.

Autorefresh also keeps the images and the path of each item that it reads
from Plex. It keeps the title, type, agent, and language of each library.

At the start of each run, autorefresh removes the events that are older
than `history-retention` and the runs that ended longer ago than that. It
keeps a run that is still `running`. A removed library loses its events,
and a removed server loses its runs. If a process stops during a run, the run
stays in the status `running`. When the scheduled service starts, it marks
each such run as `interrupted`. A process with `--once` or `--dry-run`
leaves these runs as they are, because it can run beside the service.

A dry run records no history and removes none (see [Dry run](#dry-run)).

### No library-wide refresh

Autorefresh refreshes one item at a time. It never sends a library-wide
refresh, which is the Plex "Refresh All Metadata" action on a library.

## Configuration

The configuration is a YAML file, by default `config.yml` in the default
configuration directory.

```yaml
schedule: "0 * * * *"         # cron expression for each run
minimum-age: 24h              # skip items added less than this long ago
refresh-interval: 24h         # wait at least this long between attempts on one item
max-refresh-interval: 168h    # the longest wait between attempts after the backoff
cache-refresh: "0 3 * * 0"    # cron expression for full reads of each library
oversample: 48h               # each incremental read also gets changes from this far back
refresh-delay: 5s             # wait between two due items on one server
max-refreshes-per-run: 100    # the most due items in each run on each server; 0 for no limit
ignore-reasons: []            # reasons that never cause a refresh, for example [no-art]
tmdb-token: ""                # optional; the TMDB API Read Access Token for the fix match fallback
history-retention: 720h       # how long the dashboard history is kept

ui:                           # optional; the dashboard, see "Dashboard"
  listen: ""                  # host:port, for example ":7380"; empty or omitted for no dashboard
  username: ""                # optional; set both for HTTP Basic auth
  password: ""

plex:
  - name: main                    # label for logs and the database
    url: https://plex.example.com
    token: XXXX
    timeout: 30s                  # optional; the limit of each request to this server
    libraries:                    # optional; if omitted, all movie and show libraries
      - Movies
      - TV
```

| Key | Required | Default |
| --- | --- | --- |
| `schedule` | no | `0 * * * *` |
| `minimum-age` | no | `24h` |
| `refresh-interval` | no | `24h` |
| `max-refresh-interval` | no | `168h` |
| `cache-refresh` | no | `0 3 * * 0` |
| `oversample` | no | `48h` |
| `refresh-delay` | no | `5s` |
| `max-refreshes-per-run` | no | `100` |
| `ignore-reasons` | no | none |
| `tmdb-token` | no | none |
| `history-retention` | no | `720h` |
| `ui.listen` | no | none, so no dashboard |
| `ui.username` | no | none |
| `ui.password` | no | none |
| `plex` | yes, at least one server | none |

`schedule` and `cache-refresh` are cron expressions with 5 fields, or
descriptors such as `@hourly`. They use the local time zone, which the `TZ`
environment variable sets. Choose a `cache-refresh` time at which
`schedule` also starts a run, because the full read happens in the first run
at or after that time.

Durations use Go units such as `h`, `m`, and `s`, so write 7 days as
`168h`. `refresh-interval` must be at least `1ms`. `max-refresh-interval`,
`minimum-age`, `oversample`, and `refresh-delay` must be 0 or more.
`max-refreshes-per-run` must be 0 or more, and 0 means no limit.
`history-retention` must be positive. An unknown
name in `ignore-reasons` is an error. These keys apply to all servers.

The `ui` keys configure the dashboard. [Dashboard](#dashboard) gives their
rules.

Each entry in `plex` is one Plex server:

- `name` is required and must be unique. Logs and the database use it, so a
  new `url` keeps the state of the server.
- `url` is required and must use `http` or `https`. It can have a path,
  such as `https://example.com/plex` behind a reverse proxy, but no
  userinfo, no query, and no fragment.
- `token` is required. It is the Plex token of the server.
- `libraries` is optional. It selects libraries by their title.
- `timeout` is optional. It limits each request to the server. The default
  is `30s`, and the value must be more than 0. If the answer of a request
  does not arrive in time, autorefresh skips the server for the rest of the
  run. A refresh or match request has no content, so its answer is the
  status. Set a longer timeout for a large server that answers slowly.

With `http`, the token crosses the network in clear text. If the connection
leaves a trusted network, use `https`.

`tmdb-token` is the "API Read Access Token" from the API page of your TMDB
account, not the shorter API key. Autorefresh sends it only to TMDB, in a
request header. Without it, autorefresh sends no request to TMDB.

An unknown key is an error. If the configuration file is missing or empty,
autorefresh waits for it and checks again every 5 seconds. A container can
then start before `config.yml` exists. A file that appears during the wait
is read only after it stays the same for one check, so autorefresh does not
read a file that is still being written. If the file changes while
autorefresh reads it, autorefresh waits again. SIGINT or SIGTERM during the
wait stops autorefresh without an error. If the path is a directory,
autorefresh stops with an error. Docker creates such a directory when you
bind-mount a file that does not exist.

## Command line

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--config` | `AUTOREFRESH_CONFIG` | `config.yml` in the default configuration directory | Configuration file path |
| `--database` | `AUTOREFRESH_DATABASE` | `autorefresh.db` in the default configuration directory | Database file path |
| `--dry-run` | `AUTOREFRESH_DRY_RUN` | off | Read Plex and update the cache, but send no refresh or match requests, and record no history |
| `--once` | `AUTOREFRESH_ONCE` | off | Run one check at once and exit, instead of waiting for the schedule |
| `--log` | `AUTOREFRESH_LOG` | `activity.log` in the default configuration directory | Log file path |
| `-v`, `--verbosity` | `AUTOREFRESH_VERBOSITY` | `0` | Log level: `0` is info, `1` is debug, `2` or more is trace |
| `--version` | none | none | Print version information and quit |

If the directory of the binary contains `config.yml`, the default
configuration directory is that directory. Otherwise it is `autorefresh` in
the user configuration directory, for example `~/.config/autorefresh` on
Linux. Autorefresh creates that directory with access for its owner only,
because the configuration holds the tokens.

Two processes can use the same database, for example a `--once --dry-run`
check while the service runs. A process waits up to 30 seconds for the
other one to finish a write. An older version of autorefresh does not open
a database that a newer version migrated to a newer schema.

Without `--once`, autorefresh starts the first run at once, after the
token checks below. Then it starts a run at each `schedule` time. With
`--once`, autorefresh runs one check at once and exits. If the check logs an
error, such as a database error or an unavailable Plex server, the exit code
is 1.

If autorefresh gets SIGINT or SIGTERM, it stops the schedule and cancels the
busy run. The run stops before its next request to Plex. Autorefresh then
closes the database and exits.

If the dashboard runs, autorefresh stops it first. It then stops the
schedule, cancels manual jobs, and waits for pending actions before closing
the database.
The dashboard can take up to 10 seconds to stop, and the busy run goes on
during that time. A second SIGINT or SIGTERM ends autorefresh at once.

Without `--once`, autorefresh lists the libraries of each Plex server once
at the start, before the first run. A wrong token or URL then
shows at once:

- If the server accepts the token, autorefresh logs the number of
  libraries at info level.
- If the server rejects the token, autorefresh logs a `Plex server rejected
  the token` error.
- If the server does not answer, autorefresh logs a warning.

In each case autorefresh keeps the schedule, because Plex can still be
starting.

With a `tmdb-token`, autorefresh also checks the token with TMDB once at the
start. It logs `TMDB accepted the token` at info level, a `TMDB rejected the
token` error, or a `Checking TMDB failed` warning. In each case it keeps the
schedule.

## Dry run

Use `--dry-run` to see what a run will do before it changes anything. A dry
run does these things:

- It reads Plex and updates the cache.
- It sends match searches, which are reads, to choose the action of each
  item.
- With a `tmdb-token`, it sends the TMDB lookups of the fix match fallback
  and caches the TMDB IDs that they find. For the title fallback, it also
  asks the Plex metadata provider and TMDB, which are reads too.
- It logs one `Dry run` line at info level for each due item. The line has
  the server, the library, the title, the reasons, and the action, which is
  `refresh`, `match`, `tmdb-match`, or `title-match`.
- It applies `max-refreshes-per-run` to the due items.

A dry run sends no refresh or match request and does not wait
`refresh-delay`. It records no attempts, so a later real run refreshes the
same due items.

A dry run also records no history: no event, no decision, and no run. It
removes no old history and no cache of removed servers or libraries. It
logs one `Dry run keeps cache of removed libraries` line for each server
instead. It still updates the cache and the read times, and it still keeps
the item images and paths, and the library details. When a dry run reads an item
that is complete or gone from Plex, it removes the item from the cache. A
later real run then finds no item, so it records no completion or gone event
for it. Run `--dry-run` only to check what a run will do.

```sh
autorefresh --once --dry-run
```

## Dashboard

![The Items dashboard with the Attention queue, poster artwork, and the selected item's details and history](docs/screenshots/items.png)

Screenshots show the running dashboard with sample library data and generated artwork.

<details>
<summary>More dashboard screenshots</summary>

**Status** shows server health, live progress, recently fixed items, and recent runs.

![Status dashboard showing two servers, a running scan, and recent fixes with posters](docs/screenshots/status.png)

**Fix match** shows candidate posters, Plex scores, and evidence from the last automatic match attempt.

![Fix match dialog with candidate artwork, title and folder ID evidence, and the last automatic decision](docs/screenshots/fix-match.png)

**Runs** shows run history and results across servers.

![Runs table showing manual and scheduled runs, results, and one failed run](docs/screenshots/runs.png)

**Items on a phone** keeps the tabs, filters, and posters in a single column.

<img src="docs/screenshots/phone.png" alt="Phone layout of the Items dashboard with filter controls and the Attention queue" width="390">

</details>

Autorefresh can serve a dashboard over HTTP from the same process. The
dashboard has a JSON API under `api/`. The API serves the live status of
each run, the incomplete items, the fixed items, and the history of each
item and each run. It can refresh an item, apply a Plex match, check its
metadata, and stop or resume automatic work on it.

The API can also start a manual scan or refresh selected items in bulk.
Bulk refresh sends requests to individual items and uses `refresh-delay`
between requests. While a run is busy, the API refuses a new manual scan
or bulk refresh.
If a bulk stop or resume fails partway through, earlier changes stay saved.

The dashboard has three pages. With several Plex servers, a server picker in
the top bar limits every page to one server or shows all servers. The
address keeps the choice, so a link or a reload shows the same server.

- **Status** shows a card for each Plex server: its connection, the live
  progress of a run, and the result of the last run. While a run or a bulk
  refresh works on its items, a panel under the progress bar shows the
  item in work with its poster and library, marked **Working**. For a run,
  it also shows why the item is due.
  During the wait between two items, it shows the next item, marked **Up
  next**, with the seconds until its request. Under that, it shows the last
  finished item, its outcome, and how long ago it finished. It also lists the
  items that were fixed recently and the recent runs. **Run now** starts a
  run. Its menu also has **Full cache refresh, then run**, which reads
  every library in full before it refreshes the due items. If it cannot
  read a library, the run of that server fails and refreshes no item. The
  runs of the other servers continue. With several servers, it can start either kind of run for one server or for all
  servers. A manual run or bulk refresh has a **Cancel** button. The item
  count on a server card opens the items of that server. The header shows
  when the next scheduled run starts, for example "Next run in 33m ·
  23:00". The Runs page shows the same line. Each server card shows when
  the next full cache refresh of one of its libraries is due. Each library
  has its own due time, the next `cache-refresh` time after its last full
  read, and the card shows the earliest one. A new library, or a
  `cache-refresh` schedule without a next time, is due at once. The first
  run at or after a due time reads that library in full.
- **Items** lists the items in three tabs: **Attention** (items that
  autorefresh still tries to fix), **Fixed**, and **Stopped**. You can
  search by title or path and filter by library and type. Attention and
  Stopped also filter by reason. On Attention, a switch includes the items
  whose reasons are all ignored. On Fixed, a separate switch includes the
  items that Plex completed without a request.
  **List** mode shows the posters and the item details beside the list.
  Its **Sort** menu sorts by the same columns as Table mode, such as tries
  or next due. A new sort starts in its natural direction, for example the
  most tries first, and the arrow button beside it reverses it. Both modes
  share the sort of each tab. **Default order** lists the items with the
  most tries first on Attention, the most recently stopped first on
  Stopped, and the most recently fixed first on Fixed.
  **Table** mode shows the items as text rows. It renders only the rows on
  screen, so it stays fast with many items. It can sort the items and
  select several items, or every item that matches the filters, for a bulk
  refresh, stop, or resume. A row opens the item details on a click, or on
  Enter or Space. A bulk action on every matching item sends one request
  for each server. A server without matching items is skipped, and the
  other servers still run. A bulk refresh of items on several servers runs
  one server after another, because only one run works at a time. The
  Status page shows which servers wait, and **Cancel the rest** stops the
  running server and skips the others. **Cancel** on the card of the
  running server also skips the others. If the dashboard misses the end of
  a server's job, for example after a restart of autorefresh, it reads the
  result from the stored runs. An interrupted job, or a job whose result
  it cannot find, stops the rest of the queue and shows an error. A reload
  of the page forgets the servers that have not started. The item details
  show the poster, the art, the summary, the path, the queue state, the
  last action, and when and how the item was fixed. From there you can
  refresh the item now, stop or resume automatic work on it, fix its
  match, and open it in Plex. **Refresh now** handles the item as a run
  handles a due item. An unmatched item in the cache gets the automatic
  fix match, also before its first refresh, unless `unmatched` is in
  `ignore-reasons`, and its decision is recorded in the history. If the
  decision picks a candidate, autorefresh applies that match and follows
  it up as **Match** in the Fix match dialog does. Otherwise, also when the
  search failed, autorefresh requests a refresh, and then checks an item
  in the cache after waits of 1, 2, and 4 seconds. An item that a check
  finds complete moves to the Fixed tab at once. A message tells you the
  match that was applied and what the check found, or only that Plex
  accepted the request if no check could read the item. If Plex no longer has
  the item, the message says so and autorefresh removes the item from the
  queue. Each request counts as an attempt of the item, so it restarts
  the wait before the next automatic refresh.
  Below the actions, the history of the item lists each event, newest
  first: requests by autorefresh or by hand and whether Plex accepted them,
  fix match decisions with their searches, stops and resumes, and when
  the item was fixed or removed. An item that autorefresh no longer knows,
  because its history expired, says so without its old details, and other
  load errors offer **Try again**. On a phone, the list fills the screen
  and an item opens on its own screen.
- **Runs** lists the runs of every server, or of the chosen server, newest
  first: the start, the duration, the server, the source (schedule or
  manual), the kind, the status, and the items read,
  refreshed, matched, failed, fixed, and due, with the error of a failed
  run. The kind counts the library reads that the run completed: **Full
  run** when each was a full read, **Incremental run** when each read only
  the changes since the last read, **Full and incremental run** when it
  did both, and **Bulk refresh** for a bulk refresh. A run that failed or
  was canceled counts only the reads that it completed. A run that
  completed no library read shows **Run**, as do older runs, recorded
  before autorefresh counted its full and incremental reads. A run that
  has not ended shows no duration. **Load more** reads the next page.
  The list updates when a run starts or ends. On a narrow screen, each run
  is one row with its summary.
- A click on a run opens its details in a drawer, also from **Recent
  runs** on the Status page. In the table, Enter or Space on a row opens
  it too. The head of the drawer, with its close button, stays in view
  while the items scroll. On a narrow screen, the details replace the
  list, as on the Items page. The details show the kind, the status, the start, the
  duration, the libraries read, the error, and the counts. The counts of
  refreshes and matches are the requests sent, so they include the
  requests that failed. Below, the details list the items of the run by
  outcome: **Refreshed** and **Matched** for the requests that Plex
  accepted, **Failed** for rejected requests and failed fix match
  searches, **Fixed**, **Completed by Plex**, **Removed**, and **Not
  sent** for a fix match decision whose request the end of the run
  stopped or that a later decision of the item replaced. While the run
  works, a decision whose request is next is **In progress**. Each item shows its library and what happened to it, such as
  the fix match decision before a request, and opens its item details. A
  bulk refresh lists its selected items as **Refreshed**, **Failed**,
  **Removed**, **Skipped**, and **Not reached**, each with its poster and
  library when autorefresh still knows the item. Runs from before this
  version recorded no items, so their details say that the item details
  were not recorded. The read and due counts stay numbers. A running run
  shows its counts when it ends, and reloads its items every 5 seconds,
  also after a reload failed. A
  bulk refresh shows its items when it ends. Each run has its own link,
  `#/runs/<id>`.

**Fix match** in the item details opens a dialog to choose the Plex match
of the item by hand, also for an item on the Fixed tab that Plex matched
wrongly. On open, the dialog searches Plex by the title of the item and by
each ID in its folder and file names. With a `tmdb-token`, and when the
folder and file names hold no `tmdb` ID, it also searches the TMDB IDs of
their `imdb` IDs, and of their `tvdb` IDs for a series, as the automatic
fix match does. You can also search by another title, with an optional year,
or by an ID such as `tmdb-48213`, `tvdb-31207`, or `imdb-tt7712040`. An ID
search ignores the year. Each candidate shows its poster, title, year,
type, summary, and Plex score (when Plex gives one), and how it was found:
the folder IDs that found it, the year compared with the item, and the
searches of the last automatic fix match that found it. The current match of the item in Plex
is marked, and you cannot apply it again. For a series, the header shows
the seasons and episodes that Plex has for the item, and each series
candidate with a `plex://` GUID shows its seasons and episodes when the
Plex metadata provider gives them, with a check of its episode count
against the item. Autorefresh asks the provider once for each candidate
while the page is open and autorefresh keeps running, also when the
answer failed or the dialog closed before it came. A series candidate with a TMDB ID can compare the
episode numbers of the item with that TMDB series. The TMDB ID comes from
a search by one TMDB ID that found the candidate as its only candidate, if
no search by that TMDB ID found another candidate, or else from the
metadata provider, so a candidate of a title search can compare too. This
needs a `tmdb-token`. When the episode numbers do not confirm the series, the
comparison names the season that kept it from confirming (the first season with
extra or invalid numbers, or the first season missing episodes when coverage is
below 90%), the episode counts of Plex and TMDB, and the
episode numbers that only one of them has. The comparison changes nothing. **Match** asks before it
applies the candidate. After Plex accepts the match, autorefresh also
requests a refresh of the item. The refresh counts as one more attempt of
the item, and as an accepted refresh it lengthens the wait before the next
automatic refresh, like any refresh. Then, for an item in the cache, autorefresh
reads the item from Plex again after waits of 1, 2, and 4 seconds, while
Plex loads the new metadata. An item that a check finds complete
moves to the Fixed tab at once. Otherwise the next run reads it again.
If the server is unavailable for the refresh or a check, autorefresh
stops checking.
Beside the candidates, the dialog explains the last automatic fix match:
its searches, their candidates, and why it picked one or none. A year
conflict lists the years that it compared and the season whose episode
numbers differ from TMDB, with the episode numbers that only Plex or only
TMDB has. The TMDB search of the year check says "same
item as `tmdb-<id>`" when Plex resolves that TMDB ID to the same
candidate, which does not yet confirm the year. The panel also names the
IDs that TMDB has no single entry for, and why TMDB was not asked. When the
history holds no automatic fix match of the item, **Try automatic fix
match** runs one now as a preview. The preview uses the same code as a
run, also for an item that a run would not try, and shows its decision in
the same way. It asks TMDB even when a TMDB lookup failed earlier in a
run, as the next run does. It marks the candidate that it picked, which
you can then apply with **Match**. For an unmatched item in the cache,
**Refresh now** in the item details also runs the automatic fix match
again and applies its pick, unless `unmatched` is in `ignore-reasons`. The pick gets a card even when the
opening search of its folder ID failed. The dialog keeps the last preview
of the item while the page is open, so it shows it again, with **Run
again**, when you reopen the dialog. A reload of the page, or a restart of
autorefresh, drops it. If a
run records an automatic fix match after the preview, the dialog shows
the newer of the two. Both keep whole seconds, so in the same second the
recorded decision wins. The preview records nothing, saves nothing to the
cache, and sends no match to Plex. Searches in the dialog call Plex but
record nothing. An applied match is recorded in the history of the item.

The dashboard asks before it stops work on an item, applies a match, or
refreshes items in bulk. It updates live while the event stream is
connected. It shows a banner while the stream is down, also when the first
connection fails.

### Dashboard configuration

```yaml
ui:
  listen: ":7380"
  username: admin
  password: change-me
```

- `listen` is the address of the dashboard, as `host:port`. If `listen`
  is empty or the `ui` section is missing, autorefresh starts no dashboard.
  There is no default port. This README uses `7380`. `:7380` listens on
  each network interface. `127.0.0.1:7380` listens only on the local
  machine. Use it on a host without Docker, behind a reverse proxy on the
  same machine.
- `username` and `password` turn on HTTP Basic auth for each request: the
  page, the API, the event stream, and the images. Set both, or set neither. The username must not
  contain `:` or a control character. The password must not contain a
  control character.

If autorefresh cannot listen on the address, for example because another
program uses the port, it stops at the start with a `Failed starting UI
listener` error. It sends no request to Plex before that.

With `--once` or `--dry-run`, autorefresh starts no dashboard and logs `UI
is off with --once or --dry-run`. A dry run can then run beside the
service without a conflict on the port.

### Access and security

Without a username and password, the dashboard has no authentication.
Autorefresh then logs a warning at the start. Let only an authenticating
reverse proxy or a trusted network reach the port.

Basic auth sends the password in each request. Over plain HTTP, it crosses
the network in clear text. If the connection leaves a trusted network, put
a reverse proxy with HTTPS in front of the dashboard.

The dashboard rejects change requests from other sites. If a reverse
proxy serves the dashboard over plain
HTTP, the proxy must pass the original `Host` header. Otherwise a change
request from the dashboard looks like a request from another site, and the
dashboard rejects it.

Each link of the dashboard is relative, so a reverse proxy can serve the
dashboard at a sub-path, such as `/autorefresh/`. The address of the page
must end with `/`. Let the proxy redirect `/autorefresh` to
`/autorefresh/`.

The dashboard never sends the Plex token to the browser. It loads posters
and art from Plex for the browser. In stored error texts, it replaces each
Plex token, Plex address, Plex URL path, the TMDB token, and the dashboard
password with `<redacted>`. It leaves out the poster of a match candidate
when the poster URL holds a Plex token, the TMDB token, or the dashboard
password, because the browser loads that poster from another site.

To show the counts and the TMDB ID of a series candidate in the Fix match
dialog, and for the [title fallback](#title-fallback) of a run, autorefresh
sends the Plex token of the server to the Plex metadata provider,
`metadata.provider.plex.tv`, in the `X-Plex-Token` header, with the
`plex://` GUID of the candidate. It sends nothing else there, and it
refuses a redirect to another host.

### Dashboard in Docker

Set `listen: ":7380"` in `config.yml`, and publish the port:

```sh
docker run -d --name autorefresh \
  --user 1000:1000 \
  -e TZ=Europe/London \
  -p 7380:7380 \
  -v /opt/autorefresh:/config \
  l3uddz/autorefresh:main
```

In compose, add the port to the service:

```yaml
    ports:
      - 7380:7380
```

The image declares port `7380`. Nothing listens on it until you set
`ui.listen`.

### API

| Path | Content |
| --- | --- |
| `api/status` | The live status: the health of each server and TMDB, and the progress of each run. `boot` names the process and `seq` counts its changes, so a client can order snapshots |
| `api/events` | A stream of server-sent events with the live status after each change |
| `api/servers` | The configured servers, their libraries, and the ignored reasons |
| `api/items` | One page of the items of a tab, `attention`, `fixed`, or `stopped`, with the count of each tab |
| `api/item` | One item and its history, newest event first |
| `api/item/live` | What Plex tells about one item now |
| `api/candidate` | The TMDB ID and the season and episode counts that the Plex metadata provider gives for the `plex://` GUID of a series candidate, in the `guid` query parameter, asked with the token of the server `srv` |
| `api/item/auto-match` | The automatic fix match preview of one item, as a match decision event. A decision event can also have the optional fields `itemYear`, `tmdbId`, `tmdbSkip`, `tmdbMissing`, and `episodeDiff`, which the API leaves out when they are empty |
| `api/item/episode-check` | `POST`: compares the episode numbers of one series with the TMDB ID in the `tmdb` query parameter. `diff` names the season that kept the comparison from confirming (extra or invalid numbers, or missing episodes below 90% coverage), with the counts and the episode numbers that only TMDB (`missing`) or only Plex (`extra`) has |
| `api/items/thumb`, `api/items/art` | The poster and the background art of one item |
| `api/runs` | One page of the runs, newest first |
| `api/runs/detail` | One run with the events that it wrote, or the items of a bulk refresh |

The API names a server by its position in the `plex` list, from 0, in the
`srv` query parameter. On `api/items`, `api/item`, `api/item/live`,
`api/candidate`, `api/items/thumb`, `api/items/art`, `api/runs`, and `api/runs/detail`, a
query string that does not decode, for example one with a bad percent
escape, gets a 400 answer.

## Logging

Autorefresh writes each log line to stderr and to a log file. When the log
file reaches 5 MB, autorefresh starts a new file. It keeps 5 old files and
deletes files after 14 days.

| Level | Content |
| --- | --- |
| `fatal` | Problems that stop the program at the start, such as an invalid configuration or a database that does not open |
| `error` | Problems that stop a run or the checks of a server, such as a database error or a Plex server that does not answer, and a token that a Plex server rejects at the start |
| `warn` | Problems that autorefresh survives, such as a failed request, a match conflict, a skipped library, a skipped scheduled start, or an item that reached the longest wait |
| `info` | The start, each Plex server that accepts its token at the start, each request that autorefresh sends, each item that becomes complete, each removed library cache, and one summary line for each server at the end of a run |
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

Before the summary, autorefresh logs one `Item complete` line for each item
that the reads of the run, or the checks after its matches, found complete. The line has the server, the
library, the title, and the number of refreshes. These lines come after the
refresh step, so they stay next to the summary in the log. A run without a
summary line also logs them.

If Plex accepted a request for the item, the line also has these fields:

- `last_action`: the last request that Plex accepted, `refresh`, `match`,
  `tmdb-match`, or `title-match`.
- `last_match`: the last match request that Plex accepted, `match`,
  `tmdb-match`, or `title-match`. A later refresh does not replace it, so an item that a match
  linked and a refresh then completed still shows the match.

These fields show the last requests before the read found the item
complete. They do not prove that the request fixed the item, because Plex
can also fill in metadata by its own refresh. Versions up to 0.4.1 did not
record the kind of a request, so an item whose requests came only from
such a version has neither field.

The summary line has these fields:

| Field | Content |
| --- | --- |
| `full_read_items` | The movies and series that the full reads of the run listed. If the run did no full read, the value is 0. |
| `incremental_read_items` | The movies and series that the incremental reads of the run listed. If the run did only full reads, the value is 0. |
| `incomplete_items` | The incomplete items in the cache after the run, without the items whose reasons are all ignored. An item that the run refreshed stays in this count until a later read finds it complete, unless a check after its match found it complete. |
| `due_items` | The incomplete items that were due for a request |
| `refresh_requests` | The refresh requests that the run sent, also failed ones |
| `match_requests` | The fix match requests that the run sent, also failed ones |
| `failed_requests` | The failed requests, match searches, and TMDB lookups. A `404 Not Found` answer of Plex is not a failure. |
| `remaining_items` | The due items that the run did not reach, because of `max-refreshes-per-run` or because the server became unavailable. They stay due for later runs. |
| `completed_items` | The items in the cache that the reads of the run, or the checks after its matches, found complete. The run removes them from the cache. Each one also has an `Item complete` line. |
| `completed_after_request` | The items in `completed_items` that had at least one request from autorefresh that Plex accepted. Plex runs a refresh in the background, so the effect of a refresh usually shows in a later run. A match can show in the same run, through the checks after it. |
| `completed_after_match` | The items in `completed_items` that had a match request that Plex accepted. Each one has a `last_match` field on its `Item complete` line. |
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

The image does not run as root. Its default user is `65532:65532`. Set your
own user and group with `--user`, or with `user:` in compose. That user must
own the directory that you mount at `/config`, because autorefresh writes
the database and the log file there.

```sh
docker run -d --name autorefresh \
  --user 1000:1000 \
  -e TZ=Europe/London \
  -v /opt/autorefresh:/config \
  l3uddz/autorefresh:main
```

```yaml
services:
  autorefresh:
    image: l3uddz/autorefresh:main
    container_name: autorefresh
    restart: unless-stopped
    user: 1000:1000
    environment:
      - TZ=Europe/London
    volumes:
      - /opt/autorefresh:/config
```

If an earlier image ran as root, root owns the files in `/config`. Before
you start the new image, give them to the user that the container runs as.
If you set `--user 1000:1000` or `user: 1000:1000`, as in the examples:

```sh
sudo chown -R 1000:1000 /opt/autorefresh
```

If you set no user, the container runs as the default user:

```sh
sudo chown -R 65532:65532 /opt/autorefresh
```

If you mount a named volume at `/config` instead of a directory, change the
owner inside the volume. This example uses the volume `autorefresh` and the
default user. Use your own volume name and user:

```sh
docker run --rm -v autorefresh:/config alpine chown -R 65532:65532 /config
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

You need [mise](https://mise.jdx.dev). It installs the Go, goreleaser,
Node.js, and pnpm versions from `mise.toml`.

```sh
mise install
mise run build
```

The binary is in `dist/autorefresh_<os>_<arch>/`. Run `mise tasks` to list
the other tasks.

`mise run build` first builds the dashboard frontend in `web/` from the
locked dependencies, and then builds the binary with the `webui` build tag,
which embeds the frontend. A plain `go build` needs no Node.js. Its
dashboard page only says that the UI is not built, and the API still works.

## License

[MIT](LICENSE)
