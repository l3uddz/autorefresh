import type { Candidate, EpisodeDiff, ItemDetail, MatchSearch, TimelineEvent } from './api';
import { actionText, fixedBy, plural, titleYear } from './format';
import { compareYear } from './match';

// Facts of an item that come from its timeline, newest event first. The
// item detail of the API has no completion fields: a fixed item has no
// cached row, and its detail is the snapshot of its newest event.

/** What last happened to an item: Plex completed it (the Fixed tab lists
 * it), or Plex removed it. */
export type Disposition = { kind: 'fixed' | 'removed'; event: TimelineEvent };

/** Returns the disposition of an item from its newest completed or gone
 * event, so a later completion replaces an older removal and the other
 * way round. Requests and other events change nothing. A removal counts
 * only while the item is not cached: a later read added it back. */
export function disposition(detail: ItemDetail): Disposition | undefined {
  const event = detail.timeline.find(entry => entry.kind === 'completed' || entry.kind === 'gone');
  if (event?.kind === 'completed') return { kind: 'fixed', event };
  if (event?.kind === 'gone' && !detail.item.cached) return { kind: 'removed', event };
  return undefined;
}

export type CheckResult = { kind: 'gone' } | { kind: 'complete' } | { kind: 'missing'; reasons: string[] };

/** Returns the result of "Check now" from the detail that it answered. A
 * check reads a cached item. When Plex no longer has it, autorefresh
 * removes its row and records a gone event, so the item is not cached and
 * its newest event is gone. When the check completes it, the newest event
 * is completed. */
export function checkResult(detail: ItemDetail): CheckResult {
  const reasons = detail.item.reasons ?? [];
  if (!detail.item.cached && detail.timeline[0]?.kind === 'gone') return { kind: 'gone' };
  return reasons.length ? { kind: 'missing', reasons } : { kind: 'complete' };
}

// Presentation of timeline events. The texts use only fields of the API;
// error texts are the redacted texts of the API.

/** Returns the newest automatic fix match decision, which the Fix match
 * dialog explains. Manual searches record no decision. */
export function lastDecision(timeline: TimelineEvent[]): TimelineEvent | undefined {
  return timeline.find(event => event.kind === 'match-decision');
}

export type Tone = 'success' | 'error' | 'warning' | 'neutral';

export interface EventView {
  title: string;
  /** Who did it: autorefresh or a person. */
  by?: string;
  tone: Tone;
  detail?: string;
}

/** Returns "Refresh" or "TMDB match" for the start of a sentence. */
function actionTitle(action?: string): string {
  const text = actionText(action);
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function sourceName(source?: string): string | undefined {
  if (source === 'manual') return 'by hand';
  if (source === 'auto') return 'by autorefresh';
  return undefined;
}

/** Returns the title, tone, and detail of a timeline event. */
export function eventView(event: TimelineEvent): EventView {
  const by = sourceName(event.source);
  switch (event.kind) {
    case 'request': {
      const action = actionTitle(event.action);
      const target = event.match ? ` to ${event.match.title ? titleYear(event.match.title, event.match.year) : event.match.guid}` : '';
      return event.accepted
        ? { title: `${action} accepted${target}`, by, tone: 'success', detail: event.links?.length ? `Found by ${event.links.join(', ')}` : undefined }
        : { title: `${action} failed${target}`, by, tone: 'error', detail: event.error };
    }
    case 'match-decision':
      return { ...decisionView(event), by };
    case 'completed':
      return event.fixedAction || event.fixedMatch
        ? { title: 'Fixed', tone: 'success', detail: `By ${fixedBy(event.fixedAction, event.fixedMatch)}` }
        : { title: 'Completed by Plex on its own', tone: 'neutral' };
    case 'gone':
      return { title: 'Removed: Plex no longer had it', tone: 'warning', detail: event.error };
    case 'stopped':
      return { title: 'Stopped trying', by, tone: 'warning' };
    case 'resumed':
      return { title: 'Resumed', by, tone: 'neutral' };
    default:
      return { title: event.kind, by, tone: 'neutral' };
  }
}

const conflictText: Record<string, string> = {
  'several candidates': 'A search found several candidates, so autorefresh could not tell which one is right.',
  'candidate without guid': 'A search found a candidate without a Plex GUID, which Plex cannot apply.',
  'different candidates': 'The folder IDs found different items, so autorefresh could not tell which one is right.',
  'year differs': 'The candidate has another year than the folder or TMDB, and nothing confirmed it.',
};

/** Why a decision asked TMDB nothing, by its tmdbSkip. */
const tmdbSkipText: Record<string, string> = {
  off: 'TMDB was not asked, because no tmdb-token is set.',
  failed: 'TMDB was not asked, because an earlier TMDB failure in the same run turned it off.',
  'tmdb-id': 'The TMDB fallback did not run, because the folder names hold a tmdb ID, which Plex searched directly.',
  'no-source': 'The TMDB fallback did not run, because it translates only imdb IDs and the tvdb IDs of series.',
};

/** Returns what the TMDB steps of a decision did not do: the folder IDs
 * that TMDB has no single entry for (none, or several: the lookup does
 * not tell them apart), and why TMDB was not asked. */
function tmdbNotes(event: TimelineEvent): string[] {
  const notes: string[] = [];
  if (event.tmdbMissing?.length) notes.push(`TMDB has no single entry for ${event.tmdbMissing.join(', ')}.`);
  const skip = event.tmdbSkip && tmdbSkipText[event.tmdbSkip];
  if (skip) notes.push(skip);
  return notes;
}

/** Joins words as "a", "a and b", or "a, b, and c". */
function listText(words: string[]): string {
  if (words.length < 3) return words.join(' and ');
  return `${words.slice(0, -1).join(', ')}, and ${words[words.length - 1]}`;
}

/** Returns the verdict of a candidate of a decision about an item of the
 * year itemYear. A search can return the same candidate with another year
 * or none. Older decisions gave every row of a year conflict "year
 * differs"; a row without a year reads "not compared", and a row whose year
 * does not differ from the item year "year agrees", as newer decisions
 * store them. */
export function rowVerdict(candidate: Pick<Candidate, 'verdict' | 'year'>, itemYear: number | undefined): string | undefined {
  if (candidate.verdict !== 'year differs' || !itemYear) return candidate.verdict;
  if (!candidate.year) return 'not compared';
  return compareYear(itemYear, candidate.year).kind === 'differs' ? 'year differs' : 'year agrees';
}

/** Returns the year of a candidate that differs from the item year, which
 * caused the year conflict. A failed decision has no verdicts, so this
 * compares the years themselves. */
function conflictingYear(event: TimelineEvent, itemYear: number): number | undefined {
  for (const search of event.searches) {
    const candidate = search.candidates.find(c => compareYear(itemYear, c.year).kind === 'differs');
    if (candidate) return candidate.year;
  }
  return undefined;
}

/** Returns the years of the item, its folder, and TMDB that a decision
 * compared, such as ["the item has 2009", "TMDB has 1978"]. */
function comparedYears(event: TimelineEvent): string[] {
  const years = event.itemYear ? [`the item has ${event.itemYear}`] : [];
  if (event.pathYear) years.push(`the folder has ${event.pathYear}`);
  if (event.tmdbYear) years.push(`TMDB has ${event.tmdbYear}`);
  else if (event.tmdbId) years.push('TMDB has no year');
  return years;
}

/** Explains a year conflict with the years that it compared and the
 * episode comparison. A decision from before autorefresh kept these
 * values gets the general text. */
function yearConflict(event: TimelineEvent): string {
  const candidateYear = event.itemYear && conflictingYear(event, event.itemYear);
  if (!event.itemYear || !candidateYear) return conflictText['year differs'];
  const sentences = [`${listText([`Plex has ${candidateYear}`, ...comparedYears(event)])}.`];
  if (event.episodeDiff) sentences.push(episodeDiffText(event.episodeDiff, event.tmdbId));
  sentences.push('So nothing confirmed the candidate.');
  const text = sentences.join(' ');
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** The episode numbers that a text names at most. */
const shownNumbers = 10;

function numbersText(numbers: number[] | undefined): string | undefined {
  if (!numbers?.length) return undefined;
  const shown = numbers.slice(0, shownNumbers).join(', ');
  return numbers.length > shownNumbers ? `${shown}, and ${numbers.length - shownNumbers} more` : shown;
}

/** Explains an episode comparison that differs from the TMDB ID tmdbId:
 * the season, the counts of Plex and TMDB, and, when known, the episode
 * numbers that only one of them has. */
export function episodeDiffText(diff: EpisodeDiff, tmdbId?: string): string {
  const from = tmdbId ? ` from tmdb-${tmdbId}` : '';
  const counts = diff.plex === diff.tmdb
    ? `Plex and TMDB both have ${plural(diff.plex, 'episode')}, but with other numbers`
    : `Plex has ${plural(diff.plex, 'episode')}, and TMDB has ${diff.tmdb.toLocaleString()}`;
  const only = [
    numbersText(diff.missing) && `Only TMDB has episode ${numbersText(diff.missing)}.`,
    numbersText(diff.extra) && `Only Plex has episode ${numbersText(diff.extra)}.`,
  ].filter(Boolean);
  return [`The episode numbers differ${from} in season ${diff.season}: ${counts}.`, ...only].join(' ');
}

/** Returns the error of a failed decision, with the years that it compared
 * before the failure: the candidate year of a year conflict, and the years
 * of the item, its folder, and TMDB. */
function failedDetail(event: TimelineEvent): string | undefined {
  const candidateYear = event.conflict === 'year differs' && event.itemYear ? conflictingYear(event, event.itemYear) : undefined;
  const years = [...(candidateYear ? [`Plex has ${candidateYear}`] : []), ...comparedYears(event)];
  // The item year alone was not compared with anything.
  if (years.length < 2) return event.error;
  const compared = `Before the failure, it compared these years: ${listText(years)}.`;
  return [event.error, compared].filter(Boolean).join(' ');
}

/** Returns why a decision found no match. */
function noMatchDetail(event: TimelineEvent): string {
  if (event.conflict === 'year differs') return yearConflict(event);
  return (event.conflict && conflictText[event.conflict]) || 'No search found a candidate.';
}

/** Returns the outcome of a fix match decision and why, with what the
 * TMDB steps did not do. */
export function decisionView(event: TimelineEvent): EventView {
  const view = outcomeView(event);
  const notes = tmdbNotes(event);
  return notes.length ? { ...view, detail: [view.detail, ...notes].filter(Boolean).join(' ') } : view;
}

function outcomeView(event: TimelineEvent): EventView {
  switch (event.outcome) {
    case 'matched':
      return { title: 'Fix match: matched', tone: 'success', detail: confirmation(event) ?? 'The folder ID searches agreed on one candidate.' };
    case 'no-match':
      return {
        title: `Fix match: not matched${event.conflict ? ` · ${event.conflict}` : ''}`,
        tone: 'error',
        detail: noMatchDetail(event),
      };
    case 'no-ids':
      return {
        tone: 'warning',
        title: 'Fix match: no folder IDs',
        detail: 'The folder and file names hold no tmdb, tvdb, or imdb ID, so there was nothing to search. Add one, such as {tmdb-12345}, to the folder name.',
      };
    case 'failed':
      return { title: 'Fix match: search failed', tone: 'error', detail: failedDetail(event) };
    default:
      return { title: 'Fix match', tone: 'neutral' };
  }
}

/** Returns how the years of a decision confirmed its match, if they did. */
function confirmation(event: TimelineEvent): string | undefined {
  if (event.episodeCount) return `The episode numbers of ${event.episodeCount} episodes confirmed the candidate.`;
  if (event.tmdbYear) return `TMDB confirmed the year ${event.tmdbYear}.`;
  if (event.pathYear) return `The folder year ${event.pathYear} confirmed the candidate.`;
  return undefined;
}

/** Returns what a search of a decision or the dialog was. */
export function searchSource(search: MatchSearch): string {
  switch (search.source) {
    case 'path':
      return 'folder ID';
    case 'tmdb':
      return search.fromId ? `TMDB ID of ${search.fromId}` : 'TMDB ID';
    case 'tie':
      return 'TMDB year check';
    case 'title':
      return 'title';
    case 'id':
      return 'ID';
    default:
      return 'search';
  }
}

const verdictText: Record<string, string> = {
  picked: 'picked',
  'different candidate': 'another candidate',
  'not compared': 'not compared',
};

/** Returns the verdict of a candidate of the search for people. Conflicts
 * read as they are, such as "year differs". Only the tie search of the year
 * check confirms: Plex resolves its TMDB ID to the same candidate, which
 * says nothing about the year yet. */
export function verdictName(verdict: string | undefined, search: Pick<MatchSearch, 'query'>): string | undefined {
  if (verdict === 'confirms') return `same item as ${search.query}`;
  return verdict ? verdictText[verdict] ?? verdict : undefined;
}
