// Text helpers shared by the pages. They take the current time as an
// argument, so tests do not depend on the clock.

const units: [limit: number, size: number, suffix: string][] = [
  [60, 1, 's'],
  [3600, 60, 'm'],
  [86400, 3600, 'h'],
  [Infinity, 86400, 'd'],
];

/** Returns a short duration such as "45s", "12m", "3h", or "2d 4h". */
export function duration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  const [, size, suffix] = units.find(([limit]) => s < limit)!;
  const whole = Math.floor(s / size);
  if (suffix === 'd') {
    const hours = Math.floor((s % 86400) / 3600);
    return hours ? `${whole}d ${hours}h` : `${whole}d`;
  }
  return `${whole}${suffix}`;
}

/** Returns "in 2d 4h", "3h ago", or "just now" for an ISO time. */
export function relative(value: string | undefined, now: number): string {
  if (!value) return '—';
  const diff = (Date.parse(value) - now) / 1000;
  if (Math.abs(diff) < 30) return 'just now';
  return diff > 0 ? `in ${duration(diff)}` : `${duration(-diff)} ago`;
}

/** Returns when the next scheduled run starts, as "in 45m · 23:00", or
 * "starting" once that time has come. A scheduled start that waits for a
 * busy run stays "starting" until the status changes. */
export function nextRunText(value: string | undefined, now: number): string | undefined {
  if (!value) return undefined;
  const diff = (Date.parse(value) - now) / 1000;
  if (diff <= 0) return 'starting';
  return `in ${duration(diff)} · ${new Date(value).toLocaleTimeString(undefined, { timeStyle: 'short' })}`;
}

/** Returns when the next full cache refresh is due, as "in 4d 4h · Sun
 * 3:00 AM", or "with the next run" once it is due: the first run at or
 * after that time reads the libraries fully. isDue is the server's word that
 * the time has come, which wins over the page clock. */
export function fullReadText(value: string | undefined, isDue: boolean, now: number): string | undefined {
  if (!value) return undefined;
  const diff = (Date.parse(value) - now) / 1000;
  if (isDue || Math.round(diff) <= 0) return 'with the next run';
  return `in ${duration(diff)} · ${new Date(value).toLocaleString(undefined, { weekday: 'short', hour: 'numeric', minute: '2-digit' })}`;
}

/** Returns the kind of a run: a bulk refresh, or a run named by how it read
 * the libraries. A run from before the read counts is just a run. */
export function runKind(run: { kind: string; fullReads?: number; incrementalReads?: number }): string {
  if (run.kind === 'bulk-refresh') return 'Bulk refresh';
  const full = (run.fullReads ?? 0) > 0;
  const incremental = (run.incrementalReads ?? 0) > 0;
  if (full && incremental) return 'Full and incremental run';
  if (full) return 'Full run';
  if (incremental) return 'Incremental run';
  return 'Run';
}

/** Returns the time of value as a short local date and time. */
export function dateTime(value?: string): string {
  if (!value) return '—';
  return new Date(value).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

/** Returns the duration of a run, or "—" while it has no end. */
export function runDuration(startedAt?: string, finishedAt?: string): string {
  if (!startedAt || !finishedAt) return '—';
  return duration((Date.parse(finishedAt) - Date.parse(startedAt)) / 1000);
}

/** Turns an API name such as "no-summary" into "no summary". */
export function label(value?: string): string {
  return value ? value.replaceAll('-', ' ') : '—';
}

/** Returns the name of a Plex item type for people. */
export function typeName(type?: string): string {
  return type === 'show' ? 'Series' : type === 'movie' ? 'Movie' : label(type);
}

/** Returns "1 item" or "3 items". */
export function plural(count: number, noun: string, nouns = `${noun}s`): string {
  return `${count.toLocaleString()} ${count === 1 ? noun : nouns}`;
}

/** Returns the queue state of an attention item for people. */
export function queueState(state: string | undefined, nextDue: string | undefined, now: number): string {
  switch (state) {
    case 'due':
      return 'Due';
    case 'waiting':
      return `Waiting · ${relative(nextDue, now)}`;
    case 'too-new':
      return `Too new · ${relative(nextDue, now)}`;
    case 'ignored':
      return 'Ignored reasons';
    case 'stopped':
      return 'Stopped';
    default:
      return '—';
  }
}

const outcomeText: Record<string, string> = {
  refresh: 'refresh',
  match: 'match',
  'tmdb-match': 'TMDB match',
  'refresh-failed': 'refresh failed',
  'match-failed': 'match failed',
  'tmdb-match-failed': 'TMDB match failed',
  'search-failed': 'search failed',
};

const matchText: Record<string, string> = {
  matched: 'matched',
  'no-match': 'no match',
  'no-ids': 'no folder IDs',
  failed: 'match search failed',
};

/** Returns the name of a request action, such as "TMDB match". */
export function actionText(action?: string): string {
  return action ? outcomeText[action] ?? label(action) : 'request';
}

/** Returns "Title (2019)", or the title alone without a year. */
export function titleYear(title: string, year?: number): string {
  return year ? `${title} (${year})` : title;
}

/** Returns the last action of an item, such as "refresh · no match". */
export function lastAction(outcome?: string, matchOutcome?: string): string {
  const parts = [outcome && actionText(outcome)];
  if (matchOutcome && matchOutcome !== 'matched') parts.push(matchText[matchOutcome] ?? label(matchOutcome));
  return parts.filter(Boolean).join(' · ') || 'none yet';
}

/** Returns what fixed an item, from its completion attribution. */
export function fixedBy(action?: string, match?: string): string {
  if (match) return `${actionText(match)}${action && action !== match ? ` · then ${actionText(action)}` : ''}`;
  return action ? actionText(action) : 'Plex on its own';
}
