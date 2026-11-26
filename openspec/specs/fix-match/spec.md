# fix-match Specification

## Purpose

Define how autorefresh chooses, confirms, and applies the automatic fix match of an unmatched movie or series, so that it matches an item only when the evidence names one Plex entry, and records why it decided.

## Requirements

### Requirement: Items that seek a fix match

A run SHALL try the automatic fix match only for a cached item that is unmatched itself, whose `unmatched` reason is not in `ignore-reasons`, and for which Plex accepted at least one request before. Every other due item SHALL get a plain refresh.

#### Scenario: First attempt of an unmatched item

- **GIVEN** an unmatched item for which Plex has accepted no refresh or match request yet
- **WHEN** a run handles it
- **THEN** the run sends a plain refresh and makes no match search

#### Scenario: Unmatched reason is ignored

- **GIVEN** `ignore-reasons` contains `unmatched`
- **WHEN** a run handles an unmatched item that had a refresh before
- **THEN** the run makes no match search

#### Scenario: Matched series with unmatched episodes

- **WHEN** a matched series has only unmatched regular episodes
- **THEN** it gets plain refreshes and no fix match

### Requirement: Paths of the item

The fix match SHALL read IDs and years from the paths of the item. For a movie, these are the file paths from the library read of the run. Otherwise, and for every series, autorefresh SHALL ask Plex for the item and use its folder locations, or its file paths when it has no location.

#### Scenario: Movie without listed files

- **WHEN** the run has no file paths for an unmatched movie
- **THEN** autorefresh asks Plex for the paths of that movie

#### Scenario: Paths request fails

- **WHEN** Plex answers the paths request with an error other than 404
- **THEN** the item gets no match and no refresh in this run, and the decision is `failed`

### Requirement: Path ID format

Autorefresh SHALL read an ID only from one whole group in `{}`, `[]`, or `()`, with the same bracket kind on both sides and no bracket, `/`, or `\` inside. A group SHALL hold `imdb`, `tmdb`, or `tvdb`, in any case, with an optional `id` suffix, then any separator that is not a letter or digit, then the value. An IMDb value is `tt` and digits and is lowercased. A TMDB or TVDB value is positive digits with any leading zeros stripped; zero values SHALL be rejected.

#### Scenario: Accepted forms

- **WHEN** the paths contain `{tmdb-747059}`, `[imdb:tt19120100]`, `(tvdb-463649)`, `{imdbid-tt31289994}`, `{tvdb=1}`, or `{TMDB_2}`
- **THEN** each group gives one ID with its source and value

#### Scenario: Leading zeros normalized and zero rejected

- **WHEN** the paths contain `{tmdb-001396}` and `{tvdb-0}`
- **THEN** `{tmdb-001396}` gives TMDB ID `1396` and `{tvdb-0}` gives no ID

#### Scenario: Bare IMDb ID

- **WHEN** a group holds only an IMDb value, such as `{tt0115097}` or `[TT1]`
- **THEN** it gives the IMDb ID `tt0115097` or `tt1`

#### Scenario: Rejected forms

- **WHEN** a group is `{12345}`, `{tt}`, `(2020)`, `{imdb-12345}`, `{tmdb-tt1}`, `{tmdb-0}`, `{tmdb-1 imdb-tt2}`, or `{tmdb-1]`, or the ID is outside brackets
- **THEN** it gives no ID

#### Scenario: Same ID twice

- **WHEN** the folder and the file name hold the same ID, also once bare and once with the `imdb` prefix
- **THEN** autorefresh uses that ID once, and keeps the IDs in the order in which it found them

### Requirement: Folder year

Autorefresh SHALL read the folder year from a 4-digit year in parentheses between 1880 and 2100. It SHALL read only the series folder of a series, or the movie folder and the file name of a movie, never a library root folder. When these parts hold different years, there SHALL be no folder year.

#### Scenario: Bare year in the title

- **WHEN** the movie path is `/m/Concert 2000 (2018) {imdb-tt1}/Concert 2000 (2018).mkv`
- **THEN** the folder year is 2018

#### Scenario: Year of a library root

- **WHEN** the only year is in a root folder, such as `/media/Backup (2019)/Movies/Film/Film.mkv`
- **THEN** there is no folder year

#### Scenario: Different years

- **WHEN** the paths hold `(1984)` and `(2018)`, or `[2020]` only
- **THEN** there is no folder year

### Requirement: Match search by path ID

For each different path ID, autorefresh SHALL ask Plex once for match candidates, with the query `<source>-<value>`, such as `tmdb-747059`, the agent and the language of the library, and no year.

#### Scenario: Several path IDs

- **WHEN** the paths hold `{tvdb-9}` and `{imdb-tt1}`
- **THEN** autorefresh searches `tvdb-9` and then `imdb-tt1`

#### Scenario: No path ID

- **WHEN** the paths hold no ID and the title fallback does not apply
- **THEN** autorefresh makes no search, sends a plain refresh, and records the decision `no-ids`

### Requirement: Failed search of a TVDB or IMDb ID

A failed Plex search for a `tvdb` or `imdb` path ID SHALL count as a search without candidates. A 404 answer and an error that makes the server unavailable SHALL keep their meaning. A failed search for a `tmdb` ID SHALL fail the match search. An ignored search error SHALL give no warning, only a debug line.

#### Scenario: TVDB search answers 500

- **WHEN** the search for `tvdb-5` answers 500 and no other search finds a candidate
- **THEN** the item gets a plain refresh, and the search is recorded as failed with no candidates

#### Scenario: Another ID finds the candidate

- **WHEN** the search for `imdb-tt1` fails and the search for `tmdb-2` finds one candidate
- **THEN** autorefresh applies that candidate and logs no search failure warning
- **AND** it logs `Match search failed` for `imdb-tt1` only at debug level

#### Scenario: TMDB ID search answers 500

- **WHEN** the search for a `tmdb` ID answers 500
- **THEN** the item gets no match and no refresh in this run, and autorefresh logs `Match search failed`

#### Scenario: Search answers 404

- **WHEN** any match search answers 404
- **THEN** autorefresh treats the item as gone from Plex

### Requirement: Single candidate choice

Autorefresh SHALL choose a candidate only when every search that found something found exactly one candidate with a GUID, and all these candidates have the same GUID. Searches without candidates SHALL not count. It SHALL check this agreement before the year. Without a choice, the item SHALL get a plain refresh.

#### Scenario: One search finds several candidates

- **WHEN** a search finds two candidates
- **THEN** the conflict is `several candidates`

#### Scenario: Candidate without a GUID

- **WHEN** a search finds one candidate that has no GUID
- **THEN** the conflict is `candidate without guid`

#### Scenario: Searches disagree

- **WHEN** two searches each find one candidate with different GUIDs
- **THEN** the conflict is `different candidates`, and autorefresh logs `Match conflict` with each candidate and the web page of the ID that found it

### Requirement: Year check of the chosen candidate

When the item and a found candidate both have a year, and the years differ by more than 1, the conflict SHALL be `year differs`. Autorefresh SHALL then try to confirm the candidate by the folder year, by TMDB, or by episode numbers. A conflict that stays SHALL give a plain refresh. The search by ID SHALL not compare titles.

#### Scenario: Years one apart

- **WHEN** the item year is 2020 and the candidate year is 2021
- **THEN** the year check passes

#### Scenario: Year unknown

- **WHEN** the item or the candidate has no year
- **THEN** the year check passes

### Requirement: Year confirmation by folder year

For a `year differs` conflict, when the folder year differs from the item year and every found candidate is within 1 year of the folder year, autorefresh SHALL accept the candidate without any TMDB request and record the folder year as the confirming path year.

#### Scenario: Plex read a bare year in the title

- **GIVEN** the item year is 2000, the folder year is 2018, and the candidate year is 2018
- **WHEN** autorefresh checks the year
- **THEN** it applies the match without TMDB, and the request log shows `item_year` 2000, `path_year` 2018, and `candidate_year` 2018

### Requirement: Year confirmation by TMDB

When the folder year does not confirm and TMDB is available, autorefresh SHALL find one TMDB ID for the item. It takes the one ID that all `tmdb` searches used, when each found a candidate. When that ID is a `tmdb` path ID, or no `tmdb` search ran, every translated path ID with a single TMDB entry SHALL agree on it. If Plex was not searched with that ID, a `tmdb-<id>` search SHALL find exactly the chosen GUID. TMDB's year then SHALL be within 1 of the item year or the folder year.

#### Scenario: TMDB year confirms

- **WHEN** TMDB's year of the one TMDB ID is within 1 year of the item year or the folder year
- **THEN** autorefresh applies the candidate and adds the TMDB web page of that ID to the links of the match

#### Scenario: TMDB IDs disagree

- **WHEN** two `tmdb` searches use different IDs, one of them finds nothing, or a translated ID differs from a `tmdb` path ID
- **THEN** TMDB confirms nothing, and the conflict stays `year differs`

#### Scenario: Check search finds another GUID

- **WHEN** the `tmdb-<id>` check search finds no candidate, several, or another GUID
- **THEN** autorefresh asks TMDB for no year, and the conflict stays `year differs`

#### Scenario: Remake

- **WHEN** TMDB's year also differs by more than 1 from the item year and the folder year of a movie
- **THEN** the conflict stays `year differs`, and the item gets a plain refresh

### Requirement: TMDB release years

The TMDB year of a movie SHALL be the earliest year of all its release dates. The TMDB year of a series SHALL be the earliest air date year of its seasons numbered 1 or higher. An entry that TMDB does not know, or that has no valid date, SHALL have no year and confirm nothing.

#### Scenario: Series with specials

- **WHEN** a TMDB series has a season 0 with an earlier air date than season 1
- **THEN** the year comes from season 1 or a later regular season, never from season 0

### Requirement: Year confirmation by episode numbers

When TMDB's year does not confirm a series, autorefresh SHALL compare the regular episode numbers of the series in Plex with TMDB, and accept the candidate when they confirm it. It SHALL record the number of local episodes that confirmed it. A movie SHALL get no episode comparison.

#### Scenario: Complete local season of a series with a wrong year

- **GIVEN** TMDB's year does not agree, Plex has episodes 1 to 3 of season 1, and TMDB lists episodes 1 to 3 in season 1 and 2 episodes in season 2
- **WHEN** autorefresh compares the episodes
- **THEN** it applies the match with an episode count of 3

#### Scenario: Movie with a year conflict

- **WHEN** TMDB's year does not confirm a movie
- **THEN** autorefresh asks TMDB for no episodes, and the item gets a plain refresh

### Requirement: Local episode numbers

Autorefresh SHALL read the local episode numbers from all episodes of the series in Plex, grouped by season. It SHALL ignore season 0. When any other episode has no season, a negative season, an episode number of 0 or less, or no file, or the listing is incomplete, there SHALL be no local numbers, and episodes SHALL confirm nothing.

#### Scenario: Episode without a file

- **WHEN** one regular episode of the series has no media file
- **THEN** the episode comparison confirms nothing and records no differing season

### Requirement: Episode comparison with TMDB seasons

Autorefresh SHALL compare each local season, in ascending order, with the same TMDB season. Each season SHALL have distinct positive numbers on both sides, and every local number SHALL be in TMDB. The first season that fails SHALL stop the check. Across the local seasons, Plex SHALL have at least 90% of TMDB's episodes. Seasons that Plex lacks SHALL give no evidence.

#### Scenario: Exactly 90%

- **WHEN** Plex has 9 of the 10 TMDB episodes of season 1
- **THEN** the episodes confirm the series with a count of 9

#### Scenario: Below 90%

- **GIVEN** no alternate TMDB episode group confirms the series
- **WHEN** season 1 is the only local season, Plex has its episodes 1 to 4 and 6 to 9, and TMDB lists episodes 1 to 9 of season 1
- **THEN** the episodes do not confirm, and the decision records season 1 with 8 Plex and 9 TMDB episodes and missing episode 5

#### Scenario: Extra episode

- **WHEN** local season 2 has episode 21 and TMDB season 2 has 20 episodes
- **THEN** the episodes do not confirm, and the decision records season 2 with extra episode 21

#### Scenario: Duplicate numbers

- **WHEN** a local or a TMDB season lists the same episode number twice, or a number of 0
- **THEN** that season does not confirm

#### Scenario: Season that TMDB lacks

- **WHEN** TMDB has no data for a local season
- **THEN** that season does not confirm

### Requirement: Alternate TMDB episode groups

When TMDB's seasons do not confirm valid local numbers, autorefresh SHALL try the episode groups of the types Original air date, DVD, Absolute, and TV, each ID once. It SHALL order the groups of each type by count: at least as many episodes as Plex, then unknown, then fewer. It SHALL then take at most 10, in turn from each type, and read them by count, then type. The first group that confirms SHALL end the check, and the decision SHALL record its name, trimmed to 120 characters, or its ID.

#### Scenario: Aired order confirms

- **WHEN** the default seasons do not confirm and an Original air date group confirms the local numbers
- **THEN** autorefresh applies the match and records that group's name

#### Scenario: Many groups of one type

- **WHEN** TMDB lists more than 10 groups of the Original air date type and one DVD group
- **THEN** autorefresh chooses at most 10 groups, and the DVD group is one of them

#### Scenario: Count order before the choice

- **WHEN** TMDB lists 12 Original air date groups, and only the last two have at least as many episodes as Plex
- **THEN** autorefresh chooses those two groups and reads them first

#### Scenario: DVD group with an unknown count

- **GIVEN** nine chosen Original air date groups with more episodes than Plex and a chosen DVD group with an unknown count
- **WHEN** none of the nine groups confirms and the budget has time left
- **THEN** autorefresh reads the DVD group last

#### Scenario: Earlier group confirms

- **WHEN** a chosen group confirms before the DVD group is reached
- **THEN** autorefresh does not read the DVD group

#### Scenario: Other group types

- **WHEN** a group has the type Digital, Story arc, or Production
- **THEN** autorefresh does not read it

#### Scenario: Invalid local numbers

- **WHEN** a local season has duplicate or non-positive numbers
- **THEN** autorefresh asks TMDB for no episode groups

### Requirement: Episode group seasons

Autorefresh SHALL read each group part with an order of 1 or higher as that season number, and each episode order plus 1 as its episode number. It SHALL skip order 0. A part or an episode without an order, with a negative order, or with a duplicate order SHALL make the whole group give no evidence.

#### Scenario: Episode without an order

- **WHEN** one episode of a group has no order
- **THEN** that group confirms nothing, and autorefresh tries the next group

### Requirement: Episode group bounds and failures

Episode group requests of one comparison SHALL share a 15 second budget within the caller's deadline. When it runs out, the check SHALL keep the result of the default seasons. A 401, or a 429 left after the TMDB retries, SHALL stop the comparison as a TMDB failure. Any other failure SHALL be logged: a failed group list keeps the result of the default seasons, and a failed read of one group makes the check try the next group.

#### Scenario: Budget runs out

- **WHEN** the 15 seconds end before a group confirms
- **THEN** autorefresh reads no more groups, records the differing season of the default seasons, and does not turn TMDB off

#### Scenario: One group fails

- **WHEN** the season read of one group answers 500
- **THEN** autorefresh logs a warning and tries the next group, and later items still use TMDB

#### Scenario: Group list fails

- **WHEN** the episode group list of the series answers 500
- **THEN** autorefresh logs a warning, reads no group, and keeps the result of the default seasons

### Requirement: TMDB fallback

When no path search found a candidate, autorefresh SHALL translate each IMDb path ID, and each TVDB path ID of a series, to a TMDB ID, and search Plex once for each different TMDB ID with `tmdb-<id>`. It SHALL then choose from these results only. A match from them SHALL have the action `tmdb-match`. A failed search with a translated ID SHALL fail the match search.

#### Scenario: TVDB ID of a series

- **GIVEN** a series folder with `{tvdb-5}`, which Plex does not resolve, and TMDB gives the TMDB ID 77
- **WHEN** autorefresh searches `tmdb-77` and finds one candidate
- **THEN** it applies that candidate with the action `tmdb-match` and the link `https://www.themoviedb.org/tv/77`

#### Scenario: TVDB ID of a movie

- **WHEN** a movie path holds only a TVDB ID
- **THEN** autorefresh asks TMDB nothing

#### Scenario: TMDB finds nothing

- **WHEN** TMDB has no entry, or several entries, for every path ID
- **THEN** the item gets a plain refresh, and the decision lists those path IDs as having no single TMDB entry

### Requirement: When the TMDB fallback is skipped

Autorefresh SHALL skip the TMDB fallback when a path search found a candidate. Otherwise, it SHALL skip it and record the first reason that applies: `tmdb-id` when the paths hold a `tmdb` ID, `no-source` when they hold no IMDb ID and no TVDB ID of a series, `off` when no TMDB token is set, and `failed` when a TMDB failure turned TMDB off earlier in the run.

#### Scenario: A first search found a candidate

- **WHEN** a path search found a candidate
- **THEN** autorefresh sends no TMDB request for the fallback and records no skip reason

#### Scenario: Path holds a TMDB ID

- **WHEN** the paths hold `{tmdb-1}` and `{tvdb-5}` and no search finds a candidate
- **THEN** the decision records the reason `tmdb-id`

### Requirement: TMDB ID lookup and cache

Autorefresh SHALL read a TMDB ID from its cache of the item and the external ID before it asks TMDB. TMDB SHALL give an ID only when its movie list, or its series list for a series, holds exactly one entry with a positive ID. Autorefresh SHALL cache each ID that TMDB found, also in a dry run, and never an empty answer. A failed cache read or write SHALL stop the run.

#### Scenario: Cached ID

- **WHEN** the cache holds the TMDB ID 77 for `tvdb-5` of the item
- **THEN** autorefresh searches `tmdb-77` and sends no TMDB request

#### Scenario: Item leaves the cache

- **WHEN** autorefresh removes the row of an item
- **THEN** the cached TMDB IDs of that item go with it

### Requirement: TMDB failure during a fix match

When a TMDB request of a fix match fails, autorefresh SHALL log `TMDB lookup failed`, count a failed request, record the decision `failed` with the error, and turn TMDB off for the rest of the run of that server. The item SHALL get a plain refresh. The end of the run SHALL not count as a TMDB failure. An episode group request SHALL count only with a 401, or a 429 left after the retries; its other errors and the end of the group budget follow the episode group rules.

#### Scenario: Lookup answers 500 or 401

- **WHEN** the TMDB lookup of `tvdb-5` fails for the first item
- **THEN** that item gets a plain refresh, a later item of the same server run asks TMDB nothing and records the skip reason `failed`, and the next run asks TMDB again

#### Scenario: Episode group answers 500

- **WHEN** the season read of one episode group of the first item answers 500
- **THEN** autorefresh logs a warning, skips that group, and a later item of the same server run still asks TMDB

#### Scenario: Episode group list answers 500

- **WHEN** the episode group list of the first item answers 500
- **THEN** autorefresh logs a warning, keeps the result of the default seasons, and a later item of the same server run still asks TMDB

#### Scenario: Year request fails

- **WHEN** TMDB's year request fails during a year confirmation
- **THEN** the decision is `failed`, keeps the conflict `year differs` and the folder year, and records no TMDB ID

### Requirement: No TMDB token

Without a TMDB token, autorefresh SHALL send no TMDB request. The fix match SHALL then use only the Plex searches by path ID and the folder year, and a series SHALL get no title fallback.

#### Scenario: Year conflict without a token

- **WHEN** a candidate has a year conflict that the folder year does not confirm
- **THEN** the conflict stays `year differs`, and the decision records the TMDB skip reason `off`

#### Scenario: Series without IDs and without a token

- **WHEN** a series folder holds no ID
- **THEN** autorefresh searches nothing and records the decision `no-ids`

### Requirement: Title fallback of a series

Autorefresh SHALL search Plex by the title and the year of a series when the series seeks a fix match, has a year, TMDB is available and not turned off in the run, its folders hold no different years, and no search by ID found a candidate. Only a candidate that the steps below confirm SHALL be applied, with the action `title-match`.

#### Scenario: Series without folder IDs

- **GIVEN** the series is unmatched, `unmatched` is not in `ignore-reasons`, a TMDB token is set, no TMDB failure turned TMDB off in the run, the folders hold no different years, and in a run Plex accepted a request for the series before
- **WHEN** a series with the year 2020 has no ID in its folder
- **THEN** autorefresh asks Plex for match candidates with its title and the year 2020

#### Scenario: Title search finds nothing

- **WHEN** the title search finds no candidate
- **THEN** the item gets a plain refresh, and the decision is `no-match`

#### Scenario: Movie

- **WHEN** an unmatched movie has no ID that finds a candidate
- **THEN** autorefresh makes no title search

### Requirement: Same-title candidate

A title candidate SHALL match the item's title (ignoring case, spaces, and punctuation) and have a year within 1 of the item year and any folder year. Titles without letters or digits SHALL match nothing. Duplicate rows with the same non-empty GUID SHALL count as one candidate. Exactly one candidate with a GUID SHALL remain; several SHALL be `several candidates`. Otherwise the stale Plex year TMDB rule SHALL apply.

#### Scenario: Two candidates with the same title and year

- **WHEN** the title search finds two candidates with different GUIDs and the title and year of the item
- **THEN** the conflict is `several candidates`, and the item gets a plain refresh

#### Scenario: Same series listed twice

- **WHEN** the title search returns two rows with the same non-empty GUID that both match the item title and year
- **THEN** autorefresh treats them as one candidate and confirms it instead of reporting `several candidates`

#### Scenario: Folder year differs from the item year

- **WHEN** the folder year differs by more than 1 from the year of a candidate
- **THEN** that candidate is not a same-title candidate

### Requirement: Confirmation of a title candidate

For the chosen title candidate, autorefresh SHALL ask the Plex metadata provider for its TMDB ID. That ID SHALL equal every TMDB ID that the folder names, directly or through TMDB, and no folder ID may have several TMDB entries. The TMDB episode numbers, with the episode groups, SHALL then confirm the local seasons.

#### Scenario: Confirmed

- **WHEN** the provider gives the TMDB ID 77, the folder names no other TMDB ID, and the episodes confirm
- **THEN** autorefresh applies the candidate with the action `title-match` and the link `https://www.themoviedb.org/tv/77`

#### Scenario: Provider has no TMDB ID

- **WHEN** the provider does not know the GUID or gives no single TMDB ID
- **THEN** the conflict is `not confirmed`

#### Scenario: Folder ID differs

- **WHEN** the folder holds `{tmdb-99}` and the provider gives 77, or the folder holds a TVDB ID with several TMDB entries
- **THEN** the conflict is `folder id differs`

#### Scenario: Episodes differ

- **WHEN** the TMDB episode numbers do not confirm the local seasons
- **THEN** the conflict is `not confirmed`, and the decision records the differing season

### Requirement: TMDB premiere year for a stale Plex year

When no candidate has the same title and an agreeing year, and the folder year is absent or equals the item year, autorefresh SHALL check the same-title candidates with a GUID and another known Plex year. It SHALL check at most 3. For each one, TMDB's premiere year of the provider's TMDB ID SHALL equal the item year, and the folder year if there is one. The one candidate that passes SHALL then need the folder ID and episode checks.

#### Scenario: Plex year of an earlier pilot

- **GIVEN** the item year is 2020 and the one same-title candidate has the Plex year 2017
- **WHEN** TMDB's premiere year of the provider's TMDB ID is 2020 and the episodes confirm
- **THEN** autorefresh applies that candidate and records the TMDB year 2020

#### Scenario: More than 3 candidates

- **WHEN** 4 same-title candidates have another Plex year
- **THEN** autorefresh sends no provider or TMDB request for them, and the conflict is `several candidates`

#### Scenario: No premiere year agrees

- **WHEN** no checked candidate's premiere year equals the item year
- **THEN** the conflict is `year differs`

#### Scenario: Several premiere years agree

- **WHEN** two checked candidates have a premiere year that equals the item year
- **THEN** the conflict is `several candidates`

#### Scenario: Premiere year source

- **WHEN** TMDB lists no season 1, or season 1 has no valid date
- **THEN** the candidate has no premiere year, and a later season's year never stands in

#### Scenario: Candidate without a Plex year

- **WHEN** a same-title candidate has no Plex year
- **THEN** autorefresh does not check it

### Requirement: Failures in the title fallback

When the title search, a provider lookup, or the Plex episode read of the title fallback fails, autorefresh SHALL log a warning, count a failed request, record the decision `failed`, and send a plain refresh, also after a 401 or 503 answer or a transport failure. A 404 answer of the title search or the episode read SHALL mean that the item is gone.

#### Scenario: Title search answers 500

- **WHEN** the title search fails with a server error
- **THEN** the item gets a plain refresh, and the run goes on with the next item

#### Scenario: Title search answers 503

- **WHEN** the title search answers `503 Service Unavailable`
- **THEN** the title search does not end the run, and the item gets a failed decision and a plain refresh

#### Scenario: Episode read answers 404

- **WHEN** the Plex episode read answers 404
- **THEN** autorefresh treats the item as gone from Plex

### Requirement: Metadata provider lookup

Autorefresh SHALL ask the Plex metadata provider only about a GUID of the form `plex://show/` and 24 hexadecimal characters, at `https://metadata.provider.plex.tv/library/metadata/<id>`, with the token of the server in a header. This SHALL be the only request of the Plex client to a host other than the configured server. The answer SHALL give a TMDB ID only when it names the same GUID and holds one distinct `tmdb://` ID.

#### Scenario: Other GUID

- **WHEN** the candidate GUID is a movie GUID or has another form
- **THEN** autorefresh sends no provider request, and the candidate has no TMDB ID

#### Scenario: Several TMDB IDs

- **WHEN** the provider answer holds two different `tmdb://` IDs
- **THEN** the candidate has no TMDB ID

#### Scenario: Answer for another item

- **WHEN** the provider answers 404, with no metadata, or with another GUID
- **THEN** the provider does not know the GUID

### Requirement: Applying the chosen match

For a chosen match, a run SHALL send one Plex match request with the GUID, the name, and the year of the candidate, if it has one. It SHALL record the request with the action `match`, `tmdb-match`, or `title-match`, the candidate, and the web pages of the IDs that found or confirmed it. A dry run SHALL send nothing and log the planned match.

#### Scenario: Links of a match

- **WHEN** `tmdb-747059` found the candidate of a movie
- **THEN** the match carries the link `https://www.themoviedb.org/movie/747059`

#### Scenario: Dry run

- **WHEN** a dry run finds a match
- **THEN** it logs `Dry run` with the action and the candidate and sends no match request

### Requirement: Follow-up of an accepted match

After Plex accepts a match, autorefresh SHALL send a refresh of the item and then check a cached item after waits of 1, 2, and 4 seconds. A check that finds the item complete SHALL move it to Fixed. The checks SHALL end when the item is complete, gone, or no longer cached, or when the waits run out. A run SHALL record these steps and count a completion as its own.

#### Scenario: Match fixes the item

- **WHEN** the second check finds the matched item complete
- **THEN** the item leaves the cache with a completion that credits the refresh after the match, and the run counts it as completed after a match

#### Scenario: Refresh answers 404

- **WHEN** the refresh after the match answers 404
- **THEN** autorefresh records the item as gone, makes no check, and the run goes on

#### Scenario: Server becomes unavailable

- **WHEN** the refresh after the match finds the server unavailable
- **THEN** the follow-up ends, and the run of that server ends as failed

#### Scenario: Refresh answers 500

- **WHEN** the refresh after the match answers 500
- **THEN** autorefresh logs `Following up a request failed` and still checks the item

### Requirement: Content of a match decision

Each fix match SHALL produce a decision with its outcome (`matched`, `no-match`, `no-ids`, or `failed`), the conflict, the error, the item year, the folder year, the TMDB year, and the episode count. It SHALL list each search with its query, source (`path`, `tmdb`, `tie`, or `title`), the ID it came from, whether it failed, and its candidates. It SHALL keep the TMDB ID, the skip reason, the IDs without one TMDB entry, the episode group, and the differing season.

#### Scenario: Decision is not recorded

- **WHEN** the run is a dry run, a store write fails, the run ends during the search, or Plex answers 404 for the item
- **THEN** autorefresh records no decision

#### Scenario: Failure after a conflict

- **WHEN** a step fails after the searches found a year conflict
- **THEN** the decision keeps the conflict and the years that it compared before the failure

### Requirement: Candidate verdicts

Each candidate of a decision SHALL have the GUID, name, year, type, summary, poster, and score that Plex gave, and a verdict: `picked`, `confirms` for a check search, `different candidate`, `not compared`, `year agrees`, `other title`, or a conflict text. A decision that a handled failure ended SHALL leave every verdict empty.

#### Scenario: Year conflict of one search

- **WHEN** a year conflict comes from one search and another search found the same candidate with an agreeing year
- **THEN** the first candidate gets `year differs` and the other gets `year agrees`

#### Scenario: Other title in a title search

- **WHEN** a title search candidate has another title or a year that does not agree, and TMDB did not check it
- **THEN** its verdict is `other title`

### Requirement: Preview of the automatic decision

Autorefresh SHALL preview the automatic decision for any known item using run rules, always reading item paths from Plex and ignoring prior run TMDB failures. It SHALL record no decision, send no match, and write nothing to the cache, while reusing TMDB ID translations resolved during the same preview. Handled failures SHALL stay in the decision; cancellation, store errors, or Plex 404 SHALL return an error.

#### Scenario: Item that a run skips

- **WHEN** the preview runs for a matched item for which Plex accepted no request yet
- **THEN** it skips the run's eligibility check, returns the decision that the searches give, and the item history stays unchanged

#### Scenario: Preview of a movie

- **WHEN** the preview runs for an unmatched movie
- **THEN** it asks Plex for the paths of the movie, also when a run would take them from its library read

#### Scenario: Preview reuses translated TMDB ID in title fallback

- **GIVEN** a series folder holds `{tvdb-5}` whose TMDB translation yields no Plex match, so the title fallback runs
- **WHEN** the preview confirms the title candidate against folder IDs
- **THEN** autorefresh reuses the TMDB ID resolved earlier in the same preview and does not call TMDB a second time for `tvdb-5`

#### Scenario: Time of the preview

- **WHEN** a preview returns its decision
- **THEN** the decision time has whole seconds, like a recorded decision
