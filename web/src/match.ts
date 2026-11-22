import type { Candidate, MatchSearch, TimelineEvent } from './api';
import { maxSearchBytes } from './bulk';

// The evidence of the Fix match dialog: candidates grouped by Plex GUID,
// with the searches that found each one. Every Plex field stays text.

/** A search that found a candidate. */
export interface Evidence {
  query: string;
  source?: string;
  fromId?: string;
  /** The search was typed in the dialog, not part of the opening search. */
  isTyped: boolean;
}

export interface CandidateGroup {
  /** The Plex GUID, or empty for a candidate without one. */
  guid: string;
  candidate: Candidate;
  foundBy: Evidence[];
  /** The one TMDB ID that a search tied to this candidate, if exactly one
   * did. Only such a candidate offers the episode check. */
  tmdbId?: string;
}

const tmdbQuery = /^tmdb-(\d+)$/;

/** Returns the TMDB ID of the search query, such as 48213 of tmdb-48213. */
export function tmdbIdOf(query: string): string | undefined {
  return tmdbQuery.exec(query)?.[1];
}

/** Reports whether the search ties its candidate to a TMDB ID: a search by
 * a TMDB ID that found exactly one candidate. A search with several
 * candidates cannot tell which one the ID names, as in the automatic fix
 * match. */
function tmdbTie(search: MatchSearch): string | undefined {
  return search.candidates.length === 1 ? tmdbIdOf(search.query) : undefined;
}

function sameEvidence(a: Evidence, b: Evidence): boolean {
  return a.query === b.query && a.source === b.source && a.fromId === b.fromId;
}

/** Fills the empty fields of into from more, so a candidate that one search
 * returned without a summary or poster shows the ones of another. */
function merge(into: Candidate, more: Candidate): Candidate {
  return {
    ...into,
    name: into.name || more.name,
    year: into.year || more.year,
    type: into.type || more.type,
    summary: into.summary || more.summary,
    thumb: into.thumb || more.thumb,
    score: Math.max(into.score, more.score),
  };
}

/** Reports whether the evidence is a folder ID search: a path ID, or the
 * TMDB ID of one. */
export function isFolderEvidence(evidence: Evidence): boolean {
  return !evidence.isTyped && (evidence.source === 'path' || evidence.source === 'tmdb');
}

interface Builder {
  group: CandidateGroup;
  tmdbIds: Set<string>;
  order: number;
}

/** Groups the candidates of the opening and the typed searches by GUID.
 * Candidates without a GUID stay apart, because Plex cannot apply them.
 * Groups that the typed search found come first, then those that more
 * folder IDs found, then the higher Plex score. */
export function groupCandidates(opening: MatchSearch[], typed: MatchSearch[] = []): CandidateGroup[] {
  const groups = new Map<string, Builder>();
  const loose: Builder[] = [];
  let order = 0;
  const add = (search: MatchSearch, isTyped: boolean) => {
    const evidence: Evidence = { query: search.query, source: search.source, fromId: search.fromId, isTyped };
    const tie = tmdbTie(search);
    for (const candidate of search.candidates) {
      let entry = candidate.guid ? groups.get(candidate.guid) : undefined;
      if (!entry) {
        entry = { group: { guid: candidate.guid, candidate: { ...candidate, verdict: undefined }, foundBy: [] }, tmdbIds: new Set(), order: order++ };
        if (candidate.guid) groups.set(candidate.guid, entry);
        else loose.push(entry);
      } else {
        entry.group.candidate = merge(entry.group.candidate, candidate);
      }
      if (!entry.group.foundBy.some(known => sameEvidence(known, evidence))) entry.group.foundBy.push(evidence);
      if (tie && candidate.guid) entry.tmdbIds.add(tie);
    }
  };
  opening.forEach(search => add(search, false));
  typed.forEach(search => add(search, true));
  const rank = ({ group }: Builder) => [
    group.foundBy.some(e => e.isTyped) ? 1 : 0,
    group.foundBy.filter(isFolderEvidence).length,
    group.candidate.score,
  ];
  return [...groups.values(), ...loose]
    .sort((a, b) => {
      const [x, y] = [rank(a), rank(b)];
      return y[0] - x[0] || y[1] - x[1] || y[2] - x[2] || a.order - b.order;
    })
    .map(({ group, tmdbIds }) => (tmdbIds.size === 1 ? { ...group, tmdbId: [...tmdbIds][0] } : group));
}

/** Reports whether group is the current match of the item in Plex. */
export function isCurrentMatch(group: CandidateGroup, liveGuid: string | undefined): boolean {
  return Boolean(liveGuid && group.guid && group.guid === liveGuid);
}

/** Reports whether group offers the TMDB episode check: a candidate of a
 * series, tied to one TMDB ID, while autorefresh has a TMDB token. A
 * candidate without a type counts as a series, because the search of a
 * series item uses the series agent; a candidate of another type does not. */
export function offersEpisodeCheck(group: CandidateGroup, itemType: string | undefined, isTMDBOn: boolean): boolean {
  const type = group.candidate.type;
  return isTMDBOn && itemType === 'show' && (!type || type === 'show') && group.guid !== '' && group.tmdbId !== undefined;
}

export type YearCheck = { kind: 'same' | 'off' | 'differs'; year: number; diff: number } | { kind: 'unknown' };

/** Compares the year of a candidate with the year of the item. One year
 * off is common for releases near the new year. */
export function compareYear(itemYear: number | undefined, candidateYear: number | undefined): YearCheck {
  if (!itemYear || !candidateYear) return { kind: 'unknown' };
  const diff = Math.abs(itemYear - candidateYear);
  return { kind: diff === 0 ? 'same' : diff === 1 ? 'off' : 'differs', year: candidateYear, diff };
}

/** What the last automatic fix match did with a candidate: the searches
 * that found it and the verdict of each. */
export interface AutoEvidence {
  query: string;
  verdict?: string;
}

/** Returns the searches of the decision that found the candidate guid. */
export function autoEvidence(decision: TimelineEvent | undefined, guid: string): AutoEvidence[] {
  if (!decision || !guid) return [];
  return decision.searches.flatMap(search =>
    search.candidates.filter(c => c.guid === guid).map(c => ({ query: search.query, verdict: c.verdict })),
  );
}

const imdbValue = /^tt\d+$/i;
const idPattern = /^(imdb|tmdb|tvdb)(?:id)?[^a-z0-9]+(.+)$/i;

/** Reports whether text is an ID search such as tmdb-48213 or tt7712040,
 * by the rules of the API. An ID search ignores the year. */
export function isIDSearch(text: string): boolean {
  const value = text.trim();
  if (imdbValue.test(value)) return true;
  const match = idPattern.exec(value);
  if (!match) return false;
  return match[1].toLowerCase() === 'imdb' ? imdbValue.test(match[2]) : /^\d+$/.test(match[2]);
}

export type SearchInput = { error: string } | { q: string; year?: number };

/** Checks a typed search before it is sent. The API runs the opening
 * search for an empty text, so a typed search needs text. The year is
 * optional: a positive number of at most four digits. */
export function checkSearch(text: string, year: string): SearchInput {
  const q = text.trim();
  if (!q) return { error: 'Enter a title or an ID.' };
  if (new TextEncoder().encode(q).length > maxSearchBytes) return { error: `Use at most ${maxSearchBytes} bytes.` };
  const y = year.trim();
  if (!y) return { q };
  if (!/^\d{1,4}$/.test(y) || Number(y) < 1) return { error: 'The year must be a number from 1 to 9999.' };
  return isIDSearch(q) ? { q } : { q, year: Number(y) };
}

/** Returns the query of GET /api/item/matches for the item params and an
 * optional typed search. */
export function matchesPath(params: string, search?: { q: string; year?: number }): string {
  const query = new URLSearchParams(params);
  if (search) {
    query.set('q', search.q);
    if (search.year) query.set('year', String(search.year));
  }
  return `item/matches?${query}`;
}

/** The body of POST /api/item/match: exactly the fields that the API
 * takes, which rejects any other. */
export function matchBody(candidate: Candidate): { guid: string; name: string; year: number } {
  return { guid: candidate.guid, name: candidate.name ?? '', year: candidate.year ?? 0 };
}
