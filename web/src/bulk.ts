import { ApiError, isNoItems, post } from './api';
import { plural } from './format';
import type { ItemKey, TabName } from './route';

// The list filters, and the requests that a bulk action sends for them.

export interface Filters {
  srv?: number;
  library?: string;
  reason?: string;
  type?: string;
  q?: string;
  /** Attention: include items whose reasons are all ignored. Fixed: include
   * items that Plex completed without a request. The Items page keeps one
   * value for each tab. */
  all?: boolean;
}

export type BulkAction = 'refresh' | 'stop' | 'resume';

/** The bulk actions of each tab. Fixed has none. */
export const tabActions: Record<TabName, BulkAction[]> = {
  attention: ['refresh', 'stop'],
  stopped: ['resume'],
  fixed: [],
};

/** A selection is either a set of rows or every item that matches the
 * filters, also the items of pages that are not loaded. */
export type Selection = { kind: 'keys'; items: ItemKey[] } | { kind: 'matching' };

export interface BulkRequest {
  srv: number;
  path: string;
  body: { keys: string[] } | { matching: true };
}

/** Returns the filter parameters of a list or a bulk request. The bulk API
 * rejects tab and sort, so they are added by listParams only. The Fixed tab
 * has no reason filter, and Stopped has no "all". */
export function filterParams(filters: Filters, tab: TabName): URLSearchParams {
  const p = new URLSearchParams();
  if (filters.srv !== undefined) p.set('srv', String(filters.srv));
  if (filters.library && filters.srv !== undefined) p.set('library', filters.library);
  if (filters.reason && tab !== 'fixed') p.set('reason', filters.reason);
  if (filters.type) p.set('type', filters.type);
  const q = filters.q?.trim();
  if (q) p.set('q', q);
  if (filters.all && tab !== 'stopped') p.set('all', '1');
  return p;
}

/** Returns the parameters of GET /api/items without a cursor. */
export function listParams(tab: TabName, filters: Filters, sort: string): URLSearchParams {
  const p = filterParams(filters, tab);
  p.set('tab', tab);
  if (sort) p.set('sort', sort);
  return p;
}

/** Returns the requests of action on selection: one for each server,
 * because the bulk API takes one server at a time. servers lists every
 * configured server for a matching selection without a server filter. */
export function bulkRequests(action: BulkAction, tab: TabName, filters: Filters, selection: Selection, servers: number[]): BulkRequest[] {
  const path = `items/${action}`;
  if (selection.kind === 'matching') {
    const targets = filters.srv !== undefined ? [filters.srv] : servers;
    return targets.map(srv => {
      const params = filterParams({ ...filters, srv }, tab);
      return { srv, path: `${path}?${params}`, body: { matching: true } };
    });
  }
  const keys = new Map<number, string[]>();
  for (const item of selection.items) keys.set(item.srv, [...keys.get(item.srv) ?? [], item.key]);
  return [...keys].map(([srv, list]) => ({ srv, path: `${path}?${new URLSearchParams({ srv: String(srv) })}`, body: { keys: list } }));
}

/** Returns the text of a failed bulk request for people. */
export function bulkError(error: Error): string {
  if (error instanceof ApiError && error.status === 409) return 'A run or another bulk job is busy. Try again when it ends.';
  if (error instanceof ApiError && error.message === 'too many items') return 'Select at most 5,000 items of one server at a time.';
  return error.message;
}

/** Sends a bulk stop or resume to each server and reports every server,
 * also when a later one fails. A server without matching items is no
 * failure. It throws when no server changed anything and one failed, so
 * the selection stays for another try. */
export async function stopOrResume(action: 'stop' | 'resume', requests: BulkRequest[], names: Record<number, string>): Promise<{ text: string; isError: boolean }> {
  if (!requests.length) throw new Error('The servers are not loaded yet. Try again in a moment.');
  let changed = 0;
  let skipped = 0;
  let empty = 0;
  const failures: string[] = [];
  for (const request of requests) {
    try {
      const result = await post<{ changed: number; skipped: unknown[] }>(request.path, request.body);
      changed += result.changed;
      skipped += result.skipped.length;
    } catch (error) {
      if (isNoItems(error)) empty++;
      else failures.push(requests.length > 1 ? `${names[request.srv] ?? `Server ${request.srv}`}: ${bulkError(error as Error)}` : bulkError(error as Error));
    }
  }
  if (failures.length && failures.length + empty === requests.length) throw new Error(failures.join('; '));
  if (empty === requests.length) return { text: 'No items match the filters.', isError: false };
  const verb = action === 'stop' ? 'Stopped' : 'Resumed';
  const done = `${verb} ${plural(changed, 'item')}${skipped ? ` · ${skipped.toLocaleString()} skipped` : ''}`;
  return { text: failures.length ? `${done} · failed on ${failures.join('; ')}` : done, isError: failures.length > 0 };
}
