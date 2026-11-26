# Proposal: TMDB fallback for TVDB and IMDb IDs, and store foreign keys

> Imported from the retired superpowers workflow (#147). `design.md` is the approved design. The implementation plan is in `plan.md`, kept as written, and `tasks.md` only points to it, because the plans did not track progress in their checkboxes. `openspec/specs/` describes the current behaviour, which has moved on since this change.

## Why

Plex stops using TheTVDB as a metadata source on December 3, 2026. Many series folders hold only a TVDB ID, so the fix match search for `tvdb-<id>` may find nothing. TMDB can translate a TVDB or IMDb ID to a TMDB ID, which Plex can search for. The cache of those IDs must go with its item, and the store had no foreign keys.

## What Changes

- If no search of a fix match finds a candidate, an unmatched movie or series gets a second search with the TMDB IDs that TMDB gives for its `tvdb` and `imdb` path IDs.
- Cache each TMDB ID found for an item, and remove the cached IDs with the item.
- Enforce foreign keys in the store for all tables.

Non-goals: no TVDB lookup in movie libraries, no warnings about TVDB episode ordering, no Plex configuration changes, and no cache of TMDB answers that found nothing.

## Impact

`internal/tmdb` (new), `internal/store`, `internal/refresher`, `internal/config`, and the CLI startup check. Issues #68 and #69.
