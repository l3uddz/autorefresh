import { useEffect, useState } from 'react';
import { useQuery, useQueryClient, type QueryClient, type UseQueryResult } from '@tanstack/react-query';
import type { ItemKey } from './route';
import type { RunRecord, Servers, Status } from './api-types';

export * from './api-types';

/** An answer of the API other than 2xx, with the message of its body. */
export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

/** Reports whether error is the answer of a bulk request whose filters
 * match no item of its server. That is no failure: the next server may
 * still have items. */
export function isNoItems(error: unknown): boolean {
  return error instanceof ApiError && error.status === 400 && error.message === 'no items';
}

// Every URL is relative to the page, so the UI works at any sub-path.
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`./api/${path}`, init);
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string } | null;
    throw new ApiError(response.status, body?.error ?? `Request failed (${response.status})`);
  }
  return response.json() as Promise<T>;
}

/** Sends a POST. Routes without a body get an empty one, as the API
 * requires; every POST carries the JSON content type. */
export function post<T>(path: string, body?: unknown): Promise<T> {
  return api<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export function itemParams(item: ItemKey): string {
  return new URLSearchParams({ srv: String(item.srv), key: item.key }).toString();
}

export const itemID = (item: ItemKey) => `${item.srv}/${item.key}`;

/** Returns the URL of an image of the API at a fixed size. */
export function imageSize(url: string | undefined, size: 'sm' | 'md' | 'lg'): string | undefined {
  return url && `${url}&size=${size}`;
}

export function useServers() {
  return useQuery({
    queryKey: ['servers'],
    queryFn: ({ signal }) => api<Servers>('servers', { signal }),
    staleTime: 60_000,
  });
}

const retiredBoots = new WeakMap<QueryClient, Set<string>>();

/** Keeps the highest sequence for each process and ignores retired ones. */
export function newerStatus(cached: Status | undefined, next: Status, client: QueryClient): Status {
  if (!cached) return next;
  if (cached.boot === next.boot) return next.seq >= cached.seq ? next : cached;
  const retired = retiredBoots.get(client) ?? new Set<string>();
  if (retired.has(next.boot)) return cached;
  retired.add(cached.boot);
  retiredBoots.set(client, retired);
  client.removeQueries({ queryKey: ['candidate', cached.boot] });
  client.removeQueries({ queryKey: ['auto-match', cached.boot] });
  return next;
}

/** The query of GET /api/status. Its answer replaces the cache only when
 * it is newer. */
export function statusQuery(client: QueryClient) {
  return {
    queryKey: ['status'],
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const startingBoot = client.getQueryData<Status>(['status'])?.boot;
      const next = await api<Status>('status', { signal });
      // If the cache switched boots during the GET, a response from a
      // different boot cannot establish which process is current.
      const cached = client.getQueryData<Status>(['status']);
      if (cached && cached.boot !== startingBoot && cached.boot !== next.boot) return cached;
      return newerStatus(cached, next, client);
    },
  };
}

export function useStatus(): UseQueryResult<Status>;
export function useStatus<T>(select: (status: Status) => T): UseQueryResult<T>;
export function useStatus<T = Status>(select?: (status: Status) => T) {
  const client = useQueryClient();
  return useQuery({ ...statusQuery(client), staleTime: Infinity, select });
}

/** Reads the latest runs of the server srv, or of every server. */
export function useRuns(limit: number, srv?: number) {
  const params = new URLSearchParams({ limit: String(limit) });
  if (srv !== undefined) params.set('srv', String(srv));
  return useQuery({
    queryKey: ['runs', limit, srv],
    queryFn: ({ signal }) => api<{ runs: RunRecord[]; next?: string }>(`runs?${params}`, { signal }),
  });
}

/** Refreshes the cached lists after a write, so counts and rows follow. */
export function invalidateLists(client: QueryClient) {
  for (const key of ['items', 'item', 'runs', 'servers', 'counts']) {
    void client.invalidateQueries({ queryKey: [key] });
  }
}

/** The state of the event stream: unavailable means that it never opened
 * and the last try failed. */
export type Connection = 'connecting' | 'open' | 'reconnecting' | 'unavailable';

const retryDelay = 3000;

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

function isRunProgress(value: unknown): boolean {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const r = value as Record<string, unknown>;
  return (
    isFiniteNumber(r.id) &&
    typeof r.kind === 'string' &&
    typeof r.source === 'string' &&
    typeof r.status === 'string' &&
    typeof r.startedAt === 'string' &&
    typeof r.recorded === 'boolean' &&
    isFiniteNumber(r.librariesRead) &&
    isFiniteNumber(r.selected) &&
    isFiniteNumber(r.due) &&
    isFiniteNumber(r.done) &&
    isFiniteNumber(r.refreshed) &&
    isFiniteNumber(r.matched) &&
    isFiniteNumber(r.failed) &&
    isFiniteNumber(r.skipped)
  );
}

/** Validates that value has the required runtime shape of a Status snapshot
 * before the SSE handler reads boot, seq, or servers. */
export function isStatusSnapshot(value: unknown): value is Status {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const v = value as Record<string, unknown>;
  if (typeof v.boot !== 'string' || !isFiniteNumber(v.seq) || typeof v.tmdb !== 'string') {
    return false;
  }
  if (!Array.isArray(v.servers)) return false;
  return v.servers.every(entry => {
    if (typeof entry !== 'object' || entry === null || Array.isArray(entry)) return false;
    const s = entry as Record<string, unknown>;
    if (!isFiniteNumber(s.srv) || typeof s.name !== 'string' || typeof s.health !== 'string') return false;
    if (s.run !== null && !isRunProgress(s.run)) return false;
    if (s.last !== undefined && !isRunProgress(s.last)) return false;
    return true;
  });
}

/** Parses and validates one SSE status event payload, returning undefined on
 * malformed JSON or an unexpected snapshot shape. */
export function parseStatusEvent(raw: string): Status | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return undefined;
  }
  return isStatusSnapshot(parsed) ? parsed : undefined;
}

/** Keeps the status query up to date from GET /api/events and refreshes
 * the lists when a run ends, and when a stream connects: a run may have
 * ended before it, after the lists loaded. A run that starts refreshes the
 * run history. It returns the state of the stream. */
export function useLiveStatus(): Connection {
  const client = useQueryClient();
  const [connection, setConnection] = useState<Connection>('connecting');

  useEffect(() => {
    let disposed = false;
    let stream: EventSource | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    // The last runs of the newest status, undefined until the first
    // status after each open.
    let lastRuns: string | undefined;
    // The running runs of the newest status.
    let lastRunning: string | undefined;
    let wasOpen = false;

    const connect = () => {
      if (disposed) return;
      clearTimeout(retry);
      retry = undefined;
      const source = new EventSource('./api/events');
      stream = source;
      const onStatus = (event: MessageEvent<string>) => {
        if (disposed || stream !== source) return;
        const next = parseStatusEvent(event.data);
        if (!next) return;
        const status = newerStatus(client.getQueryData<Status>(['status']), next, client);
        const runs = JSON.stringify(status.servers.map(({ last }) => [last?.id, last?.finishedAt]));
        const running = JSON.stringify(status.servers.map(({ run }) => run?.id));
        if (runs !== lastRuns) {
          invalidateLists(client);
        } else if (lastRunning !== undefined && running !== lastRunning) {
          // A run that starts adds a row to the run history.
          void client.invalidateQueries({ queryKey: ['runs'] });
        }
        lastRuns = runs;
        lastRunning = running;
        if (status === next) client.setQueryData(['status'], status);
      };
      source.addEventListener('status', onStatus);
      source.onopen = () => {
        if (disposed || stream !== source) return;
        // Each open, also a reconnect of the browser, starts again.
        lastRuns = undefined;
        wasOpen = true;
        setConnection('open');
      };
      source.onerror = () => {
        if (disposed || stream !== source) return;
        setConnection(wasOpen ? 'reconnecting' : 'unavailable');
        // A stream that the browser gave up on (for example after a 503 or
        // a proxy error page) does not retry by itself.
        if (source.readyState === EventSource.CLOSED) {
          source.close();
          clearTimeout(retry);
          retry = setTimeout(connect, retryDelay);
        }
      };
    };
    connect();
    return () => {
      disposed = true;
      clearTimeout(retry);
      stream?.close();
      stream = undefined;
    };
  }, [client]);

  return connection;
}
