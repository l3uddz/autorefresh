# Design

## Context

The Items and Runs tables use `useRowOpen` to add click and keyboard handlers to body rows. `hasHover` adds the Astryx background highlight. The Items table uses plain text columns and a flat row model so Astryx can render only the visible rows. The List view uses linked Astryx list rows and has its own cursor styling.

## Decisions

Add a StyleX `cursor: 'pointer'` style to the Astryx body-row `xstyle` in `useRowOpen`. Append it to the styles supplied by other table plugins. This places the cue where both tables already attach activation and keeps headers outside its scope.

A link in the primary table cell would require a custom cell renderer, which prevents the Items table from using row virtualization. A global table style would also cover rows that have no details action.

## Risks and verification

Cursor styles can inherit into child elements. Check the computed cursor over text, padding, and empty cell space, and check that the Items selection checkbox keeps its usual cursor and toggles selection. Compare a hoverable but inactive surface, such as a plain header, to confirm the style stays on body rows. Verify row click and keyboard activation and check an Items list row and a narrow Runs list row with a mouse.

The styling adds no Plex or TMDB requests and changes no request pacing.
