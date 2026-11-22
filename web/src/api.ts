import { useEffect, useState } from 'react';
import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import type { ItemKey, TabName } from './route';

// Types of the JSON API in internal/web. Times are RFC 3339 strings, and
// an omitted time is undefined.

export interface ItemRecord {
  srv: number;
  server?: string;
  section?: string;
  library?: string;
  key: string;
  type?: string;
  title?: string;
  year?: number;
  path?: string;
  thumb?: string;
  art?: string;
  cached: boolean;
  reasons?: string[];
  state?: string;
  nextDue?: string;
  addedAt?: string;
  lastAttempt?: string;
  stoppedAt?: string;
  attempts: number;
  refreshes: number;
  lastAction?: string;
  lastOutcome?: string;
  lastMatchOutcome?: string;
  completedAt?: string;
  fixedAction?: string;
  fixedMatch?: string;
}

export interface ItemPage {
  items: ItemRecord[];
  next?: string;
  counts: Record<TabName, number>;
}

export interface TimelineEvent {
  id: number;
  at?: string;
  kind: string;
  source?: string;
  action?: string;
  accepted: boolean;
  outcome?: string;
  error?: string;
  match?: { guid: string; title?: string; year?: number };
  /** The request that fixed the item, on a completed event. */
  fixedAction?: string;
  /** The match that fixed the item, on a completed event. */
  fixedMatch?: string;
}

export interface ItemDetail {
  item: ItemRecord;
  timeline: TimelineEvent[];
}

export interface LiveItem {
  summary?: string;
  guid?: string;
  seasons?: number;
  episodes?: number;
  locations: string[];
  files: string[];
  path?: string;
}

export interface Library {
  section: string;
  title: string;
  type: string;
  lastRead?: string;
}

export interface ServerInfo {
  srv: number;
  name: string;
  machineIdentifier?: string;
  scanned: boolean;
  libraries: Library[];
}

export interface Servers {
  servers: ServerInfo[];
  ignoreReasons: string[];
}

export interface RunProgress {
  id: number;
  kind: string;
  source: string;
  status: string;
  startedAt: string;
  finishedAt?: string;
  error?: string;
  recorded: boolean;
  librariesRead: number;
  library?: string;
  selected: number;
  due: number;
  done: number;
  refreshed: number;
  matched: number;
  failed: number;
  skipped: number;
  current?: string;
}

export type Health = 'unknown' | 'ok' | 'unreachable' | 'unauthorized';

export interface ServerStatus {
  srv: number;
  name: string;
  health: Health;
  last?: RunProgress;
  run: RunProgress | null;
}

export interface Status {
  servers: ServerStatus[];
  tmdb: 'off' | Health;
}

export interface RunRecord {
  id: number;
  srv: number;
  server: string;
  kind: string;
  source: string;
  status: string;
  startedAt?: string;
  finishedAt?: string;
  error?: string;
  readItems: number;
  refreshed: number;
  matched: number;
  failed: number;
  completed: number;
  fixed: number;
  incomplete: number;
  due: number;
  selected: number;
  skipped: number;
}

export interface RunItem {
  key: string;
  title: string;
  outcome: string;
  error?: string;
}

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

export function useStatus() {
  return useQuery({
    queryKey: ['status'],
    queryFn: ({ signal }) => api<Status>('status', { signal }),
    staleTime: Infinity,
  });
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

/** Keeps the status query up to date from GET /api/events and refreshes
 * the lists when a run ends. It returns the state of the stream. */
export function useLiveStatus(): Connection {
  const client = useQueryClient();
  const [connection, setConnection] = useState<Connection>('connecting');

  useEffect(() => {
    let stream: EventSource | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let lastRuns: string | undefined;
    let wasOpen = false;

    const onStatus = (event: MessageEvent<string>) => {
      const status = JSON.parse(event.data) as Status;
      client.setQueryData(['status'], status);
      const runs = JSON.stringify(status.servers.map(({ last }) => [last?.id, last?.finishedAt]));
      if (lastRuns !== undefined && runs !== lastRuns) invalidateLists(client);
      lastRuns = runs;
    };

    const connect = () => {
      stream = new EventSource('./api/events');
      stream.addEventListener('status', onStatus);
      stream.onopen = () => {
        // After a gap, the lists may have missed a finished run.
        if (wasOpen) invalidateLists(client);
        wasOpen = true;
        setConnection('open');
      };
      stream.onerror = () => {
        setConnection(wasOpen ? 'reconnecting' : 'unavailable');
        // A stream that the browser gave up on (for example after a 503 or
        // a proxy error page) does not retry by itself.
        if (stream?.readyState === EventSource.CLOSED) {
          stream.close();
          retry = setTimeout(connect, retryDelay);
        }
      };
    };
    connect();
    return () => {
      clearTimeout(retry);
      stream?.close();
    };
  }, [client]);

  return connection;
}
