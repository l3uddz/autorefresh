# service-operation Specification

## Purpose

Define how the autorefresh process starts, logs, checks its tokens, runs on its schedule, and stops, so that operators can predict each flag, log line, and exit code.

## Requirements

### Requirement: Command line flags

Autorefresh SHALL accept the flags `--config`, `--database`, `--log`, `-v` (`--verbosity`), `--dry-run`, `--once`, and `--version`. Each flag except `--version` SHALL also read an environment variable: `AUTOREFRESH_CONFIG`, `AUTOREFRESH_DATABASE`, `AUTOREFRESH_LOG`, `AUTOREFRESH_VERBOSITY`, `AUTOREFRESH_DRY_RUN`, and `AUTOREFRESH_ONCE`.

#### Scenario: Flag set through the environment

- **WHEN** `AUTOREFRESH_ONCE` is set to `true` and no `--once` flag is given
- **THEN** autorefresh runs as if `--once` were set

#### Scenario: Repeated verbosity flag

- **WHEN** autorefresh starts with `-vv`
- **THEN** the verbosity is `2`

### Requirement: Invalid arguments

When the command line does not parse, autorefresh SHALL print the error with a short usage text and exit with code 80 before it reads the configuration or writes any file.

#### Scenario: Unknown flag

- **WHEN** autorefresh starts with a flag that it does not define, such as `--bogus`
- **THEN** it prints the error and the usage and exits with code 80

### Requirement: Version output

The `--version` flag SHALL print one line with the version, the git commit, and the build time in the form `<version> (<commit>@<time>)`, and exit with code 0 without starting the service.

#### Scenario: Development build

- **WHEN** a binary that the release build did not stamp starts with `--version`
- **THEN** it prints `0.0.0-dev (unknown@unknown)` and exits with code 0

### Requirement: Console and log file output

Autorefresh SHALL write each log line to standard error and to the log file. The log file SHALL hold plain text without color codes and SHALL rotate at 5 MB, keep 5 old files, and remove files older than 14 days. A failed console write SHALL NOT stop the line from reaching the log file.

#### Scenario: Line in the log file

- **WHEN** autorefresh logs an info line with a field, such as `item=Movie`
- **THEN** the log file holds a line with the time, `INF`, the message, and `item=Movie`, and no color codes

#### Scenario: Console closed

- **WHEN** a write to standard error fails
- **THEN** the line still reaches the log file

#### Scenario: Durations

- **WHEN** a log line has a duration field
- **THEN** it shows the duration in the Go form, such as `duration=11m3.814s`

### Requirement: Verbosity levels

The verbosity SHALL select the lowest log level: `0` gives info, `1` gives debug, and `2` or more gives trace.

#### Scenario: Default verbosity

- **WHEN** no `-v` flag and no `AUTOREFRESH_VERBOSITY` are set
- **THEN** autorefresh logs info, warn, error, and fatal lines and drops debug and trace lines

#### Scenario: High verbosity

- **WHEN** autorefresh starts with `-v` set five times
- **THEN** it logs trace lines

### Requirement: Startup line

After it loads the configuration and opens the database, autorefresh SHALL log one `Initialised` info line with the version, the names of the Plex servers, the schedule, whether the run is a dry run, and whether TMDB is set.

#### Scenario: Normal start

- **WHEN** autorefresh starts with a valid configuration
- **THEN** it logs `Initialised` with the fields `version`, `servers`, `schedule`, `dry_run`, and `tmdb`

### Requirement: Setup errors stop the process

When it cannot read or decode the configuration, open the database, mark interrupted runs, or bind the dashboard listener, autorefresh SHALL log the error at fatal level and exit with code 1.

#### Scenario: Invalid configuration

- **WHEN** the configuration file does not decode
- **THEN** autorefresh logs `Failed decoding config` at fatal level and exits with code 1
- **AND** it creates no database file

#### Scenario: Database cannot open

- **WHEN** the database file cannot be opened
- **THEN** autorefresh logs `Failed opening database` at fatal level and exits with code 1

### Requirement: Run once

With `--once`, autorefresh SHALL run the refresher one time at once and then exit. It SHALL NOT check the tokens, start the dashboard listener, or wait for the schedule. It SHALL exit with code 1 when the run fails or the database does not close, and with code 0 after a successful run and a clean close of the database.

#### Scenario: Successful run

- **WHEN** autorefresh starts with `--once`, the run succeeds, and the database closes without an error
- **THEN** it logs the run summary, closes the database, and exits with code 0

#### Scenario: Unavailable server

- **WHEN** autorefresh starts with `--once` and a Plex server answers `503`
- **THEN** it logs `Run failed` and exits with code 1
- **AND** it leaves a database that another process can open

### Requirement: Startup token checks

Without `--once`, autorefresh SHALL check each Plex server, by a request for its libraries, and the TMDB token, if set, at the same time before the first scheduled run. The checks SHALL log their results and, when the dashboard is on, give them to the dashboard status as health. A failed check SHALL NOT stop the start.

#### Scenario: Token accepted

- **WHEN** a Plex server answers the check
- **THEN** autorefresh logs `Plex server accepted the token` at info level with the server name and the number of libraries

#### Scenario: Token rejected

- **WHEN** a Plex server answers the check with `401`
- **THEN** autorefresh logs `Plex server rejected the token` at error level with the server name
- **AND** it still starts the schedule

#### Scenario: Server unavailable

- **WHEN** a Plex server answers with another error, such as `503`, or does not accept the connection
- **THEN** autorefresh logs `Checking Plex server failed` at warn level with the server name

#### Scenario: TMDB results

- **WHEN** the TMDB check succeeds, gets `401`, or fails in another way
- **THEN** autorefresh logs `TMDB accepted the token` at info level, `TMDB rejected the token` at error level, or `Checking TMDB failed` at warn level

#### Scenario: No TMDB token

- **WHEN** the configuration has no TMDB token
- **THEN** autorefresh sends no TMDB check

#### Scenario: Slow Plex server

- **WHEN** a Plex server is slow to answer its check
- **THEN** the TMDB check runs while the Plex check waits

#### Scenario: Dashboard health

- **WHEN** the dashboard is on and a check ends
- **THEN** the dashboard status gets the result as the health of that server or of TMDB

#### Scenario: Signal during the checks

- **WHEN** SIGINT or SIGTERM arrives during the checks
- **THEN** a check that ends because of the signal is neither logged nor given to the dashboard status
- **AND** autorefresh starts no run, logs `Stopped`, and exits with code 0

### Requirement: Scheduled operation

Without `--once`, autorefresh SHALL try to start one run as soon as the token checks end, and then one at each time of the `schedule` cron expression, until the schedule stops in the shutdown order, also while the dashboard stops. The schedule SHALL use the local time zone, unless the expression starts with `TZ=<zone>` or `CRON_TZ=<zone>`. A start while another run is busy, such as a manual run from the dashboard, SHALL be skipped with the warning `Skipped scheduled start, a run is busy`.

#### Scenario: First run at once

- **WHEN** autorefresh starts without `--once`, the token checks finish without a signal, and no other run is busy when the first scheduled start begins
- **THEN** that start runs right after the token checks, without waiting for the schedule

#### Scenario: Manual run during the checks

- **WHEN** the dashboard accepted a manual run during the token checks, and that run is still busy when the first scheduled start begins
- **THEN** autorefresh skips the first scheduled start and logs `Skipped scheduled start, a run is busy`

#### Scenario: Time zone prefix

- **WHEN** `schedule` is `CRON_TZ=UTC 0 3 * * *`
- **THEN** autorefresh tries to start a run at each 03:00 UTC, whatever the local time zone is

#### Scenario: Failed scheduled run

- **WHEN** a scheduled run fails
- **THEN** autorefresh logs `Run failed` at error level and keeps the schedule

#### Scenario: Dry run on the schedule

- **WHEN** autorefresh starts with `--dry-run` and without `--once`
- **THEN** it checks the tokens and runs on the schedule as usual

### Requirement: Next scheduled time on the dashboard

When the dashboard is on, autorefresh SHALL give the dashboard status the next time of the schedule when the schedule starts and at each schedule time, also at a time that a busy run skips.

#### Scenario: Status after the start

- **WHEN** the dashboard is on and the schedule has started
- **THEN** the dashboard status shows a next run time in the future

#### Scenario: Skipped time

- **WHEN** the schedule fires while a run is busy
- **THEN** the dashboard status still gets the new next time

### Requirement: Dashboard listener start

Autorefresh SHALL bind the dashboard listener only when `ui.listen` is set and neither `--once` nor `--dry-run` is set. It SHALL bind before the token checks, so a bind error stops the start before any Plex request.

#### Scenario: Listener bound

- **WHEN** `ui.listen` is set, the address is free, and neither `--once` nor `--dry-run` is set
- **THEN** autorefresh logs `UI listening` with the address and whether authentication is on, before the token checks

#### Scenario: No authentication

- **WHEN** the listener starts without `ui.username`
- **THEN** autorefresh logs a warning that the UI has no authentication and must be reached only through an authenticating reverse proxy or a trusted network

#### Scenario: Port in use

- **WHEN** the address of `ui.listen` is already in use, and neither `--once` nor `--dry-run` is set
- **THEN** autorefresh logs `Failed starting UI listener` at fatal level and exits with code 1
- **AND** it sends no request to Plex and leaves a database that another process can open

#### Scenario: Once or dry run

- **WHEN** `ui.listen` is set and `--once` or `--dry-run` is set
- **THEN** autorefresh logs `UI is off with --once or --dry-run` once and binds no listener

#### Scenario: Requests during the token checks

- **WHEN** the dashboard listener has started and the token checks are still pending
- **THEN** the dashboard already answers requests and accepts a manual scan

### Requirement: Shutdown order

On SIGINT or SIGTERM without `--once`, autorefresh SHALL stop in this order: the dashboard, with a 5 s limit for a graceful stop, after which it cancels the requests that are still open; then the schedule, whose busy scheduled run it cancels and waits for; then the manual jobs, which it cancels and waits for, and the item actions, which it waits for; then the database. It SHALL then log `Stopped` and exit with code 0.

#### Scenario: Signal during a scheduled run

- **WHEN** a signal arrives while a scheduled run is busy
- **THEN** the dashboard stops first while the run goes on
- **AND** if the run is still busy after that, it is cancelled and returns before the manual jobs stop and the database closes

#### Scenario: Dashboard does not stop in time

- **WHEN** the dashboard does not stop gracefully within its limit
- **THEN** autorefresh logs `Stopping UI failed` at warn level and goes on with the shutdown

#### Scenario: Database close fails

- **WHEN** the database does not close after a signal
- **THEN** autorefresh logs `Failed closing database` and still exits with code 0

#### Scenario: Clean stop

- **WHEN** the shutdown ends
- **THEN** the dashboard listener is closed and the database can be opened by another process

### Requirement: Signal in run-once mode

With `--once`, SIGINT or SIGTERM SHALL cancel the run, which stops before its next request.

#### Scenario: Signal during the run

- **WHEN** a signal arrives while the run of `--once` is busy
- **THEN** the run stops before its next request and autorefresh closes the database and exits

### Requirement: Second signal

After the `Initialised` line, a second SIGINT or SIGTERM SHALL end the process at once, without waiting for the shutdown.

#### Scenario: Shutdown takes too long

- **WHEN** a second signal arrives while autorefresh waits for a busy run to stop
- **THEN** the process ends at once

### Requirement: Signal during the configuration wait

A signal that arrives while autorefresh waits for the configuration file SHALL stop the process without an error.

#### Scenario: Stop before the file exists

- **WHEN** SIGINT or SIGTERM arrives while autorefresh waits for a missing configuration file
- **THEN** it logs `Stopped`, logs no fatal line, and exits with code 0
