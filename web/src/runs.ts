import { useToast } from '@astryxdesign/core/Toast';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiError, post, type RunDetail, type RunEvent, type RunItem, type RunRecord } from './api';
import { fixedBy } from './format';
import { cancelQueueOfJob } from './queue';
import type { TabName } from './route';
import { decisionView, eventView, type EventView, type Tone } from './timeline';

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
  /** What happened to the item, such as "Refresh accepted". */
  summary: string;
  detail?: string;
  tone: Tone;
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
const itemOutcomes: Record<string, { group: string; summary: string; tone: Tone }> = {
  accepted: { group: 'refreshed', summary: 'Refresh accepted', tone: 'success' },
  failed: { group: 'failed', summary: 'Refresh failed', tone: 'error' },
  gone: { group: 'removed', summary: 'Plex no longer had it', tone: 'warning' },
  changed: { group: 'skipped', summary: 'Changed after it was selected', tone: 'neutral' },
  stopped: { group: 'skipped', summary: 'Stopped before its turn', tone: 'neutral' },
  'not-cached': { group: 'skipped', summary: 'No longer in the cache', tone: 'neutral' },
  'not-reached': { group: 'not-reached', summary: 'The job ended before this item', tone: 'neutral' },
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
 * its item that followed it, or is not sent when neither followed. */
export function runGroups(detail: RunDetail): RunGroup[] {
  if (detail.run.kind === 'bulk-refresh') return grouped(itemGroups, detail.items.map(itemEntry));
  return grouped(eventGroups, eventEntries(detail.events));
}

function grouped(order: [string, string][], entries: [group: string, entry: RunEntry][]): RunGroup[] {
  return order
    .map(([id, title]) => ({ id, title, entries: entries.filter(([group]) => group === id).map(([, entry]) => entry) }))
    .filter(group => group.entries.length > 0);
}

function itemEntry(item: RunItem, index: number): [string, RunEntry] {
  const outcome = itemOutcomes[item.outcome] ?? { group: 'skipped', summary: item.outcome, tone: 'neutral' };
  return [outcome.group, { id: `item-${index}`, key: item.key, title: item.title, summary: outcome.summary, detail: item.error, tone: outcome.tone, tab: 'attention' }];
}

function eventEntries(events: RunEvent[]): [string, RunEntry][] {
  const entries: [string, RunEntry][] = [];
  // The match decision of each item that waits for its request.
  const decisions = new Map<string, RunEvent>();
  for (const event of events) {
    if (event.kind === 'match-decision') {
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
  // The run ended before it sent the request of these decisions.
  for (const decision of decisions.values()) entries.push(['not-sent', { ...eventEntry(decision), detail: 'The run ended before the request' }]);
  return entries;
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

/** Returns the entry of event. A request names the decision before it. */
function eventEntry(event: RunEvent, decision?: RunEvent): RunEntry {
  const view = entryView(event);
  const detail = [decision && decisionView(decision).title, view.detail].filter(Boolean).join(' · ') || undefined;
  return {
    id: `event-${event.id}`,
    key: event.key,
    title: event.title,
    year: event.year,
    type: event.type,
    thumb: event.thumb,
    summary: view.title,
    detail,
    tone: view.tone,
    at: event.at,
    tab: event.kind === 'completed' ? 'fixed' : 'attention',
  };
}
