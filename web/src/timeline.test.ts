import { describe, expect, it } from 'vitest';
import type { ItemDetail, ItemRecord, TimelineEvent } from './api';
import { checkResult, decisionView, disposition, episodeDiffText, eventView, lastDecision, rowVerdict, searchSource, verdictName } from './timeline';

const item = (change: Partial<ItemRecord> = {}): ItemRecord => ({ srv: 0, key: '7', cached: false, attempts: 0, refreshes: 0, ...change });
const event = (id: number, kind: string, change: Partial<TimelineEvent> = {}): TimelineEvent => ({ id, kind, accepted: false, at: `2026-11-${10 + id}T00:00:00Z`, searches: [], ...change });

describe('disposition', () => {
  const detail = (timeline: TimelineEvent[], change: Partial<ItemRecord> = {}): ItemDetail => ({ item: item(change), timeline });

  it('takes the fixed details from the newest completed event', () => {
    // Newest first: a later request, then the newest completion, then an
    // older completion by Plex alone.
    const timeline = [
      event(4, 'request', { action: 'refresh', accepted: true }),
      event(3, 'completed', { fixedAction: 'refresh', fixedMatch: 'match' }),
      event(1, 'completed'),
    ];
    expect(disposition(detail(timeline))).toMatchObject({ kind: 'fixed', event: { id: 3, fixedAction: 'refresh', fixedMatch: 'match' } });
    expect(disposition(detail([event(1, 'request')]))).toBeUndefined();
  });

  it('shows a removal only while it is the newest completed or gone event', () => {
    // Plex removed the item, a scan added it back, and a check completed it.
    expect(disposition(detail([event(3, 'completed'), event(2, 'request'), event(1, 'gone')]))).toMatchObject({ kind: 'fixed', event: { id: 3 } });
    // A later request does not hide the removal.
    expect(disposition(detail([event(3, 'request'), event(2, 'gone'), event(1, 'completed')]))).toMatchObject({ kind: 'removed', event: { id: 2 } });
  });

  it('names no removal of a cached item, but keeps its fix', () => {
    expect(disposition(detail([event(2, 'gone'), event(1, 'completed')], { cached: true }))).toBeUndefined();
    // The Fixed tab lists an item by its newest completion, also when a
    // later read added it back.
    expect(disposition(detail([event(2, 'completed')], { cached: true }))).toMatchObject({ kind: 'fixed' });
  });
});

describe('check result', () => {
  const detail = (change: Partial<ItemRecord>, timeline: TimelineEvent[]): ItemDetail => ({ item: item(change), timeline });

  it('reports an item that Plex no longer has as gone, not complete', () => {
    expect(checkResult(detail({}, [event(2, 'gone'), event(1, 'request')]))).toEqual({ kind: 'gone' });
  });

  it('reports a completed item as complete and an incomplete one with its reasons', () => {
    expect(checkResult(detail({}, [event(2, 'completed')]))).toEqual({ kind: 'complete' });
    expect(checkResult(detail({ cached: true, reasons: ['no-art'] }, [event(1, 'request')]))).toEqual({ kind: 'missing', reasons: ['no-art'] });
  });
});

describe('event views', () => {
  it('names manual and scheduled requests with their outcome', () => {
    expect(eventView(event(1, 'request', { source: 'manual', action: 'match', accepted: true, match: { guid: 'plex://a', title: 'A', year: 2019 } }))).toEqual({ title: 'Match accepted to A (2019)', by: 'by hand', tone: 'success', detail: undefined });
    expect(eventView(event(1, 'request', { source: 'auto', action: 'refresh', error: 'Plex unavailable' }))).toEqual({ title: 'Refresh failed', by: 'by autorefresh', tone: 'error', detail: 'Plex unavailable' });
    expect(eventView(event(1, 'request', { source: 'auto', action: 'tmdb-match', accepted: true, links: ['tmdb-5'], match: { guid: 'plex://b' } })).detail).toBe('Found by tmdb-5');
  });

  it('names completed and removed items', () => {
    expect(eventView(event(1, 'completed', { fixedAction: 'refresh', fixedMatch: 'match' }))).toMatchObject({ title: 'Fixed', detail: 'By match · then refresh' });
    expect(eventView(event(1, 'completed', { fixedAction: 'tmdb-match', fixedMatch: 'tmdb-match' })).detail).toBe('By TMDB match');
    expect(eventView(event(1, 'completed'))).toMatchObject({ title: 'Completed by Plex on its own', tone: 'neutral' });
    expect(eventView(event(1, 'gone'))).toMatchObject({ title: 'Removed: Plex no longer had it', tone: 'warning' });
  });

  it('explains each decision outcome', () => {
    expect(decisionView(event(1, 'match-decision', { outcome: 'no-match', conflict: 'different candidates' }))).toMatchObject({ title: 'Fix match: not matched · different candidates', tone: 'error' });
    expect(decisionView(event(1, 'match-decision', { outcome: 'matched', tmdbYear: 2019 })).detail).toBe('TMDB confirmed the year 2019.');
    expect(decisionView(event(1, 'match-decision', { outcome: 'matched' })).detail).toBe('The folder ID searches agreed on one candidate.');
    expect(decisionView(event(1, 'match-decision', { outcome: 'no-ids' })).title).toBe('Fix match: no folder IDs');
    expect(decisionView(event(1, 'match-decision', { outcome: 'failed', error: 'TMDB unavailable' })).detail).toBe('TMDB unavailable');
  });

  it('finds the newest decision, or none', () => {
    const decision = event(2, 'match-decision', { outcome: 'no-match', searches: [
      { query: 'tmdb-1', source: 'path', failed: false, candidates: [{ guid: 'plex://a', score: 0, verdict: 'different candidates' }] },
      { query: 'imdb-tt2', source: 'path', failed: true, candidates: [] },
      { query: 'tmdb-3', source: 'tmdb', fromId: 'imdb-tt2', failed: false, candidates: [{ guid: 'plex://b', score: 0, verdict: 'different candidates' }, { guid: 'plex://c', score: 0 }] },
    ] });
    expect(lastDecision([event(3, 'request'), decision, event(1, 'match-decision')])).toBe(decision);
    expect(lastDecision([event(3, 'request')])).toBeUndefined();
    expect(decision.searches.map(searchSource)).toEqual(['folder ID', 'folder ID', 'TMDB ID of imdb-tt2']);
    expect(verdictName('confirms', { query: 'tmdb-16817' })).toBe('same item as tmdb-16817');
    expect(verdictName('year differs', { query: 'tvdb-1' })).toBe('year differs');
    expect(verdictName(undefined, { query: 'tvdb-1' })).toBeUndefined();
  });
});

describe('decision evidence', () => {
  const beechgrove = (fields: Partial<TimelineEvent>) => event(1, 'match-decision', {
    outcome: 'no-match',
    conflict: 'year differs',
    itemYear: 2009,
    searches: [
      { query: 'tvdb-248824', source: 'path', failed: false, candidates: [{ guid: 'plex://show/b', year: 1997, score: 0, verdict: 'year differs' }] },
      { query: 'tmdb-16817', source: 'tie', failed: false, candidates: [{ guid: 'plex://show/b', year: 1997, score: 0, verdict: 'confirms' }] },
    ],
    ...fields,
  });

  it('lists the compared years and the episode comparison of a year conflict', () => {
    expect(decisionView(beechgrove({ tmdbId: '16817', tmdbYear: 1978, episodeDiff: { season: 3, plex: 8, tmdb: 10 } })).detail).toBe(
      'Plex has 1997, the item has 2009, and TMDB has 1978. The episode numbers differ from tmdb-16817 in season 3: Plex has 8 episodes, and TMDB has 10. So nothing confirmed the candidate.',
    );
    expect(decisionView(beechgrove({ pathYear: 2010, tmdbId: '16817' })).detail).toBe(
      'Plex has 1997, the item has 2009, the folder has 2010, and TMDB has no year. So nothing confirmed the candidate.',
    );
    expect(decisionView(beechgrove({ tmdbSkip: 'off' })).detail).toBe(
      'Plex has 1997 and the item has 2009. So nothing confirmed the candidate. TMDB was not asked, because no tmdb-token is set.',
    );
  });

  it('names the candidate year that differs when a search found the candidate with another year', () => {
    const twoYears = beechgrove({ searches: [
      { query: 'tvdb-248824', source: 'path', failed: false, candidates: [{ guid: 'plex://show/b', year: 2009, score: 0, verdict: 'year differs' }] },
      { query: 'imdb-tt1', source: 'path', failed: false, candidates: [{ guid: 'plex://show/b', year: 1997, score: 0, verdict: 'year differs' }] },
    ] });
    expect(decisionView(twoYears).detail).toBe('Plex has 1997 and the item has 2009. So nothing confirmed the candidate.');
  });

  it('judges each row of a year conflict by its own year', () => {
    expect(rowVerdict({ verdict: 'year differs', year: 2009 }, 2009)).toBe('year agrees');
    expect(rowVerdict({ verdict: 'year differs', year: 2010 }, 2009)).toBe('year agrees');
    expect(rowVerdict({ verdict: 'year differs', year: 1997 }, 2009)).toBe('year differs');
    expect(rowVerdict({ verdict: 'year differs', year: 2009 }, undefined)).toBe('year differs');
    expect(rowVerdict({ verdict: 'picked', year: 1997 }, 2009)).toBe('picked');
    expect(rowVerdict({ verdict: 'year differs' }, 2009)).toBe('not compared');
    expect(rowVerdict({ verdict: 'year differs' }, undefined)).toBe('not compared');
  });

  it('shows the years that a failed decision compared', () => {
    const failed = (fields: Partial<TimelineEvent>) => decisionView(event(1, 'match-decision', { outcome: 'failed', error: 'tmdb lookup: tmdb-77: 500', ...fields })).detail;
    expect(failed({ itemYear: 2022, pathYear: 2022, tmdbYear: 2017, tmdbId: '77' })).toBe(
      'tmdb lookup: tmdb-77: 500 Before the failure, it compared these years: the item has 2022, the folder has 2022, and TMDB has 2017.',
    );
    expect(failed({ itemYear: 2022 })).toBe('tmdb lookup: tmdb-77: 500');
    // A failure after a year conflict names the candidate year that started the check.
    const conflict = { itemYear: 2009, conflict: 'year differs', searches: [
      { query: 'tvdb-5', source: 'path', failed: false, candidates: [{ guid: 'plex://show/b', year: 1997, score: 0 }] },
    ] };
    expect(failed(conflict)).toBe('tmdb lookup: tmdb-77: 500 Before the failure, it compared these years: Plex has 1997 and the item has 2009.');
    expect(failed({ ...conflict, conflict: undefined })).toBe('tmdb lookup: tmdb-77: 500');
  });

  it('shows a stored TMDB year without the TMDB ID of older decisions', () => {
    expect(decisionView(beechgrove({ tmdbYear: 1978 })).detail).toBe('Plex has 1997, the item has 2009, and TMDB has 1978. So nothing confirmed the candidate.');
  });

  it('keeps the general text of a year conflict without the item year', () => {
    expect(decisionView(beechgrove({ itemYear: undefined })).detail).toBe('The candidate has another year than the folder or TMDB, and nothing confirmed it.');
  });

  it('says what the TMDB fallback did not do', () => {
    const none = (fields: Partial<TimelineEvent>) => decisionView(event(1, 'match-decision', { outcome: 'no-match', ...fields })).detail;
    expect(none({ tmdbMissing: ['tvdb-357542'] })).toBe('No search found a candidate. TMDB has no single entry for tvdb-357542.');
    expect(none({ tmdbSkip: 'failed' })).toBe('No search found a candidate. TMDB was not asked, because an earlier TMDB failure in the same run turned it off.');
    expect(none({ tmdbSkip: 'tmdb-id' })).toContain('the folder names hold a tmdb ID');
    expect(none({ tmdbSkip: 'no-source' })).toContain('translates only imdb IDs and the tvdb IDs of series');
    expect(none({})).toBe('No search found a candidate.');
    const outcome = (fields: Partial<TimelineEvent>) => decisionView(event(1, 'match-decision', fields)).detail;
    expect(outcome({ outcome: 'matched', tmdbMissing: ['imdb-tt1'] })).toBe('The folder ID searches agreed on one candidate. TMDB has no single entry for imdb-tt1.');
    expect(outcome({ outcome: 'failed', error: 'tmdb lookup: tvdb-6: 500', tmdbMissing: ['tvdb-5'] })).toBe('tmdb lookup: tvdb-6: 500 TMDB has no single entry for tvdb-5.');
    expect(outcome({ outcome: 'failed', tmdbMissing: ['tvdb-5'] })).toBe('TMDB has no single entry for tvdb-5.');
  });

  it('explains the decisions of a title search', () => {
    const titleSearch = { query: 'Show', source: 'title', failed: false, candidates: [] };
    const title = (fields: Partial<TimelineEvent>) => event(1, 'match-decision', { searches: [titleSearch], itemYear: 2020, ...fields });
    expect(decisionView(title({ outcome: 'matched', episodeCount: 6, tmdbId: '88' })).detail).toBe(
      'The title search found one series with the same title and a year within 1, and the episode numbers of 6 episodes from tmdb-88 confirmed it.',
    );
    expect(decisionView(title({ outcome: 'no-match', conflict: 'not confirmed', tmdbId: '88', episodeDiff: { season: 1, plex: 3, tmdb: 2, extra: [3] } })).detail).toBe(
      'The title search found one series with the same title and a year within 1, but nothing confirmed it. The episode numbers differ from tmdb-88 in season 1: Plex has 3 episodes, and TMDB has 2. Only Plex has episode 3.',
    );
    expect(decisionView(title({ outcome: 'no-match', conflict: 'not confirmed' })).detail).toBe(
      'The title search found one series with the same title and a year within 1, but nothing confirmed it. The Plex metadata provider has no TMDB ID for it.',
    );
    expect(decisionView(title({ outcome: 'no-match', conflict: 'folder id differs', tmdbId: '88' })).detail).toBe(
      'The Plex metadata provider gives tmdb-88 for the series that the title search found, but the folder names give another TMDB ID, or an ID that TMDB has several entries for.',
    );
    const otherTitle = { ...titleSearch, candidates: [{ guid: 'plex://show/o', year: 2015, score: 0, verdict: 'other title' }] };
    expect(decisionView(title({ outcome: 'no-match', searches: [otherTitle] })).detail).toBe(
      'No result of the title search had the title of the item and a year within 1 of the year of the item.',
    );
    expect(decisionView(title({ outcome: 'no-match', searches: [otherTitle], pathYear: 2012 })).detail).toBe(
      'No result of the title search had the title of the item and a year within 1 of the year 2020 of the item and 2012 of its folder.',
    );
    expect(decisionView(title({ outcome: 'no-match' })).detail).toBe('No search found a candidate.');
    expect(decisionView(title({ outcome: 'failed', error: 'tmdb lookup: 500', tmdbId: '88' })).detail).toBe('tmdb lookup: 500');
    expect(verdictName('other title', { query: 'Show' })).toBe('other title or year');
  });

  it('explains an episode comparison that differs', () => {
    expect(episodeDiffText({ season: 1, plex: 10, tmdb: 10, missing: [11], extra: [0] }, '5')).toBe(
      'The episode numbers differ from tmdb-5 in season 1: Plex and TMDB both have 10 episodes, but with other numbers. Only TMDB has episode 11. Only Plex has episode 0.',
    );
    const many = Array.from({ length: 12 }, (_, i) => i + 1);
    expect(episodeDiffText({ season: 2, plex: 1, tmdb: 13, missing: many })).toBe(
      'The episode numbers differ in season 2: Plex has 1 episode, and TMDB has 13. Only TMDB has episode 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, and 2 more.',
    );
  });
});
