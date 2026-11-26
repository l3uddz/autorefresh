# Spec Delta: service-operation

## MODIFIED Requirements

### Requirement: Startup token checks

Without `--once`, autorefresh SHALL check each Plex server, by a request for its libraries, and the TMDB token, if set, at the same time before the first scheduled run, within a 15 second startup check timeout. The checks SHALL log their results and, when the dashboard is on, give them to the dashboard status as health. A failed check SHALL NOT stop the start.

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

#### Scenario: Startup check timeout

- **GIVEN** a Plex server is configured with a 5 minute timeout and never answers
- **WHEN** startup token checks run
- **THEN** the check times out after 15 seconds, logs `Checking Plex server failed`, and starts the schedule without waiting 5 minutes

#### Scenario: Dashboard health

- **WHEN** the dashboard is on and a check ends
- **THEN** the dashboard status shows the result of that check without waiting for the first run

#### Scenario: Signal during the checks

- **WHEN** SIGINT or SIGTERM arrives during the checks
- **THEN** a check that ends because of the signal is neither logged nor given to the dashboard status
- **AND** autorefresh starts no run, logs `Stopped`, and exits with code 0

### Requirement: Scheduled operation

Without `--once`, autorefresh SHALL try to start one run as soon as the token checks end, and one at each time of the `schedule` cron expression, until SIGINT or SIGTERM arrives. The schedule SHALL use the local time zone, unless the expression starts with `TZ=<zone>` or `CRON_TZ=<zone>`. A start while another run is busy, such as a manual run from the dashboard, SHALL be skipped with the warning `Skipped scheduled start, a run is busy`.

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

#### Scenario: Cron tick during shutdown

- **WHEN** SIGINT or SIGTERM arrives while the dashboard is stopping
- **THEN** the schedule starts no new scheduled run

### Requirement: Dashboard listener start

Autorefresh SHALL bind the dashboard listener only when `ui.listen` is set, neither `--once` nor `--dry-run` is set, and no shutdown signal has arrived during setup. It SHALL bind before the token checks, so a bind error stops the start before any Plex request.

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

#### Scenario: Signal before listener bind

- **WHEN** SIGINT or SIGTERM arrives while opening the database before the listener binds
- **THEN** autorefresh binds no dashboard listener, closes the database, and exits cleanly

#### Scenario: Requests during the token checks

- **WHEN** the dashboard listener has started and the token checks are still pending
- **THEN** the dashboard already answers requests and accepts a manual scan

### Requirement: Shutdown order

On SIGINT or SIGTERM without `--once`, autorefresh SHALL stop scheduling new runs and cancel any active scheduled run at once, stop the dashboard with a 5 s limit for a graceful stop (canceling still-open requests after that limit), wait for the active scheduled run to return, cancel and wait for manual jobs, wait for item actions, and then close the database. It SHALL then log `Stopped` and exit with code 0.

#### Scenario: Signal during a scheduled run

- **WHEN** a signal arrives while a scheduled run is busy
- **THEN** the schedule stops new ticks and cancels the active scheduled run without waiting for the dashboard stop timeout
- **AND** the dashboard stops, the scheduled run returns, manual jobs and item actions drain, and then the database closes

#### Scenario: Dashboard does not stop in time

- **WHEN** the dashboard does not stop gracefully within its limit
- **THEN** autorefresh logs `Stopping UI failed` at warn level and goes on with the shutdown

#### Scenario: Database close fails

- **WHEN** the database does not close after a signal
- **THEN** autorefresh logs `Failed closing database` and still exits with code 0

#### Scenario: Clean stop

- **WHEN** the shutdown ends
- **THEN** the dashboard listener is closed and the database can be opened by another process
