# Proposal: run details, list sort, match check, and next run

> Imported from the retired superpowers workflow (#147). `design.md` is the approved design. The implementation plan is in `plan.md`, kept as written, and `tasks.md` only points to it, because the plans did not track progress in their checkboxes. `openspec/specs/` describes the current behaviour, which has moved on since this change.

## Why

The Runs page showed the counts of each run but not which items they cover. List mode on the Items page could not sort. After a manual fix match that Plex accepted, the item stayed in Attention until its next refresh. The dashboard did not say when the next scheduled run starts.

## What Changes

- A click on a run opens a drawer that lists the items of the run, grouped by outcome, each linked to its item detail.
- List mode sorts by the same keys as Table mode, per tab.
- An item that a manual match fixed moves to Fixed at once.
- The Status and Runs pages show the next scheduled run.

Non-goals: no item list for the read and due counts, no new item filters, no backfill for older runs, and no second Plex refresh after a manual match.

## Impact

`internal/store` (events get a run ID), `internal/refresher`, `internal/web` (run detail route), and `web/`. Issues #104, #105, #106, and #107.
