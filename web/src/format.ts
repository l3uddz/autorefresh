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

/** Returns the last action of an item, such as "refresh · no match". */
export function lastAction(outcome?: string, matchOutcome?: string): string {
  const parts = [outcome && (outcomeText[outcome] ?? label(outcome))];
  if (matchOutcome && matchOutcome !== 'matched') parts.push(matchText[matchOutcome] ?? label(matchOutcome));
  return parts.filter(Boolean).join(' · ') || 'none yet';
}

/** Returns what fixed an item, from its completion attribution. */
export function fixedBy(action?: string, match?: string): string {
  if (match) return `${outcomeText[match] ?? label(match)}${action && action !== match ? ` · then ${outcomeText[action] ?? label(action)}` : ''}`;
  return action ? outcomeText[action] ?? label(action) : 'Plex on its own';
}
