import type { QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import { ApiError, api, invalidateLists, isNoItems, post, statusQuery, type RunItem, type RunRecord, type Status } from './api';
import type { BulkRequest } from './bulk';
import { plural } from './format';

// A bulk refresh job works on one server, and the run lock is global, so a
// selection that spans servers becomes a queue: one job for each server,
// each started after the previous one ends. The queue lives in this module,
// not in a component, so it keeps going when the bulk band closes or the
// page changes. A page reload drops the servers that have not started.

export type StepState = 'waiting' | 'running' | 'finished' | 'failed' | 'canceled' | 'interrupted' | 'unknown' | 'empty' | 'not-started';

export interface QueueStep {
  srv: number;
  server: string;
  state: StepState;
  /** The job (run) ID once the server admitted it. */
  job?: number;
  /** The number of items that the job took. */
  count?: number;
  /** The item results of an ended job. Undefined while it runs, and when
   * the results are unavailable. */
  items?: RunItem[];
  /** Why a step did not start. */
  note?: string;
}

export interface BulkQueue {
  steps: QueueStep[];
  isCanceled: boolean;
  isDone: boolean;
  /** Why the cancel of the running job failed. The job may still run;
   * its server card can cancel it. */
  cancelError?: string;
}

/** The timing of the queue. retries and retryDelay apply to a job that the
 * previous job's lock still blocks: the status reports the end of a job
 * just before it releases the lock. pollInterval is the time between
 * checks of a job that the event stream did not report as ended, and
 * unknownAfter is the number of checks after which a job that neither the
 * status nor the stored runs report as ended counts as unknown. */
export const queueTiming = { retries: 10, retryDelay: 2000, pollInterval: 15_000, unknownAfter: 4 };

/** The error of a bulk refresh whose filters match no item on any server. */
export class NothingMatched extends Error {
  constructor() {
    super('No items match the filters.');
  }
}

let current: BulkQueue | undefined;
let queueGeneration = 0;
// Whether a bulk refresh waits for the admission of its first job. The
// queue exists only after that, so this keeps a second start out.
let isAdmitting = false;
const listeners = new Set<() => void>();
const doneListeners = new Set<(queue: BulkQueue) => void>();

function notify() {
  for (const listener of listeners) listener();
}

function publish(next: BulkQueue) {
  current = next;
  notify();
}

function setAdmitting(value: boolean) {
  isAdmitting = value;
  notify();
}

function setStep(index: number, change: Partial<QueueStep>) {
  if (!current) return;
  publish({ ...current, steps: current.steps.map((step, i) => (i === index ? { ...step, ...change } : step)) });
}

/** Marks every step from index on as not started, with note. */
function skipFrom(index: number, note: string) {
  if (!current) return;
  publish({ ...current, steps: current.steps.map((step, i) => (i >= index ? { ...step, state: 'not-started', note } : step)) });
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Returns the latest bulk refresh queue, also one that has ended. */
export function useBulkQueue(): BulkQueue | undefined {
  return useSyncExternalStore(subscribe, () => current);
}

/** Calls listener with each queue that ends. It returns the unsubscribe. */
export function onQueueDone(listener: (queue: BulkQueue) => void): () => void {
  doneListeners.add(listener);
  return () => doneListeners.delete(listener);
}

export const isQueueActive = (queue = current) => Boolean(queue && !queue.isDone);

const isRefreshBusy = () => isAdmitting || isQueueActive();

/** Reports whether a bulk refresh is starting or running, so another one
 * cannot start. A component that mounts during the start sees it too. */
export function useIsRefreshBusy(): boolean {
  return useSyncExternalStore(subscribe, isRefreshBusy);
}

type Job = { id: number; count: number };

/** Starts the bulk refresh of requests, one server after another. It
 * returns once the first server with matching items admitted its job. A
 * server without matching items is skipped. It throws NothingMatched when
 * no server has matching items, and the error of the first server that
 * refused its job otherwise, so nothing runs. names gives the name of each
 * server. */
export async function startBulkRefresh(client: QueryClient, requests: BulkRequest[], names: Record<number, string>): Promise<BulkQueue> {
  if (isRefreshBusy()) throw new Error('A bulk refresh is already running. Wait until it ends.');
  if (!requests.length) throw new Error('The servers are not loaded yet. Try again in a moment.');
  setAdmitting(true);
  let start: { index: number; job: Job };
  try {
    start = await admitFirst(requests);
  } finally {
    setAdmitting(false);
  }
  queueGeneration++;
  publish({
    isCanceled: false,
    isDone: false,
    steps: requests.map((request, i): QueueStep => {
      const step = { srv: request.srv, server: names[request.srv] ?? `Server ${request.srv}` };
      if (i < start.index) return { ...step, state: 'empty' };
      if (i > start.index) return { ...step, state: 'waiting' };
      return { ...step, state: 'running', job: start.job.id, count: start.job.count };
    }),
  });
  const queue = current!;
  void drive(client, requests, start.index);
  return queue;
}

/** Starts the job of the first request with matching items. */
async function admitFirst(requests: BulkRequest[]): Promise<{ index: number; job: Job }> {
  for (let i = 0; i < requests.length; i++) {
    try {
      return { index: i, job: await post<Job>(requests[i].path, requests[i].body) };
    } catch (error) {
      if (!isNoItems(error)) throw error;
    }
  }
  throw new NothingMatched();
}

/** Cancels the queue: the running job stops at its next item, and the
 * waiting servers do not start. */
export async function cancelBulkQueue() {
  if (!current || current.isDone) return;
  publish({ ...current, isCanceled: true });
  const running = current.steps.find(step => step.state === 'running');
  if (running?.job !== undefined) await cancelJob(running.job);
}

/** Cancels the rest of the active queue when job id belongs to it, as when
 * the user cancels that job on its server card. It reports whether it did.
 * The caller sends the cancel of the job itself. The queue stops also when
 * the job ended before the cancel reached it. */
export function cancelQueueOfJob(id: number): boolean {
  if (!current || current.isDone || !current.steps.some(step => step.job === id)) return false;
  if (!current.isCanceled) publish({ ...current, isCanceled: true });
  return true;
}

/** Cancels job id. A failure stays on the queue, which stays canceled, so
 * the waiting servers still do not start. */
async function cancelJob(id: number) {
  const generation = queueGeneration;
  try {
    await post(`runs/cancel?${new URLSearchParams({ id: String(id) })}`);
  } catch (error) {
    // A job that already ended answers 404, which changes nothing here.
    if (error instanceof ApiError && error.status === 404) return;
    if (current && queueGeneration === generation) publish({ ...current, cancelError: (error as Error).message });
  }
}

const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

type Admission = Job | { busy: true } | { empty: true } | { error: string };

/** Starts the job of request. It retries while another job holds the run
 * lock, and gives up when the queue is canceled. */
async function admit(request: BulkRequest): Promise<Admission | undefined> {
  for (let attempt = 0; ; attempt++) {
    if (current?.isCanceled) return undefined;
    try {
      return await post<Job>(request.path, request.body);
    } catch (error) {
      if (isNoItems(error)) return { empty: true };
      if (!(error instanceof ApiError) || error.status !== 409) return { error: (error as Error).message };
      if (attempt >= queueTiming.retries) return { busy: true };
    }
    await sleep(queueTiming.retryDelay);
  }
}

/** How a job ended: its run status, or unknown, and whether its item
 * results are stored. */
interface JobEnd {
  status: string;
  hasItems: boolean;
}

/** Returns the end of job id on the server srv as status reports it,
 * 'running' while it runs, and undefined when status does not know the
 * job: before it starts, after a restart, or after a later run replaced
 * it as the last run. */
function reportedEnd(status: Status | undefined, srv: number, id: number): JobEnd | 'running' | undefined {
  const server = status?.servers.find(entry => entry.srv === srv);
  if (server?.last?.id === id) return { status: server.last.status, hasItems: server.last.recorded };
  return server?.run?.id === id ? 'running' : undefined;
}

/** Waits for the next status of the cache, or ms milliseconds. */
function nextStatus(client: QueryClient, ms: number): Promise<'status' | 'timer'> {
  return new Promise(resolve => {
    const done = (why: 'status' | 'timer') => {
      unsubscribe();
      clearTimeout(timer);
      resolve(why);
    };
    const unsubscribe = client.getQueryCache().subscribe(event => {
      if (event.type === 'updated' && event.query.queryKey[0] === 'status') done('status');
    });
    const timer = setTimeout(() => done('timer'), ms);
  });
}

/** Reads the status, or undefined when it is unavailable. The cached
 * status is the answer when it is newer. */
function fetchStatus(client: QueryClient): Promise<Status | undefined> {
  return client.fetchQuery({ ...statusQuery(client), staleTime: 0 }).catch(() => undefined);
}

/** Returns the stored run id of the server srv, undefined when the store
 * has no such run (it was never recorded or was removed), or 'error' when
 * the runs are unavailable. Runs come newest first, so the search stops at
 * the first older run. */
async function storedRun(srv: number, id: number): Promise<RunRecord | undefined | 'error'> {
  let cursor = '';
  for (;;) {
    const params = new URLSearchParams({ srv: String(srv), limit: '100' });
    if (cursor) params.set('cursor', cursor);
    let page: { runs: RunRecord[]; next?: string };
    try {
      page = await api<{ runs: RunRecord[]; next?: string }>(`runs?${params}`);
    } catch {
      return 'error';
    }
    for (const run of page.runs) {
      if (run.id === id) return run;
      if (run.id < id) return undefined;
    }
    if (!page.next) return undefined;
    cursor = page.next;
  }
}

/** Waits until job id on the server srv ends. The event stream normally
 * reports the end. Each poll covers a stream that is down. When the status
 * no longer knows the job, the stored runs tell how it ended. A poll that
 * does not read the job as running counts as a miss: the status does not
 * know the job, or is unavailable. After queueTiming.unknownAfter misses
 * in a row the job ends as unknown, also when the stored runs are unavailable, so
 * the queue never waits forever. While the status reads the job as
 * running, the wait goes on. */
async function waitForJob(client: QueryClient, srv: number, id: number): Promise<JobEnd> {
  let misses = 0;
  let wake: 'status' | 'timer' = 'timer';
  // Status updates of other runs wake the wait often. The poll keeps its
  // own deadline, so they never put it off.
  let deadline = 0;
  for (;;) {
    const cached = client.getQueryData<Status>(['status']);
    const seen = reportedEnd(cached, srv, id);
    if (seen && seen !== 'running') return seen;
    if (wake === 'timer') {
      const fresh = reportedEnd(await fetchStatus(client), srv, id);
      if (fresh && fresh !== 'running') return fresh;
      // Only consecutive misses count: a job that runs for long can see a
      // few failed reads.
      if (fresh === 'running') misses = 0;
      if (!fresh) {
        const run = await storedRun(srv, id);
        if (run !== 'error' && run?.status && run.status !== 'running') {
          return { status: run.status, hasItems: run.status !== 'interrupted' };
        }
        const updated = client.getQueryData<Status>(['status']);
        const latest = reportedEnd(updated, srv, id);
        if (latest && latest !== 'running') return latest;
        // Only a status received during this poll can confirm the job still runs.
        const changed = updated?.boot !== cached?.boot || updated?.seq !== cached?.seq;
        if (latest === 'running' && changed) misses = 0;
        else if (++misses >= queueTiming.unknownAfter) return { status: 'unknown', hasItems: false };
      }
      // The cache may have changed during the requests: check it again
      // before waiting. The check and the wait start in the same tick.
      deadline = Date.now() + queueTiming.pollInterval;
      wake = 'status';
      continue;
    }
    wake = await nextStatus(client, Math.max(0, deadline - Date.now()));
  }
}

const endStates: Record<string, StepState> = { finished: 'finished', failed: 'failed', canceled: 'canceled', interrupted: 'interrupted' };

/** Why the servers after a step that ended in state do not start, or
 * undefined when the queue goes on. */
const stopNotes: Partial<Record<StepState, string>> = {
  canceled: 'canceled',
  interrupted: 'the previous server was interrupted',
  unknown: 'the result of the previous server is unknown',
};

async function drive(client: QueryClient, requests: BulkRequest[], first: number) {
  for (let i = first; i < requests.length; i++) {
    const start = i === first ? 'running' : await startStep(i, requests[i]);
    if (start === 'skip') continue;
    if (start === 'stop') break;
    const step = current!.steps[i];
    const end = await waitForJob(client, step.srv, step.job!);
    const items = end.hasItems
      ? await api<{ items: RunItem[] }>(`runs/items?${new URLSearchParams({ id: String(step.job) })}`).then(
          body => body.items,
          () => undefined,
        )
      : undefined;
    const state = endStates[end.status] ?? 'unknown';
    setStep(i, { state, items });
    invalidateLists(client);
    const note = current?.isCanceled ? 'canceled' : stopNotes[state];
    if (note) {
      skipFrom(i + 1, note);
      break;
    }
  }
  if (!current) return;
  publish({ ...current, isDone: true });
  for (const listener of doneListeners) listener(current);
}

/** Starts step index. A refused step or one without matching items is
 * skipped: the next server may still start. A cancel or a busy run lock
 * stops the queue. */
async function startStep(index: number, request: BulkRequest): Promise<'running' | 'skip' | 'stop'> {
  const admitted = await admit(request);
  if (!admitted || 'busy' in admitted) {
    skipFrom(index, admitted ? 'another run is busy' : 'canceled');
    return 'stop';
  }
  if ('empty' in admitted) {
    setStep(index, { state: 'empty' });
    return 'skip';
  }
  if ('error' in admitted) {
    setStep(index, { state: 'not-started', note: admitted.error });
    return 'skip';
  }
  setStep(index, { state: 'running', job: admitted.id, count: admitted.count });
  // A cancel that arrived while the job was admitted reaches it here.
  if (current?.isCanceled) await cancelJob(admitted.id);
  return 'running';
}

function outcomeText(items: RunItem[]): string {
  const count = (outcome: string) => items.filter(item => item.outcome === outcome).length;
  const accepted = count('accepted');
  const failed = count('failed');
  const other = items.length - accepted - failed;
  return [`${accepted.toLocaleString()} accepted`, failed ? `${failed.toLocaleString()} failed` : '', other ? `${other.toLocaleString()} skipped` : '']
    .filter(Boolean)
    .join(' · ');
}

const stateVerbs: Record<StepState, string> = {
  waiting: 'waiting',
  running: 'running',
  finished: 'finished',
  failed: 'failed',
  canceled: 'canceled',
  interrupted: 'interrupted',
  unknown: 'result unknown',
  empty: 'no matching items',
  'not-started': 'not started',
};

function stepText(step: QueueStep): string {
  const verb = stateVerbs[step.state];
  switch (step.state) {
    case 'not-started':
      return step.note ? `${verb}: ${step.note}` : verb;
    case 'waiting':
    case 'running':
    case 'unknown':
    case 'empty':
      return verb;
    default:
      return step.items ? `${verb}: ${outcomeText(step.items)}` : `${verb}, results unavailable`;
  }
}

/** Returns the toast text of an ended queue. */
export function queueText(queue: BulkQueue): string {
  if (queue.steps.length === 1) return `Bulk refresh ${stepText(queue.steps[0])}`;
  return `Bulk refresh of ${plural(queue.steps.length, 'server')}: ${queue.steps.map(step => `${step.server} ${stepText(step)}`).join('; ')}`;
}

/** Reports whether an ended queue needs an error toast: a job failed, was
 * interrupted, or ended unknown, or a server did not start for a reason
 * other than a cancel. A server without matching items is no failure. */
export function queueFailed(queue: BulkQueue): boolean {
  return queue.steps.some(
    step => step.state === 'failed' || step.state === 'interrupted' || step.state === 'unknown' || (step.state === 'not-started' && step.note !== 'canceled'),
  );
}

/** Returns the line that describes a running queue, such as "Cabin
 * running · Home waiting". */
export function queueProgress(queue: BulkQueue): string {
  return queue.steps.map(step => `${step.server} ${stepText(step)}`).join(' · ');
}

/** Returns the latest queue outside React. Tests use it. */
export const currentQueue = () => current;

/** Forgets the queue. Tests use it. */
export function resetQueue() {
  queueGeneration++;
  current = undefined;
  isAdmitting = false;
}
