# configuration Specification

## Purpose

Define how autorefresh finds, reads, defaults, and validates its YAML configuration file, so that a bad file stops the service with a clear error that never leaks a secret.

## Requirements

### Requirement: Default file locations

When a path flag is not set, autorefresh SHALL place the configuration file `config.yml`, the database `autorefresh.db`, and the log `activity.log` in one default directory. That directory is the binary's directory when `config.yml` exists there, otherwise the `autorefresh` directory in the user configuration directory, created with owner-only access. When the user configuration directory is unknown or the directory cannot be created, it SHALL be the binary's directory.

#### Scenario: Config file next to the binary

- **GIVEN** no `--config`, `--database`, or `--log` flag
- **WHEN** a `config.yml` file exists in the directory of the binary
- **THEN** autorefresh reads that file and keeps its database and log in the same directory

#### Scenario: No config file next to the binary

- **GIVEN** no path flags and no `config.yml` next to the binary
- **WHEN** autorefresh starts
- **THEN** it uses the `autorefresh` directory in the user configuration directory, such as `~/.config/autorefresh` on Linux, and creates it with mode `0700` if it is missing

#### Scenario: User configuration directory unusable

- **GIVEN** no path flags and no `config.yml` next to the binary
- **WHEN** the user configuration directory is unknown, or autorefresh cannot create the `autorefresh` directory in it
- **THEN** it uses the directory of the binary

#### Scenario: Every path is set

- **WHEN** the config, database, and log paths are all set by flags
- **THEN** autorefresh resolves no default directory and creates none

### Requirement: Wait for the configuration file

When the configuration file is missing or empty at startup, autorefresh SHALL log one warning and check again at a fixed interval until the file has content. It SHALL read a file that appears during the wait only after its size and modification time stay the same for one interval.

#### Scenario: Container starts before the file exists

- **GIVEN** the configuration path has no file
- **WHEN** autorefresh starts
- **THEN** it logs that the config is missing and keeps waiting without exiting
- **AND** it logs that it found the config and continues once a stable, non-empty file appears

#### Scenario: Path is a directory

- **WHEN** the configuration path exists but is not a regular file, such as a directory that Docker made for a missing bind mount
- **THEN** autorefresh fails at startup with an error that the path is not a regular file

#### Scenario: Shutdown during the wait

- **WHEN** autorefresh receives SIGINT or SIGTERM while it waits for the file
- **THEN** it stops waiting and exits

### Requirement: Strict YAML decoding over defaults

Autorefresh SHALL decode the file as exactly one YAML document over the built-in defaults, and SHALL reject unknown keys. An empty document SHALL keep every default.

#### Scenario: Unknown key

- **WHEN** the file contains a key that the configuration does not define
- **THEN** autorefresh fails at startup with a decode error

#### Scenario: Several YAML documents

- **WHEN** the file contains a second YAML document
- **THEN** autorefresh fails at startup with an error that the configuration has more than one YAML document

#### Scenario: Optional keys left out

- **WHEN** the file sets only `plex`
- **THEN** every other key takes its default: `schedule` `0 * * * *`, `minimum-age` `24h`, `refresh-interval` `24h`, `max-refresh-interval` `168h`, `cache-refresh` `0 3 * * 0`, `oversample` `48h`, `refresh-delay` `5s`, `max-refreshes-per-run` `100`, `history-retention` `720h`, no ignored reasons, no TMDB token, and no dashboard listener

### Requirement: Decode errors hide values

A YAML error SHALL report only the line numbers that the error names, and SHALL NOT include any value from the file, such as a token.

#### Scenario: Type error on a token line

- **WHEN** a value in the file has the wrong type
- **THEN** the error reads `decode configuration: invalid YAML at line <n>` and contains no value from the file

### Requirement: Schedule validation

The `schedule` and `cache-refresh` keys SHALL be standard five-field cron expressions or descriptors such as `@hourly`, `@daily`, or `@every 1h`, with an optional `TZ=<zone>` or `CRON_TZ=<zone>` prefix. Each SHALL fire at least once in the future.

#### Scenario: Descriptor

- **WHEN** `schedule` is `@every 1h` or `@hourly`
- **THEN** autorefresh accepts it and runs on that schedule

#### Scenario: Expression that never fires

- **WHEN** `schedule` is a valid expression for a date that never occurs, such as February 30
- **THEN** autorefresh fails at startup with an error that the expression never fires

#### Scenario: Invalid expression

- **WHEN** `cache-refresh` does not parse as a cron expression
- **THEN** autorefresh fails at startup with an error that names `cache-refresh`

### Requirement: Duration and limit validation

Autorefresh SHALL require `refresh-interval` of at least 1 ms and a positive `history-retention`. It SHALL reject a negative `max-refresh-interval`, `minimum-age`, `oversample`, `refresh-delay`, or `max-refreshes-per-run`. A `max-refreshes-per-run` of `0` SHALL mean no limit.

#### Scenario: Negative delay

- **WHEN** `refresh-delay` is negative
- **THEN** autorefresh fails at startup with an error that `refresh-delay` must not be negative

#### Scenario: Zero history retention

- **WHEN** `history-retention` is `0s`
- **THEN** autorefresh fails at startup with an error that `history-retention` must be positive

### Requirement: Ignored reasons

Each entry of `ignore-reasons` SHALL be one of `unmatched`, `no-summary`, `no-poster`, `no-art`, `episode-no-summary`, or `episode-unmatched`.

#### Scenario: Unknown reason

- **WHEN** `ignore-reasons` lists a name outside that set
- **THEN** autorefresh fails at startup with an error that names `ignore-reasons`

### Requirement: Plex server validation

The configuration SHALL list at least one Plex server. Each server SHALL have a unique, non-blank name without control characters, an `http` or `https` URL with a host and no userinfo, query, or fragment, and a non-empty token without whitespace or control characters. A server timeout SHALL be positive and SHALL default to 30 s. Each listed library title SHALL be non-blank and without control characters.

#### Scenario: No server

- **WHEN** `plex` is empty or missing
- **THEN** autorefresh fails at startup with an error that no plex server is configured

#### Scenario: Duplicate name

- **WHEN** two servers have the same name
- **THEN** autorefresh fails at startup with an error that the name is used more than once

#### Scenario: URL with a query

- **WHEN** a server URL has a query string
- **THEN** autorefresh fails at startup with an error that the URL must not have a query

#### Scenario: Error names the server without secrets

- **WHEN** a server entry fails validation
- **THEN** the error names the server by its name, or by its position such as `#2` when the name is blank, and never contains its URL or token

#### Scenario: URL with a path

- **WHEN** a server URL has a path, such as one behind a reverse proxy
- **THEN** the configuration is valid and requests go below that path

### Requirement: TMDB token validation

An optional `tmdb-token` SHALL contain no whitespace or control characters. An empty token SHALL turn off the TMDB fallbacks of fix match.

#### Scenario: Token with a space

- **WHEN** `tmdb-token` contains a space
- **THEN** autorefresh fails at startup with an error about `tmdb-token`

### Requirement: Dashboard settings validation

When `ui.listen` is set, it SHALL be a `host:port` address with a numeric port. `ui.username` and `ui.password` SHALL be both set or both empty. The username SHALL NOT contain `:`, and neither SHALL contain control characters. These errors SHALL NOT contain a configured value.

#### Scenario: Only a username

- **WHEN** `ui.username` is set and `ui.password` is empty
- **THEN** autorefresh fails at startup with an error that both must be set or both be empty

#### Scenario: Address without a port

- **WHEN** `ui.listen` is `localhost`
- **THEN** autorefresh fails at startup with an error that `ui.listen` must be `host:port`
