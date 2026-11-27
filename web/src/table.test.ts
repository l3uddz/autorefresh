import { describe, expect, it } from 'vitest';
import type { ItemRecord } from './api';
import { chooseSort, formatSort, parseSort, sortKeys, stableRows, tableRow } from './table';

describe('table rows', () => {
  const now = Date.parse('2026-11-22T12:00:00Z');
  const item: ItemRecord = {
    srv: 1,
    server: 'Cabin',
    library: 'Films',
    key: '42',
    type: 'movie',
    title: 'Paper Moons',
    year: 2017,
    cached: true,
    reasons: ['no-art', 'no-summary'],
    state: 'waiting',
    nextDue: '2026-11-24T12:00:00Z',
    lastAttempt: '2026-11-20T12:00:00Z',
    lastOutcome: 'refresh',
    lastMatchOutcome: 'no-match',
    attempts: 4,
    refreshes: 4,
  };

  it('turns every cell into the text or number that Astryx windows', () => {
    const row = tableRow(item, now, true, ['no-summary']);
    expect(row).toMatchObject({
      id: '1/42',
      title: 'Paper Moons',
      kind: 'Movie · 2017',
      library: 'Cabin · Films',
      reasons: 'no art, no summary (ignored)',
      tries: 4,
      'last-attempt': 'refresh · no match',
      due: 'Waiting · in 2d',
    });
    for (const value of Object.values(row)) expect(['string', 'number']).toContain(typeof value);
  });

  it('leaves out the server of a single-server install and fills empty cells', () => {
    const row = tableRow({ srv: 0, key: '7', cached: true, attempts: 0, refreshes: 0, library: 'TV' }, now, false, []);
    expect(row).toMatchObject({ title: 'Item 7', library: 'TV', reasons: '—', 'last-attempt': '—', 'fixed-by': 'Plex on its own' });
  });

  it('names a wrong match and its queue state', () => {
    const row = tableRow({ ...item, reasons: ['mismatched'], state: 'manual', nextDue: undefined }, now, false, []);
    expect(row).toMatchObject({ reasons: 'mismatched', due: 'Needs a manual fix' });
  });

  it('keeps the rows when a clock tick changes no cell and replaces them when one changes', () => {
    const rows = [tableRow(item, now - 60_000, false, [])];
    // A minute later every label still reads the same.
    expect(stableRows(rows, [tableRow(item, now, false, [])])).toBe(rows);
    // A day later the due label changes.
    const later = [tableRow(item, now + 86_400_000, false, [])];
    expect(stableRows(rows, later)).toBe(later);
    expect(stableRows(rows, [])).toEqual([]);
  });
});

describe('sorts', () => {
  it('reads and writes the sort parameter of the API', () => {
    expect(parseSort('-tries')).toEqual({ key: 'tries', desc: true });
    expect(parseSort('due')).toEqual({ key: 'due', desc: false });
    expect(parseSort('')).toEqual({ key: '', desc: false });
    expect(formatSort('due', false)).toBe('due');
    expect(formatSort('tries', true)).toBe('-tries');
    expect(formatSort('', true)).toBe('');
  });

  it('starts a new sort key in its natural direction and keeps the direction of the current key', () => {
    expect(chooseSort('attention', '', 'tries')).toBe('-tries');
    expect(chooseSort('attention', '-tries', 'due')).toBe('due');
    expect(chooseSort('attention', 'tries', 'tries')).toBe('tries');
    expect(chooseSort('attention', '-tries', '')).toBe('');
  });

  it('offers the sorts of each tab that the API knows', () => {
    expect(sortKeys.attention.map(sort => sort.key)).toEqual(['title', 'tries', 'last-attempt', 'due']);
    expect(sortKeys.stopped.map(sort => sort.key)).toEqual(['title', 'tries', 'stopped']);
    expect(sortKeys.fixed.map(sort => sort.key)).toEqual(['title', 'completed']);
    // A new choice of tries shows the most tries first.
    expect(sortKeys.attention.find(sort => sort.key === 'tries')?.desc).toBe(true);
    expect(sortKeys.attention.find(sort => sort.key === 'title')?.desc).toBe(false);
  });

  it('sorts only by columns that table rows have', () => {
    const row = tableRow({ srv: 0, key: '1', cached: true, attempts: 0, refreshes: 0 }, 0, false, []);
    for (const sorts of Object.values(sortKeys)) {
      for (const { key } of sorts) expect(row).toHaveProperty(key);
    }
  });
});
