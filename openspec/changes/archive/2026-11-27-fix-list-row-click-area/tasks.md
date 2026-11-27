# Tasks

## 1. Dashboard List Links and Toast Auto-Close

- [x] 1.1 `fix(web): make full linked list rows clickable across dashboard lists` — Add `web/src/listlink.tsx` exporting `LinkListItem` (wrapping Astryx `ListItem` with both `href` and surface `onClick`/`onMouseDown`/`onMouseLeave`/`onMouseUp` handling using `hasTextSelection` and `hasInteractiveAncestor`, omitting `interactiveRef` and `role`, and requiring middle-click press and release on the same eligible row surface) and unit tests in `web/src/listlink.test.tsx` that exercise events through the real rendered Astryx `ListItem`/`Item` `<li>`; switch all linked `ListItem` usages in `web/src/items.tsx` (`ItemList`), `web/src/status.tsx` (`RecentFixes`, `RunRow`), and `web/src/rundetail.tsx` (`EntryRow`) to `LinkListItem`; document `listlink.tsx` in `AGENTS.md` and note full-row list clicks in `README.md`; verify with `mise run web-lint && mise run web-test`.
- [x] 1.2 `fix(web): auto-close error toasts with default duration` — Pass `isAutoHide: true` ahead of caller `options` in `web/src/notify.tsx` so error toasts (including error bulk refresh reports) auto-close using Astryx's stock 5-second default duration while preserving caller overrides; add unit tests in `web/src/notify.test.tsx`; note the behaviour in `AGENTS.md` and `README.md`; verify with `mise run web-lint && mise run web-test`.

## 2. Verification

- [x] 2.1 Run browser verification against the built dashboard via `browserless` (using a local preview backed by stubbed API responses with no live Plex writes) to confirm clicking the empty grey row area, poster, and trailing metadata opens details across Items list mode, Status lists, and Run details, modifier/middle-click opens a new tab, dragging to select text does not navigate, and error toasts auto-close after 5 seconds; close the browserless session cleanly.
- [x] 2.2 Run the full CI verification sequence (`mise run vendor`, `mise run test`, `mise run lint`, `mise run web-install`, `mise run web-lint`, `mise run web-test`, `mise run test-webui`) and `pnpm exec astryx doctor --built .` in `web/`, and confirm `git diff --exit-code -- go.mod go.sum` is clean.

## Workflow follow-up

- Sync the delta spec in `openspec/changes/fix-list-row-click-area/specs/dashboard-ui/spec.md` into `openspec/specs/dashboard-ui/spec.md`, verify `openspec validate --all --strict`, and archive the change once implementation and review are complete.
