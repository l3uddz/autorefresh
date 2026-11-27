# Proposal

GitHub issue: #153 (context: #94)

## Why

Two dashboard interaction gaps affect everyday use:

1. In the dashboard's list views, every interactive list row renders a hover highlight and pointer cursor across the full row, but clicking the empty area around or below the text, the poster, or the trailing metadata does nothing on desktop. Astryx `ListItem` with `href` puts its anchor only around the label and description and forwards clicks on the rest of the row only for coarse (touch) pointers. Users click the grey space beside or below a row's text expecting to open its details, and instead only select text.
2. Error action toasts stay open indefinitely until dismissed manually, because Astryx defaults `isAutoHide` to `false` for `type: 'error'` while auto-closing `info` toasts after 5 seconds. That also keeps failed bulk refresh end reports open past their 10-second duration.

## What Changes

- Make the full surface of every linked list row in the dashboard open its target on click, including the poster, empty row area, padding, and trailing metadata, across all list views (Items list mode, Status recently fixed and recent runs, narrow Runs list, and Run details item lists).
- Preserve native link semantics and keyboard focus on each row, support opening in a new tab on Ctrl/Meta/Shift-click and middle-click across the full row surface, avoid navigating when the user finishes a text selection inside a row, and leave clicks on any nested interactive controls untouched.
- Default every action toast (success, notice, and error) to auto-close using Astryx's stock 5-second auto-hide duration (which pauses on hover and focus), while keeping per-toast overrides such as the 10-second bulk refresh end report working for all outcomes including errors.

### Surface Summary

- **Configuration keys**: None added, changed, or removed.
- **API routes**: None added, changed, or removed.
- **Dashboard pages**: No pages added or removed. List row click behaviour changes on the Status page (Recently fixed and Recent runs), the Items page (List mode on Attention, Fixed, and Stopped tabs), the Runs page (narrow-screen list), and Run details (item outcome lists). Error toast auto-close applies to every action toast across the dashboard.
- **Plex safety**: No Plex or TMDB requests change.

### Non-Goals

- Changing table-mode row interaction (`ItemTable` and `RunTable` already handle clicks across the full table row).
- Changing visual layout, spacing, hover colours, or Astryx theme tokens of list rows.
- Adding a user-facing configuration option for toast duration (Astryx's stock 5-second default is used).
- Upgrading `@astryxdesign/core` or patching files inside `node_modules`.

## Capabilities

### New Capabilities
<!-- None -->

### Modified Capabilities
- `dashboard-ui`: Require every linked list row across the Status, Items, Runs, and Run details views to open its target from clicks anywhere on the row surface (not only on the label or description text), while keeping native link behaviour, modifier/middle-click new-tab navigation, and text-selection safety; and require every action toast, including error toasts, to auto-close after 5 seconds unless another duration is given.

## Impact

- **Frontend (`web/src/`)**: Add a small shared link-row wrapper over Astryx `ListItem` with unit tests, use it in `items.tsx`, `status.tsx`, and `rundetail.tsx` (covering narrow-screen `runspage.tsx` through `RunRow`), and default `isAutoHide: true` in `notify.tsx` with unit tests.
- **Docs**: Update `AGENTS.md` to note the new frontend helper module and toast auto-close behaviour, and add a brief note under Dashboard in `README.md` covering full-row list clicks and automatic toast dismissal.
