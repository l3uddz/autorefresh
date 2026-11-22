import { QueryClient } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { newerStatus, statusQuery, type Status } from './api';

const status = (boot: string, seq: number): Status => ({ boot, seq, tmdb: 'off', servers: [] });

describe('status order', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('keeps the newer snapshot of one process', () => {
    const cached = status('a', 5);
    expect(newerStatus(cached, status('a', 4))).toBe(cached);
    const next = status('a', 6);
    expect(newerStatus(cached, next)).toBe(next);
    expect(newerStatus(undefined, next)).toBe(next);
  });

  it('takes the snapshot of a restarted process, whatever its count', () => {
    const next = status('b', 0);
    expect(newerStatus(status('a', 9), next)).toBe(next);
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
});
