import type { ItemRecord } from './api';
import type { TabName } from './route';
import { fixedBy, label, lastAction, queueState, relative, typeName } from './format';

// Table mode windows its rows (Astryx useTableRowsVirtualization). Astryx
// windows only columns without a renderCell, which print the string or
// number at row[column.key], so each row is plain text, built here. The
// column key is also the sort key of the API.

export interface TableRow extends Record<string, unknown> {
  id: string;
  srv: number;
  key: string;
  title: string;
  kind: string;
  library: string;
  reasons: string;
  tries: number;
  'last-attempt': string;
  due: string;
  stopped: string;
  'fixed-by': string;
  completed: string;
}

/** A sort that the API knows. desc is the direction that a new choice in
 * the sort control starts with: the most tries or the newest first. */
export interface SortKey {
  key: string;
  label: string;
  desc: boolean;
}

/** The sorts that the API knows for each tab, in the order the sort
 * control lists them. Each key is a column of TableRow, and table mode
 * makes exactly these columns sortable. */
export const sortKeys: Record<TabName, readonly SortKey[]> = {
  attention: [
    { key: 'title', label: 'Item', desc: false },
    { key: 'tries', label: 'Tries', desc: true },
    { key: 'last-attempt', label: 'Last action', desc: true },
    { key: 'due', label: 'Next due', desc: false },
  ],
  stopped: [
    { key: 'title', label: 'Item', desc: false },
    { key: 'tries', label: 'Tries', desc: true },
    { key: 'stopped', label: 'Stopped', desc: true },
  ],
  fixed: [
    { key: 'title', label: 'Item', desc: false },
    { key: 'completed', label: 'Completed', desc: true },
  ],
};

/** Reads the sort parameter of the API: "" for the tab default, "key", or
 * "-key" for descending. */
export function parseSort(sort: string): { key: string; desc: boolean } {
  return sort.startsWith('-') ? { key: sort.slice(1), desc: true } : { key: sort, desc: false };
}

/** Writes the sort parameter of the API. An empty key is the tab default. */
export function formatSort(key: string, desc: boolean): string {
  if (!key) return '';
  return desc ? `-${key}` : key;
}

/** Returns the text row of item. Ignored reasons are named as ignored. */
export function tableRow(item: ItemRecord, now: number, isMulti: boolean, ignored: readonly string[]): TableRow {
  const reasons = (item.reasons ?? []).map(reason => (ignored.includes(reason) ? `${label(reason)} (ignored)` : label(reason)));
  return {
    id: `${item.srv}/${item.key}`,
    srv: item.srv,
    key: item.key,
    title: item.title ?? `Item ${item.key}`,
    kind: [typeName(item.type), item.year].filter(Boolean).join(' · '),
    library: [isMulti && item.server, item.library].filter(Boolean).join(' · ') || '—',
    reasons: reasons.join(', ') || '—',
    tries: item.attempts,
    'last-attempt': item.lastAttempt ? lastAction(item.lastOutcome, item.lastMatchOutcome) : '—',
    due: queueState(item.state, item.nextDue, now),
    stopped: relative(item.stoppedAt, now),
    'fixed-by': fixedBy(item.fixedAction, item.fixedMatch),
    completed: relative(item.completedAt, now),
  };
}

/** Returns previous when next holds the same rows with the same text, and
 * next otherwise. A clock tick that changes no cell then keeps the table
 * data, whose new identity would move the windowed table. */
export function stableRows(previous: TableRow[], next: TableRow[]): TableRow[] {
  if (previous === next || previous.length !== next.length) return next;
  return next.every((row, i) => sameRow(previous[i], row)) ? previous : next;
}

function sameRow(a: TableRow, b: TableRow): boolean {
  const keys = Object.keys(b);
  return keys.length === Object.keys(a).length && keys.every(key => a[key] === b[key]);
}
