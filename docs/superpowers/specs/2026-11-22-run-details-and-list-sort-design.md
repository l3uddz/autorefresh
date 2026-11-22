# Run details and list sort design

Date: 2026-11-22
Status: approved by the maintainer on 2026-11-22
Issues: #104 (run details), #105 (list sort)

## Intent

The maintainer uses the dashboard to check what autorefresh does and to
find the items that need a person. Two gaps get in the way:

1. The Runs page shows the counts of each run (read, refreshed, matched,
   failed, fixed, due), but not which items those counts cover.
2. On the Items page, only Table mode can sort. List mode, the default, has
   no sort control. That makes it hard to find, for example, the items with
   the most tries.

The new parts must look like the rest of the dashboard and be pleasant to
use: Astryx components and tokens only, as `web/AGENTS.md` requires.

## Goals

- A click on a run opens a drawer with the details of that run: the same
  kind of drawer as an item in the Items table.
- The drawer lists the items of the run, grouped by outcome, and each item
  links to its item detail.
- List mode can sort by the same keys as Table mode, per tab.

## Non-goals

- No per-item list for the read and due counts. They stay numbers. Storing
  thousands of rows for each run is not worth it.
- No new item filters. The Reason filter already has Unmatched.
- No backfill for runs from before the upgrade.
- No change to the favicon or to the Open in Plex link.

## Approaches

**Run items.** Bulk jobs already store one `run_item` row for each selected
item. Scheduled and manual runs store only counts, and events have no run.

- **Run ID on each event (chosen).** A nullable `event.run_id` column. The
  refresher sets it on each event that it writes during a run. The link is
  exact. Older runs show no items.
- Time window: the events of the server between the start and the end of
  the run. It also covers older runs, but it can mix in the events of
  another process, such as `--once` beside the service, or manual actions.

The maintainer chose the run ID.

**List sort.** The API already sorts every tab, and Table mode sends its
sort through `listParams`. List mode only needs a control that sets the
same per-tab sort state.

## Design

### Run details (#104)

Storage:

- Migration `11_event_run.sql`: `ALTER TABLE event ADD COLUMN run_id INTEGER
  REFERENCES run (id) ON DELETE SET NULL`, and an index on `event (run_id)`.
  Retention and the pruning of servers remove runs. Their events keep their
  item history and lose only the link.
- The writes that a run makes carry the run ID: request events, match
  decision events, completed events, and gone events of a read. Manual item
  actions outside a run keep a null run ID. A dry run writes no events.
- A store method returns the events of one run in time order.

API:

- `GET /api/runs/events?id=<run>` returns the events of the run, in the
  shape that the item timeline uses, with the item key, server index, title,
  year, type, and thumb, so a row can show a poster and link to the item.
  Error texts are redacted like the other routes. Bulk jobs keep
  `GET /api/runs/items`.
- A run of a server that is no longer configured is not found, as in
  `GET /api/runs`.

Frontend:

- Route `#/runs/<id>`, with the `?srv=` scope as on every page. A click,
  Enter, or Space on a run row (table or list) opens it. Closing the drawer
  returns to `#/runs`.
- The drawer uses Astryx `Drawer`, as the Items table does. Its header shows
  the start time, the duration, the server (with several servers), the
  source, the kind, the status, and the error. Below are the counts.
- The items of a normal run are grouped by outcome: refreshed, matched,
  failed, fixed (completed after a request), completed by Plex alone, and
  gone. A bulk job groups its `run_item` rows by their outcome. Each row
  shows a small poster, the title, the year, and the error text, and links
  to the item detail.
- A run with counts but no recorded events says that its items were not
  recorded, because the run is from before this version or its events were
  removed. A run with no items says that nothing happened to an item.
- A running run refreshes its events with the live status, like the runs
  list.

### List sort (#105)

- The List mode header gets a sort control: a `Selector` with the sortable
  keys of the current tab, and a direction toggle. The keys and labels come
  from the same place as the sortable table columns, so the two cannot
  drift.
  - Attention: item, tries, last action, next due
  - Stopped: item, tries, stopped
  - Fixed: item, completed
- An empty sort is the tab default, as today. Both modes use the per-tab
  sort state, so switching modes keeps the order.

## Left to the builder

- The layout inside the drawer, within the Astryx rules and the look of the
  item detail.
- Whether match decision events show as their own rows or with the request
  of the same item.
- The labels of the sort control and of the outcome groups.
- How the drawer behaves on a phone, following the Items page.
