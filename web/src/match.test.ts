import { describe, expect, it } from 'vitest';
import type { Candidate, MatchSearch, TimelineEvent } from './api';
import { autoEvidence, checkSearch, compareYear, groupCandidates, isCurrentMatch, isIDSearch, matchBody, matchesPath, offersEpisodeCheck, tmdbIdOf } from './match';

const cand = (guid: string, change: Partial<Candidate> = {}): Candidate => ({ guid, score: 0, ...change });
const search = (query: string, candidates: Candidate[], change: Partial<MatchSearch> = {}): MatchSearch => ({ query, failed: false, candidates, ...change });

describe('groupCandidates', () => {
  it('groups by GUID, keeps every search that found one, and fills missing fields', () => {
    const groups = groupCandidates([
      search('The Quiet Harbor', [cand('plex://a', { name: 'A', score: 80 }), cand('plex://b', { name: 'B', score: 91 })], { source: 'title' }),
      search('tmdb-48213', [cand('plex://a', { summary: 'From the ID search', score: 98 })], { source: 'path' }),
      search('imdb-tt1', [cand('plex://a')], { source: 'path' }),
    ]);
    expect(groups.map(g => g.guid)).toEqual(['plex://a', 'plex://b']);
    expect(groups[0].candidate).toMatchObject({ name: 'A', summary: 'From the ID search', score: 98 });
    expect(groups[0].foundBy.map(e => e.query)).toEqual(['The Quiet Harbor', 'tmdb-48213', 'imdb-tt1']);
  });

  it('keeps candidates without a GUID apart and drops decision verdicts', () => {
    const groups = groupCandidates([search('x', [cand('', { name: 'One', verdict: 'picked' }), cand('', { name: 'Two' })])]);
    expect(groups).toHaveLength(2);
    expect(groups.map(g => g.candidate.verdict)).toEqual([undefined, undefined]);
  });

  it('puts candidates of the typed search first, then folder evidence, then score', () => {
    const opening = [
      search('Title', [cand('plex://high', { score: 99 }), cand('plex://folder', { score: 50 })], { source: 'title' }),
      search('tvdb-1', [cand('plex://folder')], { source: 'path' }),
    ];
    expect(groupCandidates(opening).map(g => g.guid)).toEqual(['plex://folder', 'plex://high']);
    const typed = [search('Other', [cand('plex://typed', { score: 10 })], { source: 'title' })];
    const groups = groupCandidates(opening, typed);
    expect(groups.map(g => g.guid)).toEqual(['plex://typed', 'plex://folder', 'plex://high']);
    expect(groups[0].foundBy).toEqual([{ query: 'Other', source: 'title', fromId: undefined, isTyped: true }]);
  });

  it('ties a candidate to a TMDB ID only through an unambiguous TMDB search', () => {
    const groups = groupCandidates([
      search('tmdb-10', [cand('plex://one')], { source: 'tmdb', fromId: 'tvdb-5' }),
      search('tmdb-20', [cand('plex://two'), cand('plex://three')], { source: 'path' }),
      search('tmdb-30', [cand('plex://four')], { source: 'path' }),
      search('tmdb-31', [cand('plex://four')], { source: 'tmdb', fromId: 'imdb-tt9' }),
      search('Title', [cand('plex://five')], { source: 'title' }),
    ]);
    const tmdb = Object.fromEntries(groups.map(g => [g.guid, g.tmdbId]));
    expect(tmdb).toEqual({ 'plex://one': '10', 'plex://two': undefined, 'plex://three': undefined, 'plex://four': undefined, 'plex://five': undefined });
  });

  it('ties a typed TMDB ID search too', () => {
    const [group] = groupCandidates([], [search('tmdb-77', [cand('plex://x')], { source: 'id' })]);
    expect(group.tmdbId).toBe('77');
  });
});

describe('match helpers', () => {
  it('detects the current match only for equal non-empty GUIDs', () => {
    const [group] = groupCandidates([search('x', [cand('plex://a')])]);
    expect(isCurrentMatch(group, 'plex://a')).toBe(true);
    expect(isCurrentMatch(group, 'plex://b')).toBe(false);
    expect(isCurrentMatch(group, undefined)).toBe(false);
    const [loose] = groupCandidates([search('x', [cand('')])]);
    expect(isCurrentMatch(loose, '')).toBe(false);
  });

  it('offers the episode check only for a tied series candidate of a series with TMDB on', () => {
    const [show] = groupCandidates([search('tmdb-1', [cand('plex://s', { type: 'show' })])]);
    expect(offersEpisodeCheck(show, 'show', true)).toBe(true);
    expect(offersEpisodeCheck(show, 'show', false)).toBe(false);
    expect(offersEpisodeCheck(show, 'movie', true)).toBe(false);
    const [movie] = groupCandidates([search('tmdb-1', [cand('plex://m', { type: 'movie' })])]);
    expect(offersEpisodeCheck(movie, 'show', true)).toBe(false);
    const [untyped] = groupCandidates([search('tmdb-1', [cand('plex://u')])]);
    expect(offersEpisodeCheck(untyped, 'show', true)).toBe(true);
    const [untied] = groupCandidates([search('Title', [cand('plex://s', { type: 'show' })])]);
    expect(offersEpisodeCheck(untied, 'show', true)).toBe(false);
  });

  it('reads TMDB IDs and ID searches like the API', () => {
    expect(tmdbIdOf('tmdb-48213')).toBe('48213');
    expect(tmdbIdOf('tvdb-48213')).toBeUndefined();
    expect(tmdbIdOf('tmdb-48213x')).toBeUndefined();
    for (const id of ['tmdb-1', 'TVDB-2', 'imdb-tt3', 'tt4', 'tmdbid=5', ' tt6 ']) expect(isIDSearch(id)).toBe(true);
    for (const text of ['tmdb-x', 'imdb-123', 'tt', 'The Quiet Harbor', 'tmdb5']) expect(isIDSearch(text)).toBe(false);
  });

  it('compares years', () => {
    expect(compareYear(2019, 2019)).toEqual({ kind: 'same', year: 2019, diff: 0 });
    expect(compareYear(2019, 2018)).toEqual({ kind: 'off', year: 2018, diff: 1 });
    expect(compareYear(2019, 1987)).toEqual({ kind: 'differs', year: 1987, diff: 32 });
    expect(compareYear(undefined, 2019)).toEqual({ kind: 'unknown' });
  });

  it('finds the automatic evidence of a candidate in the last decision', () => {
    const decision = { id: 1, kind: 'match-decision', accepted: false, searches: [
      search('tmdb-1', [cand('plex://a', { verdict: 'different candidates' })]),
      search('imdb-tt2', [cand('plex://b', { verdict: 'different candidates' })]),
    ] } satisfies TimelineEvent;
    expect(autoEvidence(decision, 'plex://b')).toEqual([{ query: 'imdb-tt2', verdict: 'different candidates' }]);
    expect(autoEvidence(decision, '')).toEqual([]);
    expect(autoEvidence(undefined, 'plex://a')).toEqual([]);
  });
});

describe('checkSearch', () => {
  it('requires text and keeps the byte limit of the API', () => {
    expect(checkSearch('   ', '')).toEqual({ error: 'Enter a title or an ID.' });
    expect(checkSearch('x'.repeat(200), '')).toEqual({ q: 'x'.repeat(200) });
    expect(checkSearch('é'.repeat(101), '')).toHaveProperty('error');
    expect(checkSearch('  Harbor  ', '')).toEqual({ q: 'Harbor' });
  });

  it('takes an optional positive year of at most four digits', () => {
    expect(checkSearch('Harbor', '2019')).toEqual({ q: 'Harbor', year: 2019 });
    expect(checkSearch('Harbor', '1')).toEqual({ q: 'Harbor', year: 1 });
    expect(checkSearch('Harbor', '9999')).toEqual({ q: 'Harbor', year: 9999 });
    for (const year of ['0', '0000', '10000', '-1', '19.5', '2e3', 'abcd']) expect(checkSearch('Harbor', year)).toHaveProperty('error');
  });

  it('drops the year of an ID search, which the API ignores', () => {
    expect(checkSearch('tmdb-1', '2019')).toEqual({ q: 'tmdb-1' });
  });

  it('builds the request paths and the exact match body', () => {
    expect(matchesPath('srv=0&key=7')).toBe('item/matches?srv=0&key=7');
    expect(matchesPath('srv=0&key=7', { q: 'A & B', year: 2019 })).toBe('item/matches?srv=0&key=7&q=A+%26+B&year=2019');
    expect(matchBody(cand('plex://a', { name: 'A', year: 2019, summary: 'S', score: 9, verdict: 'picked' }))).toEqual({ guid: 'plex://a', name: 'A', year: 2019 });
    expect(matchBody(cand('plex://a'))).toEqual({ guid: 'plex://a', name: '', year: 0 });
  });
});
