# Proposal: maintenance dashboard

> Imported from the retired superpowers workflow (#147). `design.md` is the approved design. The implementation plans of the five stages are in `plans/`, kept as written, and `tasks.md` only points to them, because the plans did not track progress in their checkboxes. `openspec/specs/` describes the current behaviour, which has moved on since this change.

## Why

Autorefresh runs unattended. Its log was the only way to see what it does, and the Plex web app was the only way to fix an item that it cannot fix. The maintainer wants a web UI, served by autorefresh itself, to see its status, work the list of unfixed items, fix items by hand, and see why each automatic fix match decided as it did.

## What Changes

- An optional HTTP listener in the same binary and container, with optional Basic auth.
- Storage and recording of item history, fix match decisions, and runs, with a retention period and cascading deletes.
- A read API with live status over server-sent events, and a poster proxy.
- A write API: refresh an item, Check now, fix match search and apply, the episode check, stop and resume, Run now, and bulk refresh.
- A React frontend with Status, Items (Attention, Fixed, Stopped), and Runs pages and a Fix match dialog, in stock Astryx with a dark theme.

Non-goals: no library-wide refresh or scan, no configuration editing in the UI, no light theme, no user accounts, and no bulk fix match.

## Impact

`internal/web` (new), `web/` (new), `internal/store`, `internal/refresher`, `internal/plex`, `internal/config`, `cmd/autorefresh`, the build and the release. Issue #94, delivered in five stages (plans in `plans/`, stage 4 issue #100, stage 5 issue #102).
