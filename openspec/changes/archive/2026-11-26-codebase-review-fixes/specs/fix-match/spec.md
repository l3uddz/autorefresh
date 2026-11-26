# Spec Delta: fix-match

## MODIFIED Requirements

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
