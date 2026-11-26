# Proposal: autorefresh

> Imported from the retired superpowers workflow (#147). `design.md` is the approved design. The implementation plan is in `plan.md`, kept as written, and `tasks.md` only points to it, because the plans did not track progress in their checkboxes. `openspec/specs/` describes the current behaviour, which has moved on since this change.

## Why

Plex sometimes keeps items with incomplete metadata: unmatched, or with no summary, poster, or background art. A refresh of the single item often fixes this, sometimes only after several tries over some days, and some unmatched items need a fix match. Autorefresh does this work on a schedule.

## What Changes

- Find incomplete movies and series in every configured Plex server.
- Refresh each incomplete item, one item at a time, until it is complete.
- Fix the match of an unmatched item when its file path holds IDs that identify one Plex entry.
- Keep a local cache, so that most runs read only the items that changed.
- Limit the load on Plex with the schedule, the delay between refreshes, and a limit for each run.
- Offer a dry run that logs the actions of a run but does not send them.

Non-goals: no link to autoscan, no Emby or Jellyfin, no season or episode refreshes, no web interface (later added by `2026-11-22-maintenance-dashboard`), and no limit on the attempts for an item.

## Impact

The first version of the service: `cmd/autorefresh`, `internal/config`, `internal/plex`, `internal/store`, `internal/refresher`, and the Docker image. Issue #7, design PR #8.
