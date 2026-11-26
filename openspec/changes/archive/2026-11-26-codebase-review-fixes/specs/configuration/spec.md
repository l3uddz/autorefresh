# Spec Delta: configuration

## MODIFIED Requirements

### Requirement: Wait for the configuration file

When the configuration file is missing or empty at startup, autorefresh SHALL log one warning and check again at a fixed interval until the file has content. It SHALL read a file that appears during the wait only after its size and modification time stay the same for one interval. It SHALL reject a configuration file larger than 1 MiB before reading its contents.

#### Scenario: Container starts before the file exists

- **GIVEN** the configuration path has no file
- **WHEN** autorefresh starts
- **THEN** it logs that the config is missing and keeps waiting without exiting
- **AND** it logs that it found the config and continues once a stable, non-empty file appears

#### Scenario: Path is a directory

- **WHEN** the configuration path exists but is not a regular file, such as a directory that Docker made for a missing bind mount
- **THEN** autorefresh fails at startup with an error that the path is not a regular file

#### Scenario: File larger than 1 MiB

- **WHEN** the configuration file is larger than 1 MiB
- **THEN** autorefresh fails at startup without reading the file into memory

#### Scenario: Shutdown during the wait

- **WHEN** autorefresh receives SIGINT or SIGTERM while it waits for the file
- **THEN** it stops waiting and exits

### Requirement: Schedule validation

The `schedule` and `cache-refresh` keys SHALL be standard five-field cron expressions or descriptors such as `@hourly`, `@daily`, or `@every 1h`, with an optional `TZ=<zone>` or `CRON_TZ=<zone>` prefix. Each SHALL fire at least once in the future. An `@every` duration SHALL be at least 1 second.

#### Scenario: Descriptor

- **WHEN** `schedule` is `@every 1h` or `@hourly`
- **THEN** autorefresh accepts it and runs on that schedule

#### Scenario: Sub-second or non-positive interval

- **WHEN** `schedule` is `@every 0s`, `@every -5m`, or `@every 500ms`
- **THEN** autorefresh fails at startup with an error that names `schedule`

#### Scenario: Expression that never fires

- **WHEN** `schedule` is a valid expression for a date that never occurs, such as February 30
- **THEN** autorefresh fails at startup with an error that the expression never fires

#### Scenario: Invalid expression

- **WHEN** `cache-refresh` does not parse as a cron expression
- **THEN** autorefresh fails at startup with an error that names `cache-refresh`

### Requirement: Plex server validation

The configuration SHALL list at least one Plex server. Each server SHALL have a unique, non-blank name without control characters, an `http` or `https` URL with a host, an optional port from 1 to 65535 without a trailing bare colon, and no userinfo, query, or fragment, and a non-empty printable ASCII token without whitespace or control characters. A server timeout SHALL be positive and SHALL default to 30 s. Each listed library title SHALL be non-blank and without control characters.

#### Scenario: No server

- **WHEN** `plex` is empty or missing
- **THEN** autorefresh fails at startup with an error that no plex server is configured

#### Scenario: Duplicate name

- **WHEN** two servers have the same name
- **THEN** autorefresh fails at startup with an error that the name is used more than once

#### Scenario: URL with a query

- **WHEN** a server URL has a query string
- **THEN** autorefresh fails at startup with an error that the URL must not have a query

#### Scenario: URL with an invalid or empty port

- **WHEN** a server URL has port `99999`, port `0`, or a trailing colon with no port such as `http://plex:/`
- **THEN** autorefresh fails at startup with an error that names the server and rejects the port

#### Scenario: Token with non-ASCII whitespace

- **WHEN** a server token contains a non-breaking space or non-ASCII character
- **THEN** autorefresh fails at startup with an error that names the server without revealing the token

#### Scenario: Error names the server without secrets

- **WHEN** a server entry fails validation
- **THEN** the error names the server by its name, or by its position such as `#2` when the name is blank, and never contains its URL or token

#### Scenario: URL with a path

- **WHEN** a server URL has a path, such as one behind a reverse proxy
- **THEN** the configuration is valid and requests go below that path

### Requirement: TMDB token validation

An optional `tmdb-token` SHALL contain only printable ASCII characters without whitespace or control characters. An empty token SHALL turn off the TMDB fallbacks of fix match.

#### Scenario: Token with a space

- **WHEN** `tmdb-token` contains a space
- **THEN** autorefresh fails at startup with an error about `tmdb-token`

#### Scenario: Token with non-ASCII whitespace

- **WHEN** `tmdb-token` contains a zero-width space or non-breaking space
- **THEN** autorefresh fails at startup with an error about `tmdb-token`
