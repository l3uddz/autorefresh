import { describe, expect, it } from 'vitest';
import type { Candidate, EpisodeCheck, MatchSearch, TimelineEvent } from './api';
import { autoEvidence, autoEvidenceText, autoMatchPath, previewSearches, checkSearch, compareYear, groupCandidates, isCurrentMatch, isIDSearch, listView, matchBody, matchesPath, asksProvider, candidateQuery, compareEpisodeCount, episodeCheckId, sameSearch, seasonView, tmdbIdOf, episodeCheckText, comparisonNotes } from './match';

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

  it('gives the pick of a preview a card when the opening folder search failed', () => {
    const preview = { id: 0, kind: 'match-decision', accepted: false, searches: [
      search('tmdb-1', [cand('plex://a', { verdict: 'picked' })], { source: 'path' }),
      search('tmdb-7', [cand('plex://a', { verdict: 'confirms' })], { source: 'tie' }),
    ] } satisfies TimelineEvent;
    const opening = [search('Title', [cand('plex://b')], { source: 'title' }), search('tmdb-1', [], { source: 'path', failed: true })];
    expect(previewSearches(preview).map(s => s.source)).toEqual(['path']);
    expect(previewSearches(undefined)).toEqual([]);
    const groups = groupCandidates([...opening, ...previewSearches(preview)]);
    expect(groups.map(g => g.guid)).toEqual(['plex://a', 'plex://b']);
    expect(groups[0].foundBy).toEqual([{ query: 'tmdb-1', source: 'path', fromId: undefined, isTyped: false }]);
  });

  it('gives the pick of a preview title search a card that the opening search did not return', () => {
    const preview = { id: 0, kind: 'match-decision', accepted: false, searches: [
      search('Show', [cand('plex://a', { verdict: 'picked' }), cand('plex://o', { verdict: 'other title' })], { source: 'title' }),
    ] } satisfies TimelineEvent;
    expect(previewSearches(preview).map(s => s.candidates.map(c => c.guid))).toEqual([['plex://a']]);
    const groups = groupCandidates([search('Show', [cand('plex://o')], { source: 'title' }), ...previewSearches(preview)]);
    expect(groups.map(g => g.guid).sort()).toEqual(['plex://a', 'plex://o']);
    const unmatched = { ...preview, searches: [search('Show', [cand('plex://a', { verdict: 'not confirmed' })], { source: 'title' })] };
    expect(previewSearches(unmatched)).toEqual([]);
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

  it('ties no candidate to a TMDB ID whose searches found different candidates', () => {
    const groups = groupCandidates(
      [search('tmdb-48213', [cand('plex://a')], { source: 'path' }), search('tmdb-48213', [cand('plex://b')], { source: 'path' })],
      [search('tmdb-9', [cand('plex://c')], { source: 'id' })],
    );
    expect(groups.map(g => [g.guid, g.tmdbId])).toEqual([['plex://c', '9'], ['plex://a', undefined], ['plex://b', undefined]]);
  });

  it('ties a typed TMDB ID search too', () => {
    const [group] = groupCandidates([], [search('tmdb-77', [cand('plex://x')], { source: 'id' })]);
    expect(group.tmdbId).toBe('77');
  });

  it('does not tie a title search whose title looks like a TMDB ID', () => {
    const [group] = groupCandidates([search('tmdb-5', [cand('plex://x', { type: 'show' })], { source: 'title' })]);
    expect(group.tmdbId).toBeUndefined();
    expect(episodeCheckId(group, 'show', true)).toBeUndefined();
  });

  it('keeps typed evidence and its rank when the typed search repeats the opening title', () => {
    const opening = [
      search('The Quiet Harbor', [cand('plex://wrong', { score: 64 }), cand('plex://folder', { score: 98 })], { source: 'title' }),
      search('tmdb-1', [cand('plex://folder')], { source: 'path' }),
    ];
    const typed = [search('The Quiet Harbor', [cand('plex://wrong')], { source: 'title' })];
    const groups = groupCandidates(opening, typed, 2019);
    expect(groups.map(g => g.guid)).toEqual(['plex://wrong', 'plex://folder']);
    expect(groups[0].foundBy).toEqual([
      { query: 'The Quiet Harbor', source: 'title', fromId: undefined, isTyped: false },
      { query: 'The Quiet Harbor', source: 'title', fromId: undefined, isTyped: true, year: 2019 },
    ]);
  });

  it('gives the typed year only to a typed title search', () => {
    const [group] = groupCandidates([], [search('tmdb-7', [cand('plex://x')], { source: 'id' })], 2019);
    expect(group.foundBy[0]).not.toHaveProperty('year');
  });

  it('gives every group a key that survives other searches', () => {
    const opening = [search('x', [cand(''), cand('plex://a'), cand('')], { source: 'title' })];
    const keys = groupCandidates(opening).map(g => g.key);
    expect(new Set(keys).size).toBe(3);
    const typed = [search('y', [cand(''), cand('plex://b', { score: 99 })], { source: 'title' })];
    const after = groupCandidates(opening, typed).map(g => g.key);
    expect(new Set(after).size).toBe(5);
    for (const key of keys) expect(after).toContain(key);
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

  it('offers the episode check for a series candidate with a TMDB ID while TMDB is on', () => {
    const [show] = groupCandidates([search('tmdb-1', [cand('plex://s', { type: 'show' })], { source: 'path' })]);
    expect(episodeCheckId(show, 'show', true)).toBe('1');
    expect(episodeCheckId(show, 'show', true, '9')).toBe('1');
    expect(episodeCheckId(show, 'show', false)).toBeUndefined();
    expect(episodeCheckId(show, 'movie', true)).toBeUndefined();
    const [movie] = groupCandidates([search('tmdb-1', [cand('plex://m', { type: 'movie' })], { source: 'path' })]);
    expect(episodeCheckId(movie, 'show', true)).toBeUndefined();
    const [untyped] = groupCandidates([search('tmdb-1', [cand('plex://u')], { source: 'path' })]);
    expect(episodeCheckId(untyped, 'show', true)).toBe('1');
    const [untied] = groupCandidates([search('Title', [cand('plex://s', { type: 'show' })])]);
    expect(episodeCheckId(untied, 'show', true)).toBeUndefined();
    // The metadata provider gives a title search candidate its TMDB ID.
    expect(episodeCheckId(untied, 'show', true, '9')).toBe('9');
  });

  it('asks the metadata provider only about plex:// series candidates of a series', () => {
    const group = (guid: string, type?: string) => groupCandidates([search('Title', [cand(guid, { type })])])[0];
    expect(asksProvider(group('plex://show/5d9c086c46115600200aa2fe', 'show'), 'show', 'b1')).toBe(true);
    expect(asksProvider(group('plex://show/5d9c086c46115600200aa2fe'), 'show', 'b1')).toBe(true);
    expect(asksProvider(group('plex://show/5d9c086c46115600200aa2fe', 'show'), 'movie', 'b1')).toBe(false);
    expect(asksProvider(group('plex://movie/5d9c086c46115600200aa2fe', 'movie'), 'show', 'b1')).toBe(false);
    expect(asksProvider(group('com.plexapp.agents.thetvdb://5', 'show'), 'show', 'b1')).toBe(false);
    // Before the status names the process, the dialog asks nothing.
    expect(asksProvider(group('plex://show/5d9c086c46115600200aa2fe', 'show'), 'show', undefined)).toBe(false);
    const query = candidateQuery('srv=1&key=7', 'plex://show/5d9c086c46115600200aa2fe', 'b1');
    expect(query.queryKey).toEqual(['candidate', 'b1', '1', 'plex://show/5d9c086c46115600200aa2fe']);
  });

  it('compares the episode counts of a candidate and the item', () => {
    expect(compareEpisodeCount(10, 10)).toEqual({ kind: 'same', episodes: 10, yours: 10 });
    expect(compareEpisodeCount(10, 12)).toEqual({ kind: 'differs', episodes: 12, yours: 10 });
    expect(compareEpisodeCount(undefined, 12)).toEqual({ kind: 'unknown' });
    expect(compareEpisodeCount(10, undefined)).toEqual({ kind: 'unknown' });
    expect(compareEpisodeCount(10, 0)).toEqual({ kind: 'differs', episodes: 0, yours: 10 });
  });

  it('reads TMDB IDs and ID searches like the API', () => {
    for (const source of ['path', 'tmdb', 'id']) expect(tmdbIdOf({ query: 'tmdb-48213', source })).toBe('48213');
    expect(tmdbIdOf({ query: 'tmdb-123456789012', source: 'id' })).toBe('123456789012');
    for (const query of ['tvdb-48213', 'tmdb-48213x', 'TMDB-48213', 'tmdb-1234567890123']) expect(tmdbIdOf({ query, source: 'id' })).toBeUndefined();
    // A title search sends the item title as it is, so it names no ID.
    expect(tmdbIdOf({ query: 'tmdb-5', source: 'title' })).toBeUndefined();
    expect(tmdbIdOf({ query: 'tmdb-5' })).toBeUndefined();
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

  it('names the last decision or the preview in the automatic evidence', () => {
    expect(autoEvidenceText({ query: 'tmdb-1', verdict: 'picked' }, false)).toBe('The last automatic fix match found it via tmdb-1: picked');
    expect(autoEvidenceText({ query: 'tmdb-1', verdict: 'picked' }, true)).toBe('The automatic fix match preview found it via tmdb-1: picked');
    expect(autoEvidenceText({ query: 'imdb-tt2' }, true)).toBe('The automatic fix match preview found it via imdb-tt2');
    expect(autoEvidenceText({ query: 'tmdb-7', verdict: 'confirms' }, false)).toBe('The last automatic fix match found it via tmdb-7: same item as tmdb-7');
  });
});

describe('dialog state', () => {
  it('treats only the same text and year as the same search', () => {
    expect(sameSearch({ q: 'A', year: 2019 }, { q: 'A', year: 2019 })).toBe(true);
    expect(sameSearch({ q: 'A' }, { q: 'A', year: undefined })).toBe(true);
    expect(sameSearch({ q: 'A', year: 2019 }, { q: 'A' })).toBe(false);
    expect(sameSearch({ q: 'A' }, { q: 'B' })).toBe(false);
    expect(sameSearch(undefined, { q: 'A' })).toBe(false);
  });

  it('shows candidates whenever any search found some', () => {
    expect(listView(2, true, false)).toBe('list');
    expect(listView(0, true, false)).toBe('loading');
    expect(listView(0, true, true)).toBe('loading');
    expect(listView(0, false, true)).toBe('empty');
    expect(listView(0, false, false)).toBe('none');
  });
});

describe('checkSearch', () => {
  it('requires text and keeps the byte limit of the API', () => {
    expect(checkSearch('   ', '')).toEqual({ error: 'Enter a title or an ID.', field: 'q' });
    expect(checkSearch('x'.repeat(200), '')).toEqual({ q: 'x'.repeat(200) });
    expect(checkSearch('é'.repeat(101), '')).toEqual({ error: 'Use at most 200 bytes.', field: 'q' });
    expect(checkSearch('  Harbor  ', '')).toEqual({ q: 'Harbor' });
  });

  it('takes an optional positive year of at most four digits', () => {
    expect(checkSearch('Harbor', '2019')).toEqual({ q: 'Harbor', year: 2019 });
    expect(checkSearch('Harbor', '1')).toEqual({ q: 'Harbor', year: 1 });
    expect(checkSearch('Harbor', '9999')).toEqual({ q: 'Harbor', year: 9999 });
    for (const year of ['0', '0000', '10000', '-1', '19.5', '2e3', 'abcd']) {
      expect(checkSearch('Harbor', year)).toEqual({ error: 'The year must be a number from 1 to 9999.', field: 'year' });
    }
  });

  it('drops the year of an ID search, which the API ignores', () => {
    for (const year of ['2019', '0', 'abc']) expect(checkSearch('tmdb-1', year)).toEqual({ q: 'tmdb-1' });
  });

  it('builds the request paths and the exact match body', () => {
    expect(matchesPath('srv=0&key=7')).toBe('item/matches?srv=0&key=7');
    expect(autoMatchPath('srv=0&key=7')).toBe('item/auto-match?srv=0&key=7');
    expect(matchesPath('srv=0&key=7', { q: 'A & B', year: 2019 })).toBe('item/matches?srv=0&key=7&q=A+%26+B&year=2019');
    expect(matchBody(cand('plex://a', { name: 'A', year: 2019, summary: 'S', score: 9, verdict: 'picked' }))).toEqual({ guid: 'plex://a', name: 'A', year: 2019 });
    expect(matchBody(cand('plex://a'))).toEqual({ guid: 'plex://a', name: '', year: 0 });
  });
});

describe('seasonView', () => {
  it('lists each number once in order with the side that has it', () => {
    expect(seasonView({ season: 2, plex: [1, 2, 5], tmdb: [1, 2, 3, 4] })).toEqual({
      season: 2,
      plex: 3,
      tmdb: 4,
      episodes: [
        { number: 1, place: 'both' },
        { number: 2, place: 'both' },
        { number: 3, place: 'missing' },
        { number: 4, place: 'missing' },
        { number: 5, place: 'extra' },
      ],
      duplicates: [],
    });
  });

  it('keeps a number that Plex lists twice once, and names it', () => {
    const view = seasonView({ season: 1, plex: [1, 1, 1, 2], tmdb: [1] });
    expect(view.episodes).toEqual([{ number: 1, place: 'both' }, { number: 2, place: 'extra' }]);
    expect(view.duplicates).toEqual([1]);
    expect(view.plex).toBe(4);
  });

  it('shows every TMDB number as missing when Plex lists none', () => {
    expect(seasonView({ season: 1, plex: [], tmdb: [1, 2] }).episodes.map(e => e.place)).toEqual(['missing', 'missing']);
  });
});

describe('episode check texts', () => {
  const check = (change: Partial<EpisodeCheck>): EpisodeCheck => ({ match: false, episodes: 0, seasons: [], ...change });
  const seasons = [{ season: 1, plex: [1, 2], tmdb: [1, 2] }];

  it('names the TMDB seasons that confirmed the series', () => {
    const answer = check({ match: true, episodes: 4, seasons });
    expect(episodeCheckText(answer, '156686')).toBe('The episode numbers of the TMDB seasons of tmdb-156686 confirm it (4 episodes compared).');
    expect(comparisonNotes(answer, '156686')).toEqual(['Compared with the TMDB seasons of tmdb-156686.']);
  });

  it('names the confirming episode group and the groups that did not confirm it', () => {
    const answer = check({ match: true, episodes: 2, seasons, episodeGroup: 'DVD Order', episodeGroups: ['Aired Order', 'DVD Order'] });
    expect(episodeCheckText(answer, '9')).toBe('The episode numbers of the TMDB episode group “DVD Order” of tmdb-9 confirm it (2 episodes compared).');
    expect(comparisonNotes(answer, '9')).toEqual([
      'Compared with the TMDB episode group “DVD Order” of tmdb-9.',
      'The TMDB seasons did not confirm it, so the check read the TVDB-aligned episode groups of TMDB.',
      'Episode groups that did not confirm it: “Aired Order”.',
    ]);
  });

  it('shows the summary of an answer without seasons from an older server', () => {
    const answer: EpisodeCheck = { match: true, episodes: 4 };
    expect(episodeCheckText(answer, '9')).toBe('The episode numbers of the TMDB seasons of tmdb-9 confirm it (4 episodes compared).');
    expect(comparisonNotes(answer, '9')).toEqual(['Compared with the TMDB seasons of tmdb-9.']);
  });

  it('keeps a group that did not confirm it but has the name of the confirming one', () => {
    const answer = check({ match: true, episodes: 2, seasons, episodeGroup: 'DVD Order', episodeGroups: ['DVD Order', 'DVD Order'] });
    expect(comparisonNotes(answer, '9')).toContain('Episode groups that did not confirm it: “DVD Order”.');
  });

  it('says where a check that differs stopped', () => {
    const answer = check({ seasons: [{ season: 1, plex: [1, 5], tmdb: [1, 2] }], stopped: true, diff: { season: 1, plex: 2, tmdb: 2, missing: [2], extra: [5] } });
    expect(episodeCheckText(answer, '9')).toBe('The episode numbers differ from tmdb-9 in season 1: Plex and TMDB both have 2 episodes, but with other numbers. Only TMDB has episode 2. Only Plex has episode 5.');
    expect(comparisonNotes(answer, '9')).toEqual([
      'Compared with the TMDB seasons of tmdb-9.',
      'The check stopped at season 1, so it lists no later season of Plex.',
    ]);
  });
});
