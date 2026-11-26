# Proposal: run "now" panel

> Imported from the retired superpowers workflow (#147). `design.md` is the approved design. The implementation plan is in `plan.md` (or `plans/`), kept as written, and `tasks.md` only points to it, because the plans did not track progress in their checkboxes. `openspec/specs/` describes the current behaviour, which has moved on since this change.

## Why

While a run was busy, the server card almost always said "Waiting for the next item", because each item takes milliseconds and the run then waits `refresh-delay`. The card never said which library an item is in, why it is due, or what happened to the last item.

## What Changes

- A "now" panel under the progress bar of a busy server card shows the item in work, or the next item with a countdown to its start.
- A footer shows the last finished item with its outcome and a relative time.
- A bulk refresh uses the same panel and shows only what it knows.

Non-goals: no history of more than one item in the panel, no new API route, and no change to pacing, run order, or what the refresher sends.

## Impact

`internal/refresher` (progress reports), `internal/web` (status snapshot), and `web/` (the panel). Issue #126.
