# Spec Delta: dashboard-api

## MODIFIED Requirements

### Requirement: JSON content type for POST

Every known `POST` route under `/api/` SHALL require a `Content-Type` whose media type is `application/json`, with or without parameters such as `charset`. Any other type on a known `POST` route SHALL get `415` with the message `content type must be application/json`. Unknown paths under `/api/` SHALL get `404` with `not found` regardless of content type. `GET` routes SHALL NOT require a content type.

#### Scenario: Plain text type

- **WHEN** a client sends `POST /api/item/refresh?srv=0&key=1` with `Content-Type: text/plain`
- **THEN** the server answers `415` and does not refresh the item

#### Scenario: Unknown API path on POST

- **WHEN** a client sends `POST /api/does-not-exist` without a `Content-Type` header
- **THEN** the server answers `404` with `{"error":"not found"}`

#### Scenario: Charset parameter

- **WHEN** a client sends the same request with `Content-Type: application/json; charset=utf-8` and no body
- **THEN** the server accepts the request

### Requirement: Live status event stream

`GET /api/events` SHALL serve SSE (`text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`), sending a `status` snapshot at once and on real changes, plus `: keepalive` every 25 s. Close changes MAY coalesce. Stalled writes (10 s) SHALL end. The server SHALL cap concurrent streams and answer `503` when full or if shutdown begins before headers. `HEAD /api/events` SHALL return only the headers.

#### Scenario: First and later events

- **WHEN** a client opens the stream and a run then starts
- **THEN** the first `status` event shows the idle server with `"run":null`, and the next one shows the run with the same `boot` and a larger `seq`

#### Scenario: Idle stream

- **WHEN** the stream stays open for the keepalive interval
- **THEN** the server sends the comment `: keepalive`

#### Scenario: Concurrent stream limit reached

- **WHEN** the maximum number of open `/api/events` streams is already active and another client connects
- **THEN** the server answers `503` with a JSON error instead of opening another stream

#### Scenario: Shutdown starts before stream headers

- **WHEN** shutdown begins after a `/api/events` request is admitted but before stream headers are written
- **THEN** the server answers `503` with `{"error":"shutting down"}`

#### Scenario: HEAD request

- **WHEN** a client sends `HEAD /api/events`
- **THEN** the server answers `200` with the stream headers and does not keep the connection open as a stream

### Requirement: Servers route

`GET /api/servers` SHALL answer `{servers, ignoreReasons}`. Each server SHALL have `srv`, `name`, `scanned`, `libraries` (`section`, `title`, `type`, `lastRead`, optional `lastFullRead`), and optional `machineIdentifier`. Concurrent uncached identity lookups SHALL share one Plex request; caller cancellation SHALL NOT cancel it or start the 1-minute retry wait. With a cache refresh schedule and libraries, it SHALL add `nextFullRead` and `fullReadDue`. Empty lists SHALL be `[]`.

#### Scenario: Plex identity fails

- **WHEN** the identity request to a server fails
- **THEN** that server has no `machineIdentifier`, the answer holds no error text, and the server asks Plex again only after one minute
- **AND** a successful identity is cached and not asked again

#### Scenario: Concurrent identity requests

- **GIVEN** the server identity is not cached yet
- **WHEN** two clients call `GET /api/servers` at the same time and Plex answers
- **THEN** Plex receives one identity request and both clients receive `machineIdentifier`

#### Scenario: First caller disconnects during identity lookup

- **GIVEN** the server identity is not cached yet
- **WHEN** the first client disconnects while the Plex identity request is in flight and Plex succeeds
- **THEN** the identity is still cached and the next client receives `machineIdentifier` without waiting one minute

#### Scenario: First scan

- **WHEN** no library of a server has a read time and no finished scan of the server exists
- **THEN** `scanned` is `false`

#### Scenario: Library never read fully

- **GIVEN** a cache refresh schedule is configured
- **WHEN** a library has no full read yet
- **THEN** `nextFullRead` is the time of the answer and `fullReadDue` is `true`

### Requirement: Poster and art proxy

`GET /api/items/thumb` and `GET /api/items/art` (`srv`, `key`, `size` `sm`/`md`/`lg`, default `md`) SHALL fetch the item's stored image via Plex's photo transcoder. Concurrent upstream fetches SHALL be bounded, and identical in-flight requests SHALL coalesce. Answers SHALL be JPEG, PNG, or WebP (<= 10 MB) with sniffed `Content-Type`, `Content-Length`, `Cache-Control: private, max-age=86400`, `Cross-Origin-Resource-Policy: same-origin`, and no Plex header.

#### Scenario: Sizes

- **WHEN** a client requests a poster with `size=sm`, `md`, or `lg`
- **THEN** the server asks Plex for 120x180, 240x360, or 480x720 pixels, and for art 480x270, 960x540, or 1920x1080 pixels

#### Scenario: Duplicate concurrent image requests

- **WHEN** two clients request the same item poster and size at the same time
- **THEN** Plex receives one photo transcoder request and both clients receive the image

#### Scenario: Invalid size

- **WHEN** `size` is `xl` or empty
- **THEN** the server answers `400` with `{"error":"invalid size"}` and sends no request to Plex

#### Scenario: Not an image or too large

- **WHEN** Plex answers HTML, a GIF, an empty body, or an image larger than 10 MB
- **THEN** the server answers `502` with `{"error":"image unavailable"}`

#### Scenario: Image gone in Plex

- **WHEN** Plex answers that the image does not exist
- **THEN** the server answers `404` with `{"error":"not found"}`

### Requirement: Run detail and run items routes

`GET /api/runs/detail?id=` SHALL answer `{run, events, items}`: the run in the runs shape, its events in the timeline shape plus `key`, `title`, `year`, `type`, `thumb`, and `library`, and the items of a bulk job with `key`, `title`, `outcome`, `error`, and the same facts. `GET /api/runs/items?id=` SHALL answer `{items}` with the items of a bulk job. For both routes, a run that does not exist, or whose server is not configured, SHALL get `404` with `no such run`.

#### Scenario: Invalid ID

- **WHEN** `id` is missing, `0`, `x`, or a bad escape
- **THEN** the server answers `400`

#### Scenario: Run of a removed server

- **WHEN** the run belongs to a server that is no longer configured
- **THEN** both `/api/runs/detail` and `/api/runs/items` answer `404` with `{"error":"no such run"}`

#### Scenario: Missing run on run items

- **WHEN** a client requests `/api/runs/items?id=999999` for a run that does not exist
- **THEN** the server answers `404` with `{"error":"no such run"}`

### Requirement: Item action route answers

`POST /api/item/match` (`{guid, name, year}`) SHALL answer `200` `{"accepted":true}` (up to 2 m write deadline). `POST /api/item/stop` and `/resume` SHALL answer `{"changed":<bool>}`. `GET /api/item/matches` (`q`, `year`) SHALL answer `{ids, locations, files, searches, tmdbFailed}`. `GET /api/item/auto-match` SHALL answer one timeline event. `POST /api/item/episode-check` (`tmdb`) SHALL answer `{match, episodes, seasons}`. Search, preview, and episode check SHALL get up to 90 s to write.

#### Scenario: Invalid match body

- **WHEN** `guid` is empty or longer than 1024 bytes, `name` is longer than 1024 bytes, either has a control character, or `year` is outside 0 to 9999
- **THEN** the server answers `400` with `{"error":"invalid match"}`

#### Scenario: Slow match write

- **WHEN** `POST /api/item/match` finishes after the 1 minute default server write timeout but within 2 minutes
- **THEN** the client still receives `200` with `{"accepted":true}`

#### Scenario: Invalid search parameters

- **WHEN** `q` is longer than 200 bytes, or `year` is not 1 to 9999 in plain digits
- **THEN** `/api/item/matches` answers `400` with `invalid q` or `invalid year`

#### Scenario: Invalid TMDB ID

- **WHEN** `tmdb` is missing, not digits, or longer than 12 digits
- **THEN** `/api/item/episode-check` answers `400` with `{"error":"invalid tmdb"}`

#### Scenario: Episode seasons

- **WHEN** the episode check compared a season
- **THEN** each entry of `seasons` has `season` and sorted `plex` and `tmdb` lists that are never `null`, and `episodeGroup`, `diff`, `stopped`, and `episodeGroups` appear only when set
