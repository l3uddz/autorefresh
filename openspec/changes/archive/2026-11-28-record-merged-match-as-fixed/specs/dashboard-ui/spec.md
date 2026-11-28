## MODIFIED Requirements

### Requirement: Item detail

The item detail SHALL show the item art faded behind its head, the poster, the title, the type, the year, the Plex season and episode counts, the library, the reasons, and the live Plex summary and path. It SHALL list the queue state, stop time, fix, removal, attempts, last action, and last accepted match. Fixed and Removed SHALL come from the newest completed or removed event. With a known server identity, it SHALL offer "Open in Plex" in a new tab.

#### Scenario: No summary

- **WHEN** Plex has no summary for the item
- **THEN** the detail says "Plex has no summary for this item."

#### Scenario: Plex no longer has the item

- **WHEN** the live Plex view answers that the item is gone, and the newest completed or removed event is not a merged fix
- **THEN** a warning says "Plex no longer has this item" and the cached details stay

#### Scenario: Live view fails

- **WHEN** the live Plex view fails for another reason
- **THEN** a banner says "Live details are unavailable" and the cached details show

#### Scenario: Item no longer known

- **WHEN** autorefresh no longer knows the item
- **THEN** the detail shows "This item is no longer known" and no old details

#### Scenario: Detail fails

- **WHEN** the detail cannot be read for another reason
- **THEN** an error banner offers Try again

#### Scenario: Fixed item

- **WHEN** the newest completed event says that a match fixed the item
- **THEN** the detail shows "Fixed" with "match" and how long ago

#### Scenario: Removal of a tracked item

- **WHEN** the newest such event is a removal but autorefresh tracks the item again
- **THEN** the detail shows no Removed fact

#### Scenario: Last match

- **WHEN** Plex accepted a match that the user chose
- **THEN** the Match fact shows the match title and year with "matched by hand" and its date

#### Scenario: Open in Plex

- **WHEN** the machine identifier of the item's server is known
- **THEN** "Open in Plex" opens the item on app.plex.tv in a new tab
- **AND** without that identifier, the detail shows no such link

### Requirement: Refresh now

The item detail SHALL offer Refresh now without a confirmation; `manual-actions` gives the server rules. Its toast SHALL name the applied match, with its year when known, or the accepted refresh. When the answer has the item detail, the dashboard SHALL show it at once. The toast SHALL add a check result, read from the answer, only when the answer gives one: complete, merged, still missing reasons, or gone. For a plain refresh of a gone item, the toast SHALL be only the removal notice.

#### Scenario: Item complete

- **WHEN** after a plain refresh, the answer has the item detail and sets `checked`, the item is no longer in the queue, and its newest completion or removal event is a completion
- **THEN** a success toast says "Plex accepted the refresh of <title>. It is complete."

#### Scenario: Item complete after a fix match

- **WHEN** the refresh applied a fix match, and the answer has the item detail and sets `checked`, the item is no longer in the queue, and its newest completion or removal event is a completion without `mergedKey`
- **THEN** a success toast says "Matched <title> to <match>", with the year when known, followed by ". It is complete."

#### Scenario: Answer without a check result

- **WHEN** the answer has the item detail, and it fits none of the cases complete, merged, still missing, or gone
- **THEN** the dashboard shows the detail at once, and a success toast names only the accepted refresh or the applied match

#### Scenario: Still missing

- **WHEN** the answer has the item detail and sets `checked`, and the item is still in the queue with reasons
- **THEN** a notice toast says "Still missing:" with the reasons

#### Scenario: Match applied

- **WHEN** the refresh applied a fix match
- **THEN** the toast starts with "Matched <title> to <match>", followed by " (<year>)" when the year is known

#### Scenario: Answer without the item detail

- **WHEN** Plex accepted the refresh or the fix match, but the answer has no item detail
- **THEN** the dashboard keeps the detail that it shows and reads it again
- **AND** a success toast says "Plex accepted the refresh of <title>", or "Matched <title> to <match>" after a fix match, with the year when known, and no check result

#### Scenario: Item gone after the refresh

- **WHEN** the answer has the item detail, the item is no longer in the queue, and either the answer sets `checked` and the newest completion or removal event is a removal, or it does not and the newest event is a removal
- **THEN** after a plain refresh, a notice toast says only "Plex no longer has <title>. Autorefresh removed it from the queue."
- **AND** after a fix match, the notice toast says "Matched <title> to <match>", with the year when known, followed by ". Then Plex no longer has <title>. Autorefresh removed it from the queue."

#### Scenario: Plex answers the refresh with 404

- **WHEN** the refresh fails because Plex no longer has the item
- **THEN** an error toast says "Plex no longer has this item. It was deleted or moved."

#### Scenario: Match that Plex merged

- **WHEN** the refresh applied a fix match, and the answer has the item detail and sets `checked`, the item is no longer in the queue, and its newest completion or removal event is a completion with `mergedKey`
- **THEN** a success toast says "Matched <title> to <match>", with the year when known, followed by ". Plex merged it into the item that already had this match."

#### Scenario: Merge without an applied match

- **WHEN** after a plain refresh, the answer has the item detail and sets `checked`, the item is no longer in the queue, and its newest completion or removal event is a completion with `mergedKey`
- **THEN** a success toast says "Plex accepted the refresh of <title>. It is complete."

## ADDED Requirements

### Requirement: Merged fix in the item detail

When the newest completed or removed event of an item is a merged fix, the item detail SHALL show the Fixed fact as "<match> · merged into another item" with how long ago. When the live Plex view answers that the item is gone, it SHALL say "Plex merged this item into another item" instead of the gone warning. With a known server identity, it SHALL offer "Open the merged item in Plex", which opens the kept item on app.plex.tv in a new tab.

#### Scenario: Detail of a merged fix

- **WHEN** the newest completed event of item `560945` is a merged fix into `564849` by a match, and the live view answers 404
- **THEN** the Fixed fact says "match · merged into another item", an info banner says "Plex merged this item into another item", and "Open the merged item in Plex" opens item `564849`

#### Scenario: Unknown server identity

- **WHEN** the machine identifier of the server is not known
- **THEN** the banner shows no link

#### Scenario: Removed item

- **WHEN** the newest completed or removed event is a removal, and the live view answers 404
- **THEN** the warning says "Plex no longer has this item"

### Requirement: Merged fix in the history

The history SHALL show a merged fix as "Fixed: Plex merged it into another item" with "By <match> · Plex kept the item that already had this match".

#### Scenario: History of a merged fix

- **WHEN** the history holds a `completed` event with `mergedKey` and the match kind `match`
- **THEN** it shows "Fixed: Plex merged it into another item" with "By match · Plex kept the item that already had this match" and a success marker
