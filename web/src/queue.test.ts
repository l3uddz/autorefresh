import { QueryClient } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { RunProgress, RunRecord, Status } from './api';
import type { BulkRequest } from './bulk';
import { cancelBulkQueue, cancelQueueOfJob, isQueueActive, NothingMatched, onQueueDone, queueFailed, queueText, queueTiming, resetQueue, startBulkRefresh, type BulkQueue } from './queue';

const requests: BulkRequest[] = [
  { srv: 0, path: 'items/refresh?srv=0', body: { keys: ['1', '2'] } },
  { srv: 1, path: 'items/refresh?srv=1', body: { keys: ['3'] } },
];
const names = { 0: 'Home', 1: 'Cabin' };

interface Answer {
  status: number;
  body: unknown;
}

/** What the fake API reads besides refresh answers: the status that GET
 * status answers (404 without one), and the stored runs of each server,
 * newest first (404 without them). */
interface Stored {
  status?: Status;
  runs?: Record<number, RunRecord[]>;
}

/** A fake API: refresh POSTs take answers from a list per server, cancel
 * records its job, run items answer from a map, and status and runs answer
 * from stored. */
function fakeApi(refresh: Record<number, Answer[]>, items: Record<number, string[]> = {}, stored: Stored = {}) {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://ui/');
    const path = url.pathname.replace(/^\/api\//, '');
    calls.push(`${init?.method ?? 'GET'} ${path}${url.search}`);
    let answer: Answer = { status: 404, body: { error: 'no such route' } };
    if (path === 'items/refresh') answer = refresh[Number(url.searchParams.get('srv'))].shift()!;
    if (path === 'runs/cancel') answer = { status: 200, body: {} };
    if (path === 'status' && stored.status) answer = { status: 200, body: stored.status };
    if (path === 'runs' && stored.runs) answer = { status: 200, body: { runs: stored.runs[Number(url.searchParams.get('srv'))] ?? [] } };
    if (path === 'runs/items') {
      const outcomes = items[Number(url.searchParams.get('id'))];
      answer = outcomes ? { status: 200, body: { items: outcomes.map((outcome, i) => ({ key: String(i), title: '', outcome })) } } : { status: 500, body: { error: 'store failed' } };
    }
    return new Response(JSON.stringify(answer.body), { status: answer.status });
  });
  vi.stubGlobal('fetch', fetchMock);
  return calls;
}

const accepted = (id: number, count: number): Answer => ({ status: 202, body: { id, count } });
const noItems: Answer = { status: 400, body: { error: 'no items' } };

function storedRun(id: number, srv: number, status: string): RunRecord {
  return { id, srv, server: '', kind: 'bulk-refresh', source: 'manual', status, readItems: 0, refreshed: 0, matched: 0, failed: 0, completed: 0, fixed: 0, incomplete: 0, due: 0, selected: 0, skipped: 0 };
}

/** A status in which no server runs or ran anything, as after a restart. */
function freshStatus(): Status {
  return { tmdb: 'off', servers: [0, 1, 2].map(srv => ({ srv, name: '', health: 'ok' as const, run: null })) };
}

function lastRun(id: number, status: string, recorded = true): RunProgress {
  return { id, kind: 'bulk-refresh', source: 'manual', status, startedAt: '', recorded, librariesRead: 0, selected: 0, due: 0, done: 0, refreshed: 0, matched: 0, failed: 0, skipped: 0 };
}

/** Reports in the status that job id of the server srv ended. */
function end(client: QueryClient, srv: number, run: RunProgress) {
  const status = client.getQueryData<Status>(['status']) ?? { tmdb: 'off', servers: [0, 1, 2].map(i => ({ srv: i, name: '', health: 'ok' as const, run: null })) };
  client.setQueryData<Status>(['status'], { ...status, servers: status.servers.map(server => (server.srv === srv ? { ...server, last: run } : server)) });
}

function nextDone(): Promise<BulkQueue> {
  return new Promise(resolve => {
    const off = onQueueDone(queue => {
      off();
      resolve(queue);
    });
  });
}

const refreshCalls = (calls: string[]) => calls.filter(call => call.startsWith('POST items/refresh'));

describe('bulk refresh queue', () => {
  let client: QueryClient;
  beforeEach(() => {
    client = new QueryClient();
    resetQueue();
    Object.assign(queueTiming, { retries: 3, retryDelay: 0, pollInterval: 60_000, unknownAfter: 3 });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    client.clear();
  });

  it('starts the next server only after the job of the previous one ends', async () => {
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [accepted(8, 1)] }, { 7: ['accepted', 'failed'], 8: ['accepted'] });
    const done = nextDone();
    const queue = await startBulkRefresh(client, requests, names);
    expect(queue.steps.map(step => step.state)).toEqual(['running', 'waiting']);
    await new Promise(resolve => setTimeout(resolve, 10));
    expect(refreshCalls(calls)).toEqual(['POST items/refresh?srv=0']);

    end(client, 0, lastRun(7, 'finished'));
    await vi.waitFor(() => expect(refreshCalls(calls)).toHaveLength(2));
    end(client, 1, lastRun(8, 'finished'));

    const result = await done;
    expect(queueText(result)).toBe('Bulk refresh of 2 servers: Home finished: 1 accepted · 1 failed; Cabin finished: 1 accepted');
    expect(queueFailed(result)).toBe(false);
  });

  it('does not start the waiting servers after a cancel', async () => {
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [accepted(8, 1)] }, { 7: ['accepted'] });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    await cancelBulkQueue();
    expect(calls).toContain('POST runs/cancel?id=7');
    end(client, 0, lastRun(7, 'canceled'));

    const result = await done;
    expect(refreshCalls(calls)).toHaveLength(1);
    expect(queueText(result)).toBe('Bulk refresh of 2 servers: Home canceled: 1 accepted; Cabin not started: canceled');
    expect(queueFailed(result)).toBe(false);
  });

  it('stops the queue when the running job is canceled from its server card', async () => {
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [accepted(8, 1)] }, { 7: [] });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    end(client, 0, lastRun(7, 'canceled'));
    const result = await done;
    expect(refreshCalls(calls)).toHaveLength(1);
    expect(result.steps.map(step => step.state)).toEqual(['canceled', 'not-started']);
  });

  it('retries while the previous job still holds the run lock', async () => {
    const busy = { status: 409, body: { error: 'a run is busy' } };
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [busy, busy, accepted(8, 1)] }, { 7: ['accepted'], 8: ['accepted'] });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    end(client, 0, lastRun(7, 'finished'));
    await vi.waitFor(() => expect(refreshCalls(calls)).toHaveLength(4));
    end(client, 1, lastRun(8, 'finished'));
    expect((await done).steps.map(step => step.state)).toEqual(['finished', 'finished']);
  });

  it('reports a server that stays busy as not started', async () => {
    const busy = { status: 409, body: { error: 'a run is busy' } };
    fakeApi({ 0: [accepted(7, 2)], 1: [busy, busy, busy, busy] }, { 7: ['accepted'] });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    end(client, 0, lastRun(7, 'finished'));
    const result = await done;
    expect(queueText(result)).toBe('Bulk refresh of 2 servers: Home finished: 1 accepted; Cabin not started: another run is busy');
    expect(queueFailed(result)).toBe(true);
  });

  it('skips a server that refuses its job and starts the next one', async () => {
    const three = [...requests, { srv: 2, path: 'items/refresh?srv=2', body: { keys: ['4'] } }];
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [{ status: 400, body: { error: 'too many items' } }], 2: [accepted(9, 1)] }, { 7: ['accepted'], 9: ['accepted'] });
    const done = nextDone();
    await startBulkRefresh(client, three, { ...names, 2: 'Attic' });
    end(client, 0, lastRun(7, 'finished'));
    await vi.waitFor(() => expect(refreshCalls(calls)).toEqual(['POST items/refresh?srv=0', 'POST items/refresh?srv=1', 'POST items/refresh?srv=2']));
    end(client, 2, lastRun(9, 'finished'));
    const result = await done;
    expect(queueText(result)).toBe('Bulk refresh of 3 servers: Home finished: 1 accepted; Cabin not started: too many items; Attic finished: 1 accepted');
    expect(queueFailed(result)).toBe(true);
  });

  it('says that results are unavailable instead of counting none', async () => {
    fakeApi({ 0: [accepted(7, 2)] }, {});
    const done = nextDone();
    await startBulkRefresh(client, requests.slice(0, 1), names);
    end(client, 0, lastRun(7, 'finished', false));
    expect(queueText(await done)).toBe('Bulk refresh finished, results unavailable');
  });

  it('skips a first server without matching items and starts the next one', async () => {
    const calls = fakeApi({ 0: [noItems], 1: [accepted(8, 1)] }, { 8: ['accepted'] });
    const done = nextDone();
    const queue = await startBulkRefresh(client, requests, names);
    expect(queue.steps.map(step => step.state)).toEqual(['empty', 'running']);
    expect(refreshCalls(calls)).toEqual(['POST items/refresh?srv=0', 'POST items/refresh?srv=1']);
    end(client, 1, lastRun(8, 'finished'));
    const result = await done;
    expect(queueText(result)).toBe('Bulk refresh of 2 servers: Home no matching items; Cabin finished: 1 accepted');
    expect(queueFailed(result)).toBe(false);
  });

  it('skips a later server without matching items and starts the one after it', async () => {
    const three = [...requests, { srv: 2, path: 'items/refresh?srv=2', body: { matching: true as const } }];
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [noItems], 2: [accepted(9, 1)] }, { 7: ['accepted'], 9: ['accepted'] });
    const done = nextDone();
    await startBulkRefresh(client, three, { ...names, 2: 'Attic' });
    end(client, 0, lastRun(7, 'finished'));
    await vi.waitFor(() => expect(refreshCalls(calls)).toHaveLength(3));
    end(client, 2, lastRun(9, 'finished'));
    const result = await done;
    expect(result.steps.map(step => step.state)).toEqual(['finished', 'empty', 'finished']);
    expect(queueFailed(result)).toBe(false);
  });

  it('reports that nothing matched when no server has matching items', async () => {
    fakeApi({ 0: [noItems], 1: [noItems] });
    await expect(startBulkRefresh(client, requests, names)).rejects.toBeInstanceOf(NothingMatched);
    expect(isQueueActive()).toBe(false);
  });

  it('reads how the job ended from the stored runs after a restart', async () => {
    // The restarted service knows no job, and it marked the job's run as
    // interrupted. The queue must not wait forever or start the next server.
    Object.assign(queueTiming, { pollInterval: 5 });
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [accepted(8, 1)] }, {}, { status: freshStatus(), runs: { 0: [storedRun(7, 0, 'interrupted'), storedRun(6, 0, 'finished')] } });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    const result = await done;
    expect(refreshCalls(calls)).toHaveLength(1);
    expect(calls).not.toContain('GET runs/items?id=7');
    expect(queueText(result)).toBe('Bulk refresh of 2 servers: Home interrupted, results unavailable; Cabin not started: the previous server was interrupted');
    expect(queueFailed(result)).toBe(true);
  });

  it('reads the result of a job whose last run a later run replaced', async () => {
    // A scheduled run ended after the job, so the status names it as last.
    Object.assign(queueTiming, { pollInterval: 5 });
    const status = freshStatus();
    status.servers[0].last = lastRun(10, 'finished');
    status.servers[1].last = lastRun(8, 'finished');
    const runs = { 0: [storedRun(10, 0, 'finished'), storedRun(7, 0, 'finished')] };
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [accepted(8, 1)] }, { 7: ['accepted', 'accepted'], 8: ['failed'] }, { status, runs });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    const result = await done;
    expect(refreshCalls(calls)).toHaveLength(2);
    expect(queueText(result)).toBe('Bulk refresh of 2 servers: Home finished: 2 accepted; Cabin finished: 0 accepted · 1 failed');
  });

  it('reads the stored runs while status updates of another run keep arriving', async () => {
    // After a restart, the first poll finds the job's run still running.
    // Then the run is marked interrupted while a scheduled run of another
    // server sends progress faster than the poll interval.
    Object.assign(queueTiming, { pollInterval: 40 });
    const runs = { 0: [storedRun(7, 0, 'running')] };
    fakeApi({ 0: [accepted(7, 2)], 1: [accepted(8, 1)] }, {}, { status: freshStatus(), runs });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    await new Promise(resolve => setTimeout(resolve, 10));
    runs[0] = [storedRun(7, 0, 'interrupted')];
    const progress = setInterval(() => end(client, 2, lastRun(99, 'running')), 5);
    try {
      const result = await done;
      expect(result.steps.map(step => step.state)).toEqual(['interrupted', 'not-started']);
    } finally {
      clearInterval(progress);
    }
  });

  it('ends a job that neither the status nor the stored runs know as unknown', async () => {
    Object.assign(queueTiming, { pollInterval: 5 });
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [accepted(8, 1)] }, {}, { status: freshStatus(), runs: { 0: [storedRun(5, 0, 'finished')] } });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    const result = await done;
    expect(refreshCalls(calls)).toHaveLength(1);
    expect(queueText(result)).toBe('Bulk refresh of 2 servers: Home result unknown; Cabin not started: the result of the previous server is unknown');
    expect(queueFailed(result)).toBe(true);
  });

  it('stops the queue when the server card cancels a job that already finished', async () => {
    const calls = fakeApi({ 0: [accepted(7, 2)], 1: [accepted(8, 1)] }, { 7: ['accepted'] });
    const done = nextDone();
    await startBulkRefresh(client, requests, names);
    end(client, 0, lastRun(7, 'finished'));
    // The cancel of job 7 reaches the queue after the job ended.
    expect(cancelQueueOfJob(7)).toBe(true);
    const result = await done;
    expect(refreshCalls(calls)).toHaveLength(1);
    expect(queueText(result)).toBe('Bulk refresh of 2 servers: Home finished: 1 accepted; Cabin not started: canceled');
    expect(cancelQueueOfJob(7)).toBe(false);
  });

  it('starts nothing when the first server refuses the job', async () => {
    const calls = fakeApi({ 0: [{ status: 400, body: { error: 'too many items' } }] });
    await expect(startBulkRefresh(client, requests, names)).rejects.toThrow('too many items');
    expect(refreshCalls(calls)).toHaveLength(1);
    expect(isQueueActive()).toBe(false);
  });
});

describe('queue text', () => {
  it('counts accepted, failed, and skipped items of one server', () => {
    const items = ['accepted', 'accepted', 'failed', 'gone', 'changed'].map((outcome, i) => ({ key: String(i), title: '', outcome }));
    const queue = (state: 'finished' | 'canceled', list = items): BulkQueue => ({ isCanceled: false, isDone: true, steps: [{ srv: 0, server: 'Home', state, items: list }] });
    expect(queueText(queue('finished'))).toBe('Bulk refresh finished: 2 accepted · 1 failed · 2 skipped');
    expect(queueText(queue('canceled', items.slice(0, 1)))).toBe('Bulk refresh canceled: 1 accepted');
  });
});
