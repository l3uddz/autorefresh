import { describe, expect, it } from 'vitest';
import type { RunDetail, RunEvent, RunItem, RunRecord } from './api';
import { runCounts, runGroups } from './runs';

const record = (change: Partial<RunRecord> = {}): RunRecord => ({
  id: 9, srv: 0, server: 'Home', kind: 'run', source: 'schedule', status: 'finished',
  readItems: 0, refreshed: 0, matched: 0, failed: 0, completed: 0, fixed: 0, incomplete: 0, due: 0, selected: 0, skipped: 0,
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

  it('joins a match decision to the removal of its item', () => {
    // Plex answered 404 to the match request, so the item is gone.
    const groups = shape(detail([event('1', 'match-decision', { outcome: 'matched' }), event('1', 'gone')]));
    expect(groups).toEqual([['Removed', [['1', 'Plex no longer had it', 'Fix match: matched', 'attention']]]]);
  });

  it('counts requests sent, which include the ones that failed', () => {
    const run = record({ readItems: 40, refreshed: 2, matched: 1, failed: 1, fixed: 3, due: 5 });
    expect(runCounts(run)).toEqual([['Read', 40], ['Refreshes sent', 2], ['Matches sent', 1], ['Failed', 1], ['Fixed', 3], ['Due', 5]]);
    expect(runCounts(record({ kind: 'bulk-refresh', selected: 4, refreshed: 3, failed: 1, skipped: 1 }))).toEqual([
      ['Selected', 4], ['Refreshes sent', 3], ['Failed', 1], ['Skipped', 1],
    ]);
  });

  it('keeps the item of each entry', () => {
    const [group] = runGroups(detail([event('7', 'request', { action: 'refresh', accepted: true, year: 2019, type: 'movie', thumb: '/api/items/thumb?x' })]));
    expect(group.entries[0]).toMatchObject({ key: '7', title: 'Item 7', year: 2019, type: 'movie', thumb: '/api/items/thumb?x' });
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
        ['6', 'Stopped before its turn', undefined, 'attention'],
        ['7', 'No longer in the cache', undefined, 'attention'],
      ]],
      ['Not reached', [['2', 'The job ended before this item', undefined, 'attention']]],
    ]);
  });
});
