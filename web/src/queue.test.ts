import { QueryClient } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { RunProgress, Status } from './api';
import type { BulkRequest } from './bulk';
import { cancelBulkQueue, isQueueActive, onQueueDone, queueFailed, queueText, queueTiming, resetQueue, startBulkRefresh, type BulkQueue } from './queue';

const requests: BulkRequest[] = [
  { srv: 0, path: 'items/refresh?srv=0', body: { keys: ['1', '2'] } },
  { srv: 1, path: 'items/refresh?srv=1', body: { keys: ['3'] } },
];
const names = { 0: 'Home', 1: 'Cabin' };

interface Answer {
  status: number;
  body: unknown;
}

/** A fake API: refresh POSTs take answers from a list per server, cancel
 * records its job, and run items answer from a map. */
function fakeApi(refresh: Record<number, Answer[]>, items: Record<number, string[]> = {}) {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://ui/');
    const path = url.pathname.replace(/^\/api\//, '');
    calls.push(`${init?.method ?? 'GET'} ${path}${url.search}`);
    let answer: Answer = { status: 404, body: { error: 'no such route' } };
    if (path === 'items/refresh') answer = refresh[Number(url.searchParams.get('srv'))].shift()!;
    if (path === 'runs/cancel') answer = { status: 200, body: {} };
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
    Object.assign(queueTiming, { retries: 3, retryDelay: 0, pollInterval: 60_000 });
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
