import { describe, expect, it } from 'vitest';
import { ApiError, type RunDetail, type RunEvent, type RunItem, type RunRecord } from './api';
import { filterGroups, nextCountFilter, noEntries, runCounts, runGroups, runPollInterval } from './runs';

const record = (change: Partial<RunRecord> = {}): RunRecord => ({
  id: 9, srv: 0, server: 'Home', kind: 'run', source: 'schedule', status: 'finished',
  readItems: 0, refreshed: 0, matched: 0, failed: 0, completed: 0, fixed: 0, incomplete: 0, due: 0, selected: 0, skipped: 0, eventsLinked: true,
  ...change,
});
let nextID = 1;
const event = (key: string, kind: string, change: Partial<RunEvent> = {}): RunEvent => ({
  id: nextID++, key, title: `Item ${key}`, kind, accepted: false, searches: [], ...change,
});
const detail = (events: RunEvent[], change: Partial<RunRecord> = {}, items: RunItem[] = []): RunDetail => ({ run: record(change), events, items });
const shape = (d: RunDetail) => runGroups(d).map(group => [group.title, group.entries.map(entry => [entry.key, entry.summary, entry.detail, entry.tab])]);

describe('run groups', () => {
  it('groups the events of a run by outcome in a fixed order and leaves out empty groups', () => {
    const groups = shape(detail([
      event('1', 'completed', { fixedAction: 'refresh' }),
      event('2', 'gone'),
      event('3', 'completed'),
      event('4', 'request', { action: 'refresh', accepted: true }),
      event('5', 'request', { action: 'tmdb-match', accepted: true, match: { guid: 'plex://movie/5', title: 'Five', year: 2005 } }),
      event('6', 'request', { action: 'refresh', error: 'Plex answered 500' }),
    ]));
    expect(groups).toEqual([
      ['Refreshed', [['4', 'Refresh accepted', undefined, 'attention']]],
      ['Matched', [['5', 'TMDB match accepted to Five (2005)', undefined, 'attention']]],
      ['Failed', [['6', 'Refresh failed', 'Plex answered 500', 'attention']]],
      ['Fixed', [['1', 'By refresh', undefined, 'fixed']]],
      ['Completed by Plex', [['3', 'Completed without a request', undefined, 'fixed']]],
      ['Removed', [['2', 'Plex no longer had it', undefined, 'attention']]],
    ]);
  });

  it('joins a match decision to the request of its item that followed it', () => {
    const groups = shape(detail([
      event('1', 'match-decision', { outcome: 'matched', tmdbYear: 2001 }),
      event('2', 'match-decision', { outcome: 'no-match', conflict: 'several candidates' }),
      event('1', 'request', { action: 'tmdb-match', accepted: true }),
      event('2', 'request', { action: 'refresh', accepted: true }),
    ]));
    expect(groups).toEqual([
      ['Refreshed', [['2', 'Refresh accepted', 'Fix match: not matched · several candidates', 'attention']]],
      ['Matched', [['1', 'TMDB match accepted', 'Fix match: matched', 'attention']]],
    ]);
  });

  it('lists a match decision that no request followed as not sent', () => {
    // The run ended between the decision and its request.
    const groups = shape(detail([event('1', 'match-decision', { outcome: 'matched' })]));
    expect(groups).toEqual([['Not sent', [['1', 'Fix match: matched', 'The run ended before the request', 'attention']]]]);
  });

  it('lists a match decision of a running run that waits for its request as in progress', () => {
    const groups = shape(detail([event('1', 'match-decision', { outcome: 'matched' })], { status: 'running' }));
    expect(groups).toEqual([['In progress', [['1', 'Fix match: matched', 'Its request is not sent yet', 'attention']]]]);
  });

  it('lists a failed fix match search with its error as failed', () => {
    // A failed search sends no request and writes no request event.
    const groups = shape(detail([event('1', 'match-decision', { outcome: 'failed', error: 'Plex answered 500' })]));
    expect(groups).toEqual([['Failed', [['1', 'Fix match: search failed', 'Plex answered 500', 'attention']]]]);
  });

  it('keeps the error of a failed decision before a request', () => {
    // A failed TMDB lookup ends in a plain refresh.
    const groups = shape(detail([
      event('1', 'match-decision', { outcome: 'failed', error: 'TMDB answered 503' }),
      event('1', 'request', { action: 'refresh', accepted: true }),
    ]));
    expect(groups).toEqual([['Refreshed', [['1', 'Refresh accepted', 'Fix match: search failed · TMDB answered 503', 'attention']]]]);
  });

  it('keeps an earlier decision of an item when a later one replaces it', () => {
    const groups = shape(detail([
      event('1', 'match-decision', { outcome: 'matched' }),
      event('1', 'match-decision', { outcome: 'no-ids' }),
    ]));
    expect(groups).toEqual([['Not sent', [
      ['1', 'Fix match: matched', 'A later decision replaced it', 'attention'],
      ['1', 'Fix match: no folder IDs', 'The run ended before the request', 'attention'],
    ]]]);
  });

  it('does not say that a running run ended for a replaced decision', () => {
    const groups = shape(detail([
      event('1', 'match-decision', { outcome: 'matched' }),
      event('1', 'match-decision', { outcome: 'no-ids' }),
    ], { status: 'running' }));
    expect(groups).toEqual([
      ['In progress', [['1', 'Fix match: no folder IDs', 'Its request is not sent yet', 'attention']]],
      ['Not sent', [['1', 'Fix match: matched', 'A later decision replaced it', 'attention']]],
    ]);
  });

  it('lists a match, the refresh after it, and the fix that its checks found', () => {
    const groups = shape(detail([
      event('1', 'match-decision', { outcome: 'matched' }),
      event('1', 'request', { action: 'match', accepted: true, match: { guid: 'plex://movie/1', title: 'One', year: 2001 } }),
      event('1', 'request', { action: 'refresh', accepted: true }),
      event('1', 'completed', { fixedAction: 'refresh', fixedMatch: 'match' }),
    ]));
    expect(groups).toEqual([
      ['Refreshed', [['1', 'Refresh accepted', undefined, 'attention']]],
      ['Matched', [['1', 'Match accepted to One (2001)', 'Fix match: matched', 'attention']]],
      ['Fixed', [['1', 'By match · then refresh', undefined, 'fixed']]],
    ]);
  });

  it('joins a match decision to the removal of its item', () => {
    // Plex answered 404 to the match request, so the item is gone.
    const groups = shape(detail([event('1', 'match-decision', { outcome: 'matched' }), event('1', 'gone', { action: 'match' })]));
    expect(groups).toEqual([['Removed', [['1', 'Plex no longer had it', 'Fix match: matched · Its match request found it gone', 'attention']]]]);
  });

  it('filters a request count by the requests that found their item gone too', () => {
    const d = detail([
      event('1', 'request', { action: 'refresh', accepted: true }),
      event('2', 'gone', { action: 'refresh' }),
      event('3', 'gone', { action: 'title-match' }),
      // A read found it gone, and an older run recorded no action.
      event('4', 'gone'),
    ]);
    const shown = (name: string) => {
      const count = runCounts(d.run).find(c => c.name === name)!;
      return filterGroups(runGroups(d), count.shows!).map(group => [group.title, group.entries.map(entry => entry.key)]);
    };
    expect(shown('Refreshes sent')).toEqual([['Refreshed', ['1']], ['Removed', ['2']]]);
    expect(shown('Matches sent')).toEqual([['Removed', ['3']]]);
    expect(shown('Failed')).toEqual([]);
  });

  it('counts requests sent, which include the ones that failed', () => {
    const run = record({ readItems: 40, refreshed: 2, matched: 1, failed: 1, fixed: 3, due: 5 });
    const values = (r: RunRecord) => runCounts(r).map(count => [count.name, count.value, Boolean(count.shows)]);
    expect(values(run)).toEqual([
      ['Read', 40, false], ['Refreshes sent', 2, true], ['Matches sent', 1, true], ['Failed', 1, true], ['Fixed', 3, true], ['Due', 5, false],
    ]);
    expect(values(record({ kind: 'bulk-refresh', selected: 4, refreshed: 3, failed: 1, skipped: 1 }))).toEqual([
      ['Selected', 4, false], ['Refreshes sent', 3, true], ['Failed', 1, true], ['Skipped', 1, true],
    ]);
  });

  it('filters a request count by every request it counts, also a failed one', () => {
    const d = detail([
      event('1', 'request', { action: 'refresh', accepted: true }),
      event('2', 'request', { action: 'refresh', accepted: false }),
      event('3', 'request', { action: 'match', accepted: false }),
      event('4', 'completed', { fixedAction: 'refresh' }),
    ]);
    const shown = (name: string) => {
      const count = runCounts(d.run).find(c => c.name === name)!;
      return filterGroups(runGroups(d), count.shows!).map(group => [group.title, group.entries.map(entry => entry.key)]);
    };
    expect(shown('Refreshes sent')).toEqual([['Refreshed', ['1']], ['Failed', ['2']]]);
    expect(shown('Matches sent')).toEqual([['Failed', ['3']]]);
    expect(shown('Failed')).toEqual([['Failed', ['2', '3']]]);
    expect(shown('Fixed')).toEqual([['Fixed', ['4']]]);
  });

  it('filters Failed by a failed decision before an accepted refresh too', () => {
    // A TMDB lookup failed, so the run counted a failure and sent a plain refresh.
    const d = detail([event('5', 'match-decision', { outcome: 'failed', error: 'TMDB unavailable' }), event('5', 'request', { action: 'refresh', accepted: true })]);
    const count = runCounts(d.run).find(c => c.name === 'Failed')!;
    expect(filterGroups(runGroups(d), count.shows!).map(group => [group.title, group.entries.map(entry => entry.key)])).toEqual([['Refreshed', ['5']]]);
  });

  it('filters the sent refreshes of a bulk refresh by their outcome', () => {
    const items: RunItem[] = ['accepted', 'failed', 'gone', 'stopped', 'not-reached'].map((outcome, i) => ({ key: String(i), title: `Item ${i}`, outcome }));
    const d = detail([], { kind: 'bulk-refresh' }, items);
    const count = runCounts(d.run).find(c => c.name === 'Refreshes sent')!;
    expect(filterGroups(runGroups(d), count.shows!).flatMap(group => group.entries.map(entry => entry.key))).toEqual(['0', '1', '2']);
  });

  it('filters by the clicked count until a second click on it', () => {
    expect(nextCountFilter(undefined, 'Fixed')).toBe('Fixed');
    expect(nextCountFilter('Fixed', 'Failed')).toBe('Failed');
    expect(nextCountFilter('Fixed', 'Fixed')).toBeUndefined();
  });

  it('keeps the item of each entry', () => {
    const [group] = runGroups(detail([event('7', 'request', { action: 'refresh', accepted: true, year: 2019, type: 'movie', thumb: '/api/items/thumb?x' })]));
    expect(group.entries[0]).toMatchObject({ key: '7', title: 'Item 7', year: 2019, type: 'movie', thumb: '/api/items/thumb?x' });
  });

  it('keeps the item of each bulk item', () => {
    const items: RunItem[] = [{ key: '1', title: 'One', outcome: 'accepted', year: 2019, type: 'show', thumb: '/api/items/thumb?x', library: 'TV' }];
    const [group] = runGroups(detail([], { kind: 'bulk-refresh' }, items));
    expect(group.entries[0]).toMatchObject({ key: '1', title: 'One', year: 2019, type: 'show', thumb: '/api/items/thumb?x', library: 'TV' });
  });

  it('groups the items of a bulk refresh by outcome', () => {
    const items: RunItem[] = [
      { key: '1', title: 'One', outcome: 'accepted' },
      { key: '2', title: 'Two', outcome: 'not-reached' },
      { key: '3', title: 'Three', outcome: 'failed', error: 'Plex answered 500' },
      { key: '4', title: 'Four', outcome: 'changed' },
      { key: '5', title: 'Five', outcome: 'gone' },
      { key: '6', title: 'Six', outcome: 'stopped' },
      { key: '7', title: 'Seven', outcome: 'not-cached' },
    ];
    expect(shape(detail([], { kind: 'bulk-refresh' }, items))).toEqual([
      ['Refreshed', [['1', 'Refresh accepted', undefined, 'attention']]],
      ['Failed', [['3', 'Refresh failed', 'Plex answered 500', 'attention']]],
      ['Removed', [['5', 'Plex no longer had it', undefined, 'attention']]],
      ['Skipped', [
        ['4', 'Changed after it was selected', undefined, 'attention'],
        ['6', 'Stopped before its turn', undefined, 'stopped'],
        ['7', 'No longer in the cache', undefined, 'attention'],
      ]],
      ['Not reached', [['2', 'The job ended before this item', undefined, 'attention']]],
    ]);
  });
});

describe('run poll interval', () => {
  const running = record({ status: 'running' });
  it('polls a running run, also after a failed reload that kept its data', () => {
    expect(runPollInterval(running, null)).toBe(5_000);
    expect(runPollInterval(running, new ApiError(500, 'Plex answered 500'))).toBe(5_000);
  });
  it('stops for a removed run, an ended run, a bulk refresh, or no data', () => {
    expect(runPollInterval(running, new ApiError(404, 'no such run'))).toBe(false);
    expect(runPollInterval(record(), null)).toBe(false);
    expect(runPollInterval(record({ status: 'running', kind: 'bulk-refresh' }), null)).toBe(false);
    expect(runPollInterval(undefined, null)).toBe(false);
  });
});

describe('no entries', () => {
  const notRecorded = 'Item details were not recorded for this run';
  it('says that a run from before runs linked their events recorded no items, also with no counts', () => {
    expect(noEntries(record({ eventsLinked: false }))).toEqual({ title: notRecorded, description: 'The run is from before autorefresh recorded them.' });
    expect(noEntries(record({ eventsLinked: false, refreshed: 2 })).title).toBe(notRecorded);
  });
  it('says that the history of a newer run that changed items expired', () => {
    expect(noEntries(record({ refreshed: 2 }))).toEqual({ title: notRecorded, description: 'Their history expired.' });
    expect(noEntries(record({ kind: 'bulk-refresh', selected: 3, eventsLinked: false }))).toEqual({ title: notRecorded, description: 'Their history expired.' });
  });
  it('says that a newer run changed no item', () => {
    expect(noEntries(record({ readItems: 12 }))).toEqual({ title: 'No item changed in this run', description: 'It read 12 items and refreshed none.' });
    expect(noEntries(record({ status: 'canceled' }))).toEqual({ title: 'No item changed in this run', description: 'The run ended before it changed an item.' });
  });
  it('says that a running run shows its items as it works', () => {
    expect(noEntries(record({ status: 'running' })).title).toBe('No item changed yet');
    expect(noEntries(record({ status: 'running', kind: 'bulk-refresh' })).title).toBe('The items show when the bulk refresh ends');
  });
});
