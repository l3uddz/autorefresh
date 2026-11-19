# Autorefresh design

Date: 2026-11-19. Issue: #7.

This document records the agreed design of autorefresh. It is the input for
the implementation plan. The owner agreed the decisions in a design
discussion on 2026-11-19. The Plex facts come from read-only requests to the
owner's Plex server on the same day. The owner also approved some
single-item refreshes and one fix match.

## Intent

Plex sometimes keeps items with incomplete metadata. An item can stay
unmatched, or it can have no summary, no poster, or no background art. The
owner found that a refresh of the single item often fixes this. Sometimes the
refresh works only after several tries over some days, because the metadata
provider does not have the data yet. Some unmatched items need a fix match,
which links the item to a specific entry in the Plex metadata database.

Autorefresh does this work on a schedule. It finds incomplete movies and
series and refreshes each one again until the metadata is complete. When a
plain refresh does not match an item, and the file path contains metadata
IDs, autorefresh uses those IDs to fix the match.

## Goals

- Find incomplete movies and series in every Plex server in the configuration.
- Refresh each incomplete item, one item at a time, until it is complete.
- If the file path of an unmatched item contains IDs that identify one
  Plex entry, fix the match of that item.
- Keep a local cache, so that most runs read only the items that changed.
- Let the owner limit the load on Plex: the schedule, the delay between
  refreshes, and a limit for each run.
- Offer a dry-run mode that logs the actions of a run but does not send them.

## Non-goals

- Autorefresh has no link to autoscan. It talks only to Plex.
- Autorefresh does not support Emby or Jellyfin.
- Autorefresh does not refresh seasons or single episodes.
- Autorefresh has no web interface and no HTTP API.
- Autorefresh does not limit the number of refresh attempts for an item.

## Anti-goals

Each of these results is a failure of the project, also with working code:

- Autorefresh starts a library-wide refresh. This is the Plex "Refresh All
  Metadata" action on a library section. Autorefresh must never send it.
- Autorefresh matches an item to the wrong title.
- Autorefresh sends so many requests that Plex becomes slow for viewers.
- A token or server address appears in a log line or an error message.

## Plex facts

The investigation used Plex Media Server 1.43.5. All requests send the
token in the `X-Plex-Token` header and ask for JSON with
`Accept: application/json`, as the autoscan Plex client does.

### Libraries and item types

`GET /library/sections` lists the libraries. Each entry has `key` (the
section ID), `type` (`movie` or `show`), `title`, `agent` (for example
`tv.plex.agents.movie` or `tv.plex.agents.series`), and `language`.

`GET /library/sections/<id>/all?type=<n>` lists one item type. The types are
1 for movie, 2 for show (series), 3 for season, and 4 for episode.

The owner's largest library has 11,849 series, 29,257 seasons, and 367,868
episodes. The largest movie library has 29,160 movies.

### Paging and filters

- `X-Plex-Container-Start` and `X-Plex-Container-Size` select a page. Each
  response has `totalSize`. A size of 0 returns only the count.
- `sort=addedAt:desc` sorts the result.
- `unmatched=1` returns unmatched items. `unmatched=0` returns matched items.
- `summary=` with an empty value returns the items with an empty summary.
  Plex does not list this filter in its field list, so it is undocumented.
  The client must also check the summary of each returned item.
- `addedAt<<=-1d` and `addedAt>>=-1d` filter on relative dates.
  `updatedAt>>=<unix time>` returns the items that changed since that time.
  `episode.addedAt>>=-1d` on type 2 returns series that got new episodes.
- Filters with `==` returned no items in all tests. Do not use them.
- There is no filter for artwork. `thumb=` and `art=` return status 500.
- `excludeElements=Media,Role,Director,Writer,Genre,Country,Image` makes a
  page about 25% smaller. Movie listings need `Media` for the file paths.

### How incomplete items look

- An unmatched item has a guid of the form `local://<ratingKey>`. A matched
  item has a guid of the form `plex://movie/<id>`, `plex://show/<id>`, or
  `plex://episode/<id>`. The owner said that `local://` means that the
  metadata is missing.
- A missing summary is an empty string in `summary`.
- A missing poster or background art is an absent or empty `thumb` or `art`
  field.
- Unmatched series have no `thumb`, no `art`, no summary, and no
  `updatedAt`. Unmatched movies still have a poster and art, probably frames
  from the video.
- Matched items can also miss artwork. Of the matched items added in the
  last 60 days, 16 movies and 7 series had no background art.
- Most seasons have no summary. 25,219 of 29,257 seasons have none. This is
  normal, so seasons are not checked.
- Episode thumbnails are usually frames from the video, so a missing
  episode thumbnail is not a useful signal.

### Refresh

`PUT /library/metadata/<ratingKey>/refresh` refreshes one item. Plex returns
200 with an empty body at once and does the work in the background. Plex
gives no completion signal, and the item does not appear in `/activities`.
If the refresh finds nothing new, `updatedAt` does not change.

Observed results:

- An unmatched series became matched within 6 seconds. It got a summary and
  a poster, and its seasons and episodes updated too.
- A matched series with 7 episodes without a summary: 4 seconds after one
  refresh of the series, all 50 episodes had a summary. So a series refresh
  also refreshes its episodes.
- A matched movie and a matched episode without a summary did not change.
  The provider did not have the data yet.
- An unmatched movie stayed unmatched. It needed a fix match.

### Match search and fix match

`GET /library/metadata/<ratingKey>/matches?manual=1&title=<source>-<id>&agent=<agent>`
searches the Plex metadata database by an external ID. The agent comes from
the library section. The response lists candidates in
`MediaContainer.SearchResult`, each with `guid`, `name`, and `year`.

- `title=tmdb-747059` returned exactly one movie candidate.
- `title=imdb-tt19120100` returned the same candidate.
- `title=imdb-tt13167288` returned no candidates. This ID was in the same
  file name, so IDs in a path can be wrong.
- `title=tvdb-485190` and `title=imdb-tt50940908` with
  `agent=tv.plex.agents.series` each returned the same series candidate.
- For one unmatched series, the TVDB ID search and the plain search returned
  no candidates. A fix match cannot help such an item. It needs further
  refreshes.

`PUT /library/metadata/<ratingKey>/match?guid=<guid>&name=<name>&year=<year>`
applies a match. Plex returned 200 with an empty body. Within 3 seconds, the
movie had the `plex://` guid, a summary, a poster, and art. The `ratingKey`
did not change.

### JSON shapes

- `ratingKey`, `grandparentRatingKey`, and the section `key` are JSON
  strings, for example `"1194652"`. `addedAt` and `updatedAt` are numbers
  (Unix seconds).
- A missing `thumb`, `art`, or `updatedAt` is an absent key, not an empty
  string or `null`. An absent `summary` did not occur. An empty summary is
  `""`.
- Listings return items in `MediaContainer.Metadata`. `MediaContainer`
  also has `size`, `offset`, and `totalSize`.
- A movie file path is at `Metadata[].Media[].Part[].file`. A series folder
  path is at `Metadata[].Location[].path` of the single-item response.
- Match search results are at `MediaContainer.SearchResult[]`, with the keys
  `guid`, `name`, `year`, `type`, `summary`, and `thumb`.

### Paths

Movie listings include the file paths in `Media[].Part[].file`. Series
listings do not include the folder paths. `GET /library/metadata/<ratingKey>`
for a series returns them in `Location[].path`.

The owner's paths contain IDs such as `{tmdb-747059}`, `{imdb-tt19120100}`,
`{imdbid-tt31289994}`, and `{tvdb-463649}`. Other users can write
`imdb:` or use another separator, and many users have no IDs in their paths.

### Timing on the largest library

| Request | Time |
| --- | --- |
| Count with a filter (`X-Plex-Container-Size=0`) | 1 to 2 seconds |
| Filtered page of 200 episodes at offset 6,000 | 0.4 seconds |
| Page of 500 movies added in the last 60 days | 0.75 seconds |
| Unfiltered page of 1,000 episodes (2.6 MB) | 5 seconds |
| Single item | 0.2 seconds |
| Refresh or match request | 0.2 to 0.5 seconds |

A full listing of all movies in three libraries takes about 95 seconds. A
full listing of all series in three libraries takes about 2 minutes. A full
listing of all episodes in the largest library takes about 30 minutes,
so autorefresh never lists all episodes.

## Approaches

The owner left the code structure to the builder's proposal and asked for a
standard Go layout. Three approaches were considered:

1. Chosen: one binary with small packages under `internal/` and a SQLite
   cache. This follows the autoscan patterns for the HTTP client, the
   datastore, and migrations.
2. Rejected: a stateless app that lists everything on each run. It has no
   record of the last refresh of an item. Without that record, the refresh
   interval and the cache are impossible.
3. Rejected: a JSON file in place of SQLite. Each change rewrites the
   whole file, and it breaks the autoscan pattern.

## What counts as missing

The owner agreed these rules.

| Type | Missing when |
| --- | --- |
| Movie | The item is unmatched (`local://` guid), or the summary, poster, or art is missing. |
| Series | The item is unmatched, or the summary, poster, or art is missing. |
| Series | Some matched episodes of the series have an empty summary. |
| Season | Never. |
| Episode | Never on its own. Episodes count only through their series. |

An episode without a summary causes a refresh of its series. Autorefresh
never refreshes a single episode.

Unmatched episodes belong to an unmatched series, so the series rule covers
them.

## How autorefresh finds items

Autorefresh checks every library of type `movie` with the agent
`tv.plex.agents.movie`, and every library of type `show` with the agent
`tv.plex.agents.series`. If a server has a `libraries` list in the
configuration, it checks only those libraries.

Autorefresh skips libraries with other agents and logs a warning once for
each of them. Libraries with the "Personal Media" agent have only `local://`
items. Without this rule, autorefresh refreshes all of their items for ever. The
investigation tested the guid forms, the match search, and the match only
with the two Plex agents above. This rule is the builder's call, because all
of the owner's libraries use these two agents.

### Full read

A full read lists every movie (type 1) or series (type 2) in a library, page
by page. Autorefresh evaluates each item with the rules above.

If a library has no full read yet, or its last full read is older than
`cache-refresh`, autorefresh does a full read of it.

### Incremental read

On other runs, autorefresh reads only the changes. Let T be the time of the
last read of the library minus `oversample`. Autorefresh then requests:

- items with `updatedAt>>=T`
- items with `addedAt>>=T`
- all unmatched items (`unmatched=1`), because unmatched series have no
  `updatedAt`

### Episodes without a summary

On every run, for every `show` library, autorefresh requests the episodes
with `type=4&unmatched=0&summary=&addedAt<<=-<minimum-age>`. It groups the
results by `grandparentRatingKey`, which is the series. Each series in the
result is incomplete for the reason "episodes without a summary". On the
owner's largest library, this is about 6,900 episodes in about 7 pages, from
762 series.

When a series no longer appears in this result, the reason is gone.

### Minimum age

Autorefresh skips items that Plex added less than `minimum-age` ago. Plex can
still be working on them. For movies and series, autorefresh compares
`addedAt` with the current time. For episodes, the filter does it.

## Refresh rules

- If an item is incomplete, older than `minimum-age`, and not refreshed in
  the last `refresh-interval`, the item is due. An item that was never
  refreshed is due at once.
- Autorefresh never gives up. It refreshes an incomplete item again after
  each `refresh-interval` until the item is complete or gone from Plex.
- Each run refreshes the due items in this order: first the items that were
  never refreshed, then the items with the oldest last refresh. Ties go to
  the item that Plex added first.
- Autorefresh sends one refresh at a time on each server. It waits
  `refresh-delay` between two refreshes on one server.
- If `max-refreshes-per-run` is set, a run sends at most that many refresh
  and match requests on each server. If it is not set, there is no limit.
- Autorefresh never sends a library-wide refresh.

### Fix match

Autorefresh tries a fix match only for an unmatched item that it refreshed
at least once before. A plain refresh alone fixed one unmatched series, so
the first attempt is always a plain refresh.

1. Autorefresh gets the paths of the item. For a movie, these are the file
   paths from the listing. For a series, it requests the item to get
   `Location[].path`.
2. It reads IDs from the folder names and the file names. It accepts the
   sources `imdb`, `tmdb`, and `tvdb`, also written with an `id` suffix, for
   example `imdbid`. It accepts any separator between the source and the
   value, such as `-`, `:`, `=`, or `_`. An IMDb value starts with `tt`.
   TMDb and TVDB values are numbers.
3. It searches Plex once for each distinct ID, with the agent and the
   language of the library section.
4. It applies a match in one case only: every ID that found something found
   exactly one candidate, and all of these candidates have the same guid. IDs that
   find nothing are ignored.
5. If the IDs find different candidates, or one ID finds more than one,
   autorefresh logs the conflict and sends a plain refresh.
6. If the path has no IDs, or no ID finds anything, autorefresh sends a
   plain refresh.

A match request counts as the refresh of the item for `refresh-interval`
and for `max-refreshes-per-run`.

## Cache and state

Autorefresh keeps a SQLite database, by default `autorefresh.db` in the
default configuration directory. It uses `modernc.org/sqlite` with one open
connection, and migrations as SQL files embedded in the binary, as autoscan
does.

The database holds:

- For each library: the server name, the section ID, the time of the last
  full read, and the time of the last read of any kind.
- For each incomplete movie or series: the server name, the section ID, the
  `ratingKey`, the title, the year, the type, the reasons, `addedAt`, the
  time of the last refresh, and the number of refreshes.

The keys use the server `name` from the configuration, so a change of the
server URL keeps the state.

When a read shows that an item is complete, autorefresh deletes its row.
It logs that the item is fixed and the number of refreshes that it took. A full
read deletes the rows of items that Plex no longer returns. A refresh or
match request that returns 404 also deletes the row.

## Configuration

The configuration is a YAML file, by default `config.yml` in the default
configuration directory.

```yaml
schedule: "0 * * * *"         # cron expression for each run
minimum-age: 24h              # skip items added less than this long ago
refresh-interval: 12h         # wait at least this long between refreshes of one item
cache-refresh: 168h           # do a full read of each library this often
oversample: 48h               # each incremental read also gets changes from this far back
refresh-delay: 5s             # wait between two refreshes on one server
max-refreshes-per-run: 100    # optional; no limit if it is not set

plex:
  - name: main                    # label for logs and the database
    url: https://plex.domain.tld
    token: XXXX
    libraries:                    # optional; if omitted, all movie and show libraries
      - Movies
      - TV
```

The owner agreed the keys and these values. The owner's own setup leaves out
`libraries` and `max-refreshes-per-run`.

- The timing keys apply to all servers. There is no per-server timing configuration.
- `name`, `url`, and `token` are required for each server. Each `name` must
  be unique.
- `libraries` selects libraries by their title.

## Command line

The existing flags stay: `--version`, `--log`, and `-v`. New flags:

| Flag | Environment variable | Meaning |
| --- | --- | --- |
| `--config` | `AUTOREFRESH_CONFIG` | Configuration file path. Default: `config.yml` in the default configuration directory. |
| `--database` | `AUTOREFRESH_DATABASE` | Database file path. Default: `autorefresh.db` in the default configuration directory. |
| `--dry-run` | `AUTOREFRESH_DRY_RUN` | Read Plex and update the cache, but send no refresh or match requests. |
| `--once` | `AUTOREFRESH_ONCE` | Run one check at once and exit, instead of waiting for the schedule. |

In a dry run, autorefresh logs one line for each item that is due for a
refresh or a match. The line has the server, the library, the title, the reasons, and
the action. A dry run does not record refreshes, so a later real run behaves
as if the dry run never happened.

## Package structure

The owner asked for a standard Go layout. All packages are under
`internal/`, because autorefresh is an application and not a library.

| Package | Responsibility |
| --- | --- |
| `cmd/autorefresh` | Flags, default paths, logging, loading the configuration, opening the database, and the schedule. It only wires the other packages together. |
| `internal/config` | The configuration types, defaults, and validation. |
| `internal/plex` | The Plex API client: libraries, paged listings with filters, single items, refresh, match search, and match. It keeps the token and the server address out of errors and logs, as autoscan's `targets/internal/httpapi` does. |
| `internal/store` | The SQLite database, migrations, and the queries for libraries and incomplete items. |
| `internal/refresher` | The rules: what counts as missing, minimum age, the due order, path ID parsing, fix match, pacing, and dry run. It defines small interfaces for the Plex client and the store where it uses them. |

The schedule uses `github.com/robfig/cron/v3`, because autoscan has no
scheduler to reuse.

## Testing

- Tests use `net/http/httptest` and recorded Plex responses in the
  `testdata/` directory of the package that reads them. They need no network
  and no Plex server.
- The investigation kept cleaned responses outside the repository, so they
  are not available to a later session. The builder records new responses
  with read-only requests to the owner's server. The owner gives the URL and
  the token through a secure prompt, never in the chat or in a file in the
  repository.
- Recorded responses come from the owner's server. Before they go into the
  public repository, the builder replaces the owner's titles, file names, and
  paths with neutral values. The builder also removes the server address, the
  token, the machine identifier, and the section UUIDs.

## Builder decisions

The design leaves these points to the builder:

- the page size for listings
- the HTTP timeout for each request, which is 30 seconds in autoscan
- the exact table layout and migration file names
- the log levels and the field names, within the AGENTS.md logging rules
