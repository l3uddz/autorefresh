import type { TabName } from './route';

// Types of the JSON API in internal/web. Times are RFC 3339 strings, and
// an omitted time is undefined.

export type ItemState = 'due' | 'waiting' | 'too-new' | 'ignored' | 'manual' | 'stopped';

export type TimelineEventKind = 'request' | 'completed' | 'gone' | 'stopped' | 'resumed' | 'match-decision';

export type RunKind = 'run' | 'bulk-refresh';

export type RunSource = 'schedule' | 'manual';

export type RunStatus = 'running' | 'finished' | 'failed' | 'canceled' | 'interrupted';

export type RunItemOutcome = 'accepted' | 'failed' | 'gone' | 'stopped' | 'changed' | 'not-cached' | 'not-reached';

export type Health = 'unknown' | 'ok' | 'unreachable' | 'unauthorized';

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
  state?: ItemState;
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
  kind: TimelineEventKind;
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
  /** The year of the item when autorefresh decided, on a match decision. */
  itemYear?: number;
  /** The TMDB ID whose year and episodes the year check compared. */
  tmdbId?: string;
  /** Why the decision asked TMDB nothing: off, failed, tmdb-id, or no-source. */
  tmdbSkip?: string;
  /** The folder IDs that TMDB has no single entry for. */
  tmdbMissing?: string[];
  /** The alternate TMDB episode group that confirmed a series, if any. */
  episodeGroup?: string;
  /** The first season whose episode numbers differ from TMDB. */
  episodeDiff?: EpisodeDiff;
}

/** The first season whose regular episode numbers differ from TMDB, with
 * the episode counts of Plex and TMDB. The episode check also names the
 * episode numbers that only TMDB (missing) or only Plex (extra) has. */
export interface EpisodeDiff {
  season: number;
  plex: number;
  tmdb: number;
  missing?: number[];
  extra?: number[];
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

/** What the Plex metadata provider tells about a series candidate. */
export interface CandidateSeries {
  tmdbId?: string;
  seasons?: number;
  episodes?: number;
}

/** The answer of the episode check of a candidate. Seasons are the seasons
 * that the deciding comparison read: those of the confirming episode group,
 * or else the TMDB seasons up to the one that differs. Stopped is true when
 * that season ended the check before the later seasons of Plex.
 * EpisodeGroups names each TMDB episode group whose seasons the check read. */
export interface EpisodeCheck {
  match: boolean;
  episodes: number;
  episodeGroup?: string;
  diff?: EpisodeDiff;
  /** Absent in the answer of a server from before the comparison. */
  seasons?: SeasonEpisodes[];
  stopped?: boolean;
  episodeGroups?: string[];
}

/** The sorted regular episode numbers of one season in Plex and in TMDB. */
export interface SeasonEpisodes {
  season: number;
  plex: number[];
  tmdb: number[];
}

export interface ItemDetail {
  item: ItemRecord;
  timeline: TimelineEvent[];
}

/** The answer of POST /api/item/refresh: the item detail after the
 * refresh, the fix match that the refresh applied, if any, and whether a
 * check after the request saved the current reasons of the item. Plex
 * accepted the refresh, so the answer leaves out a detail that autorefresh
 * failed to read. */
export interface RefreshAnswer extends Partial<ItemDetail> {
  match?: TimelineEvent['match'];
  checked: boolean;
}

export interface LiveItem {
  summary?: string;
  guid?: string;
  seasons?: number;
  episodes?: number;
  locations: string[];
  files: string[];
  path?: string;
  /** The external IDs that Plex lists, such as `tmdb://100`. */
  externalIds: string[];
  /** The wrong match check of a matched movie. */
  mismatch?: WrongMatch;
}

export type WrongMatchResult = 'mismatched' | 'agrees' | 'undecided' | 'no-path-ids';

export interface WrongMatch {
  result: WrongMatchResult;
  /** The compared sources, in the order tmdb, imdb, tvdb. */
  sources: ComparedSource[];
}

export interface ComparedSource {
  source: string;
  /** The ID that the file path names. */
  path: string;
  /** The IDs of the source that Plex lists. */
  plex: string[];
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
  kind: RunKind;
  source: RunSource;
  status: RunStatus;
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
  /** The item in work. */
  current?: NowItem;
  /** The item after the pace wait, which ends at nextAt. */
  next?: NowItem;
  nextAt?: string;
  /** The last finished item, with its outcome and finish time. */
  last?: NowItem;
}

/** An item of a run in the live status. thumb is the poster proxy URL. */
export interface NowItem {
  key: string;
  title?: string;
  year?: number;
  type?: string;
  library?: string;
  reasons?: string[];
  thumb?: string;
  outcome?: string;
  finishedAt?: string;
}

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
  kind: RunKind;
  source: RunSource;
  status: RunStatus;
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
  outcome: RunItemOutcome | (string & {});
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
