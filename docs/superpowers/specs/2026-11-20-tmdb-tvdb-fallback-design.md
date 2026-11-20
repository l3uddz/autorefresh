# TMDB fallback for TVDB and IMDb IDs, and foreign keys in the store

Date: 2026-11-20

## Intent

Plex stops using TheTVDB as a metadata source on December 3, 2026. The
fix match of autorefresh reads IDs such as `{tvdb-463649}` from folder and
file names and asks Plex to search for `tvdb-463649`. Sonarr's default
folder format holds the TVDB ID, so many series folders give no other ID.
If Plex stops resolving TVDB IDs, these searches find nothing, and an
unmatched series gets only plain refreshes that probably do not fix it.

TMDB can translate a TVDB ID or an IMDb ID to a TMDB ID. Autorefresh then
searches Plex with `tmdb-<id>`, which Plex supports. The owner wants this
as a fallback for unmatched items, and to get ahead of the TVDB shutdown.
Plex does not say whether its TVDB ID search stops working, so the fallback
must do no harm while it still works. Plex is not dropping IMDb. The IMDb
fallback helps in one case: Plex does not know an IMDb ID that TMDB knows,
for example for a new title. Nobody saw this case yet, but the IMDb fallback
uses the same code, so it costs almost nothing.

The fallback caches the TMDB IDs that it finds. The cache must disappear
with its item. The store has no foreign keys today, so this work also adds
foreign keys to the existing tables.

## Goals

- If no search of a fix match finds a candidate, an unmatched movie or
  series gets a second search with the TMDB IDs that TMDB gives for its
  `tvdb` and `imdb` path IDs.
- Autorefresh caches each TMDB ID that it finds for an item. If it removes
  the item, it also removes the cached IDs of that item.
- The store enforces foreign keys for all tables, the existing ones too.

## Non-goals

- A lookup of a TVDB ID in a movie library. TMDB maps TVDB IDs only to
  series, and TVDB IDs on movies are rare.
- Warnings about TVDB episode ordering. The owner already changed the
  libraries to TMDB ordering.
- Changes to the Plex configuration, such as the episode ordering of a
  library or a show.
- A cache of TMDB answers that found nothing.

## Anti-goals

- A fix match that works today must not stop working. If any normal search
  finds a candidate, the fallback does not run and sends no TMDB request.
- A TMDB problem must not affect the Plex side. A TMDB failure must never
  stop the run of a server, and it must never delete an item.
- Without a TMDB token, autorefresh behaves exactly as it does now.

## Constraints

- Tests use `net/http/httptest` and recorded responses in `testdata/`.
  They need no network. Remove private values from recorded responses.
- Log with the zerolog global logger, with values in fields.
- The token must not appear in logs, errors, or URLs.
- Update `README.md`: the fix match section, the configuration, and the
  sentence that autorefresh talks only to Plex.

## Approaches

Two ways were considered. The owner picked the first one.

1. Fallback only. Autorefresh asks TMDB in one case only: no Plex search
   of the fix match found a candidate. TMDB calls stay rare, because fix
   match runs only for unmatched items that already had one refresh, and
   the wait backs off. Nothing changes while Plex still resolves TVDB IDs.
2. Always translate. Autorefresh translates every `tvdb` ID and searches
   Plex with both IDs. The match rules then require both searches to
   agree, which catches a wrong TVDB mapping. But each fix match needs a
   TMDB call, and new "different candidates" conflicts can block a match
   that works today.

Approach 1 is the only one that cannot make today's matches worse.

## Design

The work has two parts. Part 1 stands alone, and part 2 depends on it.

### Part 1: foreign keys in the store

- If the store opens the database, it turns on SQLite foreign keys for its
  connection.
- A new migration rebuilds the `item` table with a foreign key from
  `(server, section)` to `library(server, section)` with
  `ON DELETE CASCADE`. SQLite cannot add a foreign key to an existing
  table. So the migration creates a new table and copies the rows. Then it
  drops the old table and renames the new one.
- Before the copy, the migration adds a `library` row with empty read
  times for each `(server, section)` that has items but no `library` row.
  This keeps the refresh history of such items. An empty full read time
  causes a full read of that library in the next run, which is correct.
- `ApplyRead` writes the `library` row before it writes items, because the
  foreign key needs the parent row first.
- The cleanup of unused libraries deletes the `library` rows. The cascade
  deletes their items.

### Part 2: TMDB fallback

#### Configuration

One new optional top-level key, `tmdb-token`. Its value is the TMDB "API
Read Access Token" from the API page of the TMDB account. Autorefresh
sends it in an `Authorization: Bearer` header, so it never appears in a
URL. If the key is empty or absent, autorefresh sends no TMDB requests.

#### TMDB client

A new package `internal/tmdb` with a small client:

- Find the TMDB ID for an external ID with
  `GET https://api.themoviedb.org/3/find/{id}?external_source={source}`.
  The source is `tvdb_id` or `imdb_id`. For a movie, the client reads
  `movie_results`. For a series, it reads `tv_results`. If that list holds
  exactly one entry, the answer is its TMDB ID. Otherwise, the answer is
  "nothing found". This is not an error.
- Check the token. Autorefresh uses this check at startup.
- Errors of this package are its own. They never wrap or equal
  `plex.ErrUnavailable` or `plex.ErrNotFound`, because the refresher uses
  those errors to stop a server and to delete an item.

#### Cache

A new table holds one row for each item and external ID:
`(server, rating_key, source, external_id, tmdb_id)`, with the primary key
`(server, rating_key, source, external_id)`. The source is `tvdb` or
`imdb`. The table has a foreign key from `(server, rating_key)` to
`item(server, rating_key)` with `ON DELETE CASCADE`. So every removal of
an item removes its cached IDs: an item that became complete, an item gone
from Plex, and the cleanup of unused servers and libraries.

The cache is per item and not one shared table, because items do not store
their path IDs. So a shared table has no way to find and remove an unused
external ID.

If TMDB found an entry, autorefresh writes a row. Otherwise, it writes
nothing.

#### Search errors of TVDB and IMDb IDs

Nobody knows how Plex answers a `tvdb-` search after December 3. It can
give an empty list or an error. So for a `tvdb` or an `imdb` path ID, a
failed Plex search counts as a search with no candidate, with these
exceptions:

- A 404 answer still means that the item is gone from Plex, and
  autorefresh still removes it. The 404 is about the item, not the ID.
- An error that stops the server, such as a timeout, 401, 502, 503, or
  504, still stops the server for the run.

The other errors, such as 400 or 500, get a debug log line. A failed
search of a `tmdb` path ID keeps the current handling.

This rule also applies without a TMDB token. Then an item whose `tvdb`
search fails can still get a match from another path ID, or a plain
refresh.

#### Fix match flow

The fallback is a second pass after the normal searches. All of these
conditions must be true for it to run:

- A TMDB token is set.
- No normal search found a candidate. If one found a candidate,
  `chooseMatch` decides as it does now, and TMDB gets no request.
- The paths hold no `tmdb` ID. If they hold one, Plex was already searched
  with a TMDB ID, and a translated ID adds nothing.

Then for each `tvdb` and `imdb` path ID, without a `tvdb` ID of a movie:

1. Autorefresh reads the cached TMDB ID of the item and the external ID.
2. If no row exists, it asks TMDB. If TMDB found an entry, it writes the
   row.

Autorefresh then searches Plex once for each different TMDB ID, with
`tmdb-<id>`. `chooseMatch` reads these results with the current rules,
including the year rule. If two external IDs give different TMDB IDs that
both find a candidate, the result is the "different candidates" conflict.
If TMDB found nothing for all IDs, the item gets a plain refresh, as
today. A failed Plex search with a translated TMDB ID is a failed match
search, with the current handling.

The debug log of each search shows the external ID that the TMDB ID came
from.

#### TMDB failures

A TMDB request can fail, for example from a timeout, a server error, a
rate limit, or a rejected token. Then autorefresh handles the item like a
failed match search:

- It sends no request for the item in this run.
- It records a failed attempt, so the item waits `refresh-interval`. A
  failed attempt does not make the wait longer.
- It logs a `TMDB lookup failed` warning with the item fields and counts the
  failure in `failed_requests`.
- The run of the server continues with the next item.

The reason is that the item is unmatched and already had a plain refresh,
so another plain refresh probably fixes nothing. A plain refresh that Plex
accepts also moves the item to a longer wait.

#### Startup check

If a token is set, autorefresh checks it once at startup, next to the Plex
token check, and only logs. A rejected token gives an error line. Another
failure gives a warning line, because TMDB can be down for a short time.

## Calls made in the conversation

These are the agent's proposals that the owner accepted:

- Approach 1, fallback only.
- The TMDB token is optional.
- The fallback covers IMDb IDs too (the owner's request), for movies and
  series. TVDB IDs get no lookup in movie libraries.
- No lookup for paths that already hold a TMDB ID.
- The fallback is a second pass. It needs a result with no candidate from
  every normal search.
- A failed Plex search of a `tvdb` or `imdb` ID counts as "no candidate",
  except for 404 answers and errors that stop the server.
- A TMDB failure skips the item for the run, and an empty TMDB answer gives
  a plain refresh.
- A per-item cache with a cascading foreign key, and foreign keys for the
  existing tables too (the owner's request).

## Left to the builder

- The names of the table, the columns, and the Go types and functions.
- The request timeout of the TMDB client. A fixed value such as the Plex
  default of 30s is enough. No configuration key is needed.
- Whether a dry run writes the TMDB cache. Reads already update the cache in
  a dry run, so writing it is consistent. A dry run still records no
  attempts.
- The delivery order. Part 1 is its own issue and PR into `main`. Part 2 is
  a second issue and PR after part 1 merges.
