import { describe, expect, it } from 'vitest';
import type { ItemDetail, ItemRecord, RefreshAnswer, TimelineEvent, TimelineEventKind } from './api';
import { decisionView, refreshOutcome, refreshResult, refreshText, disposition, episodeDiffText, eventView, lastDecision, rowVerdict, searchSource, verdictName } from './timeline';

const item = (change: Partial<ItemRecord> = {}): ItemRecord => ({ srv: 0, key: '7', cached: false, attempts: 0, refreshes: 0, ...change });
const event = (id: number, kind: TimelineEventKind, change: Partial<TimelineEvent> = {}): TimelineEvent => ({ id, kind, accepted: false, at: `2026-11-${10 + id}T00:00:00Z`, searches: [], ...change });

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

describe('refresh result', () => {
  const answer = (change: Partial<ItemRecord>, timeline: TimelineEvent[], checked = true): RefreshAnswer => ({ item: item(change), timeline, checked });

  it('reports an item that Plex no longer has as gone, not complete', () => {
    expect(refreshResult(answer({}, [event(2, 'gone'), event(1, 'request')]))).toEqual({ kind: 'gone' });
  });

  it('reports a completed item as complete and an incomplete one with its reasons', () => {
    expect(refreshResult(answer({}, [event(2, 'completed'), event(1, 'request')]))).toEqual({ kind: 'complete' });
    // A read of a run completed the item during the search, before the
    // refresh, whose request is the newest event.
    expect(refreshResult(answer({}, [event(3, 'request'), event(2, 'completed'), event(1, 'match-decision')]))).toEqual({ kind: 'complete' });
    // A fixed item that was not cached: only the refresh was sent.
    expect(refreshResult(answer({}, [event(3, 'request'), event(2, 'completed')], false))).toEqual({ kind: 'sent' });
    expect(refreshResult(answer({ cached: true, reasons: ['no-art'] }, [event(1, 'request')]))).toEqual({ kind: 'missing', reasons: ['no-art'] });
  });

  it('gives a success outcome only when no check found the item incomplete or gone', () => {
    expect(refreshOutcome(answer({}, [event(2, 'completed'), event(1, 'request')]))).toBe('success');
    expect(refreshOutcome(answer({}, [event(1, 'request')]))).toBe('success');
    expect(refreshOutcome(answer({ cached: true, reasons: ['no-art'] }, [event(1, 'request')]))).toBe('notice');
    expect(refreshOutcome(answer({}, [event(2, 'gone'), event(1, 'request')]))).toBe('notice');
  });

  it('reports only the request when no check saved the reasons', () => {
    expect(refreshResult(answer({}, [event(1, 'request')]))).toEqual({ kind: 'sent' });
    expect(refreshResult({ checked: true })).toEqual({ kind: 'sent' });
    expect(refreshText({ checked: false, match: { guid: 'plex://show/1', title: 'Show' } }, 'Item')).toBe('Matched Item to Show');
    expect(refreshResult(answer({ cached: true, reasons: ['no-art'] }, [event(1, 'request')], false))).toEqual({ kind: 'sent' });
  });

  it('names the match that the refresh applied and what the check found', () => {
    const match = { guid: 'plex://show/1', title: 'Show', year: 2022 };
    expect(refreshText({ ...answer({}, [event(2, 'completed')]), match }, 'Item')).toBe('Matched Item to Show (2022). It is complete.');
    expect(refreshText(answer({ cached: true, reasons: ['no-art', 'no-summary'] }, [event(1, 'request')]), 'Item')).toBe('Plex accepted the refresh of Item. Still missing: no art, no summary');
    expect(refreshText(answer({ cached: true, reasons: ['no-art'] }, [event(1, 'request')], false), 'Item')).toBe('Plex accepted the refresh of Item');
    expect(refreshText(answer({}, [event(2, 'gone')]), 'Item')).toBe('Plex no longer has Item. Autorefresh removed it from the queue.');
    expect(refreshText({ ...answer({}, [event(3, 'gone'), event(2, 'request')]), match }, 'Item')).toBe('Matched Item to Show (2022). Then Plex no longer has Item. Autorefresh removed it from the queue.');
  });

  it('reports a match that Plex merged into another item', () => {
    const match = { guid: 'plex://movie/1', title: 'Film', year: 2020 };
    const merged = [event(3, 'completed', { fixedAction: 'match', fixedMatch: 'match', mergedKey: '900' }), event(2, 'request')];
    expect(refreshResult({ ...answer({}, merged), match })).toEqual({ kind: 'merged' });
    expect(refreshOutcome({ ...answer({}, merged), match })).toBe('success');
    expect(refreshText({ ...answer({}, merged), match }, 'Item')).toBe('Matched Item to Film (2020). Plex merged it into the item that already had this match.');
    // Without an applied match, the refresh of an older merge is a plain completion.
    expect(refreshResult(answer({}, merged))).toEqual({ kind: 'complete' });
    expect(refreshText(answer({}, merged), 'Item')).toBe('Plex accepted the refresh of Item. It is complete.');
    // A completion without a merged key is complete.
    expect(refreshResult({ ...answer({}, [event(2, 'completed'), event(1, 'request')]), match })).toEqual({ kind: 'complete' });
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
    expect(eventView(event(1, 'completed', { source: 'auto', fixedAction: 'match', fixedMatch: 'match', mergedKey: '900' }))).toEqual({
      title: 'Fixed: Plex merged it into another item',
      tone: 'success',
      detail: 'By match · Plex kept the item that already had this match',
    });
    expect(eventView(event(1, 'completed', { fixedAction: 'tmdb-match', fixedMatch: 'tmdb-match' })).detail).toBe('By TMDB match');
    expect(eventView(event(1, 'completed'))).toMatchObject({ title: 'Completed by Plex on its own', tone: 'neutral' });
    expect(eventView(event(1, 'gone'))).toMatchObject({ title: 'Removed: Plex no longer had it', tone: 'warning' });
    expect(eventView(event(1, 'gone', { action: 'tmdb-match' })).detail).toBe('Its TMDB match request found it gone');
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
    expect(decisionView(title({ outcome: 'matched', episodeCount: 14, tmdbId: '88', episodeGroup: 'DVD Order' })).detail).toBe(
      'The title search found one series with the same title and a year within 1, and the episode numbers of 14 episodes from tmdb-88 via episode group “DVD Order” confirmed it.',
    );
    expect(decisionView(event(1, 'match-decision', { outcome: 'matched', episodeCount: 14, episodeGroup: 'Aired Order' })).detail).toBe(
      'The episode numbers of 14 episodes via episode group “Aired Order” confirmed the candidate.',
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
      'No result of the title search had the title of the item and a year within 1 of the year 2020 of the item.',
    );
    expect(decisionView(title({ outcome: 'no-match', searches: [otherTitle], pathYear: 2020 })).detail).toBe(
      'No result of the title search had the title of the item and a year within 1 of the year 2020 of the item.',
    );
    expect(decisionView(title({ outcome: 'no-match', conflict: 'not confirmed', tmdbId: '88' })).detail).toBe(
      'The title search found one series with the same title and a year within 1, but nothing confirmed it. Plex returned no usable episode numbers to compare with tmdb-88.',
    );
    expect(decisionView(title({ outcome: 'no-match', searches: [otherTitle], pathYear: 2012 })).detail).toBe(
      'No result of the title search had the title of the item and a year within 1 of the year 2020 of the item and 2012 of its folder.',
    );
    expect(decisionView(title({ outcome: 'no-match' })).detail).toBe('No search found a candidate.');
    expect(decisionView(title({ outcome: 'failed', error: 'tmdb lookup: 500', tmdbId: '88', pathYear: 2020 })).detail).toBe('tmdb lookup: 500');
    expect(verdictName('other title', { query: 'Show' })).toBe('other title or year');
  });

  it('explains a title search whose TMDB year stood in for a stale Plex year', () => {
    const search = (verdict: string, year = 2019) => ({ query: 'Lazy Susan', source: 'title', failed: false, candidates: [
      { guid: 'plex://show/o', year: 2022, score: 0, verdict: 'other title' },
      { guid: 'plex://show/l', year, score: 0, verdict },
    ] });
    const title = (verdict: string, fields: Partial<TimelineEvent>) => event(1, 'match-decision', {
      searches: [search(verdict)], itemYear: 2022, pathYear: 2022, tmdbId: '156686', ...fields,
    });
    expect(decisionView(title('picked', { outcome: 'matched', tmdbYear: 2022, episodeCount: 4 })).detail).toBe(
      'The title search found a series with the same title that Plex gives the year 2019. TMDB gives tmdb-156686 the year 2022 of the item, and the episode numbers of 4 episodes from tmdb-156686 confirmed it.',
    );
    expect(decisionView(title('not confirmed', { outcome: 'no-match', conflict: 'not confirmed', tmdbYear: 2022, episodeDiff: { season: 1, plex: 4, tmdb: 5, missing: [5] } })).detail).toBe(
      'The title search found a series with the same title that Plex gives the year 2019. TMDB gives tmdb-156686 the year 2022 of the item, but nothing confirmed it. The episode numbers differ from tmdb-156686 in season 1: Plex has 4 episodes, and TMDB has 5. Only TMDB has episode 5.',
    );
    // The year conflict names the candidate that TMDB's year did not confirm, not one with another title.
    expect(decisionView(title('year differs', { outcome: 'no-match', conflict: 'year differs', tmdbYear: 2019 })).detail).toBe(
      'Plex has 2019, the item has 2022, the folder has 2022, and TMDB has 2019. So nothing confirmed the candidate.',
    );
    expect(decisionView(title('', { outcome: 'failed', error: 'episode numbers: 500', tmdbYear: 2022 })).detail).toBe(
      'episode numbers: 500 Before the failure, TMDB gave tmdb-156686 the year 2022 of the item.',
    );
    expect(decisionView(title('year differs', { outcome: 'no-match', conflict: 'year differs', tmdbId: undefined })).detail).toBe(
      'Plex has 2019, the item has 2022, and the folder has 2022. So nothing confirmed the candidate.',
    );
    // One series in two rows was checked once.
    const twice = search('year differs');
    twice.candidates.push({ guid: 'plex://show/l', year: 2018, score: 0, verdict: 'year differs' });
    expect(decisionView(title('year differs', { outcome: 'no-match', conflict: 'year differs', tmdbYear: 2019, searches: [twice] })).detail).toBe(
      'Plex has 2019, the item has 2022, the folder has 2022, and TMDB has 2019. So nothing confirmed the candidate.',
    );
    const two = search('year differs');
    two.candidates.push({ guid: 'plex://show/m', year: 2015, score: 0, verdict: 'year differs' });
    expect(decisionView(title('year differs', { outcome: 'no-match', conflict: 'year differs', tmdbId: undefined, searches: [two] })).detail).toBe(
      'TMDB\'s year confirmed none of the 2 series of the title search with the title of the item and another Plex year.',
    );
  });

  it('explains an episode comparison that differs', () => {
    expect(episodeDiffText({ season: 1, plex: 10, tmdb: 10, missing: [11], extra: [0] }, '5')).toBe(
      'The episode numbers differ from tmdb-5 in season 1: Plex and TMDB both have 10 episodes, but with other numbers. Only TMDB has episode 11. Only Plex has episode 0.',
    );
    expect(episodeDiffText({ season: 1, plex: 3, tmdb: 3 }, '5')).toBe(
      'The episode numbers differ from tmdb-5 in season 1: Plex and TMDB both have 3 episodes, but with invalid or duplicate numbers.',
    );
    const many = Array.from({ length: 12 }, (_, i) => i + 1);
    expect(episodeDiffText({ season: 2, plex: 1, tmdb: 13, missing: many })).toBe(
      'The episode numbers differ in season 2: Plex has 1 episode, and TMDB has 13. Only TMDB has episode 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, and 2 more.',
    );
  });
});
