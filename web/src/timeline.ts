import type { ItemDetail, TimelineEvent } from './api';

// Facts of an item that come from its timeline, newest event first. The
// item detail of the API has no completion fields: a fixed item has no
// cached row, and its detail is the snapshot of its newest event.

/** Returns the newest completed event: the one that the Fixed tab lists
 * for the item. */
export function fixedEvent(timeline: TimelineEvent[]): TimelineEvent | undefined {
  return timeline.find(event => event.kind === 'completed');
}

/** Returns the newest gone event of an item that is no longer cached, so
 * the detail can say that Plex removed it. */
export function goneEvent(detail: ItemDetail): TimelineEvent | undefined {
  return detail.item.cached ? undefined : detail.timeline.find(event => event.kind === 'gone');
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
