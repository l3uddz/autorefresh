import type { ItemDetail, MatchSearch, TimelineEvent } from './api';
import { actionText, fixedBy, titleYear } from './format';

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

/** Returns the outcome of a fix match decision and why. */
export function decisionView(event: TimelineEvent): EventView {
  switch (event.outcome) {
    case 'matched':
      return { title: 'Fix match: matched', tone: 'success', detail: confirmation(event) ?? 'The folder ID searches agreed on one candidate.' };
    case 'no-match':
      return {
        title: `Fix match: not matched${event.conflict ? ` · ${event.conflict}` : ''}`,
        tone: 'error',
        detail: (event.conflict && conflictText[event.conflict]) || 'No search found a candidate.',
      };
    case 'no-ids':
      return {
        tone: 'warning',
        title: 'Fix match: no folder IDs',
        detail: 'The folder and file names hold no tmdb, tvdb, or imdb ID, so there was nothing to search. Add one, such as {tmdb-12345}, to the folder name.',
      };
    case 'failed':
      return { title: 'Fix match: search failed', tone: 'error', detail: event.error };
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
  confirms: 'confirms the pick',
  'different candidate': 'another candidate',
  'not compared': 'not compared',
};

/** Returns the verdict of a candidate for people. Conflicts read as they
 * are, such as "year differs". */
export function verdictName(verdict?: string): string | undefined {
  return verdict ? verdictText[verdict] ?? verdict : undefined;
}
