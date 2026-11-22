import type { ItemDetail, TimelineEvent } from './api';

// Facts of an item that come from its timeline, newest event first. The
// item detail of the API has no completion fields: a fixed item has no
// cached row, and its detail is the snapshot of its newest event.

/** What last happened to an item: Plex completed it (the Fixed tab lists
 * it), or Plex removed it. */
export type Disposition = { kind: 'fixed' | 'removed'; event: TimelineEvent };

/** Returns the disposition of an item from its newest completed or gone
 * event, so a later completion replaces an older removal and the other
 * way round. Requests and other events change nothing. A removal counts
 * only while the item is not cached: a later read added it back. */
export function disposition(detail: ItemDetail): Disposition | undefined {
  const event = detail.timeline.find(entry => entry.kind === 'completed' || entry.kind === 'gone');
  if (event?.kind === 'completed') return { kind: 'fixed', event };
  if (event?.kind === 'gone' && !detail.item.cached) return { kind: 'removed', event };
  return undefined;
}

export type CheckResult = { kind: 'gone' } | { kind: 'complete' } | { kind: 'missing'; reasons: string[] };

/** Returns the result of "Check now" from the detail that it answered. A
 * check reads a cached item. When Plex no longer has it, autorefresh
 * removes its row and records a gone event, so the item is not cached and
 * its newest event is gone. When the check completes it, the newest event
 * is completed. */
export function checkResult(detail: ItemDetail): CheckResult {
  const reasons = detail.item.reasons ?? [];
  if (!detail.item.cached && detail.timeline[0]?.kind === 'gone') return { kind: 'gone' };
  return reasons.length ? { kind: 'missing', reasons } : { kind: 'complete' };
}
