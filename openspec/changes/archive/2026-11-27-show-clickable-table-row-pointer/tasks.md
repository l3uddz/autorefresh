# Tasks

## 1. Show the pointer on clickable table rows

- [x] 1.1 Add the cursor style to the shared row-opening plugin and describe the cue in `README.md`. In a browser with test data, verify the spec on Items and Runs, including cell padding and the Started cell. Check that plain headers keep their usual cursor, row click and Enter or Space still open details, Items still renders rows as they scroll into view, and Items List mode and narrow Runs list rows still open details. Run `mise run vendor`, `mise run test`, `mise run lint`, `mise run web-install`, `mise run web-lint`, `mise run web-test`, and `mise run test-webui` in order, then run `pnpm exec astryx doctor --built .` in `web/`. Use commit subject `fix(web): show a pointer on clickable dashboard table rows`.
