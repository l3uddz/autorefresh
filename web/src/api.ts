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
  /** The ID of the accepted match request that fixed the item. */
  fixedMatchEventId?: number;
  /** The match rule that stopped a match, on a match decision. */
  conflict?: string;
  pathYear?: number;
  tmdbYear?: number;
  episodeCount?: number;
  /** The IDs whose search found or confirmed the request. */
  links?: string[];
  /** The searches of a match decision, in the order autorefresh made them. */
  searches: MatchSearch[];
}

/** One Plex match search: of a match decision, or of the Fix match dialog.
 * Source is path, tmdb, or tie in a decision, and title, id, path, or tmdb
 * in the dialog. FromId is the folder ID that a TMDB search came from. */
export interface MatchSearch {
  query: string;
  source?: string;
  fromId?: string;
  failed: boolean;
  candidates: Candidate[];
}

/** A match candidate of Plex. A score of 0 means that Plex gave none. The
 * thumb is a remote https URL, or absent. */
export interface Candidate {
  guid: string;
  name?: string;
  year?: number;
  type?: string;
  summary?: string;
  thumb?: string;
  score: number;
  /** The verdict of a match decision: picked, or why it was not. */
  verdict?: string;
}

/** A folder ID of an item, with the page of its provider. */
export interface FolderID {
  id: string;
  link: string;
}

/** The answer of GET /api/item/matches. Only the opening search (without
 * q) reads the paths, so a typed search has no IDs, locations, or files. */
export interface Matches {
  ids: FolderID[];
  locations: string[];
  files: string[];
  searches: MatchSearch[];
  tmdbFailed: boolean;
}

/** The answer of POST /api/item/episode-check. */
export interface EpisodeCheck {
  match: boolean;
  episodes: number;
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
  /** When the next full cache refresh of a library is due, absent without
   * libraries. */
  nextFullRead?: string;
  /** True when the next full cache refresh is due at the time of the
   * answer. */
  fullReadDue?: boolean;
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
  /** The process that took the snapshot. A restart starts a new one. */
  boot: string;
  /** The number of changes of that process before the snapshot. */
  seq: number;
  servers: ServerStatus[];
  tmdb: 'off' | Health;
  /** The next start of the run schedule, absent without a schedule. */
  nextRun?: string;
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
  /** The libraries that the run read fully and incrementally. Older runs
   * have neither. */
  fullReads?: number;
  incrementalReads?: number;
  /** False for a run from before runs linked their events, which lists no
   * item. */
  eventsLinked: boolean;
}

export interface RunItem {
  key: string;
  title: string;
  outcome: string;
  error?: string;
  /** The facts of the item, only in the details of a run. */
  year?: number;
  type?: string;
  thumb?: string;
  library?: string;
}

/** An event that a run wrote, with the item that it names. */
export interface RunEvent extends TimelineEvent {
  key: string;
  title?: string;
  /** The title of the library of the item. */
  library?: string;
  year?: number;
  type?: string;
  thumb?: string;
}

/** One run with the events that it wrote and, for a bulk refresh, the
 * outcome of each selected item. */
export interface RunDetail {
  run: RunRecord;
  events: RunEvent[];
  items: RunItem[];
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

const retiredBoots = new WeakMap<QueryClient, Set<string>>();

/** Keeps the highest sequence for each process and ignores retired ones. */
export function newerStatus(cached: Status | undefined, next: Status, client: QueryClient): Status {
  if (!cached) return next;
  if (cached.boot === next.boot) return next.seq >= cached.seq ? next : cached;
  const retired = retiredBoots.get(client) ?? new Set<string>();
  if (retired.has(next.boot)) return cached;
  retired.add(cached.boot);
  retiredBoots.set(client, retired);
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

export function useStatus() {
  const client = useQueryClient();
  return useQuery({ ...statusQuery(client), staleTime: Infinity });
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
 * the lists when a run ends, and when a stream connects: a run may have
 * ended before it, after the lists loaded. A run that starts refreshes the
 * run history. It returns the state of the stream. */
export function useLiveStatus(): Connection {
  const client = useQueryClient();
  const [connection, setConnection] = useState<Connection>('connecting');

  useEffect(() => {
    let stream: EventSource | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    // The last runs of the newest status, undefined until the first
    // status after each open.
    let lastRuns: string | undefined;
    // The running runs of the newest status.
    let lastRunning: string | undefined;
    let wasOpen = false;

    const onStatus = (event: MessageEvent<string>) => {
      const next = JSON.parse(event.data) as Status;
      const status = newerStatus(client.getQueryData<Status>(['status']), next, client);
      const runs = JSON.stringify(status.servers.map(({ last }) => [last?.id, last?.finishedAt]));
      if (runs !== lastRuns) invalidateLists(client);
      lastRuns = runs;
      // A run that starts adds a row to the run history.
      const running = JSON.stringify(status.servers.map(({ run }) => run?.id));
      if (lastRunning !== undefined && running !== lastRunning) void client.invalidateQueries({ queryKey: ['runs'] });
      lastRunning = running;
      if (status === next) client.setQueryData(['status'], status);
    };

    const connect = () => {
      stream = new EventSource('./api/events');
      stream.addEventListener('status', onStatus);
      stream.onopen = () => {
        // Each open, also a reconnect of the browser, starts again.
        lastRuns = undefined;
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
