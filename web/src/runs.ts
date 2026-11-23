import { useToast } from '@astryxdesign/core/Toast';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiError, post, type RunDetail, type RunEvent, type RunItem, type RunRecord } from './api';
import { fixedBy, plural } from './format';
import { cancelQueueOfJob } from './queue';
import type { TabName } from './route';
import { decisionView, eventView, type EventView } from './timeline';

/** A manual run of the server srv, or of every server for null. With full,
 * the run reads every library fully before it refreshes the due items. */
export interface RunRequest {
  srv: number | null;
  full: boolean;
}

/** Starts a manual run of the server srv, or of every server for null. A
 * busy run answers 409, which the toast explains. */
export function useRunNow() {
  const showToast = useToast();
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ srv, full }: RunRequest) => post<{ ids: number[] }>('runs', { srv, full }),
    onSuccess: (_, { full }) => {
      showToast({ body: full ? 'Full cache refresh started' : 'Run started', uniqueID: 'run-now' });
      void client.invalidateQueries({ queryKey: ['status'] });
    },
    onError: error => showToast({ body: runError(error), type: 'error', uniqueID: 'run-now' }),
  });
}

/** Cancels the manual run or bulk job id. It stops at the next item. A
 * job of the bulk refresh queue also cancels the rest of the queue, before
 * the request, so the rest stays canceled when the job ended first. */
export function useCancelRun() {
  const showToast = useToast();
  return useMutation({
    mutationFn: async (id: number) => {
      const isQueued = cancelQueueOfJob(id);
      try {
        await post(`runs/cancel?${new URLSearchParams({ id: String(id) })}`);
      } catch (error) {
        if (isQueued && error instanceof ApiError && error.status === 404) return { isQueued, hasEnded: true };
        throw error;
      }
      return { isQueued, hasEnded: false };
    },
    onSuccess: ({ isQueued, hasEnded }) => showToast({ body: cancelText(isQueued, hasEnded), uniqueID: 'cancel-run' }),
    onError: error => showToast({ body: runError(error), type: 'error', uniqueID: 'cancel-run' }),
  });
}

function cancelText(isQueued: boolean, hasEnded: boolean): string {
  if (hasEnded) return 'That job had already ended. The other servers of the bulk refresh do not start.';
  return isQueued ? 'Canceling at the next item. The other servers of the bulk refresh do not start.' : 'Canceling at the next item';
}

function runError(error: Error): string {
  if (error instanceof ApiError && error.status === 409) return 'A run is busy. Try again when it ends.';
  if (error instanceof ApiError && error.status === 404) return 'That run has already ended.';
  return error.message;
}

/** One item of a run group, as the run details show it. */
export interface RunEntry {
  id: string;
  key: string;
  title?: string;
  year?: number;
  type?: string;
  thumb?: string;
  /** The title of the library of the item. */
  library?: string;
  /** What happened to the item, such as "Refresh accepted". */
  summary: string;
  detail?: string;
  at?: string;
  /** The Items tab that the item link opens. */
  tab: TabName;
}

export interface RunGroup {
  id: string;
  title: string;
  entries: RunEntry[];
}

const eventGroups: [id: string, title: string][] = [
  ['in-progress', 'In progress'],
  ['refreshed', 'Refreshed'],
  ['matched', 'Matched'],
  ['failed', 'Failed'],
  ['fixed', 'Fixed'],
  ['completed', 'Completed by Plex'],
  ['removed', 'Removed'],
  ['not-sent', 'Not sent'],
];

const itemGroups: [id: string, title: string][] = [
  ['refreshed', 'Refreshed'],
  ['failed', 'Failed'],
  ['removed', 'Removed'],
  ['skipped', 'Skipped'],
  ['not-reached', 'Not reached'],
];

/** The group and text of each bulk item outcome. */
const itemOutcomes: Record<string, { group: string; summary: string; tab?: TabName }> = {
  accepted: { group: 'refreshed', summary: 'Refresh accepted' },
  failed: { group: 'failed', summary: 'Refresh failed' },
  gone: { group: 'removed', summary: 'Plex no longer had it' },
  changed: { group: 'skipped', summary: 'Changed after it was selected' },
  stopped: { group: 'skipped', summary: 'Stopped before its turn', tab: 'stopped' },
  'not-cached': { group: 'skipped', summary: 'No longer in the cache' },
  'not-reached': { group: 'not-reached', summary: 'The job ended before this item' },
};

/** Returns the counts of a run, by name. The refreshes and matches are the
 * requests sent, so they include the ones that failed. */
export function runCounts(run: RunRecord): [name: string, value: number][] {
  if (run.kind === 'bulk-refresh') return [['Selected', run.selected], ['Refreshes sent', run.refreshed], ['Failed', run.failed], ['Skipped', run.skipped]];
  return [['Read', run.readItems], ['Refreshes sent', run.refreshed], ['Matches sent', run.matched], ['Failed', run.failed], ['Fixed', run.fixed], ['Due', run.due]];
}

/** Returns the items of a run grouped by outcome, in a fixed group order,
 * without empty groups. A bulk refresh groups its selected items. A run
 * groups its events: a match decision joins the request or the removal of
 * its item that followed it. Without either, it is in progress while the
 * run still works, or else not sent. */
export function runGroups(detail: RunDetail): RunGroup[] {
  if (detail.run.kind === 'bulk-refresh') return grouped(itemGroups, detail.items.map(itemEntry));
  return grouped(eventGroups, eventEntries(detail.events, detail.run.status === 'running'));
}

function grouped(order: [string, string][], entries: [group: string, entry: RunEntry][]): RunGroup[] {
  return order
    .map(([id, title]) => ({ id, title, entries: entries.filter(([group]) => group === id).map(([, entry]) => entry) }))
    .filter(group => group.entries.length > 0);
}

function itemEntry(item: RunItem, index: number): [string, RunEntry] {
  const outcome = itemOutcomes[item.outcome] ?? { group: 'skipped', summary: item.outcome };
  return [
    outcome.group,
    {
      id: `item-${index}`,
      key: item.key,
      title: item.title,
      year: item.year,
      type: item.type,
      thumb: item.thumb,
      library: item.library,
      summary: outcome.summary,
      detail: item.error,
      tab: outcome.tab ?? 'attention',
    },
  ];
}

function eventEntries(events: RunEvent[], isRunning: boolean): [string, RunEntry][] {
  const entries: [string, RunEntry][] = [];
  // The match decision of each item that waits for its request.
  const decisions = new Map<string, RunEvent>();
  for (const event of events) {
    if (event.kind === 'match-decision') {
      const earlier = decisions.get(event.key);
      if (earlier) entries.push(pendingEntry(earlier, false));
      decisions.set(event.key, event);
      continue;
    }
    const group = eventGroup(event);
    if (!group) continue;
    // A request or a removal ends the decision of its item: Plex answers a
    // match request of an item that it no longer has with a removal.
    const decision = event.kind === 'request' || event.kind === 'gone' ? decisions.get(event.key) : undefined;
    if (decision) decisions.delete(event.key);
    entries.push([group, eventEntry(event, decision)]);
  }
  for (const decision of decisions.values()) entries.push(pendingEntry(decision, isRunning));
  return entries;
}

/** Returns the entry of a decision that no request or removal followed. A
 * failed search sends no request. A running run can still send the request
 * of the last decision of an item. Otherwise the run ended before it sent
 * the request. */
function pendingEntry(decision: RunEvent, isWaiting: boolean): [string, RunEntry] {
  if (decision.outcome === 'failed') return ['failed', eventEntry(decision)];
  if (isWaiting) return ['in-progress', { ...eventEntry(decision), detail: 'Its request is not sent yet' }];
  return ['not-sent', { ...eventEntry(decision), detail: 'The run ended before the request' }];
}

/** How often the details of a running run reload. */
const runningInterval = 5_000;

/** Returns how often the details of run reload, or false for no reload. A
 * running run reloads, also after a failed reload that kept its data. A
 * removed run, an ended run, and a bulk refresh, which records its items
 * when it ends, do not. */
export function runPollInterval(run: RunRecord | undefined, error: Error | null): number | false {
  if (error instanceof ApiError && error.status === 404) return false;
  return run?.status === 'running' && run.kind !== 'bulk-refresh' ? runningInterval : false;
}

function eventGroup(event: RunEvent): string | undefined {
  switch (event.kind) {
    case 'request':
      if (!event.accepted) return 'failed';
      return event.action === 'refresh' ? 'refreshed' : 'matched';
    case 'completed':
      return event.fixedAction || event.fixedMatch ? 'fixed' : 'completed';
    case 'gone':
      return 'removed';
    default:
      return undefined;
  }
}

/** Returns the title and detail of event under its group, which already
 * says that an item was fixed, completed, or removed. */
function entryView(event: RunEvent): EventView {
  const view = event.kind === 'match-decision' ? decisionView(event) : eventView(event);
  switch (event.kind) {
    case 'completed':
      return event.fixedAction || event.fixedMatch ? { ...view, title: `By ${fixedBy(event.fixedAction, event.fixedMatch)}`, detail: undefined } : { ...view, title: 'Completed without a request' };
    case 'gone':
      return { ...view, title: 'Plex no longer had it' };
    default:
      return view;
  }
}

/** Returns the entry of event. A request or a removal names the decision
 * before it, and the error of a failed decision. */
function eventEntry(event: RunEvent, decision?: RunEvent): RunEntry {
  const view = entryView(event);
  const before = decision && [decisionView(decision).title, decision.outcome === 'failed' ? decision.error : undefined];
  const detail = [...(before ?? []), view.detail].filter(Boolean).join(' · ') || undefined;
  return {
    id: `event-${event.id}`,
    key: event.key,
    title: event.title,
    year: event.year,
    type: event.type,
    thumb: event.thumb,
    library: event.library,
    summary: view.title,
    detail,
    at: event.at,
    tab: event.kind === 'completed' ? 'fixed' : 'attention',
  };
}

/** Returns why a run lists no item: it is still at work, it is from before
 * runs linked their events, its history expired, or it changed none. A run
 * from before runs linked their events can have changed items that its
 * counts leave out, such as removed items, so its counts do not decide. */
export function noEntries(run: RunRecord): { title: string; description: string } {
  if (run.status === 'running' && run.kind === 'bulk-refresh') {
    return { title: 'The items show when the bulk refresh ends', description: 'A bulk refresh records the outcome of its items when it ends.' };
  }
  if (run.status === 'running') return { title: 'No item changed yet', description: 'The items show here as the run works on them.' };
  const notRecorded = 'Item details were not recorded for this run';
  if (run.kind !== 'bulk-refresh' && !run.eventsLinked) return { title: notRecorded, description: 'The run is from before autorefresh recorded them.' };
  const changed = run.kind === 'bulk-refresh' ? run.selected : run.refreshed + run.matched + run.failed + run.completed;
  if (changed > 0) return { title: notRecorded, description: 'Their history expired.' };
  const description = run.status === 'finished' ? `It read ${plural(run.readItems, 'item')} and refreshed none.` : 'The run ended before it changed an item.';
  return { title: 'No item changed in this run', description };
}
