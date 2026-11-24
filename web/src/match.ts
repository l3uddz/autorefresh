import { queryOptions } from '@tanstack/react-query';
import { api, type Candidate, type CandidateSeries, type EpisodeCheck, type MatchSearch, type SeasonEpisodes, type TimelineEvent } from './api';
import { maxSearchBytes } from './bulk';
import { plural } from './format';
import { episodeDiffText, rowVerdict, verdictName } from './timeline';

// The evidence of the Fix match dialog: candidates grouped by Plex GUID,
// with the searches that found each one. Every Plex field stays text.

/** A search that found a candidate. */
export interface Evidence {
  query: string;
  source?: string;
  fromId?: string;
  /** The search was typed in the dialog, not part of the opening search. */
  isTyped: boolean;
  /** The year of a typed title search. */
  year?: number;
}

export interface CandidateGroup {
  /** A key that stays the same while the searches change: the GUID, or
   * the position of a candidate without one. */
  key: string;
  /** The Plex GUID, or empty for a candidate without one. */
  guid: string;
  candidate: Candidate;
  foundBy: Evidence[];
  /** The one TMDB ID that a search tied to this candidate, if exactly one
   * did. It wins over the TMDB ID of the metadata provider. */
  tmdbId?: string;
}

// The API takes a TMDB ID of at most 12 digits for the episode check.
const tmdbQuery = /^tmdb-(\d{1,12})$/;

// The sources whose query is an ID: a folder ID, the TMDB ID of one, or a
// typed ID search. A title search sends the title as it is, so a title
// such as tmdb-5 names no ID.
const idSources = new Set(['path', 'tmdb', 'id']);

/** Returns the TMDB ID of an ID search, such as 48213 of tmdb-48213. */
export function tmdbIdOf(search: Pick<MatchSearch, 'query' | 'source'>): string | undefined {
  return search.source && idSources.has(search.source) ? tmdbQuery.exec(search.query)?.[1] : undefined;
}

/** Reports whether the search ties its candidate to a TMDB ID: a search by
 * a TMDB ID that found exactly one candidate. A search with several
 * candidates cannot tell which one the ID names, as in the automatic fix
 * match. */
function tmdbTie(search: MatchSearch): string | undefined {
  return search.candidates.length === 1 ? tmdbIdOf(search) : undefined;
}

/** A typed search repeats an opening search when it sends the same title,
 * but it is still evidence of its own. */
function sameEvidence(a: Evidence, b: Evidence): boolean {
  return a.query === b.query && a.source === b.source && a.fromId === b.fromId && a.isTyped === b.isTyped && a.year === b.year;
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
 * folder IDs found, then the higher Plex score. A candidate ties to a TMDB
 * ID only when a search of that ID found it as the only candidate and no
 * search of that ID found another candidate.
 * typedYear is the year that the typed search sent, which its answer does
 * not carry. */
export function groupCandidates(opening: MatchSearch[], typed: MatchSearch[] = [], typedYear?: number): CandidateGroup[] {
  const groups = new Map<string, Builder>();
  const loose: Builder[] = [];
  // The GUIDs that the searches of each TMDB ID found. Searches that
  // disagree tie no candidate to the ID.
  const found = new Map<string, Set<string>>();
  let order = 0;
  const add = (search: MatchSearch, isTyped: boolean) => {
    const year = isTyped && search.source === 'title' ? typedYear : undefined;
    const evidence: Evidence = { query: search.query, source: search.source, fromId: search.fromId, isTyped, ...(year ? { year } : {}) };
    const tie = tmdbTie(search);
    const id = tmdbIdOf(search);
    if (id) found.set(id, new Set([...(found.get(id) ?? []), ...search.candidates.map(c => c.guid)]));
    for (const candidate of search.candidates) {
      let entry = candidate.guid ? groups.get(candidate.guid) : undefined;
      if (!entry) {
        const key = candidate.guid ? `guid:${candidate.guid}` : `loose:${order}`;
        entry = { group: { key, guid: candidate.guid, candidate: { ...candidate, verdict: undefined }, foundBy: [] }, tmdbIds: new Set(), order: order++ };
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
    .map(({ group, tmdbIds }) => {
      const [id] = tmdbIds;
      return tmdbIds.size === 1 && found.get(id)?.size === 1 ? { ...group, tmdbId: id } : group;
    });
}

/** Reports whether group is the current match of the item in Plex. */
export function isCurrentMatch(group: CandidateGroup, liveGuid: string | undefined): boolean {
  return Boolean(liveGuid && group.guid && group.guid === liveGuid);
}

/** Reports whether group is a series candidate of a series item. A
 * candidate without a type counts as a series, because the search of a
 * series item uses the series agent; a candidate of another type does not. */
function isSeriesCandidate(group: CandidateGroup, itemType: string | undefined): boolean {
  const type = group.candidate.type;
  return itemType === 'show' && (!type || type === 'show') && group.guid !== '';
}

// The plex:// GUID of a series, which the metadata provider describes.
const providerGuid = /^plex:\/\/show\/[0-9a-f]{24}$/;

/** Reports whether the dialog asks the metadata provider about group: a
 * series candidate of a series item with a plex:// series GUID, once the
 * live status names boot, the process of the service. Before that, the
 * cache key of the answer would change under the request. */
export function asksProvider(group: CandidateGroup, itemType: string | undefined, boot: string | undefined): boolean {
  return boot !== undefined && isSeriesCandidate(group, itemType) && providerGuid.test(group.guid);
}

/** Returns the TMDB ID of the episode check of group, if it offers one: a
 * series candidate with a TMDB ID, while autorefresh has a TMDB token. A
 * search that tied the candidate to an ID wins over providerId, the TMDB ID
 * of the metadata provider. */
export function episodeCheckId(group: CandidateGroup, itemType: string | undefined, isTMDBOn: boolean, providerId?: string): string | undefined {
  return isTMDBOn && isSeriesCandidate(group, itemType) ? group.tmdbId ?? providerId : undefined;
}

/** The series of a candidate from the metadata provider, asked with the
 * token of the server of the item params. boot is the process of the
 * service: a restart can renumber the servers, so the cache starts again. The answer depends on the server
 * and the GUID only, so items share it. The provider is a remote service,
 * so each GUID is asked once while the page is open: no refetch, no retry,
 * also not when the dialog opens again after a failure, and the answer
 * stays in the cache.
 * The request takes no abort signal, so closing the dialog lets it finish
 * into the cache instead of starting it again on the next open. Its key is
 * outside the item prefix. */
export function candidateQuery(params: string, guid: string, boot: string | undefined) {
  const srv = new URLSearchParams(params).get('srv') ?? '';
  return queryOptions({
    queryKey: ['candidate', boot, srv, guid],
    queryFn: () => api<CandidateSeries>(`candidate?${new URLSearchParams({ srv, guid })}`),
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
    retryOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
}

export type EpisodeCountCheck = { kind: 'same' | 'differs'; episodes: number; yours: number } | { kind: 'unknown' };

/** Compares the episode count of a candidate with the count that Plex has
 * for the item. Only an absent count is unknown; 0 is a count. */
export function compareEpisodeCount(itemEpisodes: number | undefined, candidateEpisodes: number | undefined): EpisodeCountCheck {
  if (itemEpisodes === undefined || candidateEpisodes === undefined) return { kind: 'unknown' };
  return { kind: itemEpisodes === candidateEpisodes ? 'same' : 'differs', episodes: candidateEpisodes, yours: itemEpisodes };
}

export type YearCheck = { kind: 'same' | 'off' | 'differs'; year: number; diff: number } | { kind: 'unknown' };

/** Compares the year of a candidate with the year of the item. One year
 * off is common for releases near the new year. */
export function compareYear(itemYear: number | undefined, candidateYear: number | undefined): YearCheck {
  if (!itemYear || !candidateYear) return { kind: 'unknown' };
  const diff = Math.abs(itemYear - candidateYear);
  return { kind: diff === 0 ? 'same' : diff === 1 ? 'off' : 'differs', year: candidateYear, diff };
}

/** What an automatic fix match, the last one or a preview, did with a
 * candidate: the searches that found it and the verdict of each. */
export interface AutoEvidence {
  query: string;
  verdict?: string;
}

/** Returns the searches of the decision that found the candidate guid. */
export function autoEvidence(decision: TimelineEvent | undefined, guid: string): AutoEvidence[] {
  if (!decision || !guid) return [];
  return decision.searches.flatMap(search =>
    search.candidates.filter(c => c.guid === guid).map(c => ({ query: search.query, verdict: rowVerdict(c, decision.itemYear) })),
  );
}

/** Returns the folder ID searches of a preview decision, the kinds of
 * search that the opening search also makes, and the pick of its title
 * search. Grouped with the opening searches, they give the pick of the
 * preview a card, also when the opening search of its folder ID failed or,
 * without the year of the preview, did not return it. A tie search only
 * confirms a pick that a folder ID search found. */
export function previewSearches(decision: TimelineEvent | undefined): MatchSearch[] {
  return decision?.searches.flatMap(search => {
    if (search.source === 'path' || search.source === 'tmdb') return [search];
    const picked = search.source === 'title' ? search.candidates.filter(c => c.verdict === 'picked') : [];
    return picked.length ? [{ ...search, candidates: picked }] : [];
  }) ?? [];
}

/** Returns the line of a candidate for one search of the last automatic
 * fix match, or of a preview. */
export function autoEvidenceText(evidence: AutoEvidence, isPreview: boolean): string {
  const by = isPreview ? 'The automatic fix match preview' : 'The last automatic fix match';
  const verdict = verdictName(evidence.verdict, evidence);
  return `${by} found it via ${evidence.query}${verdict ? `: ${verdict}` : ''}`;
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

export type SearchInput = { error: string } | TypedSearch;

/** Checks a typed search before it is sent. The API runs the opening
 * search for an empty text, so a typed search needs text. The year is
 * optional: a positive number of at most four digits. */
export function checkSearch(text: string, year: string): SearchInput {
  const q = text.trim();
  if (!q) return { error: 'Enter a title or an ID.' };
  if (new TextEncoder().encode(q).length > maxSearchBytes) return { error: `Use at most ${maxSearchBytes} bytes.` };
  if (isIDSearch(q)) return { q };
  const y = year.trim();
  if (!y) return { q };
  if (!/^\d{1,4}$/.test(y) || Number(y) < 1) return { error: 'The year must be a number from 1 to 9999.' };
  return { q, year: Number(y) };
}

export interface TypedSearch {
  q: string;
  year?: number;
}

/** Reports whether two typed searches send the same request. */
export function sameSearch(a: TypedSearch | undefined, b: TypedSearch): boolean {
  return a !== undefined && a.q === b.q && a.year === b.year;
}

export type ListView = 'loading' | 'empty' | 'list' | 'none';

/** Chooses what the candidate list shows: the candidates whenever any
 * search found some, placeholders while a search runs, and "no
 * candidates" only after a search answered. A failed search shows its own
 * banner. */
export function listView(count: number, isSearching: boolean, hasAnswer: boolean): ListView {
  if (count > 0) return 'list';
  if (isSearching) return 'loading';
  return hasAnswer ? 'empty' : 'none';
}

/** Returns the query of GET /api/item/matches for the item params and an
 * optional typed search. */
export function matchesPath(params: string, search?: TypedSearch): string {
  const query = new URLSearchParams(params);
  if (search) {
    query.set('q', search.q);
    if (search.year) query.set('year', String(search.year));
  }
  return `item/matches?${query}`;
}

/** Returns the query of GET /api/item/auto-match, the automatic fix match
 * preview of the item params. */
export function autoMatchPath(params: string): string {
  return `item/auto-match?${new URLSearchParams(params)}`;
}

/** The automatic fix match preview of the item params. It calls Plex and
 * maybe TMDB, so it runs only on request (refetch), never by itself: not
 * on mount, focus, or a reconnect, and with no retry. Its answer stays in
 * the cache while the page is open, so the Fix match dialog shows it again
 * when it reopens for the item. boot is the process of the service: a
 * restart can renumber the servers, so params can name another item, and
 * the cache starts again. Its key is outside the item prefix, which
 * invalidateLists refreshes. */
export function previewQuery(params: string, boot: string | undefined) {
  return queryOptions({
    queryKey: ['auto-match', boot, params],
    queryFn: ({ signal }) => api<TimelineEvent>(autoMatchPath(params), { signal }),
    enabled: false,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
}

/** Returns the newer of the preview and the last automatic decision, so a
 * run that decided after the preview replaces it. Both times have whole
 * seconds, so a decision of the same second as the preview cannot be
 * ordered; the recorded decision then wins. */
export function newerDecision(preview: TimelineEvent | undefined, last: TimelineEvent | undefined): TimelineEvent | undefined {
  if (!preview || !last) return preview ?? last;
  return Date.parse(last.at ?? '') >= Date.parse(preview.at ?? '') ? last : preview;
}

/** The body of POST /api/item/match: exactly the fields that the API
 * takes, which rejects any other. */
export function matchBody(candidate: Candidate): { guid: string; name: string; year: number } {
  return { guid: candidate.guid, name: candidate.name ?? '', year: candidate.year ?? 0 };
}

/** The place of one episode number in a season comparison: both Plex and
 * TMDB have it, only TMDB has it (missing in Plex), or only Plex has it. */
export type EpisodePlace = 'both' | 'missing' | 'extra';

export interface SeasonView {
  season: number;
  plex: number;
  tmdb: number;
  episodes: { number: number; place: EpisodePlace }[];
  /** The numbers that Plex lists more than once. */
  duplicates: number[];
}

/** Returns the episode numbers of a compared season in order, each once,
 * with the side that has it, and the numbers that Plex lists twice. */
export function seasonView(season: SeasonEpisodes): SeasonView {
  const plex = new Set(season.plex);
  const tmdb = new Set(season.tmdb);
  const numbers = [...new Set([...plex, ...tmdb])].sort((a, b) => a - b);
  const place = (n: number): EpisodePlace => (!tmdb.has(n) ? 'extra' : plex.has(n) ? 'both' : 'missing');
  const duplicates = [...new Set(season.plex.filter((n, i) => season.plex.indexOf(n) !== i))];
  return {
    season: season.season,
    plex: season.plex.length,
    tmdb: season.tmdb.length,
    episodes: numbers.map(number => ({ number, place: place(number) })),
    duplicates,
  };
}

/** Returns the result of the episode check of the TMDB ID tmdbId. */
export function episodeCheckText(check: EpisodeCheck, tmdbId: string): string {
  if (check.match) {
    return `The episode numbers of ${listingName(check, tmdbId)} confirm it (${plural(check.episodes, 'episode')} compared).`;
  }
  if (check.diff) return episodeDiffText(check.diff, tmdbId);
  // The API does not say why: Plex listed no regular episode, an entry
  // lacked a file or a number, or the listing changed while it was read.
  return `The episodes of this item could not be compared with tmdb-${tmdbId}: the episode listing of Plex is empty, incomplete, or has an entry without a file or number.`;
}

/** Names the TMDB listing that the seasons of the check come from: the
 * confirming episode group, or else the TMDB seasons. */
function listingName(check: EpisodeCheck, tmdbId: string): string {
  return check.episodeGroup ? `the TMDB episode group “${check.episodeGroup}” of tmdb-${tmdbId}` : `the TMDB seasons of tmdb-${tmdbId}`;
}

/** Returns the notes of the comparison: the listing it used, the episode
 * groups it also read, and where it stopped. */
export function comparisonNotes(check: EpisodeCheck, tmdbId: string): string[] {
  const notes = [`Compared with ${listingName(check, tmdbId)}.`];
  if (check.episodeGroup) notes.push('The TMDB seasons did not confirm it, so the check read the TVDB-aligned episode groups of TMDB.');
  // The confirming group ends the check, so it is the last group read.
  // Other groups can have the same name.
  const read = check.episodeGroups ?? [];
  const others = check.episodeGroup ? read.slice(0, -1) : read;
  if (others.length) notes.push(`Episode groups that did not confirm it: ${others.map(name => `“${name}”`).join(', ')}.`);
  const last = check.seasons?.at(-1);
  if (check.stopped && last) notes.push(`The check stopped at season ${last.season}, so it lists no later season of Plex.`);
  return notes;
}
