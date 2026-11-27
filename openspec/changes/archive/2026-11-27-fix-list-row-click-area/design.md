# Design

## Context

See `proposal.md` for motivation and live diagnosis.

In `@astryxdesign/core` `0.7.17`, `ListItem` renders `Item` with `as="li"`. When a `ListItem` receives `href` without `onClick`:

- `Item` renders a flex `<li>` with `cursor: pointer` and a full-row hover overlay.
- `startContent` (the poster) and `endContent` (tries, relative time, status/timestamp) render in `<span>` siblings outside the anchor.
- Only `label` and `description` sit inside the `<a>` element (`flex: 1`, `align-self: auto`). On rows with `align="start"` or a tall poster, the `<a>` box covers only the text lines (40 px of a 101 px row on Fixed items), leaving the poster, the empty area below the text, the row padding, and `endContent` outside the anchor.
- `Item`'s container click handler sets `isLinkRow = isListRow && onClick == null && href != null && !isLinkRoot`. When `isLinkRow` is true, clicks outside `<a>` are forwarded to the anchor only for coarse (touch) pointers or clicks in the outer touch band, so desktop mouse clicks on the rest of the row are silently dropped.

Every linked list in the dashboard uses `ListItem` with `href`:

- `web/src/items.tsx` (`ItemList`)
- `web/src/status.tsx` (`RecentFixes`, and `RunRow` used by `RecentRuns` and narrow-screen `web/src/runspage.tsx`)
- `web/src/rundetail.tsx` (`EntryRow`)

All four pass relative hash URLs (`#/...`) built by `itemHash` or `runHash` in `web/src/route.ts`.

## Goals / Non-Goals

**Goals:**

- Make clicks anywhere on the `<li>` surface of every linked list row navigate to its `href`, including desktop fine pointers and touch coarse pointers.
- Preserve Astryx's native `<a href>` rendering (single tab stop, `aria-current="true"` on the link, link semantics for assistive tech, native link behaviour when clicking label/description).
- Support modifier clicks (Ctrl/Meta/Shift) and middle-click (`mouseup` with `button === 1`) to open the target in a new tab (`noopener`) from anywhere on the row.
- Ignore surface clicks when the user has an active text selection inside the row or clicks a nested interactive element.
- Follow `web/AGENTS.md`: use Astryx components and hooks only, with zero raw HTML elements, zero custom CSS, and zero new `stylex.create` declarations.

**Non-Goals:**

- Modifying `ItemTable` or `RunTable` (`useRowOpen` already handles full-row clicks on `<tr>`).
- Changing `@astryxdesign/core` version or patching files inside `node_modules`.
- Changing Plex or TMDB request behaviour.

## Decisions

### 1. Shared `LinkListItem` wrapper passing both `href` and `onClick`/`onMouseUp` to `ListItem`

Add a small module `web/src/listlink.tsx` exporting `LinkListItem` and `LinkListItemProps`. `LinkListItem` wraps Astryx `<ListItem>` with a required `href: string`, omits `interactiveRef` and `role` (both of which suppress the native anchor in Astryx; `interactiveRef` also emits a dev warning with `href`/`onClick`), and attaches `onClick`, `onMouseDown`, `onMouseLeave`, and `onMouseUp` handlers that delegate non-anchor surface clicks to the row's hash target while requiring middle-click press and release to stay on the same eligible row surface.

**Why this works in Astryx `0.7.17`:**

- When both `href` and `onClick` are passed to `Item`:
  - `Item` checks `href != null` before `onClick != null`, so it still renders the native `<a href={href}>` (not `<button>`).
  - `isLinkRow` becomes `false`, so `Item`'s `handleContainerClick` calls `onClick(e)` for clicks outside `button, a, input, select, textarea`.
  - Clicks directly on `<a>` (label/description) are skipped by `Item`'s `target.closest('button, a, input, select, textarea')` guard, preventing double navigation.
  - No Astryx dev warning fires (`useDevWarning` only warns when `interactiveRef` is combined with `onClick`/`href`).
- In `LinkListItem`'s handlers:
  - Reuse Astryx's exported `hasTextSelection` and `hasInteractiveAncestor` from `@astryxdesign/core/hooks` so text selection and nested-control rules match the rest of Astryx.
  - For plain primary clicks (`button === 0`, no `metaKey`/`ctrlKey`/`shiftKey`/`altKey`), navigate by assigning `window.location.hash = href.slice(1)` when `href` starts with `#` (matching `navigate` in `web/src/route.ts`), or `window.location.assign(href)` otherwise.
  - For Ctrl-click, Meta-click, Shift-click, or middle-click (`onMouseUp` with `button === 1`, matching Astryx's `useClickableContainer` for cross-browser compatibility including older Safari), call `window.open(href, '_blank', 'noopener')`. On middle-click `mousedown`/`mouseup` outside `<a>`, call `event.preventDefault()`.
  - Guard `isDisabled`, `defaultPrevented`, and `altKey`.

**Alternatives considered:**

- **Switch `ListItem` from `href` to `onClick` only**: Astryx renders `<button>` instead of `<a href>`, losing native link semantics, browser URL preview on hover, right-click "Open link in new tab" / "Copy link address", and native middle/Cmd-click on the text. Rejected.
- **Stretch `<a>` across `<li>` via CSS (`a::after { inset: 0 }`)**: `web/AGENTS.md` forbids custom CSS files and StyleX forbids descendant selectors (`& a`), so `xstyle` on `ListItem` cannot target the inner `<a>`. Rejected.
- **Patch `@astryxdesign/core` via pnpm patches**: Adds upgrade maintenance risk and diverges from the pinned upstream package when a clean public-prop composition solves it. Rejected.

### 2. Default `isAutoHide: true` for all action toasts in `web/src/notify.tsx`

In `@astryxdesign/core` `0.7.17`, `ToastViewport` resolves `isAutoHide = options.isAutoHide ?? (type === 'error' ? false : true)` and `autoHideDuration = options.autoHideDuration ?? 5000`, and `Toast` pauses auto-dismiss on hover and focus.

In `web/src/notify.tsx`, `useNotify` forwards `showToast({ isAutoHide: true, ...options, type: ..., body: ... })`.

- Success and notice toasts continue to auto-close after Astryx's stock 5-second duration.
- Error toasts now also auto-close after Astryx's stock 5-second duration instead of staying open until dismissed manually.
- Per-toast options passed by callers still take precedence, so `web/src/app.tsx`'s `{ autoHideDuration: 10_000 }` bulk refresh end report now auto-closes after 10 seconds for error outcomes too, and any future caller can pass `isAutoHide: false` if a toast must stay sticky.

**Alternatives considered:**

- **Hard-code a custom `autoHideDuration` constant in `notify.tsx`**: Unnecessary when Astryx already provides a stock 5-second default (`5000 ms`) and the maintainer prefers the stock capability. Keeping `autoHideDuration` unset lets Astryx's default apply directly while callers can still pass `autoHideDuration`.

### 3. Plex and TMDB load

This change runs solely in the browser frontend and does not alter any Plex, Plex metadata provider, or TMDB request count, pacing, or API endpoint.

### 4. Verification strategy

- Unit tests in `web/src/listlink.test.tsx` (Vitest) verify:
  - `LinkListItem` renders a native `<a href="...">` inside `<li>` (preserving link semantics).
  - Plain click on the row surface navigates to the hash target.
  - Ctrl/Meta/Shift-click and middle-click open a new tab with `noopener`, while middle-button releases whose press started outside the row, on a nested control, or left the row do not navigate.
  - Clicks inside interactive descendants (`<a>`, `<button>`, `[role="button"]`), clicks with an active text selection inside the row, disabled rows, `altKey` clicks, and `defaultPrevented` events do not navigate.
- Unit tests in `web/src/notify.test.tsx` (Vitest) verify:
  - `useNotify` passes `isAutoHide: true` for `success`, `notice`, and `error` outcomes without overriding Astryx's default `autoHideDuration`.
  - Caller-provided options (`autoHideDuration`, `isAutoHide: false`) override the defaults.
- Browser verification with `browserless` (Playwright over CDP) against a local preview of the built frontend (backed by mock/stubbed API data or a local test server with no live Plex writes) verifies clicking the empty grey row area, poster, and trailing metadata opens the detail pane/drawer across Items list mode, Status lists, and Run details, dragging to select text does not navigate, and an error toast auto-closes after 5 seconds.

## Risks / Trade-offs

- **[Upstream Astryx change to `Item` when both `href` and `onClick` are provided]** → `web/package.json` pins `@astryxdesign/core` to exact version `0.7.17`, `web/src/listlink.test.tsx` renders `<List><LinkListItem /></List>` through Astryx's real `ListItem`/`Item` components and drives `onClick`/`onMouseDown`/`onMouseUp` on the rendered `<li>`, and `browserless` verification confirms real Chromium interaction.
- **[Right-click context menu on empty grey area / poster]** → Right-clicking outside `<a>` still opens the generic browser menu rather than the link menu ("Copy link address"), because the anchor itself only wraps label and description. Clicking, modifier-clicking, and middle-clicking anywhere on the row all work as expected.
- **[Browser popup blockers on modifier-click `window.open`]** → `window.open` runs synchronously inside the user's click/mouseup event handler, which browsers treat as user-initiated activation (the same activation rule Astryx's `useClickableContainer` relies on; `LinkListItem` additionally treats Shift-click as a new-tab open on the row surface).
