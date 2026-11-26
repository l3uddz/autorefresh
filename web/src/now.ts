import type { NowItem } from './api';
import { label } from './format';

// The texts of the now panel of a busy run: the outcome of the last item,
// the countdown to the next one, and the line under an item title.

export type OutcomeTone = 'green' | 'purple' | 'red' | 'gray';

const outcomes: Record<string, { text: string; tone: OutcomeTone }> = {
  refresh: { text: 'refreshed', tone: 'green' },
  accepted: { text: 'refreshed', tone: 'green' },
  match: { text: 'matched', tone: 'purple' },
  'tmdb-match': { text: 'matched', tone: 'purple' },
  'title-match': { text: 'matched', tone: 'purple' },
  'refresh-failed': { text: 'failed', tone: 'red' },
  'match-failed': { text: 'failed', tone: 'red' },
  'tmdb-match-failed': { text: 'failed', tone: 'red' },
  'title-match-failed': { text: 'failed', tone: 'red' },
  'search-failed': { text: 'failed', tone: 'red' },
  failed: { text: 'failed', tone: 'red' },
  skipped: { text: 'skipped', tone: 'gray' },
  stopped: { text: 'skipped', tone: 'gray' },
  changed: { text: 'skipped', tone: 'gray' },
  'not-cached': { text: 'skipped', tone: 'gray' },
  gone: { text: 'gone', tone: 'gray' },
  'dry-run': { text: 'dry run', tone: 'gray' },
};

/** Returns the text and the tone of the outcome of a finished item: the
 * outcome of a request in a run, or of an item of a bulk refresh. */
export function outcomeView(outcome: string | undefined): { text: string; tone: OutcomeTone } {
  if (!outcome) return { text: '', tone: 'gray' };
  return outcomes[outcome] ?? { text: label(outcome), tone: 'gray' };
}

/** Returns the time until at, such as "in 5s", or "now" once it passed. */
export function countdown(at: string | undefined, now: number): string {
  if (!at) return '';
  const ms = Date.parse(at);
  if (!Number.isFinite(ms)) return '';
  const seconds = Math.ceil((ms - now) / 1000);
  return seconds > 0 ? `in ${seconds}s` : 'now';
}

/** Returns the library of an item and, with withReasons, its reasons, such
 * as "TV Shows · no summary". */
export function itemDetail(item: NowItem, withReasons: boolean): string {
  const reasons = withReasons ? (item.reasons ?? []).map(reason => label(reason)) : [];
  return [item.library, ...reasons].filter(Boolean).join(' · ');
}

/** Returns how long ago at was, such as "2s ago" or "1m ago", or "now"
 * within a second. */
export function since(at: string | undefined, now: number): string {
  if (!at) return '';
  const ms = Date.parse(at);
  if (!Number.isFinite(ms)) return '';
  const seconds = Math.floor((now - ms) / 1000);
  if (seconds < 1) return 'now';
  return seconds < 60 ? `${seconds}s ago` : `${Math.floor(seconds / 60)}m ago`;
}
