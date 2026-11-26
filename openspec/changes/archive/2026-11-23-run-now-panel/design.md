# Run "now" panel design

Date: 2026-11-23
Status: approved by the maintainer on 2026-11-23
Issue: #126
Mockup: `mockups/now-panel.html`

## Intent

While a run is busy, the server card on the Status page almost always says
"Waiting for the next item". Each item takes milliseconds, then the run
waits `refresh-delay` (5 s by default), and `ItemDone` clears the current
title. The card never says which library an item is in, why it is due, or
what happened to the last item. The maintainer wants to see what is being
sent and in which library, in a layout that looks good.

## Goals

- While a run works through its items, the server card shows a "now" panel
  under the progress bar:
  - **Top row:** a small poster, title, year, and a second line with the
    library icon and title and the reasons (for example
    `TV Shows · missing summary`). On the right is a state label.
  - **In work:** the item that the run works on, with a "Working" label and a pulsing status dot.
  - **Between items:** the next item, with an "Up next" label and a
    countdown (`in 4s`) to the time it starts.
  - **Footer** (after a hairline): the last finished item, with an outcome
    icon, the title, year, library, and outcome, and a relative time on the
    right (`now`, `2s ago`).
- The panel and the footer never show the same item: the top shows what
  comes next, and the footer shows what just happened.
- A bulk refresh uses the same panel. The second line shows only what is
  known: the library, without reasons.

## Non-goals

- No history of more than one item in the panel. The run drawer has the
  full list.
- No new API route. The data rides on the existing status snapshot and
  event stream.
- No change to pacing, run order, or what the refresher sends.

## Constraints

- Astryx components and tokens only (`web/AGENTS.md`). Reuse `Poster`
  (`sm`) from `media.tsx`, `StatusDot`, `Text`, the layout stacks, and
  `useSecondClock` for the countdown and the relative time: a clock that
  ticks each second only while a countdown or an age shows, and that is
  never earlier than the mount of its component. Each of them takes its
  time as key, so a new status event starts it from the time of the event. `Badge` is for counts only.
- Each production Go function has a cognitive complexity of 15 or less.
- The poster goes through the existing proxy, `imageURL("thumb", srv, key,
  thumb)` in `internal/web/items.go`. The status never carries a raw Plex
  path or token.
- A status snapshot stays a deep copy (`Snapshot`).

## Approaches

1. **A1: two quiet lines.** The current item line, then the last item line.
   This is compact, but it is plain.
2. **A2: an inset panel with a poster.** The current or next item, with a
   poster and a result badge. This is clear and stable, but it does not show
   the item before.
3. **A3: a now line with a trail of recent items.** This shows the most,
   but it is busy.

The maintainer picked **A2 with the last item line of A1 as a footer**.
The next item shows between items, so the panel is never empty during the
wait (proposed by me, approved by the maintainer).

## Design

### Backend

- `refresher.Progress` gets a value type for an item on screen, for
  example `ProgressItem{RatingKey, Title, Year, Type, Library, Thumb
  string; Reasons store.Reasons}`. In a run, `run.library(section)` gives
  the library title. A bulk refresh fills what its admission knows.
- `ItemStarted(server string, item ProgressItem)` replaces the title-only
  form.
- New `ItemNext(server string, item ProgressItem, at time.Time)`. The run
  calls it before the pace wait with the next item and the time that the
  wait ends. A run without a wait (the first item, a dry run) does not call
  it.
- `ItemDone` keeps its signature. `web.Status` moves the current item to
  `last` with the outcome and the finish time, and clears `current`.
- `RunProgress` in JSON replaces `current` (a string) with:
  - `current`: an item, while one is in work
  - `next`: an item, and `nextAt`, while the run waits for it
  - `last`: an item with `outcome` and `finishedAt`

  Each item has `key`, `title`, `year`, `type`, `library`, `reasons`
  (names, as `reasonNames` gives them), and `thumb` (the proxy URL).
  `RunFinished` keeps `last` in `srv.Last` too, but the card shows the
  panel only during a run.
- `ItemStarted` clears `next`, and `ItemNext` sets it.

### Frontend

- `RunLive` in `status.tsx` keeps the progress bar and the counts line. It
  replaces the `Now:` text with a `NowPanel` component:
  - While it reads libraries, before the batch, the existing
    "Reading libraries" / "Read <library>" text stays. No panel shows.
  - The top row shows `current`, or else `next` with the countdown. If
    there is neither (between the last item and the end of the run), the
    top row is left out and only the footer shows.
  - The footer shows `last`, if there is one.
- Outcome display (icon colour, text):
  - `refresh`, `accepted`: green check, "refreshed"
  - `match`, `tmdb-match`, `title-match`: purple swap, "matched"
  - `*-failed`, `search-failed`, `failed`: red cross, "failed"
  - `skipped`, `stopped`, `changed`, `not-cached`: grey dash, "skipped"
  - `gone`: grey, "gone"
  - `dry-run`: grey, "dry run"

  The labels sit in one pure function with a Vitest test.
- The countdown shows `now` at or after `nextAt`, and never a negative
  number.

## My calls (check these)

- **The in-work label says "Working"**, not "Refreshing". The run chooses
  between a refresh and a fix match only after the item starts. To name the
  action, another progress call is needed. Most items finish in
  milliseconds, so that call is not worth it now.
- **The JSON `current` field changes from a string to an object.** Only the
  dashboard reads it, so this breaks no other client.

## Left to the builder

- The exact Astryx inset container: the muted background, the border, and
  the radius from the tokens.
- Whether the outcome icons come from Heroicons (already a dependency) or
  from the Astryx `Icon` set.
- The test cases for `Status`, the refresher progress calls, and the
  frontend label function.
