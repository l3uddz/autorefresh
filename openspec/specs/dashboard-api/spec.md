# dashboard-api Specification

## Purpose

Define the HTTP contract of the dashboard server: how it protects and serves the page, which JSON routes it has under `/api/`, what each route reads and answers, and how it hides secrets and shuts down.

## Requirements

### Requirement: Optional Basic auth

When `ui.username` is set, the server SHALL require HTTP Basic auth with the configured username and password on every request, including the page, the assets, the images, the event stream, and the JSON API. A request without the right values SHALL get `401` with the body `{"error":"unauthorized"}` and the header `WWW-Authenticate: Basic realm="autorefresh", charset="UTF-8"`, unless a shutdown `503` or a cross-origin `403` comes first. Without a username, the server SHALL ask for no credentials.

#### Scenario: Wrong password

- **GIVEN** a username and a password are configured
- **WHEN** a request to `/` or to an `/api/` path sends the right username and a wrong password
- **THEN** the server answers `401` with the JSON error `unauthorized` and the `WWW-Authenticate` challenge

#### Scenario: No header

- **GIVEN** a username and a password are configured
- **WHEN** a request to `GET /api/items/thumb` has no `Authorization` header
- **THEN** the server answers `401` and sends no request to Plex

#### Scenario: No auth configured

- **WHEN** no username is configured and a client requests `/`
- **THEN** the server answers `200` and sends no `WWW-Authenticate` header

### Requirement: Cross-origin protection

The server SHALL reject a cross-origin browser request with a method that is not safe, such as `POST`, with `403` and the body `{"error":"cross-origin request rejected"}`. It SHALL treat such a request as cross-origin when `Sec-Fetch-Site` is neither `same-origin` nor `none`, or, without `Sec-Fetch-Site`, when the host of `Origin` differs from `Host`. `GET`, `HEAD`, and `OPTIONS` SHALL pass. It SHALL check this before Basic auth.

#### Scenario: Cross-site POST

- **WHEN** a `POST` request carries `Sec-Fetch-Site: cross-site`
- **THEN** the server answers `403` with the JSON error `cross-origin request rejected` and runs no action

#### Scenario: Foreign Origin

- **WHEN** a `POST` request to `http://ui.local/` carries `Origin: https://evil.example` and no `Sec-Fetch-Site`
- **THEN** the server answers `403`

#### Scenario: Same-origin POST

- **WHEN** a `POST` request carries `Sec-Fetch-Site: same-origin`
- **THEN** the cross-origin check lets it through to the route

### Requirement: Security headers

The server SHALL set `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, and `X-Frame-Options: DENY` on every response, also on error responses.

#### Scenario: Error responses keep the headers

- **WHEN** the server answers the page with `200`, an unknown API path with `404`, a missing login with `401`, a cross-origin request with `403`, or a request during shutdown with `503`
- **THEN** each response carries the three security headers

### Requirement: Dashboard page and assets

The server SHALL answer `GET /` with `index.html` and `Cache-Control: no-cache`, and `GET /assets/<file>` with the embedded file and `Cache-Control: public, max-age=31536000, immutable`. When a `.gz` copy of a file is embedded, it SHALL add `Vary: Accept-Encoding` and SHALL send the copy with `Content-Encoding: gzip` only when `Accept-Encoding` allows gzip. A path that ends in `.gz` SHALL get `404`.

#### Scenario: Client accepts gzip

- **GIVEN** the binary embeds the built dashboard
- **WHEN** a client requests a script under `/assets/` with `Accept-Encoding: gzip`
- **THEN** the server sends the gzip copy with `Content-Encoding: gzip`, and the copy unpacks to the plain file

#### Scenario: Client refuses gzip

- **GIVEN** the binary embeds the built dashboard
- **WHEN** a client requests `/` with `Accept-Encoding: br, gzip;q=0` or `*;q=1, gzip;q=0`
- **THEN** the server sends the plain file without `Content-Encoding` and still sets `Vary: Accept-Encoding`

#### Scenario: Script type

- **GIVEN** the binary embeds the built dashboard
- **WHEN** a client requests a `.js` file under `/assets/`
- **THEN** the `Content-Type` starts with `text/javascript`

#### Scenario: Direct request for a gzip copy

- **WHEN** a client requests `/assets/<file>.js.gz`
- **THEN** the server answers `404`

### Requirement: Fallback page without the built UI

A binary built without the dashboard frontend SHALL answer `GET /` with `200`, `Content-Type: text/html; charset=utf-8`, and `Cache-Control: no-cache`, and with a page that says that the UI is not built and links to the status with the relative link `api/status`. It SHALL answer `404` for every path under `/assets/`. The JSON API SHALL work the same as in a binary with the UI.

#### Scenario: Plain go build

- **GIVEN** the binary was built without the `webui` build tag
- **WHEN** a client requests `/`
- **THEN** the page says that the UI is not built and has no link that starts with `/`

#### Scenario: Asset in the fallback binary

- **GIVEN** the binary was built without the `webui` build tag
- **WHEN** a client requests `/assets/index.js`
- **THEN** the server answers `404`

### Requirement: JSON error responses

Every error answer of an `/api/` route SHALL have `Content-Type: application/json` and the body `{"error":"<message>"}` with a fixed message. A path under `/api/` that matches no route SHALL get `404` with the message `not found`. A failed store read SHALL get `500` with the message `internal error`, unless the client has left, when the server SHALL send no body.

#### Scenario: Unknown API path

- **WHEN** a client requests `GET /api/`, `GET /api/x`, or `GET /api/x/y`
- **THEN** the server answers `404` with `{"error":"not found"}`

#### Scenario: Store failure

- **GIVEN** the database cannot be read
- **WHEN** a client requests `/api/items`, `/api/servers`, `/api/runs`, `/api/item`, or `/api/item/live`
- **THEN** the server answers `500` with `{"error":"internal error"}`

#### Scenario: Client left during the read

- **WHEN** the request context ends before a store read returns
- **THEN** the server writes no body and logs the failure only at debug level

### Requirement: Query string parsing

On every route that reads query parameters, a query string that does not parse, such as one with a bad percent escape, SHALL make the whole request fail with `400` and the message `invalid query`. The server SHALL NOT drop the bad pair and serve the rest.

#### Scenario: Bad escape in a list query

- **WHEN** a client requests `/api/items?srv=%ZZ` or `/api/runs?cursor=%ZZ`
- **THEN** the server answers `400` with `{"error":"invalid query"}`

#### Scenario: Bad escape in an unused parameter

- **WHEN** a client requests `/api/item?srv=0&key=1&x=%ZZ` or `/api/items/thumb?srv=0&key=1&size=%ZZ`
- **THEN** the server answers `400` with `{"error":"invalid query"}`

### Requirement: Server and item parameters

The API SHALL name a server by `srv`, its index in the configured server list, and an item by `srv` and `key`, its Plex rating key. A `srv` SHALL be a decimal index of a configured server, and an empty `srv` SHALL count as absent. A `key` SHALL be digits only. Routes for one item SHALL require both and SHALL answer `400` with `invalid srv` or `invalid key`. An item that is neither cached nor in the history SHALL get `404` with `not found`.

#### Scenario: Missing or out-of-range server

- **GIVEN** one configured server
- **WHEN** a client requests `/api/item/live?key=101` or `/api/item/live?srv=3&key=101`
- **THEN** the server answers `400` with `{"error":"invalid srv"}` and sends no request to Plex

#### Scenario: Key with a slash

- **WHEN** a client requests `/api/item/live?srv=0&key=1/2`
- **THEN** the server answers `400` with `{"error":"invalid key"}`

#### Scenario: Item known only from the history

- **GIVEN** an item has no cache row but has history events
- **WHEN** a client requests that item from `/api/item` or `/api/items/art`
- **THEN** the server answers from the newest history snapshot of the item

### Requirement: JSON content type for POST

Every `POST` route under `/api/` SHALL require a `Content-Type` whose media type is `application/json`, with or without parameters such as `charset`. Any other type SHALL get `415` with the message `content type must be application/json`. `GET` routes SHALL NOT require a content type.

#### Scenario: Plain text type

- **WHEN** a client sends `POST /api/item/refresh?srv=0&key=1` with `Content-Type: text/plain`
- **THEN** the server answers `415` and does not refresh the item

#### Scenario: Charset parameter

- **WHEN** a client sends the same request with `Content-Type: application/json; charset=utf-8` and no body
- **THEN** the server accepts the request

### Requirement: Bodyless POST routes

`POST /api/item/refresh`, `POST /api/item/stop`, `POST /api/item/resume`, `POST /api/item/episode-check`, and `POST /api/runs/cancel` SHALL take their input from the query only and SHALL require an empty body. A body with any content SHALL get `400` with the message `body not allowed`.

#### Scenario: Empty JSON object

- **WHEN** a client sends `POST /api/item/refresh?srv=0&key=1` with the body `{}`
- **THEN** the server answers `400` and does not refresh the item

### Requirement: JSON body routes

`POST /api/item/match`, `POST /api/runs`, `POST /api/items/refresh`, `POST /api/items/stop`, and `POST /api/items/resume` SHALL require a body that is exactly one JSON object of at most 1 MiB with only known fields. A larger body SHALL get `413` with `body too large`. Any other bad body, such as `null`, an unknown field, a second JSON value, or no body, SHALL get `400` with `invalid body`.

#### Scenario: Body over 1 MiB

- **WHEN** a client sends `POST /api/item/match` with a body larger than 1 MiB
- **THEN** the server answers `413`

#### Scenario: Unknown field or extra value

- **WHEN** the body is `{"guid":"id","extra":true}`, `{"guid":"id"} {}`, or `null`
- **THEN** the server answers `400` and applies no match

### Requirement: Read routes

The server SHALL serve these read routes: `GET /api/status`, `GET /api/events` (also `HEAD`), `GET /api/servers`, `GET /api/items`, `GET /api/item`, `GET /api/item/live`, `GET /api/item/matches`, `GET /api/item/auto-match`, `GET /api/candidate`, `GET /api/items/thumb`, `GET /api/items/art`, `GET /api/runs`, `GET /api/runs/detail`, and `GET /api/runs/items`. The event stream and the images SHALL NOT be JSON. Every other read route SHALL answer JSON.

#### Scenario: Status read

- **WHEN** a client requests `GET /api/status`
- **THEN** the server answers `200` with `Content-Type: application/json`

### Requirement: Write routes

The server SHALL serve these write routes: `POST /api/item/refresh`, `POST /api/item/match`, `POST /api/item/stop`, `POST /api/item/resume`, `POST /api/item/episode-check`, `POST /api/runs`, `POST /api/runs/cancel`, `POST /api/items/refresh`, `POST /api/items/stop`, and `POST /api/items/resume`. This capability SHALL define only their HTTP request and answer. The `manual-actions` capability defines what each action does.

#### Scenario: Write route answers JSON

- **WHEN** a client sends a valid `POST /api/item/stop?srv=0&key=1` with `Content-Type: application/json` and no body
- **THEN** the server answers `200` with a JSON body

### Requirement: Live status snapshot

`GET /api/status` SHALL answer `200` with a snapshot `{boot, seq, servers, tmdb, nextRun}`. `boot` SHALL name the process, and `seq` SHALL count the status changes of that process, so a client can keep the newer snapshot. Each server entry SHALL have `srv`, `name`, `health` (`unknown`, `ok`, `unreachable`, or `unauthorized`), `run` (`null` while idle), and, after a run finished, `last`, the last finished run. `tmdb` SHALL be `off` without a TMDB token, otherwise a health value.

#### Scenario: Changes count up

- **GIVEN** one server check, one run start, and one item start happened since the process started
- **WHEN** a client requests `/api/status`
- **THEN** the answer has a non-empty `boot` and `seq` 3

#### Scenario: No status configured

- **WHEN** the server has no live status
- **THEN** `/api/status` answers `"servers":[]` and `"tmdb":"off"`

#### Scenario: Next scheduled run

- **WHEN** the scheduler has not started
- **THEN** the snapshot has no `nextRun` field
- **AND** after the scheduler reports a time, `nextRun` shows it and never moves back to an earlier time

### Requirement: Run progress items

A running server entry SHALL show its run with `id`, `kind`, `source`, `status`, `startedAt`, `librariesRead`, `library`, `selected`, `due`, `done`, and the request counts. It SHALL show `current`, the item in work, or `next` with `nextAt`, the item after the pace wait, and `last`, the last finished item with `outcome` and `finishedAt`. Each item SHALL have `key` and, when known, `title`, `year`, `type`, `library`, `reasons`, and `thumb`, a relative poster proxy URL.

#### Scenario: Next, then current, then last

- **WHEN** a run waits for an item, then starts it, then finishes it
- **THEN** the run shows the item first as `next` with `nextAt`, then as `current` without `next`, then as `last` with its outcome and finish time and without `current`

#### Scenario: Item without a poster

- **WHEN** the item in work has no poster and no reasons
- **THEN** its JSON has no `thumb`, `reasons`, or `finishedAt` field

#### Scenario: Run ends

- **WHEN** the run of a server finishes
- **THEN** `run` becomes `null` and `last` holds the finished run with its `id`, final `status`, and `last` item, but no `current`, `next`, or `nextAt`

### Requirement: Live status event stream

`GET /api/events` SHALL answer a server-sent event stream with `Content-Type: text/event-stream`, `Cache-Control: no-cache`, and `X-Accel-Buffering: no`. It SHALL send a `status` event with the snapshot of `GET /api/status` at once and after a change, and a `: keepalive` comment every 25 s. Changes close together MAY arrive as one event with the latest snapshot. A stream whose write stalls for 10 s SHALL end. `HEAD /api/events` SHALL answer the same headers without a body and return at once.

#### Scenario: First and later events

- **WHEN** a client opens the stream and a run then starts
- **THEN** the first `status` event shows the idle server with `"run":null`, and the next one shows the run with the same `boot` and a larger `seq`

#### Scenario: Idle stream

- **WHEN** the stream stays open for the keepalive interval
- **THEN** the server sends the comment `: keepalive`

#### Scenario: HEAD request

- **WHEN** a client sends `HEAD /api/events`
- **THEN** the server answers `200` with the stream headers and does not keep the connection open as a stream

### Requirement: Servers route

`GET /api/servers` SHALL answer `{servers, ignoreReasons}`. Each server SHALL have `srv`, `name`, `scanned`, `libraries` (each with `section`, `title`, `type`, `lastRead`, and `lastFullRead` when set), and, when Plex tells it, `machineIdentifier`. With a cache refresh schedule and libraries, it SHALL add `nextFullRead`, the earliest time a library is due for a full read, and `fullReadDue` when that time is not after now. Empty lists SHALL be `[]`.

#### Scenario: Plex identity fails

- **WHEN** the identity request to a server fails
- **THEN** that server has no `machineIdentifier`, the answer holds no error text, and the server asks Plex again only after one minute
- **AND** a successful identity is cached and not asked again

#### Scenario: First scan

- **WHEN** no library of a server has a read time and no finished scan of the server exists
- **THEN** `scanned` is `false`

#### Scenario: Library never read fully

- **GIVEN** a cache refresh schedule is configured
- **WHEN** a library has no full read yet
- **THEN** `nextFullRead` is the time of the answer and `fullReadDue` is `true`

### Requirement: Item list route

`GET /api/items` SHALL answer one page of a tab as `{items, next, counts}`. `tab` SHALL be `attention` (the default), `fixed`, or `stopped`. `counts` SHALL give the `attention`, `fixed`, and `stopped` totals under the same filters. Only items of configured servers SHALL be listed. Without configured servers, the answer SHALL be `"items":[]` with zero counts. `items` SHALL be `[]` when empty, and `next` SHALL be absent on the last page.

#### Scenario: Invalid tab

- **WHEN** `tab` is `x` or `Attention`
- **THEN** the server answers `400` with `{"error":"invalid tab"}`

#### Scenario: Removed server

- **GIVEN** the store holds items of a server that is no longer configured
- **WHEN** a client requests `/api/items`
- **THEN** neither the items nor the counts include them

#### Scenario: Item fields

- **WHEN** the list includes a cached movie with a poster
- **THEN** its entry has `srv`, `server`, `key`, `type`, `title`, `cached`, `reasons`, `state`, `attempts`, and `refreshes`, and `thumb` is a relative URL such as `api/items/thumb?key=101&srv=0&v=1700000000`

### Requirement: Item list filters

`GET /api/items` SHALL filter by `srv`, `library` (a section ID, which needs `srv`), `reason` (one reason name, which `fixed` and its count ignore), `type` (`movie` or `show`), and `q` (a search text of at most 200 bytes after trimming). `all` SHALL be empty or `1`. With `all=1`, `attention` SHALL also list items whose reasons are all ignored, with state `ignored`, and `fixed` SHALL also list items fixed without a request. `all` SHALL change only the chosen tab and its count.

#### Scenario: Library without a server

- **WHEN** a client requests `/api/items?library=1`
- **THEN** the server answers `400` with `{"error":"library needs srv"}`

#### Scenario: Invalid filter values

- **WHEN** `reason` is `x` or lists two reasons, `type` is `episode`, `q` is longer than 200 bytes, or `all` is `0` or `true`
- **THEN** the server answers `400` with `invalid reason`, `invalid type`, `invalid q`, or `invalid all`

#### Scenario: Reason on the Fixed tab

- **WHEN** a client requests `/api/items?tab=fixed&reason=no-art`
- **THEN** the list and the `fixed` count are the same as for `/api/items?tab=fixed`, so they still leave out items fixed without a request

#### Scenario: Show ignored items

- **GIVEN** an item whose only reason is ignored
- **WHEN** a client requests `/api/items?all=1`
- **THEN** the item is listed with state `ignored` and the attention count includes it

### Requirement: Item list sorts

`sort` SHALL be a sort key of the tab, with an optional `-` prefix for descending order. `attention` and `stopped` SHALL accept `title`, `year`, `added`, `due`, `tries`, `last-attempt`, and `stopped`. `fixed` SHALL accept `title`, `year`, and `completed`. Without `sort`, the order SHALL be `-tries` for `attention`, `-stopped` for `stopped`, and `-completed` for `fixed`. Any other value SHALL get `400` with `invalid sort`.

#### Scenario: Sort of another tab

- **WHEN** a client requests `tab=fixed&sort=due` or `tab=attention&sort=completed`
- **THEN** the server answers `400` with `{"error":"invalid sort"}`

#### Scenario: Double prefix

- **WHEN** `sort` is `--title`
- **THEN** the server answers `400` with `{"error":"invalid sort"}`

### Requirement: Item list pages and cursors

`limit` SHALL be 1 to 500, with 100 by default. `next` SHALL be an opaque cursor for the following page. A cursor SHALL fit only a request with the same tab, `srv`, filters, sort, and `all`, but SHALL fit another `limit`. The default sort and the same sort spelled out SHALL count as the same. A cursor that does not decode or does not fit SHALL get `400` with `invalid cursor`.

#### Scenario: Follow the pages

- **GIVEN** five items
- **WHEN** a client follows `next` with `limit=2`
- **THEN** it gets pages of 2, 2, and 1 items with no repeated key, and the last page has no `next`

#### Scenario: Cursor of another request

- **WHEN** a client sends a cursor of `srv=0` without `srv`, a cursor of `q=title` with `q=other`, or a cursor of `sort=year` with `sort=title`
- **THEN** the server answers `400` with `{"error":"invalid cursor"}`

#### Scenario: Invalid limit

- **WHEN** `limit` is `0`, `501`, or `a`
- **THEN** the server answers `400` with `{"error":"invalid limit"}`

### Requirement: Item detail route

`GET /api/item?srv=&key=` SHALL answer `{item, timeline}`: the item in the list shape, from its cache row or its newest history snapshot, and its events newest first. `timeline` SHALL be `[]` when the item has no events. Each event SHALL have `id`, `kind`, `accepted`, and `searches`, and SHALL add fields such as `at`, `source`, `action`, `outcome`, `error`, and `match` when set. A match decision SHALL also carry its evidence fields.

#### Scenario: Item without events

- **WHEN** a cached item has no history events
- **THEN** the answer has `"timeline":[]`

#### Scenario: Match decision evidence

- **WHEN** the timeline holds a fix match decision with a TMDB ID and an episode difference
- **THEN** the event has `tmdbId` and `episodeDiff` with `season`, `plex`, `tmdb`, and `missing`, and each search lists its `candidates`

### Requirement: Live Plex view route

`GET /api/item/live?srv=&key=` SHALL ask Plex about a known item within 5 s and answer `{summary, guid, locations, files, path}`, and for a show also `seasons` and `episodes`. `locations` and `files` SHALL be `[]` when empty. A show without a cached path SHALL get its first location as `path`. Plex errors SHALL get fixed messages: `404` `not found in Plex`, `502` `Plex rejected the token`, or `502` `Plex unavailable`.

#### Scenario: Unknown item

- **WHEN** the item is neither cached nor in the history
- **THEN** the server answers `404` with `not found` and sends no request to Plex

#### Scenario: Plex is slow

- **WHEN** Plex does not answer within the live timeout
- **THEN** the server answers `502` with `{"error":"Plex unavailable"}`

#### Scenario: Plex error text

- **WHEN** the Plex request fails with an error that names the Plex host
- **THEN** the answer holds only the fixed message and not the host

### Requirement: Candidate provider route

`GET /api/candidate?srv=&guid=` SHALL ask the Plex metadata provider, with the token of server `srv` and within 5 s, about a `plex://show/` GUID and answer `{tmdbId, seasons, episodes}`, each only when the provider gives it. A count of `0` SHALL stay in the answer. A GUID that the provider does not know SHALL get `200` with `{}`. `srv` SHALL be required.

#### Scenario: Invalid GUID

- **WHEN** the GUID is not a valid Plex show GUID
- **THEN** the server answers `400` with `{"error":"invalid guid"}`

#### Scenario: Provider fails

- **WHEN** the provider rejects the token or is unavailable
- **THEN** the server answers `502` with `the Plex metadata provider rejected the token` or `Plex metadata provider unavailable`

#### Scenario: Missing server

- **WHEN** the request has no `srv`
- **THEN** the server answers `400` with `invalid srv` and sends no request to the provider

### Requirement: Poster and art proxy

`GET /api/items/thumb` and `GET /api/items/art` with `srv` and `key` SHALL fetch the stored poster or art of a known item through the Plex photo transcoder. `size` SHALL be `sm`, `md` (the default), or `lg`. The answer SHALL be only a JPEG, PNG, or WebP image of at most 10 MB, with `Content-Type` from sniffing, `Content-Length`, `Cache-Control: private, max-age=86400`, and `Cross-Origin-Resource-Policy: same-origin`, and no Plex header.

#### Scenario: Sizes

- **WHEN** a client requests a poster with `size=sm`, `md`, or `lg`
- **THEN** the server asks Plex for 120x180, 240x360, or 480x720 pixels, and for art 480x270, 960x540, or 1920x1080 pixels

#### Scenario: Invalid size

- **WHEN** `size` is `xl` or empty
- **THEN** the server answers `400` with `{"error":"invalid size"}` and sends no request to Plex

#### Scenario: Not an image or too large

- **WHEN** Plex answers HTML, a GIF, an empty body, or an image larger than 10 MB
- **THEN** the server answers `502` with `{"error":"image unavailable"}`

#### Scenario: Image gone in Plex

- **WHEN** Plex answers that the image does not exist
- **THEN** the server answers `404` with `{"error":"not found"}`

### Requirement: Proxy accepts only the item's own image path

The image proxy SHALL request only a stored path of the form `/library/metadata/<key>/thumb/<digits>` or `/library/metadata/<key>/art/<digits>` for the requested key. The routes SHALL take no path or URL parameter. Any other stored value SHALL get `404` with `not found` and no request to Plex. List and status answers SHALL give a `thumb` or `art` URL only for such a path.

#### Scenario: Path of another item

- **WHEN** the stored poster and art of item 101 are both `/library/metadata/999/thumb/1`, both `https://h/x.jpg`, or both empty
- **THEN** both image routes answer `404` for item 101 and send no request to Plex
- **AND** the list entry of item 101 has no `thumb` or `art` field

### Requirement: Runs route

`GET /api/runs` SHALL answer `{runs, next}` with the runs of the configured servers, newest first. `srv` SHALL scope it to one server. `limit` SHALL be 1 to 500, with 50 by default. `next` SHALL be an opaque cursor that fits only the same `srv` and any `limit`. Each run SHALL have `id`, `srv`, `server`, `kind`, `source`, `status`, its times, its counts, `fullReads`, `incrementalReads`, and `eventsLinked`. `runs` SHALL be `[]` when empty.

#### Scenario: No configured server

- **WHEN** no server is configured but the store holds runs
- **THEN** the answer is `"runs":[]` without `next`

#### Scenario: Cursor of another scope

- **WHEN** a client sends a cursor of `srv=0` with `srv=1` or without `srv`, or a cursor without `srv` with `srv=0`
- **THEN** the server answers `400` with `{"error":"invalid cursor"}`

#### Scenario: Invalid parameters

- **WHEN** `srv` is `-1` or `a`, or `limit` is `0` or `501`
- **THEN** the server answers `400` with `invalid srv` or `invalid limit`

### Requirement: Run detail and run items routes

`GET /api/runs/detail?id=` SHALL answer `{run, events, items}`: the run in the runs shape, its events in the timeline shape plus `key`, `title`, `year`, `type`, `thumb`, and `library`, and the items of a bulk job with `key`, `title`, `outcome`, `error`, and the same facts. A run that does not exist, or whose server is not configured, SHALL get `404` with `no such run`. `GET /api/runs/items?id=` SHALL answer `{items}` with the items of a bulk job.

#### Scenario: Invalid ID

- **WHEN** `id` is missing, `0`, `x`, or a bad escape
- **THEN** the server answers `400`

#### Scenario: Run of a removed server

- **WHEN** the run belongs to a server that is no longer configured
- **THEN** `/api/runs/detail` answers `404`

### Requirement: Refresh route answer

`POST /api/item/refresh?srv=&key=` SHALL answer `200` with the item detail after the refresh (`item` and `timeline`), `match` after a fix match, with `guid` and, when known, `title` and `year`, and `checked`, whether a check after the request saved what it read, or found that a run completed or removed the item. When the detail read fails, the answer SHALL leave out `item` and `timeline` and keep the rest. The answer SHALL get up to 3 minutes to write.

#### Scenario: Detail read fails

- **GIVEN** Plex accepted the refresh and applied a match
- **WHEN** the timeline read fails
- **THEN** the server answers `200` without `item` but with `match`

#### Scenario: Slow refresh

- **WHEN** the refresh succeeds, and the action with its follow-up takes longer than the 1 minute write timeout of the server but less than 3 minutes
- **THEN** the client still receives the `200` answer with `checked`

### Requirement: Item action route answers

`POST /api/item/match` with `{guid, name, year}` SHALL answer `200` with `{"accepted":true}`. `POST /api/item/stop` and `POST /api/item/resume` SHALL answer `200` with `{"changed":<bool>}`. `GET /api/item/matches` with optional `q` and `year` SHALL answer `{ids, locations, files, searches, tmdbFailed}`. `GET /api/item/auto-match` SHALL answer the decision as one event in the timeline shape. `POST /api/item/episode-check` with `tmdb` SHALL answer `{match, episodes, seasons}`.

#### Scenario: Invalid match body

- **WHEN** `guid` is empty or longer than 1024 bytes, `name` is longer than 1024 bytes, either has a control character, or `year` is outside 0 to 9999
- **THEN** the server answers `400` with `{"error":"invalid match"}`

#### Scenario: Invalid search parameters

- **WHEN** `q` is longer than 200 bytes, or `year` is not 1 to 9999 in plain digits
- **THEN** `/api/item/matches` answers `400` with `invalid q` or `invalid year`

#### Scenario: Invalid TMDB ID

- **WHEN** `tmdb` is missing, not digits, or longer than 12 digits
- **THEN** `/api/item/episode-check` answers `400` with `{"error":"invalid tmdb"}`

#### Scenario: Episode seasons

- **WHEN** the episode check compared a season
- **THEN** each entry of `seasons` has `season` and sorted `plex` and `tmdb` lists that are never `null`, and `episodeGroup`, `diff`, `stopped`, and `episodeGroups` appear only when set

### Requirement: Item action errors

An item action that fails SHALL get a fixed status and message: `504` `timed out`, `503` `shutting down`, `409` `item busy`, `409` `item not cached`, `404` `not found`, `400` `not a show`, `400` `tmdb is off`, `500` `internal error` for a store failure, `502` `TMDB rejected the token` or `TMDB unavailable`, and for Plex `404` `not found in Plex`, `502` `Plex rejected the token`, or `502` `Plex unavailable`.

#### Scenario: Busy item

- **WHEN** another request holds the item
- **THEN** the action answers `409` with `{"error":"item busy"}`

#### Scenario: Item gone in Plex

- **WHEN** the automatic fix match preview finds that Plex no longer has the item
- **THEN** `/api/item/auto-match` answers `404`

#### Scenario: TMDB rejects the token

- **WHEN** an action fails because TMDB rejected the token
- **THEN** the server answers `502` with `TMDB rejected the token`

### Requirement: Run now and cancel routes

`POST /api/runs` with `{srv, full}` SHALL start a run of server `srv`, or of every server when `srv` is absent or `null`, and answer `202` with `{"ids":[...]}`. `full: true` SHALL ask for full library reads. A `srv` outside the list SHALL get `400` `invalid srv`. `POST /api/runs/cancel?id=` SHALL answer `200` with `{"canceled":true}`. A busy run lock SHALL get `409` `a run is busy`, and an unknown job `404` `no such job`.

#### Scenario: Run one server fully

- **WHEN** a client sends `{"srv": 1, "full": true}`
- **THEN** the server starts a full run of the second server and answers `202`

#### Scenario: Run every server

- **WHEN** a client sends `{}`
- **THEN** the server starts a run of every server without full reads and answers `202`

### Requirement: Bulk routes

`POST /api/items/refresh`, `POST /api/items/stop`, and `POST /api/items/resume` SHALL need `srv`, SHALL accept the list filters except `tab` (resume also rejects `all=1`), and SHALL take a selection body by the next requirement. Refresh and stop SHALL use the `attention` tab and resume the `stopped` tab. Refresh SHALL answer `202` with `{id, count}`. Stop and resume SHALL answer `200` with `{changed, skipped}`.

#### Scenario: Invalid selection

- **WHEN** `srv` is missing, `tab` is set, or a key is not digits
- **THEN** the server answers `400` with `invalid srv`, `invalid tab`, or `invalid key`, and starts no job

#### Scenario: Resume with all

- **WHEN** a client sends `POST /api/items/resume?srv=0&all=1`
- **THEN** the server answers `400` with `{"error":"invalid all"}`

#### Scenario: Duplicate keys

- **WHEN** the body is `{"keys":["2","1","2"]}`
- **THEN** the job gets the keys `2` and `1`, and the server answers `202`

#### Scenario: Selection limits

- **WHEN** the selection holds more than 5000 distinct items
- **THEN** the server answers `400` with `too many items`

#### Scenario: Skipped items

- **WHEN** a bulk resume finds items that are busy, not cached, or already in the wanted state, or its 30 s budget ends
- **THEN** each such item is in `skipped` with the reason `busy`, `not-cached`, `unchanged`, or `timeout`, and every item after the timeout is skipped with `timeout`

#### Scenario: Matching selection times out

- **WHEN** a bulk stop with `{"matching":true}` cannot read the matching items within its budget
- **THEN** the server answers `504` with `timed out`

### Requirement: Bulk selection body

The body of a bulk route SHALL be `{"matching":true}`, also with `"keys":null`, which selects every item that matches the filters, or a non-empty `keys` list with `matching` absent, false, or null. `matching` true with a `keys` list, also an empty one, and a body without keys and without `matching` true SHALL get `400` with `invalid body`. A matching selection that finds no item SHALL get `400` with `no items`.

#### Scenario: Keys with matching false

- **WHEN** the body is `{"keys":["1"],"matching":false}`
- **THEN** the route selects the item with key `1`

#### Scenario: Null fields

- **WHEN** the body is `{"keys":null,"matching":true}` or `{"keys":["1"],"matching":null}`
- **THEN** the route selects every matching item, or the item with key `1`

#### Scenario: Empty keys

- **WHEN** the body is `{"keys":[]}`
- **THEN** the server answers `400` with `invalid body` and starts no job

#### Scenario: Keys with matching true

- **WHEN** the body is `{"keys":["1"],"matching":true}` or `{"keys":[],"matching":true}`
- **THEN** the server answers `400` with `invalid body` and starts no job

#### Scenario: No matching item

- **WHEN** the body is `{"matching":true}` and no item matches the filters
- **THEN** the server answers `400` with `no items` and starts no job

### Requirement: Redaction of stored error texts

The API SHALL replace each configured secret in stored error texts with `<redacted>`: the Plex tokens, hosts, and path prefixes, the TMDB token, and the UI password. This SHALL apply to the errors of timeline events, runs, bulk items, the automatic fix match preview, and the last run in the live status. Overlapping matches SHALL become one `<redacted>` and leave no part of a secret.

#### Scenario: Token in an event error

- **WHEN** a stored request error is `GET /x?t=sentinel-token: 500` and the token is configured
- **THEN** the timeline shows the error with `<redacted>` in place of the token

#### Scenario: Overlapping secrets

- **WHEN** two configured secrets overlap in an error text
- **THEN** the answer holds neither secret nor any part of it

### Requirement: Safe candidate posters

Every candidate thumb in an answer SHALL be an `https` URL with a host, without user info, a fragment, or a query key that contains `token`. The API SHALL drop a thumb that holds a Plex token, the TMDB token, or the UI password, as-is or percent-decoded in its host, path, or query, and a thumb that does not parse. It SHALL keep a thumb that holds only a Plex host or path prefix. This SHALL apply to the item timeline, run details, match searches, and the preview.

#### Scenario: Credential in the query

- **WHEN** a candidate thumb is `https://img.example/p.jpg?sig=<credential>` or has the credential percent-encoded in its path or query
- **THEN** the answer has the candidate without `thumb`

#### Scenario: Unsafe scheme

- **WHEN** a stored candidate thumb is `http://h/x.jpg`
- **THEN** the answer has the candidate without `thumb`

#### Scenario: Host that is only redacted

- **WHEN** a thumb contains a configured secret that is not a credential, such as a short Plex host or a path prefix
- **THEN** the answer keeps the thumb

### Requirement: Shutdown refuses new requests

When the service stops the dashboard, the server SHALL answer each request that reaches it during the stop with `503` and `{"error":"shutting down"}`, before auth and the cross-origin check. After the stop closed the listener, the server SHALL accept no new connection.

#### Scenario: Request after stop

- **WHEN** a request reaches the server while it stops
- **THEN** the server answers `503` with `{"error":"shutting down"}` and the security headers

### Requirement: Shutdown ends event streams

When the service stops the dashboard, the server SHALL tell open event streams to end at once, before it waits for the active requests. A stream that is blocked in a write or a flush SHALL count as an active request, under the stop deadline and the forced close.

#### Scenario: Open event stream

- **WHEN** a client has an open, idle `/api/events` stream and the server stops
- **THEN** the stream ends at once

#### Scenario: Stream blocked in a write

- **WHEN** a stream is blocked in a write when the server stops
- **THEN** the stop waits for it at most until the stop deadline, and then closes it with the forced close

### Requirement: Shutdown lets active requests finish

When the service stops the dashboard, the server SHALL let active requests finish within the deadline of the stop. When the deadline passes, it SHALL cancel the active requests, close the connections, and wait at most 5 s more for their handlers.

#### Scenario: Request that finishes in time

- **WHEN** a request is active when the stop begins and finishes before the stop deadline
- **THEN** the client gets the full answer of that request

#### Scenario: Handler that blocks

- **WHEN** a request does not finish within the stop deadline
- **THEN** the server cancels its context, closes the connection, and the stop returns the deadline error
