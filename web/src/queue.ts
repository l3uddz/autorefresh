import type { QueryClient } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import { ApiError, api, invalidateLists, post, type RunItem, type RunProgress, type Status } from './api';
import type { BulkRequest } from './bulk';
import { plural } from './format';

// A bulk refresh job works on one server, and the run lock is global, so a
// selection that spans servers becomes a queue: one job for each server,
// each started after the previous one ends. The queue lives in this module,
// not in a component, so it keeps going when the bulk band closes or the
// page changes. A page reload drops the servers that have not started.

export type StepState = 'waiting' | 'running' | 'finished' | 'failed' | 'canceled' | 'not-started';

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
}

/** Retry timing for a job that the previous job's lock still blocks: the
 * status reports the end of a job just before it releases the lock. */
export const queueTiming = { retries: 10, retryDelay: 2000, pollInterval: 15_000 };

let current: BulkQueue | undefined;
const listeners = new Set<() => void>();
const doneListeners = new Set<(queue: BulkQueue) => void>();

function publish(next: BulkQueue) {
  current = next;
  for (const listener of listeners) listener();
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

/** Starts the bulk refresh of requests, one server after another. It
 * returns once the first server admitted its job, and throws when it did
 * not, so nothing runs. names gives the name of each server. */
export async function startBulkRefresh(client: QueryClient, requests: BulkRequest[], names: Record<number, string>): Promise<BulkQueue> {
  if (isQueueActive()) throw new Error('A bulk refresh is already running. Wait until it ends.');
  if (!requests.length) throw new Error('Nothing to refresh');
  const job = await post<{ id: number; count: number }>(requests[0].path, requests[0].body);
  publish({
    isCanceled: false,
    isDone: false,
    steps: requests.map((request, i) => ({
      srv: request.srv,
      server: names[request.srv] ?? `Server ${request.srv}`,
      state: i === 0 ? 'running' : 'waiting',
      ...(i === 0 ? { job: job.id, count: job.count } : {}),
    })),
  });
  const queue = current!;
  void drive(client, requests);
  return queue;
}

/** Cancels the queue: the running job stops at its next item, and the
 * waiting servers do not start. */
export async function cancelBulkQueue() {
  if (!current || current.isDone) return;
  publish({ ...current, isCanceled: true });
  const running = current.steps.find(step => step.state === 'running');
  if (running?.job !== undefined) await cancelJob(running.job);
}

async function cancelJob(id: number) {
  // A job that already ended answers 404, which changes nothing here.
  await post(`runs/cancel?${new URLSearchParams({ id: String(id) })}`).catch(() => undefined);
}

const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

type Admission = { id: number; count: number } | { busy: true } | { error: string };

/** Starts the job of request. It retries while another job holds the run
 * lock, and gives up when the queue is canceled. */
async function admit(request: BulkRequest): Promise<Admission | undefined> {
  for (let attempt = 0; ; attempt++) {
    if (current?.isCanceled) return undefined;
    try {
      return await post<{ id: number; count: number }>(request.path, request.body);
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 409) return { error: (error as Error).message };
      if (attempt >= queueTiming.retries) return { busy: true };
    }
    await sleep(queueTiming.retryDelay);
  }
}

/** Waits until the status reports the end of job id on the server srv. */
function waitForJob(client: QueryClient, srv: number, id: number): Promise<RunProgress> {
  return new Promise(resolve => {
    const ended = () => {
      const last = client.getQueryData<Status>(['status'])?.servers.find(server => server.srv === srv)?.last;
      return last?.id === id ? last : undefined;
    };
    const check = () => {
      const last = ended();
      if (!last) return;
      unsubscribe();
      clearInterval(poll);
      resolve(last);
    };
    const unsubscribe = client.getQueryCache().subscribe(event => {
      if (event.query.queryKey[0] === 'status') check();
    });
    // The event stream normally reports the end. The poll covers a stream
    // that is down, and a job that ended before the watch began.
    const poll = setInterval(() => void client.invalidateQueries({ queryKey: ['status'] }), queueTiming.pollInterval);
    void client.invalidateQueries({ queryKey: ['status'] });
    check();
  });
}

const endStates: Record<string, StepState> = { finished: 'finished', failed: 'failed', canceled: 'canceled', interrupted: 'canceled' };

async function drive(client: QueryClient, requests: BulkRequest[]) {
  for (let i = 0; i < requests.length; i++) {
    const start = i === 0 ? 'running' : await startStep(i, requests[i]);
    if (start === 'skip') continue;
    if (start === 'stop') break;
    const step = current!.steps[i];
    const last = await waitForJob(client, step.srv, step.job!);
    // Only a recorded job has stored results that match its status.
    const items = last.recorded
      ? await api<{ items: RunItem[] }>(`runs/items?${new URLSearchParams({ id: String(last.id) })}`).then(
          body => body.items,
          () => undefined,
        )
      : undefined;
    setStep(i, { state: endStates[last.status] ?? 'failed', items });
    invalidateLists(client);
    if (last.status === 'canceled' || current?.isCanceled) {
      skipFrom(i + 1, 'canceled');
      break;
    }
  }
  if (!current) return;
  publish({ ...current, isDone: true });
  for (const listener of doneListeners) listener(current);
}

/** Starts step index. A refused step is skipped: the next server may
 * still start. A cancel or a busy run lock stops the queue. */
async function startStep(index: number, request: BulkRequest): Promise<'running' | 'skip' | 'stop'> {
  const admitted = await admit(request);
  if (!admitted || 'busy' in admitted) {
    skipFrom(index, admitted ? 'another run is busy' : 'canceled');
    return 'stop';
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
  'not-started': 'not started',
};

function stepText(step: QueueStep): string {
  const verb = stateVerbs[step.state];
  if (step.state === 'not-started') return step.note ? `${verb}: ${step.note}` : verb;
  if (step.state === 'waiting' || step.state === 'running') return verb;
  return step.items ? `${verb}: ${outcomeText(step.items)}` : `${verb}, results unavailable`;
}

/** Returns the toast text of an ended queue. */
export function queueText(queue: BulkQueue): string {
  if (queue.steps.length === 1) return `Bulk refresh ${stepText(queue.steps[0])}`;
  return `Bulk refresh of ${plural(queue.steps.length, 'server')}: ${queue.steps.map(step => `${step.server} ${stepText(step)}`).join('; ')}`;
}

/** Reports whether an ended queue needs an error toast: a job failed, or a
 * server did not start for a reason other than a cancel. */
export function queueFailed(queue: BulkQueue): boolean {
  return queue.steps.some(step => step.state === 'failed' || (step.state === 'not-started' && step.note !== 'canceled'));
}

/** Returns the line that describes a running queue, such as "Cabin
 * running · Home waiting". */
export function queueProgress(queue: BulkQueue): string {
  return queue.steps.map(step => `${step.server} ${stepText(step)}`).join(' · ');
}

/** Forgets the queue. Tests use it. */
export function resetQueue() {
  current = undefined;
}
