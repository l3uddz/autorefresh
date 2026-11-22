import { afterEach, describe, expect, it, vi } from 'vitest';
import { bulkRequests, isSearchTooLong, listParams, stopOrResume, type BulkRequest } from './bulk';

describe('bulk requests', () => {
  const filters = { srv: 1, library: '3', reason: 'no-art', type: 'movie', q: '  harbor ', all: true };

  it('builds list parameters with the tab and sort', () => {
    expect(listParams('attention', filters, '-tries').toString()).toBe('srv=1&library=3&reason=no-art&type=movie&q=harbor&all=1&tab=attention&sort=-tries');
    // The Stopped tab has no "all" filter, and a library needs a server.
    expect(listParams('stopped', { library: '3', all: true }, '').toString()).toBe('tab=stopped');
    // The Fixed tab has no reason filter; its "all" includes items that
    // Plex completed alone.
    expect(listParams('fixed', filters, '').toString()).toBe('srv=1&library=3&type=movie&q=harbor&all=1&tab=fixed');
  });

  it('sends a matching selection with the filters and without tab or sort', () => {
    const [request] = bulkRequests('stop', 'attention', filters, { kind: 'matching' }, [0, 1]);
    expect(request).toEqual({ srv: 1, path: 'items/stop?srv=1&library=3&reason=no-art&type=movie&q=harbor&all=1', body: { matching: true } });
  });

  it('sends one matching request for each server without a server filter', () => {
    const requests = bulkRequests('resume', 'stopped', { reason: 'unmatched', all: true }, { kind: 'matching' }, [0, 1]);
    expect(requests.map(r => r.path)).toEqual(['items/resume?srv=0&reason=unmatched', 'items/resume?srv=1&reason=unmatched']);
  });

  it('groups selected rows by server and sends only their keys', () => {
    const items = [{ srv: 0, key: '1' }, { srv: 1, key: '2' }, { srv: 0, key: '3' }];
    expect(bulkRequests('refresh', 'attention', filters, { kind: 'keys', items }, [0, 1])).toEqual([
      { srv: 0, path: 'items/refresh?srv=0', body: { keys: ['1', '3'] } },
      { srv: 1, path: 'items/refresh?srv=1', body: { keys: ['2'] } },
    ]);
  });
});

describe('bulk stop and resume', () => {
  const requests: BulkRequest[] = [
    { srv: 0, path: 'items/stop?srv=0', body: { matching: true } },
    { srv: 1, path: 'items/stop?srv=1', body: { matching: true } },
  ];
  const names = { 0: 'Home', 1: 'Cabin' };

  /** Answers each POST with the next answer of the server in its srv. */
  function fakeApi(answers: Record<number, { status: number; body: unknown }>) {
    vi.stubGlobal('fetch', vi.fn(async (input: string) => {
      const answer = answers[Number(new URL(input, 'http://ui/').searchParams.get('srv'))];
      return new Response(JSON.stringify(answer.body), { status: answer.status });
    }));
  }
  const noItems = { status: 400, body: { error: 'no items' } };
  const changed = (count: number) => ({ status: 200, body: { changed: count, skipped: [] } });
  afterEach(() => vi.unstubAllGlobals());

  it('goes on to a later server when the first one has no matching items', async () => {
    fakeApi({ 0: noItems, 1: changed(3) });
    expect(await stopOrResume('stop', requests, names)).toEqual({ text: 'Stopped 3 items', isError: false });
  });

  it('reports no match when no server has matching items', async () => {
    fakeApi({ 0: noItems, 1: noItems });
    expect(await stopOrResume('resume', requests, names)).toEqual({ text: 'No items match the filters.', isError: false });
  });

  it('throws when a server failed and none changed anything', async () => {
    fakeApi({ 0: noItems, 1: { status: 409, body: { error: 'a run is busy' } } });
    await expect(stopOrResume('stop', requests, names)).rejects.toThrow('Cabin: A run or another bulk job is busy. Try again when it ends.');
  });

  it('reports a failed server beside the servers that changed items', async () => {
    fakeApi({ 0: changed(2), 1: { status: 500, body: { error: 'internal error' } } });
    expect(await stopOrResume('stop', requests, names)).toEqual({ text: 'Stopped 2 items · failed on Cabin: internal error', isError: true });
  });
});

describe('search limit', () => {
  it('counts the UTF-8 bytes of the trimmed search, as the API does', () => {
    expect(isSearchTooLong(`  ${'a'.repeat(200)}  `)).toBe(false);
    expect(isSearchTooLong('a'.repeat(201))).toBe(true);
    // 101 characters of two bytes each.
    expect(isSearchTooLong('é'.repeat(100))).toBe(false);
    expect(isSearchTooLong('é'.repeat(101))).toBe(true);
  });
});
