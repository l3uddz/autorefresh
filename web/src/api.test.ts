import { QueryClient } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { newerStatus, statusQuery, type Status } from './api';

const status = (boot: string, seq: number): Status => ({ boot, seq, tmdb: 'off', servers: [] });

describe('status order', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('keeps the newer snapshot of one process', () => {
    const client = new QueryClient();
    const cached = status('a', 5);
    expect(newerStatus(cached, status('a', 4), client)).toBe(cached);
    const next = status('a', 6);
    expect(newerStatus(cached, next, client)).toBe(next);
    expect(newerStatus(undefined, next, client)).toBe(next);
  });

  it('takes the snapshot of a restarted process, whatever its count', () => {
    const next = status('b', 0);
    expect(newerStatus(status('a', 9), next, new QueryClient())).toBe(next);
  });

  it('keeps a restart fetched over HTTP when an older stream event arrives', async () => {
    const client = new QueryClient();
    client.setQueryData(['status'], status('a', 10));
    const restarted = status('b', 0);
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(restarted))));
    await client.fetchQuery(statusQuery(client));
    const buffered = status('a', 11);
    expect(newerStatus(client.getQueryData<Status>(['status']), buffered, client)).toEqual(restarted);
    client.clear();
  });

  it('does not let a late GET replace a newer event', async () => {
    const client = new QueryClient();
    const event = status('a', 7);
    // The event arrives while the GET waits for its answer.
    vi.stubGlobal('fetch', vi.fn(async () => {
      client.setQueryData(['status'], event);
      return new Response(JSON.stringify(status('a', 6)));
    }));
    const query = statusQuery(client);
    expect(await query.queryFn({ signal: new AbortController().signal })).toBe(event);
    client.clear();
  });

  it('ignores an in-flight response from the process replaced during the request', async () => {
    const client = new QueryClient();
    client.setQueryData(['status'], status('a', 5));
    const restarted = status('b', 0);
    vi.stubGlobal('fetch', vi.fn(async () => {
      client.setQueryData(['status'], restarted);
      return new Response(JSON.stringify(status('a', 6)));
    }));
    const query = statusQuery(client);
    expect(await query.queryFn({ signal: new AbortController().signal })).toEqual(restarted);
    client.clear();
  });

  it('keeps the first streamed boot when the initial GET returns another boot', async () => {
    const client = new QueryClient();
    const first = status('b', 1);
    vi.stubGlobal('fetch', vi.fn(async () => {
      client.setQueryData(['status'], first);
      return new Response(JSON.stringify(status('a', 8)));
    }));

    const result = await client.fetchQuery(statusQuery(client));
    expect(result).toEqual(first);
    const next = status('b', 2);
    expect(newerStatus(result, next, client)).toEqual(next);
    client.clear();
  });

  it('evicts retired-boot candidate and auto-match caches when switching boots', () => {
    const client = new QueryClient();
    client.setQueryData(['candidate', 'a', '0', 'plex://show/1'], { tmdbId: '1' });
    client.setQueryData(['auto-match', 'a', 'srv=0&key=1'], { id: 0 });
    client.setQueryData(['candidate', 'b', '0', 'plex://show/1'], { tmdbId: '2' });

    newerStatus(status('a', 3), status('b', 0), client);

    expect(client.getQueryData(['candidate', 'a', '0', 'plex://show/1'])).toBeUndefined();
    expect(client.getQueryData(['auto-match', 'a', 'srv=0&key=1'])).toBeUndefined();
    expect(client.getQueryData(['candidate', 'b', '0', 'plex://show/1'])).toEqual({ tmdbId: '2' });
    client.clear();
  });
});
